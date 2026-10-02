package bot

import (
	"bedrock-ai/internal/bot/inventory/trading"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Server-side trade state, and the experience level that pays for it.
//
// A villager's offers are not something the bot can work out for itself. The
// server sends a serialised NBT compound in UpdateTrade — the tier, the price
// list, the XP cost, the required level — and none of it is derivable from
// anything else the bot knows. Without a handler for that packet the offers
// simply did not exist: the trading manager asked for them, got nothing, and
// refused every trade.
//
// So the packet is recorded here, keyed by the villager it was sent for, and
// read back through the narrow observer seam the manager declares.

// RecordTradeWindow stores the trade window the server opened for a villager.
func (b *Bot) RecordTradeWindow(villagerRuntimeID uint64, window trading.TradeWindow) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.TradeWindows == nil {
		b.TradeWindows = make(map[uint64]trading.TradeWindow, 8)
	}
	b.TradeWindows[villagerRuntimeID] = window
}

// LastTradeWindow reports the most recent trade window seen for a villager.
//
// The second result is false when nothing has been observed, which is not the
// same as "this villager has nothing to trade". The manager treats it as no
// reading and says so, rather than reporting a villager as having no offers
// when the truth is that the bot never saw the packet.
func (b *Bot) LastTradeWindow(villagerRuntimeID uint64) (trading.TradeWindow, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	window, ok := b.TradeWindows[villagerRuntimeID]
	return window, ok
}

// ExperienceLevel reports the bot's current XP level, and whether a level has
// ever been observed.
//
// The second result matters more than the first. A villager offer can carry an
// XP cost, and a trade that costs XP cannot be attempted without knowing the
// level — so "I have never seen one" must be a refusal, not a level of zero.
// Reading zero would let the bot walk into a trade it cannot pay for and report
// the result honestly as a success.
func (b *Bot) ExperienceLevel() (int32, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.xpLevel, b.xpLevelSeen
}

// SetExperienceLevel records a level read from an attribute packet.
func (b *Bot) SetExperienceLevel(level int32) {
	b.Mu.Lock()
	b.xpLevel = level
	b.xpLevelSeen = true
	b.Mu.Unlock()
}

// ApplyExperienceAttribute folds an UpdateAttributes payload into the XP level.
//
// The vanilla client reads XP from three attributes that change together — the
// level, and the progress within it. Only the level is reported here, because a
// fractional progress bar is not a level, and rounding it would be a number the
// server never sent.
func ApplyExperienceAttribute(attrs []protocol.Attribute, prev int32, seen bool) (level int32, nowSeen bool) {
	level, nowSeen = prev, seen
	for _, attr := range attrs {
		if attr.Name != "minecraft:player.experience" {
			continue
		}
		level, nowSeen = int32(attr.Value), true
	}
	return level, nowSeen
}
