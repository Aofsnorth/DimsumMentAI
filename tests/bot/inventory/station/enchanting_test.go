package station_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"

	"bedrock-ai/internal/bot/inventory/station"
)

// fakeEnchanter stands in for the bot side of the enchanting table. The vendored
// gophertunnel protocol does expose packet.PlayerEnchantOptions and
// protocol.EnchantmentOption, but internal/bot has no handler for that packet
// and no method that submits a CraftRecipe stack request against
// ContainerEnchantingInput — so the manager talks to a seam instead. Wiring the
// real *bot.Bot to this seam is the follow-up task.
type fakeEnchanter struct {
	mu sync.Mutex

	options   []protocol.EnchantmentOption
	level     int
	lapis     int
	applyErr  error
	applied   []int
	windowIDs []byte
	// bot lets afterApply push the enchanted item back into the table's input
	// slot, standing in for the server-side result of a successful enchant.
	bot        *fakeBot
	afterApply func(bot *fakeBot)
}

func (f *fakeEnchanter) EnchantOptions() []protocol.EnchantmentOption {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]protocol.EnchantmentOption, len(f.options))
	copy(out, f.options)
	return out
}

func (f *fakeEnchanter) ExperienceLevel() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.level
}

// PlaceLapisInEnchantingTable models the lapis that has to sit in the material
// slot for an option to be affordable.
func (f *fakeEnchanter) PlaceLapisInEnchantingTable(windowID byte, count int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windowIDs = append(f.windowIDs, windowID)
	f.lapis += count
	return nil
}

func (f *fakeEnchanter) ApplyEnchant(windowID byte, optionIndex int) error {
	f.mu.Lock()
	applyErr := f.applyErr
	opts := f.options
	f.mu.Unlock()

	if applyErr != nil {
		return applyErr
	}
	if optionIndex < 0 || optionIndex >= len(opts) {
		return errors.New("option index out of range")
	}

	f.mu.Lock()
	f.applied = append(f.applied, optionIndex)
	f.windowIDs = append(f.windowIDs, windowID)
	f.level -= int(opts[optionIndex].Cost)
	afterApply, bot := f.afterApply, f.bot
	f.mu.Unlock()

	if afterApply != nil && bot != nil {
		afterApply(bot)
	}
	return nil
}

func (f *fakeEnchanter) appliedIndexes() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]int, len(f.applied))
	copy(out, f.applied)
	return out
}

func option(cost uint8, power byte, name string, recipeID uint32) protocol.EnchantmentOption {
	opt := protocol.EnchantmentOption{Cost: cost, Name: name, RecipeNetworkID: recipeID}
	opt.Enchantments.Enchantments[1] = []protocol.EnchantmentInstance{{Type: 9, Level: power}}
	return opt
}

var (
	_ station.Enchanter = (*fakeEnchanter)(nil)
)

// TestSelectEnchantOptionPrefersTheBestAffordableOne. An enchanting table
// offers three options and a bot with 12 levels should not burn them on the
// cheapest one when the strongest is affordable.
func TestSelectEnchantOptionPrefersTheBestAffordableOne(t *testing.T) {
	t.Parallel()

	opts := []protocol.EnchantmentOption{
		option(1, 1, "bless inside creature", 900),
		option(30, 5, "elder free of inside", 901),
		option(8, 3, "animal imbue range", 902),
	}

	idx, chosen, ok := station.SelectEnchantOption(opts, 12)
	if !ok {
		t.Fatal("no option selected even though two are affordable")
	}
	if idx != 2 {
		t.Errorf("selected option %d, want 2 (the strongest affordable one)", idx)
	}
	if chosen.Cost != 8 {
		t.Errorf("selected cost = %d, want 8", chosen.Cost)
	}
}

// TestSelectEnchantOptionRefusesWhenNothingIsAffordable. Spending levels the
// bot does not have is rejected by the server, and a manager that picked anyway
// would report an enchant that never happened.
func TestSelectEnchantOptionRefusesWhenNothingIsAffordable(t *testing.T) {
	t.Parallel()

	opts := []protocol.EnchantmentOption{
		option(30, 5, "elder free of inside", 901),
		option(45, 6, "free inside elder", 902),
	}
	if idx, _, ok := station.SelectEnchantOption(opts, 3); ok {
		t.Errorf("selected option %d at level 3, want no selection", idx)
	}
	if _, _, ok := station.SelectEnchantOption(nil, 30); ok {
		t.Error("selected an option from an empty list")
	}
	if _, _, ok := station.SelectEnchantOption(opts, -1); ok {
		t.Error("selected an option at a negative XP level")
	}
}

// TestSelectEnchantOptionTiesPreferTheLowerCost. Two options at the same level
// cost should cost the bot the same either way, but the cheaper one leaves more
// XP for the next enchant.
func TestSelectEnchantOptionTiesPreferTheLowerCost(t *testing.T) {
	t.Parallel()

	cheap := option(5, 2, "a", 1)
	free := option(0, 2, "b", 2)
	opts := []protocol.EnchantmentOption{cheap, free}

	idx, chosen, ok := station.SelectEnchantOption(opts, 10)
	if !ok {
		t.Fatal("no option selected")
	}
	if idx != 1 || chosen.Cost != 0 {
		t.Errorf("selected option %d (cost %d), want option 1 (cost 0)", idx, chosen.Cost)
	}
}

