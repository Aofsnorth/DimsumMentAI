package station_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/inventory/station"
)

// These tests cover the wiring the station package could not own: the bot-side
// Enchanter that turns a PlayerEnchantOptions packet into options the manager
// can choose from, and that submits the option's recipe network ID as a
// CraftRecipe stack request.
//
// The pure steps are the ones worth pinning. ApplyEnchant itself needs a live
// connection — it blocks on the server's ItemStackResponse — so what is tested
// here is everything it does before and around that call: which option an index
// resolves to, which actions the request is built from, and what the bot reports
// when the server has offered nothing.

// TestWiringEnchantOptionAtRejectsAnIndexTheServerNeverOffered. The manager
// hands back an index into the offered list, and an index the server did not
// offer is a bug upstream, not something to clamp.
func TestWiringEnchantOptionAtRejectsAnIndexTheServerNeverOffered(t *testing.T) {
	t.Parallel()

	opts := []protocol.EnchantmentOption{
		option(3, 2, "animal imbue range", 902),
		option(8, 3, "bless inside creature", 903),
	}

	if _, err := bot.EnchantOptionAt(opts, -1); err == nil {
		t.Error("resolved option -1, want a refusal")
	}
	if _, err := bot.EnchantOptionAt(opts, 2); err == nil {
		t.Error("resolved option 2 of a two-option list, want a refusal")
	}
	if _, err := bot.EnchantOptionAt(nil, 0); err == nil {
		t.Error("resolved option 0 of an empty list, want a refusal")
	}

	got, err := bot.EnchantOptionAt(opts, 1)
	if err != nil {
		t.Fatalf("resolving a valid option: %v", err)
	}
	if got.RecipeNetworkID != 903 {
		t.Errorf("resolved recipe network ID %d, want 903", got.RecipeNetworkID)
	}
}

// TestWiringEnchantOptionAtRejectsAZeroRecipeNetworkID guards the one field the
// enchant actually rides on. RecipeNetworkID 0 is not an enchantment: it is the
// ID space ordinary crafting recipes share, so sending it would ask the server
// for a craft rather than an enchant.
func TestWiringEnchantOptionAtRejectsAZeroRecipeNetworkID(t *testing.T) {
	t.Parallel()

	_, err := bot.EnchantOptionAt([]protocol.EnchantmentOption{option(3, 2, "no id", 0)}, 0)
	if err == nil {
		t.Fatal("accepted an option with recipe network ID 0; that is not an enchantment")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("recipe network ID")) {
		t.Errorf("error %q does not say what was wrong with the option", err)
	}
}

// TestWiringBuildApplyEnchantActionsSubmitsTheOptionRecipeNetworkID is the load
// bearing test for the whole feature. The server identifies which enchantment
// was chosen purely by the recipe network ID inside a CraftRecipe action — the
// item and the lapis are already in the table, so this is the only thing in the
// request that says which of the three buttons was pressed.
func TestWiringBuildApplyEnchantActionsSubmitsTheOptionRecipeNetworkID(t *testing.T) {
	t.Parallel()

	opt := option(3, 2, "animal imbue range", 4242)
	actions := bot.BuildApplyEnchantActions(opt)

	if len(actions) != 1 {
		t.Fatalf("built %d actions for an enchant, want exactly 1 (the CraftRecipe)", len(actions))
	}
	craft, ok := actions[0].(*protocol.CraftRecipeStackRequestAction)
	if !ok {
		t.Fatalf("action is %T, want *protocol.CraftRecipeStackRequestAction", actions[0])
	}
	if craft.RecipeNetworkID != 4242 {
		t.Errorf("submitted recipe network ID %d, want 4242", craft.RecipeNetworkID)
	}
	if craft.NumberOfCrafts != 1 {
		t.Errorf("submitted NumberOfCrafts %d, want 1", craft.NumberOfCrafts)
	}
}

// TestWiringBuildApplyEnchantActionsSendsNothingElse pins the other half. The
// tool and the lapis were moved into the table by their own transfers, which the
// server already confirmed. Repeating the consumption here would spend the
// second lapis and desync the table, so the request is one action and one.
func TestWiringBuildApplyEnchantActionsSendsNothingElse(t *testing.T) {
	t.Parallel()

	for i, action := range bot.BuildApplyEnchantActions(option(1, 1, "a", 7)) {
		if _, ok := action.(*protocol.CraftRecipeStackRequestAction); !ok {
			t.Errorf("action %d is %T, want only CraftRecipe actions", i, action)
		}
	}
}

