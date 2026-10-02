package bot_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/inventory/trading"
	"bedrock-ai/internal/bot/network/player"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// discardLogger keeps the handlers' log output out of the test run. The trade
// handler logs on several paths, including the refusal ones, and a bot that
// shouts at nobody is the quiet case being tested.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// A villager's price list is a serialised NBT compound the server sends and
// nothing else carries. No handler for the packet meant no offers ever existed:
// the trading manager asked, got nothing, and refused every trade as having
// nothing observed — which reads from a player's side as a villager that simply
// will not trade.
//
// The tests below pin the wiring, because the trading package's own tests all
// pass against a fake and would not have caught a missing handler.

// newTradeBot builds a bot with the maps the trade handler writes to.
func newTradeBot(t *testing.T) *bot.Bot {
	t.Helper()
	return &bot.Bot{
		Logger:              discardLogger(),
		UniqueIDToRuntimeID: map[int64]uint64{4242: 77},
	}
}

// TestTheUpdateTradeHandlerIsRegistered is the whole bug in one assertion. A
// handler that exists but is not in the dispatch table never runs, which is
// exactly how the offers went missing in the first place.
func TestTheUpdateTradeHandlerIsRegistered(t *testing.T) {
	t.Parallel()

	b := newTradeBot(t)

	handled := player.HandlePlayerPacket(b, &packet.UpdateTrade{
		WindowID:         9,
		WindowType:       15,
		VillagerUniqueID: 4242,
		EntityUniqueID:   4242,
		DisplayName:      "Farmer",
	})
	if !handled {
		t.Fatal("the dispatcher did not claim UpdateTrade; the offers are never recorded")
	}
}

// TestAnUntrackedVillagerIsNotFiledAgainstTheWrongMob. The packet names a
// villager by unique ID and the bot tracks by runtime ID. When the mapping is not
// known the offers are dropped rather than attached to whatever mob happens to
// share the ID.
func TestAnUntrackedVillagerIsNotFiledAgainstTheWrongMob(t *testing.T) {
	t.Parallel()

	b := newTradeBot(t)

	player.HandlePlayerPacket(b, &packet.UpdateTrade{
		WindowID:         3,
		VillagerUniqueID: 999999, // never spawned here
		EntityUniqueID:   999999,
	})

	if _, ok := b.LastTradeWindow(77); ok {
		t.Error("offers for an untracked villager were filed against a tracked one")
	}
}

// TestAnUnreadableOfferBlobIsNotAnEmptyOfferList is the honest default. A blob
// that fails to decode is not the same as a villager with no trades, and a
// manager that cannot tell them apart would report an empty list as a real one.
func TestAnUnreadableOfferBlobIsNotAnEmptyOfferList(t *testing.T) {
	t.Parallel()

	b := newTradeBot(t)

	player.HandlePlayerPacket(b, &packet.UpdateTrade{
		WindowID:         4,
		VillagerUniqueID: 4242,
		EntityUniqueID:   4242,
		SerialisedOffers: []byte{0xFF, 0xFF, 0xFF}, // not valid NBT
	})

	window, ok := b.LastTradeWindow(77)
	if ok {
		t.Errorf("a blob that failed to decode was recorded as a trade window: %+v", window)
	}
}

// TestOffersAreKeyedPerVillager. Two villagers trade different things, and
// filing both under one ID would have the bot pay a farmer's prices at a
// librarian.
//
// This records through the bot's own method rather than through the packet
// handler, because a packet needs a decodable NBT offer blob to get that far
// and building a valid one here would test the encoder instead of the keying.
// The handler's half — that it refuses a blob it cannot read rather than
// recording an empty offer list — is pinned by the test above.
func TestOffersAreKeyedPerVillager(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: discardLogger()}

	b.RecordTradeWindow(77, trading.TradeWindow{WindowID: 1, DisplayName: "Farmer", TradeTier: 2})
	b.RecordTradeWindow(88, trading.TradeWindow{WindowID: 2, DisplayName: "Librarian", TradeTier: 1})

	first, okFirst := b.LastTradeWindow(77)
	second, okSecond := b.LastTradeWindow(88)
	if !okFirst || !okSecond {
		t.Fatalf("both villagers should have a recorded window (got %v/%v)", okFirst, okSecond)
	}
	if first.DisplayName == second.DisplayName {
		t.Errorf("two villagers share the display name %q; the windows were filed under one ID", first.DisplayName)
	}
	if first.TradeTier == second.TradeTier {
		t.Errorf("two villagers share trade tier %d; the windows were filed under one ID", first.TradeTier)
	}
	if _, ok := b.LastTradeWindow(99); ok {
		t.Error("a villager that was never recorded reported a trade window")
	}

	// A re-record for the same villager replaces rather than accumulates, so a
	// villager that re-levels does not keep answering with yesterday's prices.
	b.RecordTradeWindow(77, trading.TradeWindow{WindowID: 1, DisplayName: "Farmer", TradeTier: 3})
	updated, _ := b.LastTradeWindow(77)
	if updated.TradeTier != 3 {
		t.Errorf("trade tier = %d after a re-record, want 3; the old offer list is still being answered with", updated.TradeTier)
	}
}