// TestEnchantItemConsumesLevelsAndLapis is the acceptance test for 4.2: a sword
// is enchanted and the XP and lapis are actually spent.
func TestEnchantItemConsumesLevelsAndLapis(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(5, 64, 5, "enchanting_table")
	bot.addItem(0, 1, "diamond_sword", 1)
	bot.addItem(12, 2, "lapis_lazuli", 3)

	enc := &fakeEnchanter{
		options: []protocol.EnchantmentOption{option(3, 2, "animal imbue range", 902)},
		level:   30,
		bot:     bot,
	}
	enc.afterApply = func(b *fakeBot) {
		b.addItem(60, 55, "diamond_sword", 1)
		b.addContainer(station.EnchantInputSlot, 55, "diamond_sword", 1)
	}

	m := station.NewManager(bot, discardLogger())
	m.SetEnchanter(enc)
	m.SetPollInterval(time.Millisecond)
	m.SetEnchantBudget(2 * time.Second)

	result, ok := m.EnchantItem(context.Background(), "diamond_sword")
	if !ok {
		t.Fatal("EnchantItem reported failure for an affordable option")
	}
	if result.OptionIndex != 0 {
		t.Errorf("enchanted with option %d, want 0", result.OptionIndex)
	}
	if result.LevelsSpent <= 0 {
		t.Errorf("EnchantItem spent %d levels, want the option cost to be charged", result.LevelsSpent)
	}
	if enc.level >= 30 {
		t.Errorf("the bot still has %d levels; the enchant was not charged", enc.level)
	}
	if enc.lapis == 0 {
		t.Error("no lapis was consumed; an enchant costs one lapis")
	}
}

// TestEnchantItemFailsWithoutAnEnchanter. The honest failure: nothing in the
// bot can read PlayerEnchantOptions yet, so the manager must refuse rather
// than invent an option and claim an enchant.
func TestEnchantItemFailsWithoutAnEnchanter(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(5, 64, 5, "enchanting_table")
	bot.addItem(0, 1, "diamond_sword", 1)
	bot.addItem(12, 2, "lapis_lazuli", 3)

	m := station.NewManager(bot, discardLogger())
	m.SetPollInterval(time.Millisecond)
	m.SetEnchantBudget(50 * time.Millisecond)

	if _, ok := m.EnchantItem(context.Background(), "diamond_sword"); ok {
		t.Error("EnchantItem reported an enchant with no way to read the server's options")
	}
}

// TestEnchantItemFailsWhenTheServerSendsNoOptions. A table with air in the input
// slot sends an empty options packet; inventing an enchant there is a lie.
func TestEnchantItemFailsWhenTheServerSendsNoOptions(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(5, 64, 5, "enchanting_table")
	bot.addItem(0, 1, "diamond_sword", 1)
	bot.addItem(12, 2, "lapis_lazuli", 3)

	enc := &fakeEnchanter{level: 30}
	m := station.NewManager(bot, discardLogger())
	m.SetEnchanter(enc)
	m.SetPollInterval(time.Millisecond)
	m.SetEnchantBudget(50 * time.Millisecond)

	if _, ok := m.EnchantItem(context.Background(), "diamond_sword"); ok {
		t.Error("EnchantItem reported success although the server sent no options")
	}
	if len(enc.appliedIndexes()) != 0 {
		t.Error("the manager applied an enchant with no options to apply")
	}
}

// TestEnchantItemFailsWithoutLapis. An enchant with no lapis in the material
// slot is rejected; a manager that skips the check reports a free enchant.
func TestEnchantItemFailsWithoutLapis(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(5, 64, 5, "enchanting_table")
	bot.addItem(0, 1, "diamond_sword", 1)

	enc := &fakeEnchanter{
		options: []protocol.EnchantmentOption{option(3, 2, "animal imbue range", 902)},
		level:   30,
	}
	m := station.NewManager(bot, discardLogger())
	m.SetEnchanter(enc)
	m.SetPollInterval(time.Millisecond)
	m.SetEnchantBudget(50 * time.Millisecond)

	if _, ok := m.EnchantItem(context.Background(), "diamond_sword"); ok {
		t.Error("EnchantItem reported success with no lapis in the inventory")
	}
}

// TestEnchantItemFailsWhenTheApplyIsRejected. The server can refuse the stack
// request; the manager must report that rather than the option it asked for.
func TestEnchantItemFailsWhenTheApplyIsRejected(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(5, 64, 5, "enchanting_table")
	bot.addItem(0, 1, "diamond_sword", 1)
	bot.addItem(12, 2, "lapis_lazuli", 3)

	enc := &fakeEnchanter{
		options:  []protocol.EnchantmentOption{option(3, 2, "animal imbue range", 902)},
		level:    30,
		applyErr: errors.New("stack request rejected"),
	}
	m := station.NewManager(bot, discardLogger())
	m.SetEnchanter(enc)
	m.SetPollInterval(time.Millisecond)
	m.SetEnchantBudget(50 * time.Millisecond)

	if _, ok := m.EnchantItem(context.Background(), "diamond_sword"); ok {
		t.Error("EnchantItem reported success although the server rejected the enchant")
	}
}

// TestEnchantItemFailsWithoutATable, the block search guard.
func TestEnchantItemFailsWithoutATable(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.addBlock(5, 64, 5, "stone")
	bot.addItem(0, 1, "diamond_sword", 1)
	bot.addItem(12, 2, "lapis_lazuli", 3)

	enc := &fakeEnchanter{
		options: []protocol.EnchantmentOption{option(3, 2, "animal imbue range", 902)},
		level:   30,
	}
	m := station.NewManager(bot, discardLogger())
	m.SetEnchanter(enc)
	m.SetPollInterval(time.Millisecond)

	if _, ok := m.EnchantItem(context.Background(), "diamond_sword"); ok {
		t.Error("EnchantItem reported success with no enchanting table nearby")
	}
	if bot.clicked {
		t.Error("EnchantItem clicked a block that is not an enchanting table")
	}
}
