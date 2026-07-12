// Package husbandry provides animal breeding and feeding methods for the bot.
package husbandry

import (
	"context"
	"strings"
	"time"

	"bedrock-ai/internal/event"
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

// BreedAnimals feeds two nearby animals of the same type to breed them
func (m *Manager) BreedAnimals(ctx context.Context, animalType string) bool {
	m.mu.Lock()
	m.isBusy = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.isBusy = false
		m.mu.Unlock()
	}()

	if _, _, ok := m.findItemSlot(func(name string) bool { return m.isFoodFor(name, animalType) }); !ok {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "breed",
			Item:    animalType,
			Count:   0,
			Success: false,
			Error:   "gak tau makanan atau gak punya makanan",
		})
		return false
	}

	foodSlot, foodStack, ok := m.findItemSlot(func(name string) bool { return m.isFoodFor(name, animalType) })
	if !ok {
		return false
	}

	typeLower := strings.ToLower(animalType)
	targets := m.findEntitiesWithinRadius(func(t string) bool { return strings.EqualFold(t, typeLower) }, 32)
	if len(targets) < 2 {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "breed",
			Item:    animalType,
			Count:   len(targets),
			Success: false,
			Error:   "butuh minimal 2",
		})
		return false
	}

	if err := m.bot.EquipItem(foodSlot); err != nil {
		return false
	}

	feeded := 0
	for i := 0; i < 2 && i < len(targets); i++ {
		m.interactWithEntity(targets[i], foodSlot, foodStack, 1.0)
		feeded++
		time.Sleep(500 * time.Millisecond)

		select {
		case <-ctx.Done():
			return false
		default:
		}
	}

	if feeded == 2 {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "breed",
			Item:    animalType,
			Count:   feeded,
			Success: true,
		})
		return true
	}
	return false
}

// FeedAnimal feeds a specific animal nearby
func (m *Manager) FeedAnimal(ctx context.Context, animalType string) bool {
	foodSlot, foodStack, ok := m.findItemSlot(func(name string) bool { return m.isFoodFor(name, animalType) })
	if !ok {
		return false
	}

	typeLower := strings.ToLower(animalType)
	closest, ok := m.findNearestEntity(func(t string) bool { return strings.EqualFold(t, typeLower) })
	if !ok {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "feed",
			Item:    animalType,
			Count:   0,
			Success: false,
			Error:   "gak ada",
		})
		return false
	}

	if err := m.bot.EquipItem(foodSlot); err != nil {
		return false
	}

	m.interactWithEntity(closest, foodSlot, foodStack, 1.0)

	m.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "feed",
		Item:    animalType,
		Count:   1,
		Success: true,
	})
	return true
}
