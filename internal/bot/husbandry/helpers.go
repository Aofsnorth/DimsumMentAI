// Package husbandry provides common helpers for animal interactions.
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

// isEmptyBucket reports whether the lower-cased name is an empty bucket.
func isEmptyBucket(name string) bool {
	n := normalise(name)
	if !strings.Contains(n, "bucket") {
		return false
	}
	for _, filled := range []string{
		"lava", "water", "milk", "fish", "axolotl", "tadpole",
		"cod", "salmon", "tropical", "pufferfish", "powder_snow",
	} {
		if strings.Contains(n, filled) {
			return false
		}
	}
	return true
}

// findItemSlot finds the first inventory slot whose lower-cased item name
// satisfies the supplied predicate.
func (m *Manager) findItemSlot(predicate func(name string) bool) (uint32, protocol.ItemStack, bool) {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		if predicate(normalise(names[item.NetworkID])) {
			return slot, item, true
		}
	}
	return 0, protocol.ItemStack{}, false
}

// findNearestEntity returns the nearest living entity whose lower-cased type
// satisfies the supplied predicate.
func (m *Manager) findNearestEntity(predicate func(entityType string) bool) (*entity.Info, bool) {
	pos := m.bot.GetCoords()

	var closest *entity.Info
	closestDist := float32(math.MaxFloat32)
	for _, ent := range m.bot.GetEntities() {
		if ent == nil || ent.Health <= 0 {
			continue
		}
		if predicate(entity.NormalizeName(ent.Type)) {
			dist := pos.Sub(ent.Position).Len()
			if dist < closestDist {
				closestDist = dist
				closest = ent
			}
		}
	}
	return closest, closest != nil
}

// findEntitiesWithinRadius returns all living entities whose lower-cased type
// satisfies the predicate and are within radius of the bot.
func (m *Manager) findEntitiesWithinRadius(predicate func(entityType string) bool, radius float32) []*entity.Info {
	pos := m.bot.GetCoords()

	var matches []*entity.Info
	for _, ent := range m.bot.GetEntities() {
		if ent == nil || ent.Health <= 0 {
			continue
		}
		if predicate(entity.NormalizeName(ent.Type)) {
			if pos.Sub(ent.Position).Len() <= radius {
				matches = append(matches, ent)
			}
		}
	}
	return matches
}

// interactWithEntity navigates to the entity, looks at it, and sends the
// interact transaction that carries the held item.
func (m *Manager) interactWithEntity(target *entity.Info, slot uint32, stack protocol.ItemStack, lookYOffset float32, t Timings) {
	m.bot.NavigateTo(target.Position)
	sleepCtx(context.Background(), t.Approach)
	m.bot.StopMovement()

	m.bot.LookAt(target.Position.Add(mgl32.Vec3{0, lookYOffset, 0}))
	sleepCtx(context.Background(), t.Aim)

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
	_ = m.bot.WritePacket(tx)
}

// inventory takes a flattened reading of what the bot is carrying.
func (m *Manager) inventory() Inventory {
	return NewInventory(m.bot.GetInventorySlots(), m.bot.GetItemNames())
}

// report sends an action status, with a count that is always the number of
// things actually observed.
func (m *Manager) report(action, item string, success bool, reason string) {
	status := event.ActionStatus{
		Action:  action,
		Item:    item,
		Success: success,
	}
	if success {
		status.Count = 1
	} else {
		status.Error = reason
	}
	m.bot.ReportActionStatus("", status)
}

// newCountStatus builds a success status carrying a real count, for the actions
// that produce more than one thing.
func newCountStatus(action, item string, count int) event.ActionStatus {
	return event.ActionStatus{Action: action, Item: item, Count: count, Success: true}
}

// sleepCtx waits for d, returning false if the context ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
