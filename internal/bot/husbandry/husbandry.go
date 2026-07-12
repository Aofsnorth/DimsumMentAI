// Package husbandry provides animal breeding, feeding, milking, shearing,
// and taming interactions for the bot.
package husbandry

import (
	"log/slog"
	"strings"
	"sync"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot interface for husbandry subsystem
type Bot interface {
	GetCoords() mgl32.Vec3
	WritePacket(pk packet.Packet) error
	GetEntities() map[uint64]*entity.Info
	NavigateTo(pos mgl32.Vec3)
	StopMovement()
	LookAt(pos mgl32.Vec3)
	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	EquipItem(slot uint32) error
	SendChat(msg string)
	ReportActionStatus(user string, status event.ActionStatus)
	GetEntityRuntimeID() uint64
	FormatItemName(name string) string
}

// Manager handles animal breeding, feeding, milking, and shearing
type Manager struct {
	bot    Bot
	logger *slog.Logger
	mu     sync.Mutex
	isBusy bool
}

// NewManager creates a new husbandry manager.
func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{
		bot:    bot,
		logger: logger,
	}
}

// Animal breeding food mapping
var breedFood = map[string][]string{
	"cow":     {"wheat"},
	"pig":     {"carrot", "potato", "beetroot"},
	"sheep":   {"wheat"},
	"chicken": {"wheat_seeds", "melon_seeds", "pumpkin_seeds", "beetroot_seeds"},
	"horse":   {"golden_apple", "golden_carrot"},
	"donkey":  {"golden_apple", "golden_carrot"},
	"rabbit":  {"carrot", "golden_carrot", "dandelion"},
	"wolf":    {"bone"},
	"cat":     {"cod", "salmon"},
	"ocelot":  {"cod", "salmon"},
	"parrot":  {"wheat_seeds", "melon_seeds", "pumpkin_seeds", "beetroot_seeds"},
	"llama":   {"hay_bale", "wheat"},
	"turtle":  {"seagrass"},
	"panda":   {"bamboo"},
	"fox":     {"sweet_berries", "glow_berries"},
	"bee":     {"any_flower"},
	"goat":    {"wheat"},
	"axolotl": {"tropical_fish_bucket"},
	"frog":    {"slime_ball"},
	"camel":   {"cactus"},
	"sniffer": {"torchflower_seeds"},
	"hoglin":  {"crimson_fungus"},
	"strider": {"warped_fungus"},
}

// passiveMobs are animals that can be interacted with
var passiveMobs = map[string]bool{
	"cow": true, "pig": true, "sheep": true, "chicken": true,
	"horse": true, "donkey": true, "rabbit": true, "wolf": true,
	"cat": true, "ocelot": true, "parrot": true, "llama": true,
	"turtle": true, "panda": true, "fox": true, "bee": true,
	"goat": true, "axolotl": true, "frog": true, "camel": true,
	"mooshroom": true, "sniffer": true,
}

// FindNearbyAnimals finds passive mobs within radius
func (m *Manager) FindNearbyAnimals(radius float32) []*entity.Info {
	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()

	var animals []*entity.Info
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		typeLower := strings.ToLower(ent.Type)
		if !passiveMobs[typeLower] {
			continue
		}
		dist := pos.Sub(ent.Position).Len()
		if dist <= radius {
			animals = append(animals, ent)
		}
	}
	return animals
}

// Stop stops current husbandry operation
func (m *Manager) Stop() {
	m.mu.Lock()
	m.isBusy = false
	m.mu.Unlock()
	m.bot.StopMovement()
}
