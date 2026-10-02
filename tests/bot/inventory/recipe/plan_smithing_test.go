package recipe_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/inventory/recipe"
)

// smithingRecipes is what a server advertises for a smithing table: one
// transform recipe per diamond item.
func smithingRecipes() []recipe.StationRecipe {
	return []recipe.StationRecipe{
		{NetworkID: 101, Block: "smithing_table", ResultName: "netherite_sword", ResultCount: 1},
		{NetworkID: 102, Block: "smithing_table", ResultName: "netherite_chestplate", ResultCount: 1},
		{NetworkID: 103, Block: "crafting_table", ResultName: "netherite_sword", ResultCount: 1},
	}
}

// TestNetheriteUpgradePlansTemplateBaseAndAddition is acceptance 4.5. The smithing
// transform has three descriptors and the plan has to fill all three, in the
// order the station declares them, or the result preview never appears.
func TestNetheriteUpgradePlansTemplateBaseAndAddition(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "netherite_upgrade_smithing_template", Count: 1},
		{Slot: 4, Name: "diamond_sword", Count: 1},
		{Slot: 7, Name: "netherite_ingot", Count: 4},
	}
	plan, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
	if err != nil {
		t.Fatalf("PlanNetheriteUpgrade() error = %v", err)
	}
	if plan.Station.Kind != recipe.KindSmithingTable {
		t.Fatalf("plan station = %q, want smithing_table", plan.Station.Kind)
	}
	if plan.ResultName != "netherite_sword" {
		t.Errorf("ResultName = %q, want netherite_sword", plan.ResultName)
	}
	if plan.ResultCount != 1 {
		t.Errorf("ResultCount = %d, want 1", plan.ResultCount)
	}
	if plan.RecipeNetworkID != 101 {
		t.Errorf("RecipeNetworkID = %d, want the smithing-table recipe 101, not the crafting-table one 103", plan.RecipeNetworkID)
	}
	if !plan.UsesRecipe() {
		t.Error("UsesRecipe() = false for a smithing plan")
	}
	if plan.Pattern != "" {
		t.Errorf("Pattern = %q; only the loom sends a pattern", plan.Pattern)
	}

	want := []struct {
		role   recipe.Role
		source uint32
		item   string
		count  int
	}{
		{recipe.RoleTemplate, 0, "netherite_upgrade_smithing_template", 1},
		{recipe.RoleBase, 4, "diamond_sword", 1},
		{recipe.RoleAddition, 7, "netherite_ingot", 1},
	}
	if len(plan.Inputs) != len(want) {
		t.Fatalf("plan has %d inputs, want %d: %+v", len(plan.Inputs), len(want), plan.Inputs)
	}
	for i, w := range want {
		got := plan.Inputs[i]
		if got.Slot.Role != w.role {
			t.Errorf("input %d role = %q, want %q", i, got.Slot.Role, w.role)
		}
		if got.SourceSlot != w.source || got.ItemName != w.item || got.Count != w.count {
			t.Errorf("input %d = %+v, want slot %d %q x%d", i, got, w.source, w.item, w.count)
		}
	}
}

// TestNetheriteUpgradeConsumesExactlyOneTemplate is the rule that decides whether
// the bot's template count stays honest. The template is eaten by the craft, so
// a plan that staged three of them would either be rejected or leave the server
// and the bag disagreeing about how many templates exist.
func TestNetheriteUpgradeConsumesExactlyOneTemplate(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "netherite_upgrade_smithing_template", Count: 5},
		{Slot: 4, Name: "diamond_sword", Count: 1},
		{Slot: 7, Name: "netherite_ingot", Count: 4},
	}
	plan, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
	if err != nil {
		t.Fatalf("PlanNetheriteUpgrade() error = %v", err)
	}
	tmpl, ok := plan.Input(recipe.RoleTemplate)
	if !ok {
		t.Fatal("the plan staged no template")
	}
	if tmpl.Count != 1 {
		t.Errorf("staged %d templates, want exactly 1 consumed per craft", tmpl.Count)
	}
}

