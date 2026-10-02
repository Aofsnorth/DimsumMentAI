package station_test

import (
	"context"
	"testing"
	"time"

	"bedrock-ai/internal/bot/inventory/station"
)

// TestGrindstoneRemovesEnchantments is the acceptance test for 4.4: an
// enchanted tool comes back unenchanted, confirmed from the server's output
// slot rather than assumed.
func TestGrindstoneRemovesEnchantments(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(7, 64, 5, "grindstone")
	bot.addItem(0, 1, "diamond_sword", 1)

	bot.outputAfterReads = 1
	bot.outputSlot = station.GrindstoneOutputSlot
	bot.outputName = "diamond_sword"
	bot.outputNetID = 63

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetGrindstoneBudget(2 * time.Second)

	result, ok := m.DisenchantItem(context.Background(), "diamond_sword")
	if !ok {
		t.Fatal("DisenchantItem reported failure for a tool the grindstone accepts")
	}
	if result.OutputName != "diamond_sword" {
		t.Errorf("disenchant output = %q, want diamond_sword", result.OutputName)
	}
	if !result.Taken {
		t.Error("the disenchanted tool was never taken into the inventory")
	}
	if bot.closedWindow != bot.windowID {
		t.Errorf("closed window %d, want the server-assigned %d", bot.closedWindow, bot.windowID)
	}
}

// TestGrindstoneFailsWhenNothingComesOut. A tool with no enchantment left to
// strip produces no output; reporting success would hand the caller an item the
// server never returned.
func TestGrindstoneFailsWhenNothingComesOut(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(7, 64, 5, "grindstone")
	bot.addItem(0, 1, "iron_hoe", 1)

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetGrindstoneBudget(60 * time.Millisecond)

	result, ok := m.DisenchantItem(context.Background(), "iron_hoe")
	if ok {
		t.Error("DisenchantItem reported success although the grindstone produced nothing")
	}
	if result.Taken {
		t.Error("DisenchantItem claimed to have taken an output the server never sent")
	}
	if bot.closedWindow != bot.windowID {
		t.Error("a failed disenchant must still close the window it opened")
	}
}

// TestGrindstoneFailsWithoutAGrindstone, the name-based search guard.
func TestGrindstoneFailsWithoutAGrindstone(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(7, 64, 5, "stone")
	bot.addItem(0, 1, "diamond_sword", 1)

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)

	if _, ok := m.DisenchantItem(context.Background(), "diamond_sword"); ok {
		t.Error("DisenchantItem reported success with no grindstone nearby")
	}
	if bot.clicked {
		t.Error("DisenchantItem clicked a block that is not a grindstone")
	}
}

// TestGrindstoneFailsWhenTheTakeIsRejected. The result was in the slot, but the
// ItemStackRequest to move it home was refused; that is a failure.
func TestGrindstoneFailsWhenTheTakeIsRejected(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(7, 64, 5, "grindstone")
	bot.addItem(0, 1, "diamond_sword", 1)

	bot.outputAfterReads = 1
	bot.outputSlot = station.GrindstoneOutputSlot
	bot.outputName = "diamond_sword"
	bot.outputNetID = 63
	bot.takeErr[station.GrindstoneOutputSlot] = errFakeNoItem

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetGrindstoneBudget(time.Second)

	result, ok := m.DisenchantItem(context.Background(), "diamond_sword")
	if ok {
		t.Error("DisenchantItem reported success although the take was rejected")
	}
	if result.Taken {
		t.Error("DisenchantItem reported the item as taken after a rejected stack request")
	}
}

// TestGrindstoneReportsTheMaterialsItActuallyGot. The acceptance says the
// materials are returned, so the manager reads the refund back out of the
// bot's own inventory instead of stating what a grindstone usually gives.
func TestGrindstoneReportsTheMaterialsItActuallyGot(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(7, 64, 5, "grindstone")
	bot.addItem(0, 1, "diamond_sword", 1)

	bot.outputAfterReads = 1
	bot.outputSlot = station.GrindstoneOutputSlot
	bot.outputName = "diamond_sword"
	bot.outputNetID = 63
	bot.outputMaterials = []string{"lapis_lazuli"}

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetGrindstoneBudget(2 * time.Second)

	result, ok := m.DisenchantItem(context.Background(), "diamond_sword")
	if !ok {
		t.Fatal("DisenchantItem reported failure for a strip the grindstone can make")
	}
	if !result.MaterialsReturned {
		t.Error("the grindstone returned lapis and the manager did not see it")
	}
	if result.Materials != "lapis_lazuli" {
		t.Errorf("reported materials = %q, want lapis_lazuli", result.Materials)
	}
}

// TestGrindstoneReportsNoMaterialsWhenTheServerReturnsNone. Claiming a refund
// the server never made is the same class of lie as claiming the strip itself.
func TestGrindstoneReportsNoMaterialsWhenTheServerReturnsNone(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(7, 64, 5, "grindstone")
	bot.addItem(0, 1, "diamond_sword", 1)

	bot.outputAfterReads = 1
	bot.outputSlot = station.GrindstoneOutputSlot
	bot.outputName = "diamond_sword"
	bot.outputNetID = 63

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetGrindstoneBudget(2 * time.Second)

	result, ok := m.DisenchantItem(context.Background(), "diamond_sword")
	if !ok {
		t.Fatal("DisenchantItem failed; this test is about the refund reporting")
	}
	if result.MaterialsReturned {
		t.Errorf("reported the refund %q although the server returned nothing", result.Materials)
	}
}

// TestGrindstoneRefusesAnItemItCannotStrip. A block in a grindstone produces no
// output; refusing up front avoids opening a window and moving an item for
// nothing.
func TestGrindstoneRefusesAnItemItCannotStrip(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(7, 64, 5, "grindstone")
	bot.addItem(0, 1, "stone", 8)

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)

	if _, ok := m.DisenchantItem(context.Background(), "stone"); ok {
		t.Error("DisenchantItem reported success for an item that cannot be enchanted")
	}
	if bot.clicked {
		t.Error("DisenchantItem opened the grindstone for an item it should have refused up front")
	}
}

// TestGrindstoneMaterialsAreReturned pins the material names a grindstone gives
// back. Reporting the wrong refund is how a bot tells its caller it earned
// something it did not — and the refund is never a brew ingredient, because a
// grindstone has nothing to do with a brewing stand.
func TestGrindstoneMaterialsAreReturned(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"lapis_lazuli", "enchanted_book", "minecraft:lapis_lazuli"} {
		if !station.IsGrindstoneMaterial(name) {
			t.Errorf("IsGrindstoneMaterial(%q) = false, want true", name)
		}
	}
	for _, notMaterial := range []string{"diamond_sword", "lapis", "", "cobblestone", "nether_wart", "blaze_powder"} {
		if station.IsGrindstoneMaterial(notMaterial) {
			t.Errorf("IsGrindstoneMaterial(%q) = true, want false", notMaterial)
		}
	}
}
