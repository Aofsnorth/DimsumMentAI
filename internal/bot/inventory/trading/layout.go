package trading

import "github.com/sandertv/gophertunnel/minecraft/protocol"

// Container addressing for the villager trade window.
//
// These are not the window ID the server assigned, and using the window ID here
// is the bug this file exists to prevent. A server resolves the ContainerID on
// a StackRequestSlotInfo against these constants and refuses one it does not
// recognise, so a trade transfer addressed by window ID is rejected while the
// code around it reads as though it worked. A chest is the one container where
// the two coincide, which is why the chest helpers get away with it and a
// station cannot.
//
// The values are pinned by tests/bot/inventory/trading/layout_test.go against
// gophertunnel v1.62.0's protocol/container.go, not against a comment.
const (
	// TradeWindowType is the window type a server opens a villager trade UI
	// with. packet.UpdateTrade's own comment says vanilla always sends 15, and
	// protocol.ContainerTypeTrade is the matching constant.
	TradeWindowType = byte(protocol.ContainerTypeTrade)

	// IngredientOneContainerID addresses the villager's first input slot.
	IngredientOneContainerID = byte(protocol.ContainerTradeIngredientOne)

	// IngredientTwoContainerID addresses the second input slot. It is a
	// different container from the first, which is why two-input trades cannot
	// share one ID.
	IngredientTwoContainerID = byte(protocol.ContainerTradeIngredientTwo)

	// ResultContainerID addresses the output the server computes once the
	// inputs are right. Taking from it is what completes the trade.
	ResultContainerID = byte(protocol.ContainerTradeResultPreview)

	// IngredientTwoOneContainerID and its neighbours are the second trade UI's
	// containers, used by hosts that open the older layout. The current UI does
	// not touch them; they are named here so a caller that meets one is not left
	// guessing what the number means.
	IngredientTwoOneContainerID = byte(protocol.ContainerTradeTwoIngredientOne)
	IngredientTwoTwoContainerID = byte(protocol.ContainerTradeTwoIngredientTwo)
	TwoResultContainerID        = byte(protocol.ContainerTradeTwoResultPreview)
)

// Window slot layout. Bedrock numbers a trade window 0 = first input, 1 =
// second input, 2 = the result. Writing an item into any other slot does nothing
// at all, so the layout lives in named constants that the tests pin.
const (
	// SlotIngredientOne is the first input: the emeralds in a bread trade.
	SlotIngredientOne uint32 = 0

	// SlotIngredientTwo is the second input, used only by a two-input offer.
	SlotIngredientTwo uint32 = 1

	// SlotResult is the output the server puts there once the inputs match.
	SlotResult uint32 = 2

	// TradeWindowSlots is how many slots the window has. An offer needing more
	// inputs than this cannot be staged and is never chosen.
	TradeWindowSlots uint32 = 3
)

// ContainerIDForSlot maps a trade window slot to the container that addresses
// it. Every one of the three slots is a separate container, so this is a lookup
// rather than an offset: getting it wrong is a refusal from the server, not a
// quiet no-op.
func ContainerIDForSlot(slot uint32) (byte, bool) {
	switch slot {
	case SlotIngredientOne:
		return IngredientOneContainerID, true
	case SlotIngredientTwo:
		return IngredientTwoContainerID, true
	case SlotResult:
		return ResultContainerID, true
	default:
		return 0, false
	}
}
