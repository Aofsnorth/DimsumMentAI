package combat

import (
	"log/slog"
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
	shieldUp     bool
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

	if t, ok := cm.recentKills[id]; ok && time.Since(t) < 2*time.Second {
		return
	}

	cm.targetID = id
	cm.inCombat = true
	cm.logger.Info("Engaging combat target", "name", entity.Name, "id", id, "type", entity.Type)

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
