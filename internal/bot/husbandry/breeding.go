// Package husbandry provides animal breeding and feeding methods for the bot.
package husbandry

import (
	"context"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

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

	// Find breeding food
	foods, ok := breedFood[strings.ToLower(animalType)]
	if !ok {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "breed",
			Item:    animalType,
			Count:   0,
			Success: false,
			Error:   "gak tau makanan",
		})
		return false
	}

	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	var foodSlot uint32
	found := false
	for _, foodName := range foods {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			name = strings.TrimPrefix(name, "minecraft:")
			if strings.Contains(name, foodName) {
				foodSlot = slot
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "breed",
			Item:    animalType,
			Count:   0,
			Success: false,
			Error:   "gak punya makanan",
		})
		return false
	}

	// Find animals of the target type
	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()
	typeLower := strings.ToLower(animalType)

	var targets []*entity.Info
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		if strings.EqualFold(ent.Type, typeLower) {
			dist := pos.Sub(ent.Position).Len()
			if dist <= 32 {
				targets = append(targets, ent)
			}
		}
	}

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

	// Feed first animal
	feeded := 0
	for i := 0; i < 2 && i < len(targets); i++ {
		target := targets[i]
		m.bot.NavigateTo(target.Position)
		time.Sleep(1 * time.Second)
		m.bot.StopMovement()

		m.bot.LookAt(target.Position.Add(mgl32.Vec3{0, 1.0, 0}))
		time.Sleep(200 * time.Millisecond)

		// Interact (feed) the animal
		tx := &packet.InventoryTransaction{
			TransactionData: &protocol.UseItemOnEntityTransactionData{
				TargetEntityRuntimeID: target.ID,
				ActionType:            0, // interact
				HotBarSlot:            safecast.To[int32](foodSlot),
				HeldItem:              protocol.ItemInstance{Stack: inv[foodSlot]},
				Position:              m.bot.GetCoords(),
				ClickedPosition:       mgl32.Vec3{0, 0, 0},
			},
		}
		_ = m.bot.WritePacket(tx)
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
	foods, ok := breedFood[strings.ToLower(animalType)]
	if !ok {
		return false
	}

	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	var foodSlot uint32
	found := false
	for _, foodName := range foods {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			name = strings.TrimPrefix(name, "minecraft:")
			if strings.Contains(name, foodName) {
				foodSlot = slot
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return false
	}

	// Find nearest animal of type
	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()
	typeLower := strings.ToLower(animalType)

	var closest *entity.Info
	closestDist := float32(math.MaxFloat32)
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		if strings.EqualFold(ent.Type, typeLower) {
			dist := pos.Sub(ent.Position).Len()
			if dist < closestDist {
				closestDist = dist
				closest = ent
			}
		}
	}

	if closest == nil {
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

	m.bot.NavigateTo(closest.Position)
	time.Sleep(1 * time.Second)
	m.bot.StopMovement()
	m.bot.LookAt(closest.Position.Add(mgl32.Vec3{0, 1.0, 0}))
	time.Sleep(200 * time.Millisecond)

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: closest.ID,
			ActionType:            0,
			HotBarSlot:            safecast.To[int32](foodSlot),
			HeldItem:              protocol.ItemInstance{Stack: inv[foodSlot]},
			Position:              m.bot.GetCoords(),
			ClickedPosition:       mgl32.Vec3{0, 0, 0},
		},
	}
	_ = m.bot.WritePacket(tx)

	m.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "feed",
		Item:    animalType,
		Count:   1,
		Success: true,
	})
	return true
}