// TestWiringRecordedOptionsAreWhatTheManagerChoosesFrom closes the loop the
// packet handler opens: the options a PlayerEnchantOptions carried, once
// recorded, are the same list the manager selects against.
func TestWiringRecordedOptionsAreWhatTheManagerChoosesFrom(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	bot.ResetEnchantState(b)
	t.Cleanup(func() { bot.ResetEnchantState(b) })

	offered := []protocol.EnchantmentOption{
		option(1, 1, "bless inside creature", 900),
		option(30, 5, "elder free of inside", 901),
		option(8, 3, "animal imbue range", 902),
	}
	bot.RecordEnchantOptions(b, offered)

	recorded := bot.ReadEnchantOptions(b)
	if len(recorded) != len(offered) {
		t.Fatalf("recorded %d options, want %d", len(recorded), len(offered))
	}
	idx, chosen, ok := station.SelectEnchantOption(recorded, 12)
	if !ok {
		t.Fatal("the manager found nothing affordable among the recorded options")
	}
	if idx != 2 || chosen.RecipeNetworkID != 902 {
		t.Errorf("manager chose option %d (net id %d), want option 2 (net id 902)", idx, chosen.RecipeNetworkID)
	}
}

// TestWiringEnchantOptionsAreEmptyBeforeTheServerOffersAnything. The vanilla
// server sends an empty PlayerEnchantOptions the moment the table opens, and
// nothing at all until then. Both states must read as "nothing to sell" rather
// than as a stale list from a previous table.
func TestWiringEnchantOptionsAreEmptyBeforeTheServerOffersAnything(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	bot.ResetEnchantState(b)
	t.Cleanup(func() { bot.ResetEnchantState(b) })

	if got := bot.ReadEnchantOptions(b); len(got) != 0 {
		t.Errorf("a fresh bot reports %d options, want 0", len(got))
	}

	bot.RecordEnchantOptions(b, []protocol.EnchantmentOption{option(3, 2, "a", 1)})
	bot.RecordEnchantOptions(b, nil)
	if got := bot.ReadEnchantOptions(b); len(got) != 0 {
		t.Errorf("after the server's empty offer the bot reports %d options, want 0", len(got))
	}
}

// TestWiringResetEnchantStateDropsOptionsAcrossSessions. A recipe network ID is
// only meaningful in the world that issued it. A reconnect that keeps the old
// list would let the manager ask the new server for a recipe it never sent.
func TestWiringResetEnchantStateDropsOptionsAcrossSessions(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	bot.ResetEnchantState(b)
	t.Cleanup(func() { bot.ResetEnchantState(b) })

	bot.RecordEnchantOptions(b, []protocol.EnchantmentOption{option(3, 2, "a", 4242)})
	if len(bot.ReadEnchantOptions(b)) == 0 {
		t.Fatal("the recorded options did not take")
	}

	bot.ResetEnchantState(b)
	if got := bot.ReadEnchantOptions(b); len(got) != 0 {
		t.Errorf("after a session reset the bot still reports %d options, want 0", len(got))
	}
}

// TestWiringReadEnchantOptionsReturnsACopy. The caller holds the slice while the
// packet handler may still be writing a new list, so a shared backing array
// would be a data race that shows up as an option quietly changing cost.
func TestWiringReadEnchantOptionsReturnsACopy(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	bot.ResetEnchantState(b)
	t.Cleanup(func() { bot.ResetEnchantState(b) })

	bot.RecordEnchantOptions(b, []protocol.EnchantmentOption{option(3, 2, "a", 1)})

	first := bot.ReadEnchantOptions(b)
	first[0].Cost = 200
	first[0].RecipeNetworkID = 9999

	second := bot.ReadEnchantOptions(b)
	if second[0].Cost != 3 || second[0].RecipeNetworkID != 1 {
		t.Errorf("mutating the returned slice changed the stored options: cost %d, net id %d",
			second[0].Cost, second[0].RecipeNetworkID)
	}
}

// TestWiringRecordEnchantOptionsKeepsTheServersOrder. The manager's index is the
// index into this list, so reordering it silently changes which option an index
// buys.
func TestWiringRecordEnchantOptionsKeepsTheServersOrder(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	bot.ResetEnchantState(b)
	t.Cleanup(func() { bot.ResetEnchantState(b) })

	offered := []protocol.EnchantmentOption{
		option(1, 1, "first", 100),
		option(2, 2, "second", 200),
		option(3, 3, "third", 300),
	}
	bot.RecordEnchantOptions(b, offered)

	got := bot.ReadEnchantOptions(b)
	for i, want := range offered {
		if got[i].RecipeNetworkID != want.RecipeNetworkID {
			t.Errorf("option %d is net id %d, want %d", i, got[i].RecipeNetworkID, want.RecipeNetworkID)
		}
	}
}

