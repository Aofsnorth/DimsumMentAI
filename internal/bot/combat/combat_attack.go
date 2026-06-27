package combat

import (
	"bedrock-ai/internal/event"
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (cm *CombatManager) Tick(ctx context.Context) {
	cm.mu.Lock()
	if !cm.inCombat {
		cm.mu.Unlock()
		return
	}
	targetID := cm.targetID
	friendly := cm.friendlyMode
	cm.mu.Unlock()

	entities := cm.bot.GetEntities()
	target, ok := entities[targetID]
	if !ok || target.Health <= 0 {
		cm.mu.Lock()
		if cm.inCombat && cm.targetID == targetID {
			cm.recentKills[targetID] = time.Now()
			cm.inCombat = false
			cm.targetID = 0
			cm.logger.Info("Target eliminated or despawned")
			cm.mu.Unlock()
			cm.bot.StopMovement()

			go func() {
				time.Sleep(1 * time.Second)
				cm.bot.InjectAIEvent(fmt.Sprintf("[SYSTEM: Target eliminated. Drop collected or none found. Tell the player naturally.]"))
			}()
			return
		}
		cm.mu.Unlock()
		return
	}

	if friendly && target.Type == "player" && target.Health <= 4 {
		cm.logger.Info("Friendly PVP: stopping attack as target is low health", "target", target.Name)
		cm.Disengage()
		cm.bot.ReportActionStatus("", event.ActionStatus{Action: "combat", Success: true, Error: "friendly PVP stop"})
		return
	}

	botPos := cm.bot.GetCoords()
	dist := cm.distance(botPos, target.Position)

	if dist > 32 {
		cm.logger.Info("Target too far, disengaging", "distance", dist)
		cm.Disengage()
		return
	}

	targetCenter := target.Position.Add(mgl32.Vec3{0, 1.2, 0})
	cm.bot.LookAt(targetCenter)

	if dist > 3.0 {
		cm.bot.NavigateTo(target.Position)
	} else {
		cm.bot.StopMovement()
	}

	if dist <= 3.5 && time.Since(cm.lastAttack) >= 500*time.Millisecond {
		cm.attack(targetID, target.Position)
		cm.lastAttack = time.Now()
	}
}

func (cm *CombatManager) attack(targetID uint64, targetPos mgl32.Vec3) {
	botRuntimeID := cm.bot.GetEntityRuntimeID()

	_ = cm.bot.WritePacket(&packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: botRuntimeID,
	})

	slot := cm.bot.GetHeldItemSlot()
	inv := cm.bot.GetInventorySlots()
	item, ok := inv[slot]

	var rawItem protocol.ItemInstance
	if ok && item.Count > 0 {
		rawItem = protocol.ItemInstance{
			Stack: item,
		}
	} else {
		rawItem = protocol.ItemInstance{}
	}

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: targetID,
			ActionType:            1,
			HotBarSlot:            int32(slot),
			HeldItem:              rawItem,
			Position:              cm.bot.GetCoords(),
			ClickedPosition:       mgl32.Vec3{0, 0, 0},
		},
	}

	if err := cm.bot.WritePacket(tx); err != nil {
		cm.logger.Error("Failed to write combat attack transaction", "error", err)
	}
}

func (cm *CombatManager) equipBestWeapon() {
	inv := cm.bot.GetInventorySlots()
	names := cm.bot.GetItemNames()

	for _, weaponName := range weaponPriority {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name, ok := names[item.NetworkID]
			if !ok {
				continue
			}
			if containsIgnoreCase(name, weaponName) {
				if err := cm.bot.EquipItem(slot); err == nil {
					cm.logger.Info("Equipped best weapon for combat", "name", name, "slot", slot)
					return
				}
			}
		}
	}
	cm.logger.Info("No weapon found in inventory; fighting with fists")
}

func (cm *CombatManager) distance(a, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

func containsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
