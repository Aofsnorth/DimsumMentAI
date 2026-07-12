// Package husbandry provides common helpers for animal interactions.
package husbandry

import (
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// isEmptyBucket reports whether the lower-cased name is an empty bucket.
func isEmptyBucket(name string) bool {
	return strings.Contains(name, "bucket") &&
		!strings.Contains(name, "lava") &&
		!strings.Contains(name, "water") &&
		!strings.Contains(name, "milk") &&
		!strings.Contains(name, "fish") &&
		!strings.Contains(name, "axolotl") &&
		!strings.Contains(name, "tadpole") &&
		!strings.Contains(name, "cod") &&
		!strings.Contains(name, "salmon") &&
		!strings.Contains(name, "tropical") &&
		!strings.Contains(name, "pufferfish")
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
		name := strings.ToLower(names[item.NetworkID])
		name = strings.TrimPrefix(name, "minecraft:")
		if predicate(name) {
			return slot, item, true
		}
	}
	return 0, protocol.ItemStack{}, false
}

// findNearestEntity returns the nearest living entity whose lower-cased type
// satisfies the supplied predicate.
func (m *Manager) findNearestEntity(predicate func(entityType string) bool) (*entity.Info, bool) {
	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()

	var closest *entity.Info
	closestDist := float32(math.MaxFloat32)
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		if predicate(strings.ToLower(ent.Type)) {
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
	entities := m.bot.GetEntities()

	var matches []*entity.Info
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		if predicate(strings.ToLower(ent.Type)) {
			dist := pos.Sub(ent.Position).Len()
			if dist <= radius {
				matches = append(matches, ent)
			}
		}
	}
	return matches
}

// interactWithEntity navigates to the entity, looks at it, and sends an
// interact transaction using the supplied slot and stack.
func (m *Manager) interactWithEntity(target *entity.Info, slot uint32, stack protocol.ItemStack, lookYOffset float32) {
	m.bot.NavigateTo(target.Position)
	time.Sleep(1 * time.Second)
	m.bot.StopMovement()

	m.bot.LookAt(target.Position.Add(mgl32.Vec3{0, lookYOffset, 0}))
	time.Sleep(200 * time.Millisecond)

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