// TestWiringPlayerEnchantOptionsSurvivesAWireRoundTrip is the test that says the
// payload is the one the server actually sends. The handler is only as good as
// the struct it is handed, so the packet is marshalled the way the connection
// marshals it and read back the way the connection reads it.
func TestWiringPlayerEnchantOptionsSurvivesAWireRoundTrip(t *testing.T) {
	t.Parallel()

	original := packet.PlayerEnchantOptions{Options: []protocol.EnchantmentOption{
		option(1, 1, "bless inside creature", 900),
		option(30, 5, "elder free of inside", 901),
	}}

	var buf bytes.Buffer
	original.Marshal(protocol.NewWriter(&buf, 0))
	if buf.Len() == 0 {
		t.Fatal("marshalling PlayerEnchantOptions produced no bytes")
	}

	var got packet.PlayerEnchantOptions
	got.Marshal(protocol.NewReader(bytes.NewReader(buf.Bytes()), 0, false))

	if len(got.Options) != len(original.Options) {
		t.Fatalf("read back %d options, want %d", len(got.Options), len(original.Options))
	}
	for i, want := range original.Options {
		if got.Options[i].RecipeNetworkID != want.RecipeNetworkID {
			t.Errorf("option %d round tripped as net id %d, want %d",
				i, got.Options[i].RecipeNetworkID, want.RecipeNetworkID)
		}
		if got.Options[i].Cost != want.Cost {
			t.Errorf("option %d round tripped at cost %d, want %d", i, got.Options[i].Cost, want.Cost)
		}
	}

	// The handler in network/player is keyed on this ID; a drift here means the
	// packet is dispatched nowhere.
	if original.ID() != packet.IDPlayerEnchantOptions {
		t.Errorf("PlayerEnchantOptions reports id %d, want IDPlayerEnchantOptions", original.ID())
	}
}

// TestWiringEnchantTableRefusesAnEnchantNothingHasBeenOffered. This is the
// honest-failure rule in its purest form: with no options recorded there is no
// enchantment to apply, and the seam says so instead of picking one. A zero Bot
// has no connection, so this also pins that the refusal happens before anything
// tries to write a packet.
func TestWiringEnchantTableRefusesAnEnchantNothingHasBeenOffered(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	bot.ResetEnchantState(b)
	t.Cleanup(func() { bot.ResetEnchantState(b) })

	seam := bot.EnchantTable{B: b}
	if err := seam.ApplyEnchant(7, 0); err == nil {
		t.Fatal("ApplyEnchant succeeded with no options on offer")
	}
	if got := seam.EnchantOptions(); len(got) != 0 {
		t.Errorf("the seam reports %d options, want 0", len(got))
	}
}

// TestWiringApplyEnchantRejectsAnIndexTheServerNeverOffered. Same rule one step
// later: an out-of-range index must be refused, not clamped to the last option.
func TestWiringApplyEnchantRejectsAnIndexTheServerNeverOffered(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	bot.ResetEnchantState(b)
	t.Cleanup(func() { bot.ResetEnchantState(b) })
	bot.RecordEnchantOptions(b, []protocol.EnchantmentOption{option(3, 2, "a", 4242)})

	seam := bot.EnchantTable{B: b}
	if err := seam.ApplyEnchant(7, 5); err == nil {
		t.Fatal("ApplyEnchant accepted an index past the end of the offered list")
	}
	if err := seam.ApplyEnchant(7, -1); err == nil {
		t.Fatal("ApplyEnchant accepted a negative index")
	}
}

// TestWiringApplyEnchantRejectsAZeroRecipeNetworkID. Reaching the write path with
// a zero ID would ask the server for an ordinary craft instead of an enchant, so
// the refusal has to happen while the option is still in hand.
func TestWiringApplyEnchantRejectsAZeroRecipeNetworkID(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	bot.ResetEnchantState(b)
	t.Cleanup(func() { bot.ResetEnchantState(b) })
	bot.RecordEnchantOptions(b, []protocol.EnchantmentOption{option(3, 2, "a", 0)})

	seam := bot.EnchantTable{B: b}
	if err := seam.ApplyEnchant(7, 0); err == nil {
		t.Fatal("ApplyEnchant accepted an option with no recipe network ID")
	}
}

// TestWiringEnchantTableSatisfiesTheStationSeam is the wiring test in one line.
// If this stops compiling, the manager is back to refusing every enchant.
func TestWiringEnchantTableSatisfiesTheStationSeam(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	seam := bot.EnchantTable{B: b}
	manager := station.NewManager(newFakeBot(), discardLogger())
	manager.SetEnchanter(seam)
}

