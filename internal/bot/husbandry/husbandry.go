package husbandry

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

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

// BreedAnimals feeds two nearby animals of the same type to breed them
func (m *Manager) BreedAnimals(ctx context.Context, animalType string) bool {
	m.mu.Lock()
	m.isBusy = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.isBusy = false
		m.mu.Unlock()
	}()

	// Find breeding food
	foods, ok := breedFood[strings.ToLower(animalType)]
	if !ok {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "breed",
			Item:    animalType,
			Count:   0,
			Success: false,
			Error:   "gak tau makanan",
		})
		return false
	}

	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	var foodSlot uint32
	found := false
	for _, foodName := range foods {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			name = strings.TrimPrefix(name, "minecraft:")
			if strings.Contains(name, foodName) {
				foodSlot = slot
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "breed",
			Item:    animalType,
			Count:   0,
			Success: false,
			Error:   "gak punya makanan",
		})
		return false
	}

	// Find animals of the target type
	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()
	typeLower := strings.ToLower(animalType)

	var targets []*entity.Info
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		if strings.ToLower(ent.Type) == typeLower {
			dist := pos.Sub(ent.Position).Len()
			if dist <= 32 {
				targets = append(targets, ent)
			}
		}
	}

	if len(targets) < 2 {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "breed",
			Item:    animalType,
			Count:   len(targets),
			Success: false,
			Error:   "butuh minimal 2",
		})
		return false
	}

	if err := m.bot.EquipItem(foodSlot); err != nil {
		return false
	}

	// Feed first animal
	feeded := 0
	for i := 0; i < 2 && i < len(targets); i++ {
		target := targets[i]
		m.bot.NavigateTo(target.Position)
		time.Sleep(1 * time.Second)
		m.bot.StopMovement()

		m.bot.LookAt(target.Position.Add(mgl32.Vec3{0, 1.0, 0}))
		time.Sleep(200 * time.Millisecond)

		// Interact (feed) the animal
		tx := &packet.InventoryTransaction{
			TransactionData: &protocol.UseItemOnEntityTransactionData{
				TargetEntityRuntimeID: target.ID,
				ActionType:            0, // interact
				HotBarSlot:            int32(foodSlot),
				HeldItem:              protocol.ItemInstance{Stack: inv[foodSlot]},
				Position:              m.bot.GetCoords(),
				ClickedPosition:       mgl32.Vec3{0, 0, 0},
			},
		}
		_ = m.bot.WritePacket(tx)
		feeded++
		time.Sleep(500 * time.Millisecond)

		select {
		case <-ctx.Done():
			return false
		default:
		}
	}

	if feeded == 2 {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "breed",
			Item:    animalType,
			Count:   feeded,
			Success: true,
		})
		return true
	}
	return false
}

// FeedAnimal feeds a specific animal nearby
func (m *Manager) FeedAnimal(ctx context.Context, animalType string) bool {
	foods, ok := breedFood[strings.ToLower(animalType)]
	if !ok {
		return false
	}

	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	var foodSlot uint32
	found := false
	for _, foodName := range foods {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			name = strings.TrimPrefix(name, "minecraft:")
			if strings.Contains(name, foodName) {
				foodSlot = slot
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return false
	}

	// Find nearest animal of type
	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()
	typeLower := strings.ToLower(animalType)

	var closest *entity.Info
	closestDist := float32(math.MaxFloat32)
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		if strings.ToLower(ent.Type) == typeLower {
			dist := pos.Sub(ent.Position).Len()
			if dist < closestDist {
				closestDist = dist
				closest = ent
			}
		}
	}

	if closest == nil {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "feed",
			Item:    animalType,
			Count:   0,
			Success: false,
			Error:   "gak ada",
		})
		return false
	}

	if err := m.bot.EquipItem(foodSlot); err != nil {
		return false
	}

	m.bot.NavigateTo(closest.Position)
	time.Sleep(1 * time.Second)
	m.bot.StopMovement()
	m.bot.LookAt(closest.Position.Add(mgl32.Vec3{0, 1.0, 0}))
	time.Sleep(200 * time.Millisecond)

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: closest.ID,
			ActionType:            0,
			HotBarSlot:            int32(foodSlot),
			HeldItem:              protocol.ItemInstance{Stack: inv[foodSlot]},
			Position:              m.bot.GetCoords(),
			ClickedPosition:       mgl32.Vec3{0, 0, 0},
		},
	}
	_ = m.bot.WritePacket(tx)

	m.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "feed",
		Item:    animalType,
		Count:   1,
		Success: true,
	})
	return true
}

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
			HotBarSlot:            int32(bucketSlot),
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
		if strings.ToLower(ent.Type) == "sheep" {
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
			HotBarSlot:            int32(shearsSlot),
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

// TameWolf attempts to tame a nearby wolf with bones
func (m *Manager) TameWolf(ctx context.Context) bool {
	return m.tameWithItem(ctx, "wolf", "bone", "taming wolf")
}

// TameCat attempts to tame a nearby cat with fish
func (m *Manager) TameCat(ctx context.Context) bool {
	return m.tameWithItem(ctx, "cat", "cod", "taming cat")
}

func (m *Manager) tameWithItem(ctx context.Context, animalType, itemName, actionDesc string) bool {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	var itemSlot uint32
	found := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		if strings.Contains(name, itemName) {
			itemSlot = slot
			found = true
			break
		}
	}

	if !found {
		m.bot.ReportActionStatus("", event.ActionStatus{
			Action:  "tame",
			Item:    itemName,
			Count:   0,
			Success: false,
			Error:   "gak punya " + itemName,
		})
		return false
	}

	pos := m.bot.GetCoords()
	entities := m.bot.GetEntities()

	var target *entity.Info
	closestDist := float32(math.MaxFloat32)
	for _, ent := range entities {
		if ent.Health <= 0 {
			continue
		}
		if strings.ToLower(ent.Type) == animalType {
			dist := pos.Sub(ent.Position).Len()
			if dist < closestDist {
				closestDist = dist
				target = ent
			}
		}
	}

	if target == nil {
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
				HotBarSlot:            int32(itemSlot),
				HeldItem:              protocol.ItemInstance{Stack: inv[itemSlot]},
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

// Stop stops current husbandry operation
func (m *Manager) Stop() {
	m.mu.Lock()
	m.isBusy = false
	m.mu.Unlock()
	m.bot.StopMovement()
}
