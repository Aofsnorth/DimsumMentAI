package husbandry

import (
	"context"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	// tameAttempts is how many times the taming item is offered before the
	// manager gives up. Taming has a real failure chance, so more than one
	// attempt is correct — but the count bounds the work, it is never the
	// evidence. The old routine returned true once this loop finished,
	// whatever had happened to the animal.
	tameAttempts = 5

	// approachDelay is the pause after walking toward the animal before the
	// click, and aimDelay the pause after turning to face it.
	approachDelay = 800 * time.Millisecond
	aimDelay      = 200 * time.Millisecond

	// attemptGap is the pause between two taming offers.
	attemptGap = time.Second
)

// Timings is every wait in a husbandry interaction, gathered so a test can
// compress them and production can leave them alone.
type Timings struct {
	// Approach is the pause after navigating to an animal.
	Approach time.Duration
	// Aim is the pause after turning toward it.
	Aim time.Duration
	// Action is the pause after the interaction itself, before the next one.
	Action time.Duration
}

// DefaultTimings returns the production timings.
func DefaultTimings() Timings {
	return Timings{Approach: approachDelay, Aim: aimDelay, Action: attemptGap}
}

// Compressed divides every wait by factor, for tests.
//
// The timings are pure settling delays — they exist so the movement tick and
// the network round-trip keep up with the click — so shrinking them changes
// how fast a test runs and nothing about what the code decides.
func (t Timings) Compressed(factor int) Timings {
	if factor <= 0 {
		return t
	}
	d := time.Duration(factor)
	return Timings{
		Approach: t.Approach / d,
		Aim:      t.Aim / d,
		Action:   t.Action / d,
	}
}

// TameWolf attempts to tame a nearby wolf with bones.
func (m *Manager) TameWolf(ctx context.Context) bool {
	return m.tameWithItem(ctx, "wolf", "bone")
}

// TameCat attempts to tame a nearby cat with fish.
func (m *Manager) TameCat(ctx context.Context) bool {
	return m.tameWithItem(ctx, "cat", "cod")
}

// tameWithItem offers the taming item to the nearest animal of the given type
// and reports whether the animal actually became the player's.
//
// Three things can confirm that, and the manager takes the first that is
// available:
//
//  1. the collared/owner flag on the animal's entity metadata changed;
//  2. the server sent a taming-succeeded actor event;
//  3. nothing did — in which case this returns false and says why.
//
// "Three attempts and hope" is not on that list, and never was meant to be.
func (m *Manager) tameWithItem(ctx context.Context, animalType, itemName string) bool {
	itemSlot, itemStack, ok := m.findItemSlot(func(name string) bool {
		return strings.Contains(name, itemName)
	})
	if !ok {
		m.report("tame", itemName, false, "tidak punya "+itemName)
		return false
	}

	target, ok := m.findNearestEntity(func(t string) bool {
		return strings.EqualFold(t, animalType)
	})
	if !ok {
		m.report("tame", animalType, false, "tidak ada "+animalType+" di sekitar")
		return false
	}

	if err := m.bot.EquipItem(itemSlot); err != nil {
		m.report("tame", itemName, false, "tidak bisa memegang "+itemName)
		return false
	}

	// Read the animal before touching it. A transition is the confirmation;
	// a state is not, and without the "before" reading there is nothing to
	// compare against.
	meta := m.metaSource()
	before, hadBefore := m.readMeta(meta, target.ID)

	t := m.currentTimings()
	for attempt := 0; attempt < tameAttempts; attempt++ {
		if ctx.Err() != nil {
			return false
		}

		m.bot.NavigateTo(target.Position)
		sleepCtx(ctx, t.Approach)
		m.bot.StopMovement()
		m.bot.LookAt(target.Position.Add(mgl32.Vec3{0, 0.6, 0}))
		sleepCtx(ctx, t.Aim)

		offered := time.Now()
		if err := m.offerItem(target, itemSlot, itemStack); err != nil {
			m.report("tame", animalType, false, "gagal menawarkan "+itemName)
			return false
		}

		// Let the answer arrive, then read the server's verdict for this
		// attempt before starting the next one. Checking once at the end
		// would let a late success be attributed to a later attempt, and —
		// worse — would let a rejection from three attempts ago be read as
		// this one's outcome.
		if !sleepCtx(ctx, t.Action) {
			return false
		}
		if m.attemptConfirmed(target.ID, animalType, before, hadBefore, offered) {
			m.report("tame", animalType, true, "")
			return true
		}
	}

	m.logger.Warn("taming finished with no confirmation",
		"animal", animalType,
		"attempts", tameAttempts,
		"meta_seam", meta != nil,
	)
	m.report("tame", animalType, false, "tidak ada konfirmasi server")
	return false
}

// attemptConfirmed checks the two observation seams for one attempt's outcome.
func (m *Manager) attemptConfirmed(
	runtimeID uint64,
	animalType string,
	before EntityMeta,
	hadBefore bool,
	attemptedAt time.Time,
) bool {
	// The server's own verdict wins when it is available: it is unambiguous
	// about success and failure both.
	if events := m.eventSource(); events != nil {
		if succeeded, _ := TameObserved(events.ActorEventsSince(runtimeID, attemptedAt)); succeeded {
			return true
		}
	}

	after, ok := m.readMeta(m.metaSource(), runtimeID)
	if !ok {
		return false
	}
	if !hadBefore {
		// A single reading cannot show a transition. Treating "collared now"
		// as proof of "collared by me" is the same class of mistake as
		// treating five attempts as proof of anything.
		before = EntityMeta{EntityType: animalType}
	}
	return TameConfirmed(before, after)
}

// offerItem sends the interact-with-entity transaction a vanilla client sends
// when a player right-clicks an animal holding food.
func (m *Manager) offerItem(target *entity.Info, slot uint32, stack protocol.ItemStack) error {
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: target.ID,
			ActionType:            0,
			HotBarSlot:            safecast.To[int32](slot),
			HeldItem:              protocol.ItemInstance{Stack: stack},
			Position:              m.bot.GetCoords(),
			ClickedPosition:       mgl32.Vec3{0, 0, 0},
		},
	}
	return m.bot.WritePacket(tx)
}

// readMeta asks the seam what it knows about an entity. An absent seam and an
// untracked entity are both "no reading", and both come back ok=false.
func (m *Manager) readMeta(src EntityMetaSource, runtimeID uint64) (EntityMeta, bool) {
	if src == nil {
		return EntityMeta{}, false
	}
	meta, ok := src.EntityMeta(runtimeID)
	if !ok {
		return EntityMeta{}, false
	}
	if meta.EntityType == "" {
		meta.EntityType = m.entityTypeName(runtimeID)
	}
	return meta, true
}

func (m *Manager) entityTypeName(runtimeID uint64) string {
	if e, ok := m.bot.GetEntities()[runtimeID]; ok && e != nil {
		return e.Type
	}
	return ""
}
