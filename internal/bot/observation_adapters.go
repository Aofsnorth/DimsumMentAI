package bot

import (
	"time"

	"bedrock-ai/internal/bot/bucket"
	"bedrock-ai/internal/bot/fishing"
	"bedrock-ai/internal/bot/husbandry"
	"bedrock-ai/internal/bot/inventory/trading"
)

// Adapters onto the observation seam.
//
// fishing, husbandry and bucket each declare their own ActorEvent type with the
// same three fields, and Go will not let one type have two methods called
// ActorEventsSince. So the bot records the neutral ActorEvent and each consumer
// gets a small view of the same ring, and these types are what the managers are
// wired with.
//
// They are wrappers rather than methods on *Bot for the same reason: the
// managers store the interface, and a type that satisfies all three is cheaper
// to reason about than three near-identical methods with different return types.

// FisherEvents presents the event ring in the shape fishing.ActorEventSource
// wants.
type FisherEvents struct{ B *Bot }

// ActorEventsSince implements fishing.ActorEventSource.
func (f FisherEvents) ActorEventsSince(runtimeID uint64, since time.Time) []fishing.ActorEvent {
	if f.B == nil {
		return nil
	}
	raw := f.B.ActorEventsSince(runtimeID, since)
	out := make([]fishing.ActorEvent, 0, len(raw))
	for _, e := range raw {
		out = append(out, fishing.ActorEvent{RuntimeID: e.RuntimeID, Type: e.Type, At: e.At})
	}
	return out
}

// HusbandryEvents presents the same ring in husbandry's shape.
type HusbandryEvents struct{ B *Bot }

// ActorEventsSince implements husbandry.ActorEventSource.
func (h HusbandryEvents) ActorEventsSince(runtimeID uint64, since time.Time) []husbandry.ActorEvent {
	if h.B == nil {
		return nil
	}
	raw := h.B.ActorEventsSince(runtimeID, since)
	out := make([]husbandry.ActorEvent, 0, len(raw))
	for _, e := range raw {
		out = append(out, husbandry.ActorEvent{RuntimeID: e.RuntimeID, Type: e.Type, At: e.At})
	}
	return out
}

// HusbandryEntityMeta presents the metadata cache in husbandry's shape.
type HusbandryEntityMeta struct{ B *Bot }

// EntityMeta implements husbandry.EntityMetaSource.
//
// ok is false for an entity the bot never saw, which the husbandry package
// already treats as no reading rather than a negative one — that distinction is
// the whole reason taming refuses to claim success without a collar.
func (h HusbandryEntityMeta) EntityMeta(runtimeID uint64) (husbandry.EntityMeta, bool) {
	if h.B == nil {
		return husbandry.EntityMeta{}, false
	}
	state, ok := h.B.EntityMeta(runtimeID)
	if !ok {
		return husbandry.EntityMeta{}, false
	}
	return husbandry.EntityMeta{
		EntityType: state.EntityType,
		Tamed:      state.Tamed,
		Collared:   state.Collared,
		OwnerKnown: state.OwnerKnown,
		Baby:       state.Baby,
	}, true
}

// TradeWindows satisfies trading.TradeObserver.
type TradeWindows struct{ B *Bot }

// LastTradeWindow implements trading.TradeObserver.
func (w TradeWindows) LastTradeWindow(villagerRuntimeID uint64) (trading.TradeWindow, bool) {
	if w.B == nil {
		return trading.TradeWindow{}, false
	}
	return w.B.LastTradeWindow(villagerRuntimeID)
}

// Levels satisfies trading.XPObserver.
type Levels struct{ B *Bot }

// ExperienceLevel implements trading.XPObserver.
func (l Levels) ExperienceLevel() (int32, bool) {
	if l.B == nil {
		return 0, false
	}
	return l.B.ExperienceLevel()
}

// BlockStates satisfies bucket.BlockStateSource and farming.BlockStateSource,
// which ask for the same shape.
type BlockStates struct{ B *Bot }

// GetBlockState implements both. The bot's own method already has this shape,
// so the wrapper exists only to carry the bot through the interface.
func (s BlockStates) GetBlockState(x, y, z int32) (string, map[string]any, bool) {
	if s.B == nil {
		return "", nil, false
	}
	return s.B.GetBlockState(x, y, z)
}

// Compile-time proof that each adapter satisfies the seam it exists for. These
// are the assertions that would otherwise be discovered at wiring time, in
// production, as a refusal to report a bite.
var (
	_ fishing.ActorEventSource   = FisherEvents{}
	_ husbandry.ActorEventSource = HusbandryEvents{}
	_ husbandry.EntityMetaSource = HusbandryEntityMeta{}
	_ bucket.BlockStateSource    = BlockStates{}
	_ bucket.StackResponseSource = BlockStates{}
	_ trading.TradeObserver      = TradeWindows{}
	_ trading.XPObserver         = Levels{}
)

// StackResponsesSince implements bucket.StackResponseSource.
//
// It is on BlockStates because the two seams are both "what did the server say
// about this interaction", but it reports nothing: the bot does not yet record
// ItemStackResponse history. The bucket package treats an empty list as "the
// stricter check was unavailable" and still confirms from the inventory and the
// block, which is the honest outcome — a false here would suppress a real
// confirmation, and a fabricated true would claim a server answer nobody read.
func (s BlockStates) StackResponsesSince(time.Time) []bucket.StackResponse {
	return nil
}
