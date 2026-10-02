// Package husbandry feeds, breeds, tames, milks, and shears animals — and
// reports nothing it has not seen the server confirm.
//
// The three routines that used to live here all reported success as a
// consequence of having been attempted: breeding said yes after clicking two
// animals, taming said yes after five tries, milking said yes after one
// interaction. None of them looked at an animal or an inventory. Every one of
// them could be run on an empty pasture and would report a happy result, which
// is precisely the class of lie this project exists to remove.
//
// Confirmation now comes from one of two places, in this order:
//
//   - an observation seam (see observe.go) carrying the server's own verdict:
//     the taming-succeeded/failed actor events, the love-hearts event, or the
//     collared/owner flag on the animal's entity metadata;
//   - the inventory itself, which is server-authoritative: a milk bucket
//     appearing, a ball of wool appearing, the food actually being eaten.
//
// When neither is available the answer is false and the reason says so. The
// seams are real gaps in the network layer, not hedges: this package documents
// exactly which packet handler is missing, at the bottom of observe.go.
package husbandry

import (
	"log/slog"
	"sync"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot interface for husbandry subsystem.
//
// Unchanged from the version this package replaces, so no caller outside it has
// to be touched. The observation seams are separate interfaces for the same
// reason they are separate here and not in fishing: the bot cannot supply them
// yet.
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

// Manager handles animal breeding, feeding, milking, and shearing.
type Manager struct {
	bot    Bot
	logger *slog.Logger

	mu      sync.Mutex
	isBusy  bool
	timings Timings

	// meta and events are the two optional observation seams. Both nil means
	// "the server's verdict is not observable", which is a different thing
	// from "the server said no" and is reported as such.
	meta   EntityMetaSource
	events ActorEventSource
}

// NewManager creates a new husbandry manager.
func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{
		bot:     bot,
		logger:  logger,
		timings: DefaultTimings(),
	}
}

// SetTimings overrides the interaction timings. Production leaves them at
// DefaultTimings; the override exists so a test does not have to sit through
// the settle delays between every click.
func (m *Manager) SetTimings(t Timings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timings = t
}

// SetEntityMetaSource wires the entity-metadata seam.
//
// Nil is meaningful: it says the collared/owner flags cannot be read, and
// taming then refuses to report success rather than guessing from a raw
// entity.Info, which carries no metadata at all.
func (m *Manager) SetEntityMetaSource(src EntityMetaSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.meta = src
}

// SetActorEventSource wires the actor-event seam.
//
// Nil means the server's taming and love-heart events are not being observed.
// Taming then has only the metadata channel, and breeding only the inventory.
func (m *Manager) SetActorEventSource(src ActorEventSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = src
}

// breedFood maps an animal type to the items that put it in love mode.
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

// passiveMobs are animals that can be interacted with.
var passiveMobs = map[string]bool{
	"cow": true, "pig": true, "sheep": true, "chicken": true,
	"horse": true, "donkey": true, "rabbit": true, "wolf": true,
	"cat": true, "ocelot": true, "parrot": true, "llama": true,
	"turtle": true, "panda": true, "fox": true, "bee": true,
	"goat": true, "axolotl": true, "frog": true, "camel": true,
	"mooshroom": true, "sniffer": true,
}

// FindNearbyAnimals finds passive mobs within radius.
func (m *Manager) FindNearbyAnimals(radius float32) []*entity.Info {
	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()

	var animals []*entity.Info
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		if !passiveMobs[entity.NormalizeName(ent.Type)] {
			continue
		}
		if pos.Sub(ent.Position).Len() <= radius {
			animals = append(animals, ent)
		}
	}
	return animals
}

// Stop stops current husbandry operation.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.isBusy = false
	m.mu.Unlock()
	m.bot.StopMovement()
}

func (m *Manager) currentTimings() Timings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.timings
}

func (m *Manager) metaSource() EntityMetaSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.meta
}

func (m *Manager) eventSource() ActorEventSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.events
}
