// Package husbandry provides animal milking and shearing methods for the bot.
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

// MilkCow milks a nearby cow
func (m *Manager) MilkCow(ctx context.Context) bool {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	// Find bucket
	var bucketSlot uint32
	found := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "bucket") && !strings.Contains(name, "lava") &&
			!strings.Contains(name, "water") && !strings.Contains(name, "milk") &&
			!strings.Contains(name, "fish") && !strings.Contains(name, "axolotl") &&
			!strings.Contains(name, "tadpole") && !strings.Contains(name, "cod") &&
			!strings.Contains(name, "salmon") && !strings.Contains(name, "tropical") &&
			!strings.Contains(name, "pufferfish") {
			bucketSlot = slot
			found = true
			break
		}
	}

	if !found {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "milk",
			Item:    "bucket",
			Count:   0,
			Success: false,
			Error:   "gak punya bucket kosong",
		})
		return false
	}

	// Find cow
	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()

	var cow *entity.Info
	closestDist := float32(math.MaxFloat32)
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		typeLower := strings.ToLower(ent.Type)
		if typeLower == "cow" || typeLower == "mooshroom" {
			dist := pos.Sub(ent.Position).Len()
			if dist < closestDist {
				closestDist = dist
				cow = ent
			}
		}
	}

	if cow == nil {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "milk",
			Item:    "cow",
			Count:   0,
			Success: false,
			Error:   "gak ada sapi",
		})
		return false
	}

	if err := m.bot.EquipItem(bucketSlot); err != nil {
		return false
	}

	m.bot.NavigateTo(cow.Position)
	time.Sleep(1 * time.Second)
	m.bot.StopMovement()
	m.bot.LookAt(cow.Position.Add(mgl32.Vec3{0, 0.8, 0}))
	time.Sleep(200 * time.Millisecond)

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: cow.ID,
			ActionType:            0,
			HotBarSlot:            safecast.To[int32](bucketSlot),
			HeldItem:              protocol.ItemInstance{Stack: inv[bucketSlot]},
			Position:              m.bot.GetCoords(),
			ClickedPosition:       mgl32.Vec3{0, 0, 0},
		},
	}
	_ = m.bot.WritePacket(tx)

	m.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "milk",
		Item:    "cow",
		Count:   1,
		Success: true,
	})
	return true
}

// ShearSheep shears a nearby sheep
func (m *Manager) ShearSheep(ctx context.Context) bool {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	var shearsSlot uint32
	found := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, "shears") {
			shearsSlot = slot
			found = true
			break
		}
	}

	if !found {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "shear",
			Item:    "shears",
			Count:   0,
			Success: false,
			Error:   "gak punya gunting",
		})
		return false
	}

	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()

	var sheep *entity.Info
	closestDist := float32(math.MaxFloat32)
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		if strings.EqualFold(ent.Type, "sheep") {
			dist := pos.Sub(ent.Position).Len()
			if dist < closestDist {
				closestDist = dist
				sheep = ent
			}
		}
	}

	if sheep == nil {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "shear",
			Item:    "sheep",
			Count:   0,
			Success: false,
			Error:   "gak ada domba",
		})
		return false
	}

	if err := m.bot.EquipItem(shearsSlot); err != nil {
		return false
	}

	m.bot.NavigateTo(sheep.Position)
	time.Sleep(1 * time.Second)
	m.bot.StopMovement()
	m.bot.LookAt(sheep.Position.Add(mgl32.Vec3{0, 0.8, 0}))
	time.Sleep(200 * time.Millisecond)

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: sheep.ID,
			ActionType:            0,
			HotBarSlot:            safecast.To[int32](shearsSlot),
			HeldItem:              protocol.ItemInstance{Stack: inv[shearsSlot]},
			Position:              m.bot.GetCoords(),
			ClickedPosition:       mgl32.Vec3{0, 0, 0},
		},
	}
	_ = m.bot.WritePacket(tx)

	m.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "shear",
		Item:    "sheep",
		Count:   1,
		Success: true,
	})
	return true
}
