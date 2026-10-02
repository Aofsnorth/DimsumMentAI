package bot_test

import (
	"testing"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// A field can be declared, never assigned, and pass every unit test.
//
// That happened here: craftable was added to the affordance world adapter and
// read but never written, so `Craftable` was always zero, so "craft" became
// permanently illegal in a live run. The suite was green throughout, because the
// affordance tests build their own World and never construct this adapter.
//
// Only a test that builds a real bot — one holding the inputs to a recipe it
// knows — can see the difference between "craft is withheld" and "craft is
// withheld because the adapter is broken". Both look identical from the outside.

// craftingBot is a bot that genuinely can craft: it holds four logs and knows a
// recipe that turns one into a stick.
func craftingBot() *bot.Bot {
	const (
		logID   int32 = 17
		stickID int32 = 5
	)
	return &bot.Bot{
		ItemNames: map[int32]string{
			logID:   "minecraft:oak_log",
			stickID: "minecraft:stick",
		},
		InventoryMap: map[uint32]protocol.ItemStack{
			0: {ItemType: protocol.ItemType{NetworkID: logID}, Count: 4},
		},
		RecipesByNetID: map[uint32]bot.RecipeInfo{
			1: {
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "minecraft:oak_log"}, Count: 1},
				},
				Output: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: stickID}, Count: 4},
			},
		},
	}
}

// TestABotThatCanCraftIsOfferedCraft is the guard that was missing.
//
// A null bot is the wrong probe here: with no world and no recipes, craft is
// correctly withheld, and that reads identically whether the adapter works or is
// dead. A bot holding its inputs is the only probe that tells the two apart.
func TestABotThatCanCraftIsOfferedCraft(t *testing.T) {
	t.Parallel()

	if items := craftingBot().ListCraftableItems(false); len(items) == 0 {
		t.Fatal("the fixture is wrong: the bot holds a log and knows a stick recipe, " +
			"so this failure is in the test, not in the code under test")
	}

	var offered, withheld string
	for _, v := range craftingBot().DeriveAffordances(false).Names() {
		if v == "craft" {
			offered = v
		}
	}
	for _, h := range craftingBot().DeriveAffordances(false).Withheld {
		if h.Label == "craft" {
			withheld = h.Reason
		}
	}

	if offered == "" {
		t.Errorf("a bot holding a log and knowing a stick recipe is not offered "+
			"craft; craft is withheld as %q", withheld)
	}
}

// TestABotWithNothingToCraftIsNotOfferedCraft is the other direction, so the
// guard cannot pass by offering everything.
func TestABotWithNothingToCraftIsNotOfferedCraft(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{}

	for _, h := range b.DeriveAffordances(false).Withheld {
		if h.Label == "craft" && h.Reason == "" {
			t.Error("craft is withheld for no reason: a verb that vanishes " +
				"silently cannot be debugged from a live log")
		}
	}
	for _, v := range b.DeriveAffordances(false).Names() {
		if v == "craft" {
			t.Error("craft offered to a bot with no recipes, no inventory and no world")
		}
	}
}

// TestTheAdapterSurvivesABotWithNoWorld is the reconnect case, which is why the
// adapter is nil-tolerant in the first place. The derivation runs on ticks where
// none of this exists yet.
func TestTheAdapterSurvivesABotWithNoWorld(t *testing.T) {
	t.Parallel()

	if (&bot.Bot{}).DeriveAffordances(false).Names() == nil {
		t.Error("the adapter derived nothing for a bot with no world: " +
			"a nil-safe adapter must still offer the idle self-changing verbs")
	}
}
