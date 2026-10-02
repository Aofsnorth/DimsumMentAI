package husbandry

import (
	"context"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
)

// isFoodFor returns true if the lower-cased item name matches one of the
// foods for the given animal type.
func (m *Manager) isFoodFor(name, animalType string) bool {
	foods, ok := breedFood[strings.ToLower(animalType)]
	if !ok {
		return false
	}
	for _, foodName := range foods {
		if strings.Contains(name, foodName) {
			return true
		}
	}
	return false
}

// BreedAnimals feeds two nearby animals of the same type and reports whether
// breeding can be confirmed.
//
// The old version returned true the moment it had clicked two animals, which
// means it returned true on an empty pasture, on a lone cow it had just fed
// twice, and on a pair the server was ignoring. It now waits for one of three
// real observations and otherwise says it does not know.
func (m *Manager) BreedAnimals(ctx context.Context, animalType string) bool {
	m.mu.Lock()
	m.isBusy = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.isBusy = false
		m.mu.Unlock()
	}()

	foodSlot, foodStack, ok := m.findItemSlot(func(name string) bool {
		return m.isFoodFor(name, animalType)
	})
	if !ok {
		m.report("breed", animalType, false, "tidak punya makanan untuk "+animalType)
		return false
	}

	typeLower := entity.NormalizeName(animalType)
	targets := m.findEntitiesWithinRadius(func(t string) bool {
		return strings.EqualFold(t, typeLower)
	}, 32)
	if len(targets) < 2 {
		m.report("breed", animalType, false, "butuh minimal 2 ekor")
		return false
	}

	if err := m.bot.EquipItem(foodSlot); err != nil {
		m.report("breed", animalType, false, "tidak bisa memegang makanan")
		return false
	}

	// The baseline for "was the food eaten" has to be taken before the first
	// feed, or the check is circular.
	food := normalise(m.bot.GetItemNames()[foodStack.NetworkID])
	beforeFood := m.inventory().CountOf(food)

	t := m.currentTimings()
	feedFrom := time.Now()
	for i := 0; i < 2 && i < len(targets); i++ {
		m.interactWithEntity(targets[i], foodSlot, foodStack, 1.0, t)
		if !sleepCtx(ctx, t.Action) {
			return false
		}
	}

	signal := m.breedingSignal(targets, typeLower, food, beforeFood, feedFrom)
	if !BreedingConfirmed(signal) {
		m.logger.Warn("breeding finished with no confirmation",
			"animal", animalType,
			"hearts", signal.HeartsObserved,
			"baby", signal.BabyAppeared,
			"food_consumed", signal.BothFoodsConsumed,
		)
		m.report("breed", animalType, false, "tidak ada konfirmasi breeding")
		return false
	}

	m.logger.Info("breeding confirmed", "animal", animalType, "signals", signal)
	m.report("breed", animalType, true, "")
	return true
}

// breedingSignal collects the three observations that can confirm a breed.
func (m *Manager) breedingSignal(
	parents []*entity.Info,
	typeLower string,
	food string,
	foodBefore int,
	since time.Time,
) BreedSignal {
	var s BreedSignal

	if events := m.eventSource(); events != nil {
		for _, p := range parents {
			if HeartsObserved(events.ActorEventsSince(p.ID, since)) {
				s.HeartsObserved = true
				break
			}
		}
	}

	if meta := m.metaSource(); meta != nil {
		for _, p := range parents {
			if after, ok := m.readMeta(meta, p.ID); ok && after.Baby {
				s.BabyAppeared = true
				break
			}
		}
	}

	// A calf of the parents' type appearing is breeding even when neither
	// seam is wired, because the entity tracker already knows about new mobs.
	if !s.BabyAppeared {
		for id, e := range m.bot.GetEntities() {
			if e == nil || isOneOf(id, parents) {
				continue
			}
			if strings.EqualFold(e.Type, typeLower) && isBabyName(e.Name) {
				s.BabyAppeared = true
				break
			}
		}
	}

	if foodBefore-m.inventory().CountOf(food) >= 2 {
		s.BothFoodsConsumed = true
	}
	return s
}

// FeedAnimal feeds the nearest animal of a type and reports whether the server
// took the food.
//
// One feed does not breed anything; it puts a single animal in love mode, and
// the honest evidence for that is the food leaving the hand or the love-mode
// event arriving. Either one is enough. A successful click is not on the list.
func (m *Manager) FeedAnimal(ctx context.Context, animalType string) bool {
	foodSlot, foodStack, ok := m.findItemSlot(func(name string) bool {
		return m.isFoodFor(name, animalType)
	})
	if !ok {
		m.report("feed", animalType, false, "tidak punya makanan untuk "+animalType)
		return false
	}

	typeLower := entity.NormalizeName(animalType)
	closest, ok := m.findNearestEntity(func(t string) bool {
		return strings.EqualFold(t, typeLower)
	})
	if !ok {
		m.report("feed", animalType, false, "tidak ada "+animalType+" di sekitar")
		return false
	}

	if err := m.bot.EquipItem(foodSlot); err != nil {
		m.report("feed", animalType, false, "tidak bisa memegang makanan")
		return false
	}

	food := normalise(m.bot.GetItemNames()[foodStack.NetworkID])
	beforeFood := m.inventory().CountOf(food)
	attemptedAt := time.Now()
	m.interactWithEntity(closest, foodSlot, foodStack, 1.0, m.currentTimings())

	// The food leaving the inventory is a real, server-authoritative event:
	// the server moved the item, not the bot. A love-mode event is the
	// server's own confirmation on top of it.
	consumed := m.inventory().CountOf(food) < beforeFood
	hearts := false
	if events := m.eventSource(); events != nil {
		hearts = HeartsObserved(events.ActorEventsSince(closest.ID, attemptedAt))
	}

	if !consumed && !hearts {
		m.report("feed", animalType, false, "makanan tidak dimakan")
		return false
	}
	m.report("feed", animalType, true, "")
	return true
}

// isOneOf reports whether id is one of the listed entities.
func isOneOf(id uint64, list []*entity.Info) bool {
	for _, e := range list {
		if e.ID == id {
			return true
		}
	}
	return false
}

// isBabyName reports whether an entity's display name marks it as a baby.
//
// entity.Info carries no metadata, so a calf is recognisable only by what the
// server named it. Vanilla mob names are like "Baby Cow", so this is a genuine
// observation when it matches and a genuine absence when it does not — which is
// why it is one of three signals and not the only one.
func isBabyName(name string) bool {
	return strings.Contains(entity.NormalizeName(name), "baby")
}
