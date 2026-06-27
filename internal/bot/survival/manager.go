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

	// Potion
	lastPotionTime time.Time

	// Shield
	shieldActive bool

	// Configuration
	EatThreshold       int // hunger level to trigger auto-eat (default 10)
	ArmorEnabled       bool
	AutoTorchEnabled   bool
	AutoSleepEnabled   bool
	AutoShelterEnabled bool
}

func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{
		bot:                bot,
		logger:             logger,
		autoEatOn:          true,
		autoArmorOn:        true,
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

// SetWorldTime updates the tracked world time (called from packet handler)
func (m *Manager) SetWorldTime(ticks int64) {
	m.mu.Lock()
	m.worldTime = ticks % 24000
	m.isNight = m.worldTime >= 12500 && m.worldTime <= 23500
	m.isDay = !m.isNight
	m.mu.Unlock()
}

// IsNight returns whether it's currently nighttime
func (m *Manager) IsNight() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isNight
}

// GetWorldTime returns current world time in ticks (0-24000)
func (m *Manager) GetWorldTime() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.worldTime
}

// GetTimeOfDay returns a human-readable time description
func (m *Manager) GetTimeOfDay() string {
	m.mu.Lock()
	t := m.worldTime
	m.mu.Unlock()

	switch {
	case t >= 0 && t < 1000:
		return "pagi (fajar)"
	case t >= 1000 && t < 6000:
		return "siang"
	case t >= 6000 && t < 12000:
		return "sore"
	case t >= 12000 && t < 13000:
		return "senja"
	case t >= 13000 && t < 18000:
		return "malam"
	case t >= 18000 && t < 23000:
		return "tengah malam"
	default:
		return "menjelang pagi"
	}
}

// MarkDeath records the bot's death position for recovery
func (m *Manager) MarkDeath(pos mgl32.Vec3) {
	m.mu.Lock()
	m.lastDeathPos = pos
	m.hasDiedRecently = true
	m.deathTime = time.Now()
	m.mu.Unlock()
	m.logger.Info("Death recorded", "pos", pos)
}

// GetDeathPos returns the last death position
func (m *Manager) GetDeathPos() (mgl32.Vec3, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastDeathPos, m.hasDiedRecently
}

// ClearDeath clears the death recovery flag
func (m *Manager) ClearDeath() {
	m.mu.Lock()
	m.hasDiedRecently = false
	m.mu.Unlock()
}

// Tick runs the survival automation loop (called every 500ms)
func (m *Manager) Tick() {
	m.tickAutoEat()
	m.tickAutoArmor()
}

// ===================== AUTO-EAT =====================

// Food items ranked by hunger restoration
var foodPriority = []string{
	"golden_apple", "enchanted_golden_apple",
	"cooked_beef", "cooked_porkchop", "cooked_mutton", "cooked_chicken", "cooked_rabbit",
	"cooked_salmon", "cooked_cod",
	"bread", "baked_potato", "pumpkin_pie",
	"apple", "carrot", "sweet_berries", "glow_berries",
	"melon_slice", "beetroot", "dried_kelp",
	"cookie",
}

func (m *Manager) tickAutoEat() {
	if !m.autoEatOn {
		return
	}

	m.mu.Lock()
	hunger := m.hungerLevel
	lastEat := m.lastEatTime
	m.mu.Unlock()

	// Only eat when hunger is below threshold and cooldown passed
	if hunger > m.EatThreshold {
		return
	}
	if time.Since(lastEat) < 3*time.Second {
		return
	}

	m.logger.Info("Auto-eat triggered", "hunger", hunger, "threshold", m.EatThreshold)

	if m.EatBestFood() {
		m.mu.Lock()
		m.lastEatTime = time.Now()
		m.mu.Unlock()
	}
}

// EatBestFood finds and eats the best available food item
func (m *Manager) EatBestFood() bool {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	// Try food items in priority order
	for _, foodName := range foodPriority {
		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			name = strings.TrimPrefix(name, "minecraft:")
			if strings.Contains(name, foodName) {
				if m.eatFoodItem(slot, item) {
					m.logger.Info("Auto-eat: consumed food", "name", name, "slot", slot)
					return true
				}
			}
		}
	}

	m.logger.Debug("Auto-eat: no food found in inventory")
	return false
}

func (m *Manager) eatFoodItem(slot uint32, item protocol.ItemStack) bool {
	// Equip the food item
	if err := m.bot.EquipItem(slot); err != nil {
		return false
	}
	time.Sleep(150 * time.Millisecond)

	// Send use item packet (eating)
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{0, -1, 0},
			BlockFace:       255, // self-use
			HotBarSlot:      int32(slot),
			HeldItem:        protocol.ItemInstance{Stack: item},
			Position:        m.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0, 0, 0},
		},
	}
	if err := m.bot.WritePacket(tx); err != nil {
		return false
	}
	time.Sleep(1600 * time.Millisecond) // eating takes ~1.6 seconds
	return true
}

// EnableAutoEat enables/disables auto-eat
func (m *Manager) EnableAutoEat(enabled bool) {
	m.mu.Lock()
	m.autoEatOn = enabled
	m.mu.Unlock()
}

// ===================== AUTO-ARMOR =====================

// Armor slots: 0=helmet, 1=chestplate, 2=leggings, 3=boots
var armorSlots = []struct {
	name     string
	slotID   uint32
	keywords []string
}{
	{"helmet", 0, []string{"helmet", "cap", "crown"}},
	{"chestplate", 1, []string{"chestplate", "tunic"}},
	{"leggings", 2, []string{"leggings", "pants"}},
	{"boots", 3, []string{"boots", "shoes"}},
}

