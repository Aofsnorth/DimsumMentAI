// Package husbandry provides animal taming methods for the bot.
package husbandry

import (
	"context"
	"strings"
	"time"

	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// TameWolf attempts to tame a nearby wolf with bones
func (m *Manager) TameWolf(ctx context.Context) bool {
	return m.tameWithItem(ctx, "wolf", "bone")
}

// TameCat attempts to tame a nearby cat with fish
func (m *Manager) TameCat(ctx context.Context) bool {
	return m.tameWithItem(ctx, "cat", "cod")
}

func (m *Manager) tameWithItem(ctx context.Context, animalType, itemName string) bool {
	itemSlot, itemStack, ok := m.findItemSlot(func(name string) bool { return strings.Contains(name, itemName) })
	if !ok {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "tame",
			Item:    itemName,
			Count:   0,
			Success: false,
			Error:   "gak punya " + itemName,
		})
		return false
	}

	typeLower := strings.ToLower(animalType)
	target, ok := m.findNearestEntity(func(t string) bool { return strings.EqualFold(t, typeLower) })
	if !ok {
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
				HeldItem:              protocol.ItemInstance{Stack: itemStack},
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