// TestNetheriteUpgradeNeedsTheTemplate is the failure this prevents: a bot that
// upgrades through a smithing table holding only a diamond sword burns nothing,
// gets nothing, and reports a netherite sword it never has.
func TestNetheriteUpgradeNeedsTheTemplate(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 4, Name: "diamond_sword", Count: 1},
		{Slot: 7, Name: "netherite_ingot", Count: 4},
	}
	_, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
	if err == nil {
		t.Fatal("a smithing plan was produced with no upgrade template in the bag")
	}
	if !strings.Contains(err.Error(), "template") {
		t.Errorf("error = %v, want it to name the missing template", err)
	}
}

// TestNetheriteUpgradeNeedsTheIngot catches the mirror image: a template and a
// diamond sword are not enough. The addition is the netherite ingot.
func TestNetheriteUpgradeNeedsTheIngot(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "netherite_upgrade_smithing_template", Count: 1},
		{Slot: 4, Name: "diamond_sword", Count: 1},
	}
	_, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
	if err == nil {
		t.Fatal("a smithing plan was produced with no netherite ingot")
	}
	if !strings.Contains(err.Error(), "netherite_ingot") {
		t.Errorf("error = %v, want it to name the netherite ingot", err)
	}
}

// TestNetheriteUpgradeAcceptsBothTemplateSpellings covers the rename: 1.19.4 -
// 1.20.0 called the item "minecraft:netherite_upgrade" and 1.20.1 calls it
// "minecraft:netherite_upgrade_smithing_template". A bot that only knows the
// new name is useless on an older server, and the vendored protocol carries no
// item palette to arbitrate.
func TestNetheriteUpgradeAcceptsBothTemplateSpellings(t *testing.T) {
	t.Parallel()

	for _, template := range recipe.NetheriteUpgradeTemplateNames {
		inv := recipe.Inventory{
			{Slot: 0, Name: template, Count: 1},
			{Slot: 4, Name: "diamond_sword", Count: 1},
			{Slot: 7, Name: "netherite_ingot", Count: 1},
		}
		plan, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
		if err != nil {
			t.Errorf("template %q was not accepted: %v", template, err)
			continue
		}
		tmpl, _ := plan.Input(recipe.RoleTemplate)
		if tmpl.ItemName != template {
			t.Errorf("template staged as %q, want %q", tmpl.ItemName, template)
		}
	}
}

// TestNetheriteUpgradePicksTheArmourTheBotCarries stops a plan from announcing a
// sword the bot does not have while a full set of diamond armour is sitting in
// the bag.
func TestNetheriteUpgradePicksTheArmourTheBotCarries(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 1, Name: "netherite_upgrade", Count: 2},
		{Slot: 5, Name: "diamond_chestplate", Count: 1},
		{Slot: 6, Name: "netherite_ingot", Count: 9},
	}
	plan, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
	if err != nil {
		t.Fatalf("PlanNetheriteUpgrade() error = %v", err)
	}
	if plan.ResultName != "netherite_chestplate" {
		t.Errorf("ResultName = %q, want netherite_chestplate", plan.ResultName)
	}
	base, _ := plan.Input(recipe.RoleBase)
	if base.ItemName != "diamond_chestplate" {
		t.Errorf("base = %q, want diamond_chestplate", base.ItemName)
	}
}

// TestNetheriteUpgradeNeverUpgradesAnAlreadyUpgradedItem is the double-upgrade
// guard. A bag holding netherite gear and a template has nothing to upgrade, and
// a planner that matched on the "netherite_" prefix would stage a netherite
// sword as a base and produce a nonsense craft.
func TestNetheriteUpgradeNeverUpgradesAnAlreadyUpgradedItem(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "netherite_upgrade_smithing_template", Count: 1},
		{Slot: 4, Name: "netherite_sword", Count: 1},
		{Slot: 7, Name: "netherite_ingot", Count: 4},
	}
	_, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
	if err == nil {
		t.Fatal("an already-netherite item was staged as an upgrade base")
	}
}