// TestNoXPLevelIsNotTheSameAsLevelZero is the difference that keeps a bot from
// walking into a trade it cannot pay for.
//
// An offer can cost XP. Without a level the trade must be refused outright;
// reading a missing level as zero would let the bot attempt it, get rejected,
// and the "no level observed" would silently become a level of zero.
func TestNoXPLevelIsNotTheSameAsLevelZero(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: discardLogger()}

	level, seen := b.ExperienceLevel()
	if seen {
		t.Error("a bot that has never received an experience attribute reports a known level")
	}
	if level != 0 {
		t.Errorf("an unobserved level read as %d; it should be 0 AND reported unknown", level)
	}

	b.SetExperienceLevel(0)
	level, seen = b.ExperienceLevel()
	if !seen {
		t.Error("a level of zero that was actually observed is reported as unobserved")
	}
	if level != 0 {
		t.Errorf("level = %d, want 0", level)
	}
}

// attr builds one Attribute. The Name and Value live on the embedded
// AttributeValue, and this module's -lang is go1.26, which does not allow a
// promoted field in a composite literal — so the embedded field is named.
func attr(name string, value float32) protocol.Attribute {
	return protocol.Attribute{
		AttributeValue: protocol.AttributeValue{Name: name, Value: value},
	}
}

// TestTheExperienceAttributeFoldsIntoALevel. Levels arrive on UpdateAttributes
// and only when they change, so a packet without the attribute must leave the
// level alone rather than resetting it.
func TestTheExperienceAttributeFoldsIntoALevel(t *testing.T) {
	t.Parallel()

	attrs := []protocol.Attribute{attr("minecraft:player.experience", 17)}

	level, seen := bot.ApplyExperienceAttribute(attrs, 3, false)
	if !seen {
		t.Fatal("an experience attribute did not report itself seen")
	}
	if level != 17 {
		t.Errorf("level = %d, want 17", level)
	}

	// A health-only packet must not reset the level.
	level, seen = bot.ApplyExperienceAttribute([]protocol.Attribute{attr("minecraft:health", 20)}, 17, true)
	if !seen || level != 17 {
		t.Errorf("a packet without the experience attribute changed the level to %d (seen=%v), want it left at 17", level, seen)
	}

	// A brand new bot that has never seen one reports unknown.
	_, seen = bot.ApplyExperienceAttribute(nil, 0, false)
	if seen {
		t.Error("an empty attribute list reported a known level")
	}
}

// TestTheTradeObserverSeamIsWiredToTheRecording is the last link: the manager
// reads through the same value the handler writes.
func TestTheTradeObserverSeamIsWiredToTheRecording(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: discardLogger(), UniqueIDToRuntimeID: map[int64]uint64{1: 2}}

	var _ trading.TradeObserver = bot.TradeWindows{B: b}
	var _ trading.XPObserver = bot.Levels{B: b}

	b.RecordTradeWindow(2, trading.TradeWindow{WindowID: 11, DisplayName: "Butcher"})

	got, ok := bot.TradeWindows{B: b}.LastTradeWindow(2)
	if !ok {
		t.Fatal("the observer could not read back a window the bot had recorded")
	}
	if got.DisplayName != "Butcher" {
		t.Errorf("read back %q, want Butcher", got.DisplayName)
	}
	// Parenthesised: without them Go reads the composite literal as the body of
	// the if, and the file does not parse.
	if _, ok := (bot.TradeWindows{B: b}).LastTradeWindow(99); ok {
		t.Error("an unknown villager reported a trade window")
	}
}
