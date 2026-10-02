package combat

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"bedrock-ai/internal/bot/durability"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"

	"bedrock-ai/internal/bot/affordance"
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
	ReportActionStatus(user string, status event.ActionStatus)
	GetEntityRuntimeID() uint64
	GetLocalWorldModel() entity.WorldModel
	GetBlockName(x, y, z int32) (string, bool)
	FindPlayer(username string) (uint64, mgl32.Vec3, bool)
	// Appetite is how much risk the bot takes on purpose. It is read per tick
	// rather than captured at construction because the model moves it, and a
	// tactic computed once would be the same rigidity this replaced.
	Appetite() affordance.Appetite
	// FindScaffoldItem locates a block the bot could put down, which is what a
	// composed defence needs before it can compose anything.
	FindScaffoldItem() (uint32, protocol.ItemStack, bool)
	// PlaceShield puts a block between the body and a threat. It reports
	// whether the server actually placed it, so the caller can tell a wall from
	// a wish — which matters here, because the alternative to the wall is
	// running, and running is the safe answer either way.
	PlaceShield(ctx context.Context, threat mgl32.Vec3) bool
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
	pvpTarget    string
	inCombat     bool
	friendlyMode bool
	shieldUp     bool
	mu           sync.Mutex
	lastAttack   time.Time
	// Shot is the state of the ranged shot currently in flight: the draw is
	// held across several ticks before the release goes out.
	Shot Shot
	// dragonAction is the posture the End fight last chose. It is kept only so
	// the fight can log the moment the plan changes instead of logging the same
	// line five times a second, which is how a log becomes unreadable.
	dragonAction    DragonAction
	dragonActionSet bool
	recentKills     map[uint64]time.Time
	// durability counts swings per held slot so a tool can be swapped before
	// it breaks mid-vein. It is shared with the gatherer through the bot, so
	// one pickaxe has one life whether it is used on a skeleton or on stone.
	durability *durability.Tracker
}

func NewCombatManager(bot Bot, logger *slog.Logger) *CombatManager {
	cm := &CombatManager{
		bot:         bot,
		logger:      logger,
		recentKills: make(map[uint64]time.Time),
	}
	// The shared tracker lives on the bot, because the miner wears the same
	// tools. It is picked up through an optional assertion rather than added to
	// the interface: a bot that does not carry one simply gets a local tracker,
	// which is the old behaviour and still correct on its own.
	if carrier, ok := bot.(interface{ DurabilityTracker() *durability.Tracker }); ok {
		cm.durability = carrier.DurabilityTracker()
	}
	if cm.durability == nil {
		cm.durability = durability.NewTracker()
	}
	return cm
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

// PvpTargetForTest reports the retained PVP username, so a test can prove the
// engagement was resolved by name and not by a stale entity ID.
func (cm *CombatManager) PvpTargetForTest() string {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.pvpTarget
}

// SetShotDrawStartForTest backdates the in-flight draw under the manager lock, so
// a test can reach the release half of the draw/hold/release state machine
// without sleeping out the real 1.1 s draw.
func (cm *CombatManager) SetShotDrawStartForTest(at time.Time) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.Shot.SetDrawStartForTest(at)
}

func (cm *CombatManager) EngageTarget(id uint64) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	entities := cm.bot.GetEntities()
	info, ok := entities[id]
	if !ok {
		cm.logger.Warn("Failed to engage: entity not found", "id", id)
		return
	}

	if t, ok := cm.recentKills[id]; ok && time.Since(t) < 2*time.Second {
		return
	}

	cm.targetID = id
	cm.pvpTarget = ""
	cm.inCombat = true
	cm.logger.Info("Engaging combat target", "name", info.Name, "id", id, "type", info.Type)

	go cm.equipBestWeapon()
}

// EngagePlayer targets a tracked player by username for PVP. The username is
// retained so the tick loop can re-resolve the fresh position each tick.
// Returns false when the player is not currently tracked.
func (cm *CombatManager) EngagePlayer(username string) bool {
	id, _, ok := cm.bot.FindPlayer(username)
	if !ok || id == 0 {
		return false
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.targetID = id
	cm.pvpTarget = username
	cm.inCombat = true
	cm.logger.Info("Engaging PVP target", "username", username, "id", id)
	go cm.equipBestWeapon()
	return true
}

func (cm *CombatManager) Disengage() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.inCombat {
		cm.logger.Info("Disengaging from combat")
	}
	cm.inCombat = false
	cm.targetID = 0
	cm.pvpTarget = ""
	cm.bot.StopMovement()
}