// TestNetheriteUpgradeIsDeterministic pins the "first match in catalogue order"
// rule. Two calls on the same bag must produce byte-identical plans, or the
// smithing table sees a different base every time the bot retries.
func TestNetheriteUpgradeIsDeterministic(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "netherite_upgrade_smithing_template", Count: 1},
		{Slot: 4, Name: "diamond_sword", Count: 1},
		{Slot: 5, Name: "diamond_pickaxe", Count: 1},
		{Slot: 7, Name: "netherite_ingot", Count: 4},
	}
	first, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
	if err != nil {
		t.Fatalf("PlanNetheriteUpgrade() error = %v", err)
	}
	for i := 0; i < 25; i++ {
		got, err := recipe.PlanNetheriteUpgrade(inv, smithingRecipes())
		if err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if got.ResultName != first.ResultName || len(got.Inputs) != len(first.Inputs) {
			t.Fatalf("attempt %d produced %+v, first produced %+v", i, got.Inputs, first.Inputs)
		}
		base, _ := got.Input(recipe.RoleBase)
		firstBase, _ := first.Input(recipe.RoleBase)
		if base.SourceSlot != firstBase.SourceSlot {
			t.Fatalf("attempt %d staged base slot %d, first staged %d", i, base.SourceSlot, firstBase.SourceSlot)
		}
	}
}

// TestNetheriteUpgradeNeedsAServerRecipe keeps the plan from being built on a
// catalogue alone. Without the recipe network ID there is nothing to send, and
// a plan carrying zero would have the bot stage a transform the server never
// validates.
func TestNetheriteUpgradeNeedsAServerRecipe(t *testing.T) {
	t.Parallel()

	inv := recipe.Inventory{
		{Slot: 0, Name: "netherite_upgrade_smithing_template", Count: 1},
		{Slot: 4, Name: "diamond_sword", Count: 1},
		{Slot: 7, Name: "netherite_ingot", Count: 1},
	}
	_, err := recipe.PlanNetheriteUpgrade(inv, nil)
	if err == nil {
		t.Fatal("a smithing plan was built with no recipe the server advertised")
	}
}

// TestTemplateMatchesRejectsUnrelatedItems keeps the prefix match on the
// netherite template from swallowing something else. Only the two known
// spellings may match.
func TestTemplateMatchesRejectsUnrelatedItems(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"minecraft:netherite_upgrade_smithing_template",
		"netherite_upgrade",
		"NETHERITE_UPGRADE",
	} {
		if !recipe.TemplateMatches(name) {
			t.Errorf("TemplateMatches(%q) = false", name)
		}
	}
	for _, name := range []string{"", "  ", "netherite_ingot", "netherite_sword", "upgrade"} {
		if recipe.TemplateMatches(name) {
			t.Errorf("TemplateMatches(%q) = true, want false", name)
		}
	}
}

// TestNetheriteUpgradesCoverEveryDiamondTool is the catalogue behind 4.5. Every
// entry names a real diamond -> netherite pair and asks for exactly one
// template, so a hole in the table is a hole in what the bot can ever upgrade.
func TestNetheriteUpgradesCoverEveryDiamondTool(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"diamond_sword":      "netherite_sword",
		"diamond_pickaxe":    "netherite_pickaxe",
		"diamond_axe":        "netherite_axe",
		"diamond_shovel":     "netherite_shovel",
		"diamond_hoe":        "netherite_hoe",
		"diamond_helmet":     "netherite_helmet",
		"diamond_chestplate": "netherite_chestplate",
		"diamond_leggings":   "netherite_leggings",
		"diamond_boots":      "netherite_boots",
	}
	upgrades := recipe.NetheriteUpgrades()
	if len(upgrades) != len(want) {
		t.Fatalf("catalogue has %d upgrades, want %d", len(upgrades), len(want))
	}
	for _, up := range upgrades {
		if want[up.Base] != up.Result {
			t.Errorf("upgrade %q -> %q, want %q", up.Base, up.Result, want[up.Base])
		}
		if up.TemplateConsumed != 1 {
			t.Errorf("upgrade %q consumes %d templates, want 1", up.Base, up.TemplateConsumed)
		}
	}
}