// TestWiringEnchantTableReportsAnUnknownExperienceLevelAsUnaffordable. The
// interface asks for a plain int, so "never observed" has to be encoded in the
// value. Zero would be a lie that lets a level-30 option be chosen by a bot that
// has never been told its level; -1 is unaffordable by every option's cost, so
// the manager refuses instead.
func TestWiringEnchantTableReportsAnUnknownExperienceLevelAsUnaffordable(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	seam := bot.EnchantTable{B: b}

	if got := seam.ExperienceLevel(); got >= 0 {
		t.Errorf("a bot with no observed level reports %d, want a negative value that no option is affordable at", got)
	}
	if _, _, ok := station.SelectEnchantOption([]protocol.EnchantmentOption{option(0, 1, "free", 1)}, seam.ExperienceLevel()); ok {
		t.Error("an option was selected against a level the server never sent")
	}
}

// TestWiringEnchantTableReportsAnObservedExperienceLevel. The positive half of
// the same seam: once UpdateAttributes has been folded in, the level the manager
// budgets against is the server's.
func TestWiringEnchantTableReportsAnObservedExperienceLevel(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	seam := bot.EnchantTable{B: b}

	b.SetExperienceLevel(30)
	if got := seam.ExperienceLevel(); got != 30 {
		t.Errorf("reported level %d, want 30", got)
	}

	idx, _, ok := station.SelectEnchantOption([]protocol.EnchantmentOption{
		option(1, 1, "weak", 1),
		option(28, 4, "strong", 2),
	}, seam.ExperienceLevel())
	if !ok {
		t.Fatal("nothing was affordable at level 30")
	}
	if idx != 1 {
		t.Errorf("selected option %d, want 1 (the stronger affordable one)", idx)
	}
}

// TestWiringTheStationManagerIsActuallyDrivenByTheSeam is the test that would
// have caught this whole gap.
//
// With no enchanter, EnchantItem returns before it ever looks for a table, so the
// bot is never clicked. With the real seam wired it walks the whole opening path
// and places the tool into the enchanting input container — which is observable
// on the fake, and is the difference between a table that is dead at runtime and
// one that is merely unfinished.
//
// It still has to report failure, and it does: this Bot has no connection and no
// lapis, so the seam refuses to buy an enchant and no option is ever applied. The
// assertion that matters is the pairing — the manager got all the way to the
// seam, and still declined to claim an enchantment.
func TestWiringTheStationManagerIsActuallyDrivenByTheSeam(t *testing.T) {
	t.Parallel()

	var b *bot.Bot = &bot.Bot{}
	bot.ResetEnchantState(b)
	t.Cleanup(func() { bot.ResetEnchantState(b) })

	fake := newFakeBot()
	fake.addBlock(5, 64, 5, "enchanting_table")
	fake.addItem(0, 1, "diamond_sword", 1)
	fake.addItem(12, 2, "lapis_lazuli", 3)

	m := station.NewManager(fake, discardLogger())
	m.SetEnchanter(bot.EnchantTable{B: b})
	m.SetPollInterval(time.Millisecond)
	m.SetEnchantBudget(50 * time.Millisecond)

	result, ok := m.EnchantItem(context.Background(), "diamond_sword")
	if ok {
		t.Fatal("EnchantItem reported an enchant from a bot with no connection and no options on offer")
	}
	if result.Taken {
		t.Error("EnchantItem reported taking an enchanted item it never received")
	}

	// The wiring is proven by the work the manager did before it stopped.
	if !fake.clicked {
		t.Error("the manager never clicked the table; the enchanter seam is still unwired")
	}
	placed := fake.placedSlots()
	if len(placed) == 0 {
		t.Fatal("the manager placed nothing; it never reached the table's input slot")
	}
	if placed[0].containerID != station.EnchantContainerID || placed[0].slot != station.EnchantInputSlot {
		t.Errorf("placed into (container %d, slot %d), want (container %d, slot %d)",
			placed[0].containerID, placed[0].slot, station.EnchantContainerID, station.EnchantInputSlot)
	}
}

// TestWiringEnchantContainersAreTheProtocolOnes pins the two container IDs the
// table's slots resolve against. They are not the window ID the server assigned,
// and a transfer addressed by the wrong one is refused server-side while reading
// locally as a clean success, so the numbers are worth having in a test.
func TestWiringEnchantContainersAreTheProtocolOnes(t *testing.T) {
	t.Parallel()

	if got := int(station.EnchantContainerID); got != 22 {
		t.Errorf("EnchantContainerID = %d, want protocol.ContainerEnchantingInput (22)", got)
	}
	if got := int(station.EnchantMaterialContainerID); got != 23 {
		t.Errorf("EnchantMaterialContainerID = %d, want protocol.ContainerEnchantingMaterial (23)", got)
	}
	if station.EnchantContainerID == station.EnchantMaterialContainerID {
		t.Error("the tool and the lapis resolve to the same container; they are separate slots")
	}
	if station.EnchantInputSlot != 0 || station.EnchantMaterialSlot != 1 {
		t.Errorf("enchanting slots are %d/%d, want 0 (input) and 1 (material)",
			station.EnchantInputSlot, station.EnchantMaterialSlot)
	}
}