// Armor tier priority (best first)
var armorTierPriority = []string{
	"netherite", "diamond", "iron", "chainmail", "golden", "leather",
}

func (m *Manager) tickAutoArmor() {
	if !m.autoArmorOn || !m.ArmorEnabled {
		return
	}
	// Auto-armor is event-driven (called explicitly), not ticked every frame
	// to avoid constant re-equipping. We just check periodically.
}

// EquipBestArmor equips the best available armor pieces
func (m *Manager) EquipBestArmor() int {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()
	equipped := 0

	for _, armorSlot := range armorSlots {
		bestSlot := uint32(0)
		bestTier := -1
		found := false

		for slot, item := range inv {
			if item.Count <= 0 {
				continue
			}
			name := strings.ToLower(names[item.NetworkID])
			name = strings.TrimPrefix(name, "minecraft:")

			// Check if this item matches the armor type
			isArmorType := false
			for _, kw := range armorSlot.keywords {
				if strings.Contains(name, kw) {
					isArmorType = true
					break
				}
			}
			if !isArmorType {
				continue
			}

			// Determine tier
			for tierIdx, tierName := range armorTierPriority {
				if strings.Contains(name, tierName) {
					tierScore := len(armorTierPriority) - tierIdx
					if tierScore > bestTier {
						bestTier = tierScore
						bestSlot = slot
						found = true
					}
					break
				}
			}
		}

		if found {
			// Equip via armor swap packet (slot 36-39 are armor slots in Bedrock)
			m.logger.Info("Auto-armor: equipping", "type", armorSlot.name, "slot", bestSlot)
			_ = m.bot.EquipItem(bestSlot)
			equipped++
			time.Sleep(200 * time.Millisecond)
		}
	}

	if equipped > 0 {
		m.logger.Info("Auto-armor complete", "pieces_equipped", equipped)
	}
	return equipped
}

// EnableAutoArmor enables/disables auto-armor
func (m *Manager) EnableAutoArmor(enabled bool) {
	m.mu.Lock()
	m.autoArmorOn = enabled
	m.ArmorEnabled = enabled
	m.mu.Unlock()
}

// ===================== AUTO-TOOL =====================

// Tool mapping: block type -> best tool type
var blockToolMap = map[string][]string{
	"stone":          {"pickaxe"},
	"cobblestone":    {"pickaxe"},
	"deepslate":      {"pickaxe"},
	"iron_ore":       {"pickaxe"},
	"gold_ore":       {"pickaxe"},
	"diamond_ore":    {"pickaxe"},
	"coal_ore":       {"pickaxe"},
	"redstone_ore":   {"pickaxe"},
	"lapis_ore":      {"pickaxe"},
	"emerald_ore":    {"pickaxe"},
	"copper_ore":     {"pickaxe"},
	"netherrack":     {"pickaxe"},
	"obsidian":       {"pickaxe"},
	"oak_log":        {"axe"},
	"birch_log":      {"axe"},
	"spruce_log":     {"axe"},
	"jungle_log":     {"axe"},
	"acacia_log":     {"axe"},
	"dark_oak_log":   {"axe"},
	"oak_planks":     {"axe"},
	"crafting_table": {"axe"},
	"chest":          {"axe"},
	"dirt":           {"shovel"},
	"sand":           {"shovel"},
	"gravel":         {"shovel"},
	"clay":           {"shovel"},
	"soul_sand":      {"shovel"},
	"snow":           {"shovel"},
	"grass_block":    {"shovel"},
	"hay_block":      {"hoe"},
	"wheat":          {"hoe"},
	"leaves":         {"shears"},
}

// Tool tier priority (best first)
var toolTierPriority = []string{
	"netherite", "diamond", "iron", "stone", "golden", "wooden",
}

// SelectBestTool finds and equips the best tool for the given block name
func (m *Manager) SelectBestTool(blockName string) bool {
	blockName = strings.ToLower(strings.TrimPrefix(blockName, "minecraft:"))

	// Determine what tool type we need
	neededToolTypes := m.getToolTypesForBlock(blockName)
	if len(neededToolTypes) == 0 {
		return false // no specific tool needed
	}

	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	bestSlot := uint32(0)
	bestScore := -1
	found := false

	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[item.NetworkID])
		name = strings.TrimPrefix(name, "minecraft:")

		for _, toolType := range neededToolTypes {
			if !strings.Contains(name, toolType) {
				continue
			}

			// Calculate score based on tier
			score := 0
			for tierIdx, tierName := range toolTierPriority {
				if strings.Contains(name, tierName) {
					score = (len(toolTierPriority) - tierIdx) * 10
					break
				}
			}

			if score > bestScore {
				bestScore = score
				bestSlot = slot
				found = true
			}
		}
	}

	if found {
		if err := m.bot.EquipItem(bestSlot); err != nil {
			m.logger.Warn("Auto-tool: failed to equip", "slot", bestSlot, "err", err)
			return false
		}
		m.logger.Debug("Auto-tool: equipped best tool", "block", blockName, "slot", bestSlot)
		return true
	}

	return false
}

func (m *Manager) getToolTypesForBlock(blockName string) []string {
	// Check direct mapping
	for key, tools := range blockToolMap {
		if strings.Contains(blockName, key) {
			return tools
		}
	}

	// Generic ore detection
	if strings.Contains(blockName, "ore") {
		return []string{"pickaxe"}
	}
	// Generic log/wood detection
	if strings.Contains(blockName, "log") || strings.Contains(blockName, "wood") || strings.Contains(blockName, "planks") {
		return []string{"axe"}
	}
	return nil
}

// ===================== POTION USAGE =====================

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
						HotBarSlot:      int32(slot),
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
