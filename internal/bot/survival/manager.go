// Package survival provides automation for managing the bot's survival needs,
// including auto-eat, auto-armor, auto-tool, time tracking, bed sleeping, torch
// placement, death recovery, shelter, and potions.
package survival

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"
)

// Bot interface for survival subsystem
type Bot interface {
	GetCoords() mgl32.Vec3
	WritePacket(pk packet.Packet) error
	GetEntities() map[uint64]*entity.Info
	NavigateTo(pos mgl32.Vec3)
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	InjectAIEvent(msg string)
	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	EquipItem(slot uint32) error
	UnequipItem() error
	SendChat(msg string)
	ReportActionStatus(user string, status event.ActionStatus)
	GetEntityRuntimeID() uint64
	GetLocalWorldModel() entity.WorldModel
	GetBlockName(x, y, z int32) (string, bool)
	// IsBusy reports whether the bot is already occupied. The reactive loop
	// uses it to decide whether a self-care action may interrupt, and it is the
	// same definition the AGI loop uses, so the two never disagree about
	// whether the bot is free.
	IsBusy() bool
}

// Manager handles all survival automation: auto-eat, auto-armor, auto-tool,
// time tracking, bed sleeping, torch placement, death recovery, shelter, potions.
type Manager struct {
	bot    Bot
	logger *slog.Logger
	mu     sync.Mutex

	// Auto-eat state
	lastEatTime time.Time
	autoEatOn   bool
	hungerLevel int

	// Auto-armor state
	autoArmorOn bool

	// Time tracking
	worldTime int64 // 0-24000 ticks (0=dawn, 6000=noon, 12000=dusk, 18000=midnight)
	isNight   bool
	isDay     bool

	// Death recovery
	lastDeathPos    mgl32.Vec3
	hasDiedRecently bool
	deathTime       time.Time

	// Shelter
	isSheltering bool

	// Torch
	lastTorchTime time.Time
	autoTorchOn   bool

	// Night enables the reactive night routine (bed or shelter). It has its own
	// switch, separate from the per-behaviour ones, because "let the bot handle
	// nightfall" is a policy a server owner may want off while still allowing
	// torch placement.
	autoNightOn bool

	// reactive holds the cooldowns for the unattended behaviours.
	reactive reactiveState

	// Potion
	lastPotionTime time.Time

	// Configuration
	EatThreshold       int // hunger level to trigger auto-eat (default 10)
	ArmorEnabled       bool
	AutoTorchEnabled   bool
	AutoSleepEnabled   bool
	AutoShelterEnabled bool
}

func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{
		bot:       bot,
		logger:    logger,
		autoEatOn: true,
		// autoTorchOn was never set, so the torch behaviour was dead twice
		// over: this private gate was false, so even AutoPlaceTorches called
		// directly returned immediately, and nothing called it either. The
		// public AutoTorchEnabled flag below was advertising a feature that had
		// never once run.
		autoTorchOn:        true,
		autoArmorOn:        true,
		autoNightOn:        true,
		hungerLevel:        20,
		EatThreshold:       10,
		ArmorEnabled:       true,
		AutoTorchEnabled:   true,
		AutoSleepEnabled:   true,
		AutoShelterEnabled: true,
	}
}

// SetHunger updates the tracked hunger level (called from packet handler)
func (m *Manager) SetHunger(hunger int) {
	m.mu.Lock()
	m.hungerLevel = hunger
	m.mu.Unlock()
}

// Tick lives in reactive.go, where the reactive half is explained.

// healingPotions lists potion types that restore health
var healingPotions = []string{
	"potion_of_healing",
	"potion_of_regeneration",
	"potion_of_slow_falling",
}

// UseHealingPotion attempts to drink a healing potion
func (m *Manager) UseHealingPotion() bool {
	if time.Since(m.lastPotionTime) < 5*time.Second {
		return false
	}

	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		name = strings.TrimPrefix(name, "minecraft:")

		for _, potionName := range healingPotions {
			if strings.Contains(name, potionName) || (strings.Contains(name, "potion") && strings.Contains(name, "heal")) {
				if err := m.bot.EquipItem(slot); err != nil {
					continue
				}
				time.Sleep(150 * time.Millisecond)

				// Use the potion (same as eating)
				tx := &packet.InventoryTransaction{
					TransactionData: &protocol.UseItemTransactionData{
						ActionType:      protocol.UseItemActionClickBlock,
						BlockPosition:   protocol.BlockPos{0, -1, 0},
						BlockFace:       255,
						HotBarSlot:      safecast.To[int32](slot),
						HeldItem:        protocol.ItemInstance{Stack: item},
						Position:        m.bot.GetCoords(),
						ClickedPosition: mgl32.Vec3{0, 0, 0},
					},
				}
				_ = m.bot.WritePacket(tx)
				time.Sleep(1000 * time.Millisecond)

				m.mu.Lock()
				m.lastPotionTime = time.Now()
				m.mu.Unlock()
				m.logger.Info("Used healing potion", "name", name)
				return true
			}
		}
	}
	return false
}
