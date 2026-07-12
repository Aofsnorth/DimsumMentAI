// Package husbandry provides animal taming methods for the bot.
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

// TameWolf attempts to tame a nearby wolf with bones
func (m *Manager) TameWolf(ctx context.Context) bool {
	return m.tameWithItem(ctx, "wolf", "bone", "taming wolf")
}

// TameCat attempts to tame a nearby cat with fish
func (m *Manager) TameCat(ctx context.Context) bool {
	return m.tameWithItem(ctx, "cat", "cod", "taming cat")
}

func (m *Manager) tameWithItem(ctx context.Context, animalType, itemName, actionDesc string) bool {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	var itemSlot uint32
	found := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, itemName) {
			itemSlot = slot
			found = true
			break
		}
	}

	if !found {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "tame",
			Item:    itemName,
			Count:   0,
			Success: false,
			Error:   "gak punya " + itemName,
		})
		return false
	}

	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()

	var target *entity.Info
	closestDist := float32(math.MaxFloat32)
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		if strings.EqualFold(ent.Type, animalType) {
			dist := pos.Sub(ent.Position).Len()
			if dist < closestDist {
				closestDist = dist
				target = ent
			}
		}
	}

	if target == nil {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "tame",
			Item:    animalType,
			Count:   0,
			Success: false,
			Error:   "gak ada",
		})
		return false
	}

	if err := m.bot.EquipItem(itemSlot); err != nil {
		return false
	}

	// Try multiple times (taming has a chance to fail)
	for attempt := 0; attempt < 5; attempt++ {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		m.bot.NavigateTo(target.Position)
		time.Sleep(800 * time.Millisecond)
		m.bot.StopMovement()
		m.bot.LookAt(target.Position.Add(mgl32.Vec3{0, 0.6, 0}))
		time.Sleep(200 * time.Millisecond)

		tx := &packet.InventoryTransaction{
			TransactionData: &protocol.UseItemOnEntityTransactionData{
				TargetEntityRuntimeID: target.ID,
				ActionType:            0,
				HotBarSlot:            safecast.To[int32](itemSlot),
				HeldItem:              protocol.ItemInstance{Stack: inv[itemSlot]},
				Position:              m.bot.GetCoords(),
				ClickedPosition:       mgl32.Vec3{0, 0, 0},
			},
		}
		_ = m.bot.WritePacket(tx)
		time.Sleep(1 * time.Second)
	}

	m.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "tame",
		Item:    animalType,
		Count:   1,
		Success: true,
	})
	return true
}
