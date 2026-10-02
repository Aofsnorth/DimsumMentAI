package placer_test

import (
	"io"
	"log/slog"
	"strconv"
	"testing"

	"bedrock-ai/internal/bot/building/placer"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Clearing a build site by digging whatever is there is how a bot ends up
// quietly destroying a chest of somebody's storage because a schematic wanted
// that cell. These tests pin the rule that stops it: terrain gets cleared,
// anything worth keeping does not, and a site that is left alone is a site the
// bot refuses to build into rather than one it fills in.

type stubWorld struct{ solid map[string]bool }

func newStubWorld(cells ...string) *stubWorld {
	w := &stubWorld{solid: map[string]bool{}}
	for _, c := range cells {
		w.solid[c] = true
	}
	return w
}

func cellKey(x, y, z int32) string {
	return strconv.FormatInt(int64(x), 10) + "," +
		strconv.FormatInt(int64(y), 10) + "," +
		strconv.FormatInt(int64(z), 10)
}

func (w *stubWorld) IsSolid(x, y, z int32) bool { return w.solid[cellKey(x, y, z)] }
func (w *stubWorld) SetSolid(x, y, z int32, solid bool) {
	if solid {
		w.solid[cellKey(x, y, z)] = true
		return
	}
	delete(w.solid, cellKey(x, y, z))
}
func (w *stubWorld) IsHazard(int32, int32, int32) bool   { return false }
func (w *stubWorld) SetHazard(int32, int32, int32, bool) {}

// stubBot satisfies common.BotInterface with only the state these tests touch.
type stubBot struct {
	world    *stubWorld
	blocks   map[string]string
	packets  []packet.Packet
	inv      map[uint32]protocol.ItemStack
	names    map[int32]string
	entities map[uint64]*entity.Info
}

func newStubBot(blocks map[string]string) *stubBot {
	return &stubBot{
		world:    newStubWorld(),
		blocks:   blocks,
		inv:      map[uint32]protocol.ItemStack{},
		names:    map[int32]string{},
		entities: map[uint64]*entity.Info{},
	}
}

func (b *stubBot) GetCoords() mgl32.Vec3 { return mgl32.Vec3{0.5, 64, 0.5} }
func (b *stubBot) GetBlockName(x, y, z int32) (string, bool) {
	name, ok := b.blocks[cellKey(x, y, z)]
	return name, ok
}
func (b *stubBot) GetInventorySlots() map[uint32]protocol.ItemStack { return b.inv }
func (b *stubBot) GetItemNames() map[int32]string                   { return b.names }
func (b *stubBot) GetEntities() map[uint64]*entity.Info             { return b.entities }
func (b *stubBot) GetLocalWorldModel() entity.WorldModel            { return b.world }
func (b *stubBot) SendSafeChat(string)                              {}
func (b *stubBot) ReportActionStatus(string, event.ActionStatus)    {}
func (b *stubBot) WritePacket(pk packet.Packet) error {
	b.packets = append(b.packets, pk)
	return nil
}
func (b *stubBot) GetHeldItemSlot() uint32                   { return 0 }
func (b *stubBot) EquipItem(uint32) error                    { return nil }
func (b *stubBot) LookAt(mgl32.Vec3)                         {}
func (b *stubBot) GetPlayerCoords(string) (mgl32.Vec3, bool) { return mgl32.Vec3{}, false }
func (b *stubBot) NavigateToBlock(int32, int32, int32, float32) bool {
	return true
}
func (b *stubBot) CraftItem(uint32, int) error   { return nil }
func (b *stubBot) GetRecipes() map[string]uint32 { return nil }
func (b *stubBot) GetEntityRuntimeID() uint64    { return 1 }
func (b *stubBot) DropItem(string, int) error    { return nil }
func (b *stubBot) StopMovement()                 {}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestPlacer(b *stubBot) *placer.BlockPlacer {
	return placer.NewBlockPlacer(b, quietLogger())
}

// TestClearObstructionsKeepsThingsWorthKeeping is the rule itself.
func TestClearObstructionsKeepsThingsWorthKeeping(t *testing.T) {
	t.Parallel()

	// Both the vanilla storage list and the interaction vocabulary count. A
	// modded "vault" is here because the storage classifier matches the suffix,
	// which is the only reason a bot can be trusted with a server it has never
	// seen before.
	protected := []string{
		"minecraft:chest",
		"minecraft:trapped_chest",
		"minecraft:barrel",
		"minecraft:undyed_shulker_box",
		"minecraft:hopper",
		"forestry:common_storage",
		"minecraft:oak_door",
		"minecraft:oak_wall_sign",
		"minecraft:stone_button",
		"minecraft:crafting_table",
		"minecraft:furnace",
		"minecraft:jukebox",
		"minecraft:note_block",
	}

	for _, name := range protected {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := newStubBot(map[string]string{cellKey(1, 64, 1): name})
			b.world.SetSolid(1, 64, 1, true)

			if newTestPlacer(b).ClearObstructions(t.Context(), 1, 64, 1) {
				t.Errorf("clearObstructions agreed to destroy %s", name)
			}
			if !b.world.IsSolid(1, 64, 1) {
				t.Error("the world model lost a block that should have been left alone")
			}
			// A dig is four PlayerAction packets plus a swing. None of them
			// should have been written.
			for _, pk := range b.packets {
				if _, isAction := pk.(*packet.PlayerAction); isAction {
					t.Error("a break packet was written for a block worth keeping")
					break
				}
			}
		})
	}
}

// TestClearObstructionsStillDigsOrdinaryTerrain is the other half. The whole
// point of clearing a site is to get through the terrain in the way, and a
// rule that protected everything would be a rule that never builds anything.
func TestClearObstructionsStillDigsOrdinaryTerrain(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"minecraft:stone", "minecraft:dirt", "minecraft:oak_log", "minecraft:cobblestone"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := newStubBot(map[string]string{cellKey(1, 64, 1): name})
			b.world.SetSolid(1, 64, 1, true)

			if !newTestPlacer(b).ClearObstructions(t.Context(), 1, 64, 1) {
				t.Errorf("clearObstructions refused to clear plain %s", name)
			}
			if b.world.IsSolid(1, 64, 1) {
				t.Errorf("%s was not cleared", name)
			}
		})
	}
}

// TestClearObstructionsLeavesAnEmptySiteAlone keeps the common case cheap: a
// cell that is already air needs no dig and no decision.
func TestClearObstructionsLeavesAnEmptySiteAlone(t *testing.T) {
	t.Parallel()

	b := newStubBot(map[string]string{cellKey(1, 64, 1): "minecraft:air"})

	if !newTestPlacer(b).ClearObstructions(t.Context(), 1, 64, 1) {
		t.Error("an empty build site was refused")
	}
	if len(b.packets) != 0 {
		t.Errorf("wrote %d packets for an empty site, want none", len(b.packets))
	}
}

// TestAnUnnamedBlockIsStillCleared pins the deliberate trade. A cell with
// solidity data but no decoded name is ordinary terrain in a chunk the bot has
// not finished reading, and refusing every one of those would make building
// fail constantly. The classifiers above already cover what holds anything.
func TestAnUnnamedBlockIsStillCleared(t *testing.T) {
	t.Parallel()

	b := newStubBot(nil)
	b.world.SetSolid(1, 64, 1, true)

	if !newTestPlacer(b).ClearObstructions(t.Context(), 1, 64, 1) {
		t.Error("an unnamed block was refused; building would stall on every undecoded chunk")
	}
}
