package trading

import (
	"errors"
	"fmt"

	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// The observation seams.
//
// A villager's offers are per-villager state the server sends when the bot
// interacts with it: packet.UpdateTrade carries the window ID, the villager's
// tier, and a serialised NBT trade table. Nothing in this repository decoded
// that packet, so neither the offers nor the XP level are observable today.
//
// Both seams therefore default to "not observable", which is a real answer and
// not a hedge: a manager with no trade observer refuses to trade and says why,
// rather than picking a price it made up and handing over emeralds.

// TradeWindow is what the server said when it opened a villager's trade UI. It
// is a copy of packet.UpdateTrade plus the offers decoded from its blob, so the
// packet can be dropped and the trade still be understood.
type TradeWindow struct {
	// WindowID is the trading window the server had open. The window ID the
	// ContainerOpen packet assigned is what content packets are matched
	// against, so the manager prefers that one; this is kept for the
	// disagreement case, not as an alternative source of truth.
	WindowID byte

	// WindowType is the container type the server opened. Vanilla always sends
	// TradeWindowType for this UI.
	WindowType byte

	// DisplayName is the heading the trade UI shows, usually the profession.
	DisplayName string

	// TradeTier is the villager's tier: two offers at tier 0, two more per tier.
	TradeTier int32

	// OfferCount is how many offers the server says there are, which is not
	// always how many were decodable.
	OfferCount int32

	// NewTradeUI reports whether the host used the post-1.11 interface.
	NewTradeUI bool

	// DemandBasedPrices reports whether repeated purchases raise the price.
	DemandBasedPrices bool

	// VillagerUniqueID is the villager the trade table belongs to, and
	// EntityUniqueID the player it was sent for.
	VillagerUniqueID int64
	EntityUniqueID   int64

	// SerialisedOffers is the raw blob, kept so a decode failure can be
	// diagnosed after the fact.
	SerialisedOffers []byte

	// Offers are the decoded rows. Empty with a nil error means the villager
	// genuinely offers nothing; empty with an error means the blob could not be
	// read, which is a different thing and is reported as such.
	Offers []Offer
}

// TradeWindowFromPacket copies a packet.UpdateTrade into a TradeWindow and
// decodes the offers it carries.
//
// On a decode failure it still returns the window — everything but the offers
// did arrive and is worth logging — together with the error, so the caller can
// record a real window with no offers rather than dropping the whole packet.
func TradeWindowFromPacket(pk *packet.UpdateTrade) (TradeWindow, error) {
	if pk == nil {
		return TradeWindow{}, errors.New("nil UpdateTrade packet")
	}

	window := TradeWindow{
		WindowID:          pk.WindowID,
		WindowType:        pk.WindowType,
		DisplayName:       pk.DisplayName,
		TradeTier:         pk.TradeTier,
		OfferCount:        pk.Size,
		NewTradeUI:        pk.NewTradeUI,
		DemandBasedPrices: pk.DemandBasedPrices,
		VillagerUniqueID:  pk.VillagerUniqueID,
		EntityUniqueID:    pk.EntityUniqueID,
		SerialisedOffers:  append([]byte(nil), pk.SerialisedOffers...),
	}

	offers, err := DecodeOffers(pk.SerialisedOffers)
	if err != nil {
		return window, fmt.Errorf("UpdateTrade offers unreadable: %w", err)
	}
	window.Offers = offers
	return window, nil
}

// TradeObserver supplies the trade windows the server pushed.
//
// The bot cannot supply this yet: internal/bot/network/player/player.go has no
// case for packet.IDUpdateTrade, so nothing records the packet this interface
// exists to hand over. Wiring it needs one handler that calls
// TradeWindowFromPacket and stores the result keyed by villager runtime ID.
type TradeObserver interface {
	// LastTradeWindow reports the most recent trade window the server opened for
	// a villager. The second return is false when nothing has been observed for
	// that villager, which the manager reports rather than working around.
	LastTradeWindow(villagerRuntimeID uint64) (TradeWindow, bool)
}

// XPObserver reports the bot's current experience level.
//
// The bot cannot supply this yet either: levels arrive on packet.UpdateAttributes
// as a minecraft:player.experience attribute, and the bot's attribute handler
// reads health only. The second return is false when the level is not
// observable, which is not the same as a level of zero — an XP-costing offer is
// refused in that case rather than attempted blind.
type XPObserver interface {
	ExperienceLevel() (int32, bool)
}
