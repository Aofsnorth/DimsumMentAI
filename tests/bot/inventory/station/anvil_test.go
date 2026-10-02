package station_test

import (
	"context"
	"testing"
	"time"

	"bedrock-ai/internal/bot/inventory/station"
)

// TestAnvilRepairMaterial pins the material table. A repair that sends the
// wrong material does nothing, and a bot that guesses costs a rare item every
// time it tries.
func TestAnvilRepairMaterial(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"iron_pickaxe":       "iron_ingot",
		"iron_sword":         "iron_ingot",
		"diamond_pickaxe":    "diamond",
		"golden_axe":         "golden_apple",
		"netherite_sword":    "netherite_scrap",
		"stone_axe":          "cobblestone",
		"leather_chestplate": "leather",
		"turtle_helmet":      "turtle_scute",
		"chainmail_boots":    "iron_ingot",
		"shears":             "iron_ingot",
		"fishing_rod":        "stick",
	}
	for tool, want := range cases {
		if got := station.AnvilRepairMaterial(tool); got != want {
			t.Errorf("AnvilRepairMaterial(%q) = %q, want %q", tool, got, want)
		}
	}
	for _, notRepairable := range []string{"stone", "", "minecraft:oak_log"} {
		if got := station.AnvilRepairMaterial(notRepairable); got != "" {
			t.Errorf("AnvilRepairMaterial(%q) = %q, want empty: it cannot be repaired", notRepairable, got)
		}
	}
}

// TestAnvilRepairMaterialIgnoresNamespaces. The name table reports
// "minecraft:iron_pickaxe"; the material table is keyed on the bare name.
func TestAnvilRepairMaterialIgnoresNamespaces(t *testing.T) {
	t.Parallel()

	if got := station.AnvilRepairMaterial("minecraft:iron_pickaxe"); got != "iron_ingot" {
		t.Errorf("AnvilRepairMaterial(minecraft:iron_pickaxe) = %q, want iron_ingot", got)
	}
}

// TestValidateRename is the rule the anvil itself enforces server-side. Sending
// an empty or over-long name is rejected, so the bot should not spend an
// anvil charge finding that out.
func TestValidateRename(t *testing.T) {
	t.Parallel()

	if err := station.ValidateRename("Sword of Testing"); err != nil {
		t.Errorf("ValidateRename(Sword of Testing) = %v, want nil", err)
	}
	if err := station.ValidateRename("a"); err != nil {
		t.Errorf("ValidateRename(a) = %v, want nil", err)
	}

	for _, bad := range []string{"", "   ", "\t\n"} {
		if err := station.ValidateRename(bad); err == nil {
			t.Errorf("ValidateRename(%q) = nil, want an error for a blank name", bad)
		}
	}

	// The anvil caps a custom name at 35 characters; anything longer is refused.
	if err := station.ValidateRename(longName(35)); err != nil {
		t.Errorf("ValidateRename(35 chars) = %v, want nil", err)
	}
	if err := station.ValidateRename(longName(36)); err == nil {
		t.Error("ValidateRename(36 chars) = nil, want an error")
	}

	// Chat formatting is not a legal character in a custom name.
	if err := station.ValidateRename("§lBold"); err == nil {
		t.Error("ValidateRename accepted a section-sign escape in a custom name")
	}
	if err := station.ValidateRename("bad\nname"); err == nil {
		t.Error("ValidateRename accepted a newline in a custom name")
	}
}

func longName(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}

// TestAnvilRepairIs the acceptance test for 4.3: a damaged tool comes back
// repaired, confirmed from the server's own output slot.
func TestAnvilRepairIs(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "anvil")
	bot.addItem(0, 1, "iron_pickaxe", 1)
	bot.addItem(8, 2, "iron_ingot", 3)

	// The anvil computes its result as soon as both inputs are in; the fake
	// fills the output slot on the first poll after that.
	bot.outputAfterReads = 1
	bot.outputSlot = station.AnvilOutputSlot
	bot.outputName = "iron_pickaxe"
	bot.outputNetID = 41

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetAnvilBudget(2 * time.Second)

	result, ok := m.RepairItem(context.Background(), "iron_pickaxe")
	if !ok {
		t.Fatal("RepairItem reported failure for a repair the anvil can make")
	}
	if result.OutputName != "iron_pickaxe" {
		t.Errorf("repair output = %q, want iron_pickaxe", result.OutputName)
	}
	if !result.Taken {
		t.Error("the repaired tool was never taken into the inventory")
	}
	if bot.closedWindow != bot.windowID {
		t.Errorf("closed window %d, want the server-assigned %d", bot.closedWindow, bot.windowID)
	}
}

