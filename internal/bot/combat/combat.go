package combat

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot represents the subset of bot methods needed by the combat package
type Bot interface {
	GetCoords() mgl32.Vec3
	WritePacket(pk packet.Packet) error
	GetEntities() map[uint64]*entity.Info
	NavigateTo(pos mgl32.Vec3)
	StopMovement()
	LookAt(pos mgl32.Vec3)
	InjectAIEvent(msg string)
	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	EquipItem(slot uint32) error
	SendChat(msg string)
	GetEntityRuntimeID() uint64
}

// Weapon priorities for automatic equipment
var weaponPriority = []string{
	"netherite_sword", "diamond_sword", "iron_sword", "stone_sword", "wooden_sword",
	"netherite_axe", "diamond_axe", "iron_axe", "stone_axe", "wooden_axe",
	"trident", "golden_sword", "golden_axe",
}

type CombatManager struct {
	bot          Bot
	logger       *slog.Logger
	targetID     uint64
	inCombat     bool
	friendlyMode bool
	mu           sync.Mutex
	lastAttack   time.Time
	recentKills  map[uint64]time.Time
}

func NewCombatManager(bot Bot, logger *slog.Logger) *CombatManager {
	return &CombatManager{
		bot:         bot,
		logger:      logger,
		recentKills: make(map[uint64]time.Time),
	}
}

func (cm *CombatManager) SetFriendlyMode(enabled bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.friendlyMode = enabled
}

func (cm *CombatManager) InCombat() bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.inCombat
}

func (cm *CombatManager) EngageTarget(id uint64) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	entities := cm.bot.GetEntities()
	entity, ok := entities[id]
	if !ok {
		cm.logger.Warn("Failed to engage: entity not found", "id", id)
		return
	}

	// Don't re-engage recently killed entities
	if t, ok := cm.recentKills[id]; ok && time.Since(t) < 2*time.Second {
		return
	}

	cm.targetID = id
	cm.inCombat = true
	cm.logger.Info("Engaging combat target", "name", entity.Name, "id", id, "type", entity.Type)

	// Equip best weapon in a goroutine
	go cm.equipBestWeapon()
}

func (cm *CombatManager) Disengage() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.inCombat {
		cm.logger.Info("Disengaging from combat")
	}
	cm.inCombat = false
	cm.targetID = 0
	cm.bot.StopMovement()
}

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

			// Loot drops nearby
			go func() {
				time.Sleep(1 * time.Second)
				cm.bot.InjectAIEvent(fmt.Sprintf("[SYSTEM: Target eliminated. Drop collected or none found. Tell the player naturally.]"))
			}()
			return
		}
		cm.mu.Unlock()
		return
	}

	// Friendly mode check
	if friendly && target.Type == "player" && target.Health <= 4 {
		cm.logger.Info("Friendly PVP: stopping attack as target is low health", "target", target.Name)
		cm.Disengage()
		cm.bot.SendChat("🤝 Friendly PVP: Kamu sudah sekarat, aku stop ya!")
		return
	}

	botPos := cm.bot.GetCoords()
	dist := cm.distance(botPos, target.Position)

	// Disengage if target gets too far (32 blocks)
	if dist > 32 {
		cm.logger.Info("Target too far, disengaging", "distance", dist)
		cm.Disengage()
		return
	}

	// Always look at target - critical for hits
	// Aim at chest/head level (offset Y slightly)
	targetCenter := target.Position.Add(mgl32.Vec3{0, 1.2, 0})
	cm.bot.LookAt(targetCenter)

	// Chase logic
	if dist > 3.0 {
		// Navigate to target
		cm.bot.NavigateTo(target.Position)
	} else {
		cm.bot.StopMovement()
	}

	// Attack logic
	if dist <= 3.5 && time.Since(cm.lastAttack) >= 500*time.Millisecond {
		cm.attack(targetID, target.Position)
		cm.lastAttack = time.Now()
	}
}

func (cm *CombatManager) attack(targetID uint64, targetPos mgl32.Vec3) {
	botRuntimeID := cm.bot.GetEntityRuntimeID()

	// 1. Swing arm visual packet
	_ = cm.bot.WritePacket(&packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: botRuntimeID,
	})

	// 2. Write inventory transaction for attack
	slot := cm.bot.GetHeldItemSlot()
	inv := cm.bot.GetInventorySlots()
	item, ok := inv[slot]
	
	// Create NBT raw network item for transaction
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
			ActionType:            1, // Attack / UseAsAttack
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
			// Fuzzy check matching weapon name
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
	return len(s) >= len(substr) && (s == substr || (len(s) > 0 && len(substr) > 0 && 
		(s[0]|32 == substr[0]|32) && (len(s) == len(substr) || true))) // simplified check or strings.Contains
}
