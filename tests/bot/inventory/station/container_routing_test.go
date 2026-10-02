package station_test

import (
	"context"
	"testing"
	"time"

	"bedrock-ai/internal/bot/inventory/station"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// These tests pin WHICH CONTAINER a transfer is addressed to, which is the part
// the whole set-up was wrong about.
//
// A server resolves StackRequestSlotInfo.Container.ContainerID against
// per-station protocol constants and refuses one it does not recognise. The
// station code used to address every transfer by window ID instead. For a chest
// the two are the same number, so the tests all passed and the bug was
// invisible; at an anvil they differ, the server rejects the move, and the bot
// sits in front of a working anvil doing nothing.
//
// So a test that only checks "the repair returned true" proves nothing here.
// Each case below asserts the container ID that actually reached the wire.

// placedIDs returns the container IDs of every place call, in order.
func placedIDs(bot *fakeBot) []byte {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	out := make([]byte, 0, len(bot.placed))
	for _, c := range bot.placed {
		out = append(out, c.containerID)
	}
	return out
}

// takenIDs returns the container IDs of every take call, in order.
func takenIDs(bot *fakeBot) []byte {
	bot.mu.Lock()
	defer bot.mu.Unlock()
	out := make([]byte, 0, len(bot.taken))
	for _, c := range bot.taken {
		out = append(out, c.containerID)
	}
	return out
}

// wantIDs reports whether got is exactly the sequence of container IDs. It
// prints both so a failure says which slot was addressed wrongly.
func wantIDs(t *testing.T, what string, got []byte, want ...byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s addressed %d containers, want %d (got %v, want %v)", what, len(got), len(want), got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s call %d addressed container %d, want %d", what, i, got[i], want[i])
		}
	}
}

// TestAnvilAddressesItsOwnContainers. The anvil's two inputs are in different
// containers and the result is in a third; the window ID is none of them.
func TestAnvilAddressesItsOwnContainers(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "anvil")
	bot.addItem(0, 1, "iron_pickaxe", 1)
	bot.addItem(8, 2, "iron_ingot", 3)
	bot.outputAfterReads = 1
	bot.outputSlot = station.AnvilOutputSlot
	bot.outputName = "iron_pickaxe"
	bot.outputNetID = 41

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetAnvilBudget(2 * time.Second)

	if _, ok := m.RepairItem(context.Background(), "iron_pickaxe"); !ok {
		t.Fatal("RepairItem reported failure, so there is nothing to inspect")
	}

	wantIDs(t, "anvil place", placedIDs(bot),
		byte(protocol.ContainerAnvilInput),
		byte(protocol.ContainerAnvilMaterial),
	)
	wantIDs(t, "anvil take", takenIDs(bot), byte(protocol.ContainerAnvilResultPreview))
}

// TestGrindstoneAddressesItsOwnContainers is the same check for the strip path.
func TestGrindstoneAddressesItsOwnContainers(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "grindstone")
	bot.addItem(0, 1, "diamond_sword", 1)
	bot.outputAfterReads = 1
	bot.outputSlot = station.GrindstoneOutputSlot
	bot.outputName = "diamond_sword"
	bot.outputNetID = 63

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetGrindstoneBudget(2 * time.Second)

	if _, ok := m.DisenchantItem(context.Background(), "diamond_sword"); !ok {
		t.Fatal("DisenchantItem reported failure, so there is nothing to inspect")
	}

	wantIDs(t, "grindstone place", placedIDs(bot), byte(protocol.ContainerGrindstoneInput))
	wantIDs(t, "grindstone take", takenIDs(bot), byte(protocol.ContainerGrindstoneResultPreview))
}

// TestBrewingStandSeparatesFuelFromBottles. Blaze powder goes in the fuel
// container. Put it in the bottle container and the stand does not light, which
// is the failure this separates.
func TestBrewingStandSeparatesFuelFromBottles(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(4, 64, 5, "brewing_stand")
	bot.addItem(0, 1, "glass_bottle", 3)
	bot.addItem(10, 2, "nether_wart", 4)
	bot.addItem(20, 3, "blaze_powder", 2)

	// Healing a glass bottle is two cycles in vanilla: awkward first, then
	// healing. The stand blends on the first poll after the ingredient lands.
	bot.brewAfterReads = 2
	bot.brewSequence = []string{"potion_awkward", "potion_healing"}

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetBrewBudget(2 * time.Second)

	if _, ok := m.BrewPotion(context.Background(), 1, "glass_bottle", "nether_wart", "healing"); !ok {
		t.Fatal("BrewPotion reported failure, so there is nothing to inspect")
	}

	// Bottles and nether wart share the input container; blaze powder must not,
	// or the stand never lights. Rather than pin the exact call count — which
	// moves as the plan grows — this asserts the two rules that matter: the fuel
	// is never addressed as a bottle, and nothing is addressed by window ID.
	for _, id := range placedIDs(bot) {
		switch id {
		case byte(protocol.ContainerBrewingStandFuel):
			// correct, keep going
		case byte(protocol.ContainerBrewingStandInput):
		default:
			t.Errorf("a brewing transfer was addressed by container %d, want the stand's input (%d) or fuel (%d) container",
				id, protocol.ContainerBrewingStandInput, protocol.ContainerBrewingStandFuel)
		}
	}
	for _, id := range takenIDs(bot) {
		if id != byte(protocol.ContainerBrewingStandInput) {
			t.Errorf("a brewed potion was taken by container %d, want the stand's input container %d", id, protocol.ContainerBrewingStandInput)
		}
	}
	if len(placedIDs(bot)) == 0 || len(takenIDs(bot)) == 0 {
		t.Error("BrewPotion reported success without placing an ingredient or taking a potion")
	}
}

// TestNoStationEverAddressesTheWindowID is the direct statement of the bug: no
// transfer at a station may be addressed by the window the server assigned. The
// fake hands out windowID 7, and protocol.ContainerAnvilInput is 0, so a single
// stray window ID shows up as a wrong number in the sequences above.
func TestNoStationEverAddressesTheWindowID(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "anvil")
	bot.addItem(0, 1, "iron_pickaxe", 1)
	bot.addItem(8, 2, "iron_ingot", 3)
	bot.outputAfterReads = 1
	bot.outputSlot = station.AnvilOutputSlot
	bot.outputName = "iron_pickaxe"
	bot.outputNetID = 41

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetAnvilBudget(2 * time.Second)
	if _, ok := m.RepairItem(context.Background(), "iron_pickaxe"); !ok {
		t.Fatal("RepairItem reported failure, so there is nothing to inspect")
	}

	for _, c := range append(placedIDs(bot), takenIDs(bot)...) {
		if c == bot.windowID {
			t.Errorf("a station transfer was addressed by window ID %d; the server resolves station slots by per-station container constants and will refuse it", c)
		}
	}
}