// TestAnvilRepairFailsWithoutTheMaterial. The anvil simply does not produce an
// output with the wrong second input, so the honest answer is false.
func TestAnvilRepairFailsWithoutTheMaterial(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "anvil")
	bot.addItem(0, 1, "iron_pickaxe", 1)

	// Even with the server willing to answer, there is nothing to repair with.
	bot.outputAfterReads = 1
	bot.outputSlot = station.AnvilOutputSlot
	bot.outputName = "iron_pickaxe"
	bot.outputNetID = 41

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetAnvilBudget(50 * time.Millisecond)

	if _, ok := m.RepairItem(context.Background(), "iron_pickaxe"); ok {
		t.Error("RepairItem reported success with no repair material in the inventory")
	}
}

// TestAnvilRepairFailsWhenTheAnvilProducesNothing. A window opens, both inputs
// go in, and the server never fills the output slot. Reporting a repaired tool
// would be a fabrication.
func TestAnvilRepairFailsWhenTheAnvilProducesNothing(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "anvil")
	bot.addItem(0, 1, "iron_pickaxe", 1)
	bot.addItem(8, 2, "iron_ingot", 3)

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetAnvilBudget(60 * time.Millisecond)

	result, ok := m.RepairItem(context.Background(), "iron_pickaxe")
	if ok {
		t.Error("RepairItem reported success although the anvil produced no output")
	}
	if result.Taken {
		t.Error("RepairItem claimed to have taken an output the server never sent")
	}
	if bot.closedWindow != bot.windowID {
		t.Error("a failed repair must still close the window it opened")
	}
}

// TestAnvilRepairFailsWithoutAnAnvil. The name-based search, once more.
func TestAnvilRepairFailsWithoutAnAnvil(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "stone")
	bot.addItem(0, 1, "iron_pickaxe", 1)
	bot.addItem(8, 2, "iron_ingot", 3)

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)

	if _, ok := m.RepairItem(context.Background(), "iron_pickaxe"); ok {
		t.Error("RepairItem reported success with no anvil nearby")
	}
	if bot.clicked {
		t.Error("RepairItem clicked a block that is not an anvil")
	}
}

// TestRenameItemProducesAVisibleName is the second half of 4.3: the renamed
// tool comes back and its name is the one that was asked for.
func TestRenameItemProducesAVisibleName(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "chipped_anvil")
	bot.addItem(0, 1, "iron_pickaxe", 1)
	bot.addItem(8, 2, "name_tag", 1)

	bot.outputAfterReads = 1
	bot.outputSlot = station.AnvilOutputSlot
	bot.outputName = "Diggers Delight"
	bot.outputNetID = 77

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetAnvilBudget(2 * time.Second)

	result, ok := m.RenameItem(context.Background(), "iron_pickaxe", "Diggers Delight")
	if !ok {
		t.Fatal("RenameItem reported failure for a rename the anvil can make")
	}
	if result.OutputName != "Diggers Delight" {
		t.Errorf("renamed output = %q, want %q", result.OutputName, "Diggers Delight")
	}
	if !result.Taken {
		t.Error("the renamed tool was never taken into the inventory")
	}
}

// TestRenameItemRejectsABlankName before it opens the anvil, so a failed rename
// does not burn an anvil charge.
func TestRenameItemRejectsABlankName(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "anvil")
	bot.addItem(0, 1, "iron_pickaxe", 1)
	bot.addItem(8, 2, "name_tag", 1)

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)

	if _, ok := m.RenameItem(context.Background(), "iron_pickaxe", "   "); ok {
		t.Error("RenameItem reported success for a blank name")
	}
	if bot.clicked {
		t.Error("RenameItem opened the anvil for a name it should have rejected up front")
	}
}

// TestRenameItemFailsWithoutANameTag. A rename with nothing in the second slot
// is a no-op the server will simply not answer.
func TestRenameItemFailsWithoutANameTag(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "anvil")
	bot.addItem(0, 1, "iron_pickaxe", 1)

	bot.outputAfterReads = 1
	bot.outputSlot = station.AnvilOutputSlot
	bot.outputName = "iron_pickaxe"
	bot.outputNetID = 41

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetAnvilBudget(50 * time.Millisecond)

	if _, ok := m.RenameItem(context.Background(), "iron_pickaxe", "New Name"); ok {
		t.Error("RenameItem reported success with no name tag in the inventory")
	}
}

// TestRenameItemFailsWhenTheOutputIsNotRenamed. The server is allowed to
// return the original name if it refused the rename; the manager must not
// report the requested one.
func TestRenameItemFailsWhenTheOutputIsNotRenamed(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(6, 64, 5, "anvil")
	bot.addItem(0, 1, "iron_pickaxe", 1)
	bot.addItem(8, 2, "name_tag", 1)

	// The anvil answers with the tool under its original name.
	bot.outputAfterReads = 1
	bot.outputSlot = station.AnvilOutputSlot
	bot.outputName = "iron_pickaxe"
	bot.outputNetID = 41

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetAnvilBudget(60 * time.Millisecond)

	if _, ok := m.RenameItem(context.Background(), "iron_pickaxe", "Diggers Delight"); ok {
		t.Error("RenameItem reported success although the output kept its old name")
	}
}
