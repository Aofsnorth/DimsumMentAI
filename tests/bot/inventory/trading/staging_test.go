package trading_test

import (
	"context"
	"testing"

	"bedrock-ai/internal/bot/inventory/trading"

	"github.com/go-gl/mathgl/mgl32"
)

// TestTradeAddressesTheTradeContainersNotTheWindowID is the failure the whole
// layout file exists for. A station transfer addressed by window ID is refused
// by the server while the code around it reads as though it worked.
func TestTradeAddressesTheTradeContainersNotTheWindowID(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	if _, ok := m.Trade(context.Background(), "bread"); !ok {
		t.Fatal("Trade failed; the container IDs under test are what it used")
	}

	if len(bot.places) != 1 {
		t.Fatalf("recorded %d placements, want 1", len(bot.places))
	}
	got := bot.places[0]
	if got.containerID != trading.IngredientOneContainerID {
		t.Errorf("placed into container %d, want ContainerTradeIngredientOne (%d)",
			got.containerID, trading.IngredientOneContainerID)
	}
	if got.slot != trading.SlotIngredientOne {
		t.Errorf("placed into window slot %d, want %d", got.slot, trading.SlotIngredientOne)
	}
	if byte(got.containerID) == bot.openWindowID {
		t.Error("the placement used the window ID as a container ID")
	}

	if len(bot.takes) != 1 {
		t.Fatalf("recorded %d takes, want 1", len(bot.takes))
	}
	take := bot.takes[0]
	if take.containerID != trading.ResultContainerID {
		t.Errorf("took from container %d, want ContainerTradeResultPreview (%d)",
			take.containerID, trading.ResultContainerID)
	}
	if take.slot != trading.SlotResult {
		t.Errorf("took from window slot %d, want %d", take.slot, trading.SlotResult)
	}
	if byte(take.containerID) == bot.openWindowID {
		t.Error("the take used the window ID as a container ID")
	}
}

// TestTradeStagesOnlyTheOfferedInputs. A one-input trade must not put anything
// into the second ingredient slot, which would stage a trade the villager did
// not offer.
func TestTradeStagesOnlyTheOfferedInputs(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	if _, ok := m.Trade(context.Background(), "bread"); !ok {
		t.Fatal("Trade failed")
	}
	for _, p := range bot.places {
		if p.slot == trading.SlotIngredientTwo {
			t.Errorf("staged into the second ingredient slot: %+v", p)
		}
	}
}

// TestTradeArmsTheContainerWatchBeforeInteracting is the timing rule. A
// ContainerOpen can land within a frame of the interaction packet; a watch
// armed afterwards misses the window entirely and the trade never opens.
func TestTradeArmsTheContainerWatchBeforeInteracting(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	if _, ok := m.Trade(context.Background(), "bread"); !ok {
		t.Fatal("Trade failed")
	}

	watch := indexOfCall(bot.calls, "BeginContainerWatch")
	interact := indexOfCall(bot.calls, "WritePacket")
	if watch < 0 {
		t.Fatal("BeginContainerWatch was never called")
	}
	if interact < 0 {
		t.Fatal("no interaction packet was sent")
	}
	if watch > interact {
		t.Errorf("BeginContainerWatch ran at call %d, after the interaction at %d", watch, interact)
	}
}

// TestTradeClosesTheWindowItOpened. A window left open server-side desyncs the
// next one, and every other viewer sees the trade UI hanging there.
func TestTradeClosesTheWindowItOpened(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	if _, ok := m.Trade(context.Background(), "bread"); !ok {
		t.Fatal("Trade failed")
	}
	if len(bot.closed) != 1 {
		t.Fatalf("closed %d windows, want 1", len(bot.closed))
	}
	if bot.closed[0] != bot.openWindowID {
		t.Errorf("closed window %d, want the server-assigned %d", bot.closed[0], bot.openWindowID)
	}
}

// TestTradeUsesTheWindowIDTheServerAssigned. The window is never assumed to be
// 0; a host that assigns 42 must be closed on 42.
func TestTradeUsesTheWindowIDTheServerAssigned(t *testing.T) {
	t.Parallel()

	bot, obs, m := tradeFixture()
	bot.openWindowID = 7
	tw := villagerWindow()
	tw.WindowID = 7
	obs.windows[testVillagerID] = tw

	res, ok := m.Trade(context.Background(), "bread")
	if !ok {
		t.Fatalf("Trade failed: %s", res.Reason)
	}
	if res.WindowID != 7 {
		t.Errorf("WindowID = %d, want 7", res.WindowID)
	}
	if len(bot.closed) != 1 || bot.closed[0] != 7 {
		t.Errorf("closed %v, want [7]", bot.closed)
	}
}

// TestTradeStagesBothInputsOfATwoInputOffer is the other half of the layout
// test: the second ingredient goes into the second container.
func TestTradeStagesBothInputsOfATwoInputOffer(t *testing.T) {
	t.Parallel()

	bot, obs, m := tradeFixture()
	bot.putItem(1, "minecraft:book", 1)
	tw := villagerWindow()
	tw.Offers = []trading.Offer{{
		Inputs: []trading.Item{
			{Name: "minecraft:emerald", Count: 1},
			{Name: "minecraft:book", Count: 1},
		},
		Output:  trading.Item{Name: "minecraft:bread", Count: 8},
		MaxUses: 16,
	}}
	obs.windows[testVillagerID] = tw
	bot.resultName = "minecraft:bread"

	res, ok := m.Trade(context.Background(), "bread")
	if !ok {
		t.Fatalf("Trade failed: %s", res.Reason)
	}
	if len(bot.places) != 2 {
		t.Fatalf("staged %d inputs, want 2: %+v", len(bot.places), bot.places)
	}
	if bot.places[0].containerID != trading.IngredientOneContainerID {
		t.Errorf("first input went to container %d, want %d", bot.places[0].containerID, trading.IngredientOneContainerID)
	}
	if bot.places[1].containerID != trading.IngredientTwoContainerID {
		t.Errorf("second input went to container %d, want %d", bot.places[1].containerID, trading.IngredientTwoContainerID)
	}
	if bot.places[1].slot != trading.SlotIngredientTwo {
		t.Errorf("second input went to window slot %d, want %d", bot.places[1].slot, trading.SlotIngredientTwo)
	}
	if got := bot.countItem("minecraft:book"); got != 0 {
		t.Errorf("books = %d after the trade, want 0 (one spent)", got)
	}
	if len(res.Spent) != 2 {
		t.Errorf("Spent = %+v, want both inputs", res.Spent)
	}
}

// TestTradeWalksToAVillagerOutOfReach. A villager across the room is walked to
// before it is clicked, because a click from three blocks away opens nothing.
func TestTradeWalksToAVillagerOutOfReach(t *testing.T) {
	t.Parallel()

	bot, _, m := tradeFixture()
	bot.entities[testVillagerID].Position = mgl32.Vec3{6, 0, 6}

	if _, ok := m.Trade(context.Background(), "bread"); !ok {
		t.Fatal("Trade failed")
	}
	if len(bot.navigated) == 0 {
		t.Error("Trade clicked a villager it had not walked to")
	}
}
