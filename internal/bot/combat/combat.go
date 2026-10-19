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

// CombatMode is what the current engagement is allowed to become.
//
// Mob fights and duels are offensive: they close on the target, pick the best
// weapon, and run the full doctrine. Defence is the reflex answer to being hit
// by a player, and it is deliberately narrower — no weapon draw, no chase, and
// a hard cap on swings — because a single punch must not turn into a fight to
// the death.
type CombatMode int

const (
	// ModeMob is the ordinary hostile-mob fight.
	ModeMob CombatMode = iota
	// ModeDuel is a real PvP engagement: asked for explicitly, or earned by a
	// player who would not stop hitting.
	ModeDuel
	// ModeDefend is the proportionate answer to a player's first few hits.
	ModeDefend
)

// String makes the mode readable in logs.
func (m CombatMode) String() string {
	switch m {
	case ModeMob:
		return "mob"
	case ModeDuel:
		return "duel"
	case ModeDefend:
		return "defend"
	default:
		return "unknown"
	}
}

type CombatManager struct {
	bot          Bot
	logger       *slog.Logger
	targetID     uint64
	pvpTarget    string
	inCombat     bool
	friendlyMode bool
	shieldUp     bool
	mode         CombatMode
	mu           sync.Mutex
	lastAttack   time.Time
	// defendUntil is when the current defensive answer ends if the attacker
	// stops hitting. defendSwings is how much of the answer has been spent.
	defendUntil  time.Time
	defendSwings int
	// swingsInFight counts the swings this engagement has spent. A player
	// target that soaks several without dying is armoured, and that count is
	// what moves the doctrine from sword to axe.
	swingsInFight int
	// lastBanter spaces the mid-duel chat moments. A player fighting talks
	// DURING the fight — a taunt here, a reaction there — not just before and
	// after it; the intervals are what keep it human instead of spammy.
	lastBanter time.Time
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

// TargetIDForTest reports the entity the manager is currently fighting, so a
// test can prove the guard picked the nearer-to-the-player mob and not just any
// hostile. ok is false while nothing is engaged.
func (cm *CombatManager) TargetIDForTest() (uint64, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if !cm.inCombat {
		return 0, false
	}
	return cm.targetID, true
}

// SetLastAttackForTest backdates the previous swing so a test can reach the
// next swing without sleeping out the real melee cooldown.
func (cm *CombatManager) SetLastAttackForTest(at time.Time) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.lastAttack = at
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
	cm.mode = ModeMob
	cm.swingsInFight = 0
	cm.lastBanter = time.Now()
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
	cm.mode = ModeDuel
	cm.defendUntil = time.Time{}
	cm.defendSwings = 0
	cm.swingsInFight = 0
	cm.lastBanter = time.Now()
	cm.logger.Info("Engaging PVP target", "username", username, "id", id)
	go cm.equipBestWeapon()
	return true
}

// EngageDefender answers a player's hit proportionately: the bot turns, looks
// at them and hits back from where it stands, with whatever it is already
// holding, for at most defendMaxSwings swings inside defendLeash. It does not
// draw a weapon, does not strafe, and above all does not chase.
//
// This exists because the reflex used to call EngagePlayer directly, so one
// bare-handed punch produced a sword duel with a 32-block pursuit. Defending is
// what a person does about being poked; a duel is what they do about being
// attacked, and the ladder in retaliation.go decides which is which.
//
// Returns false when the player is not tracked. An engagement already running
// at ModeDuel is left alone: a reflex must not downgrade a real fight or steal
// its target.
func (cm *CombatManager) EngageDefender(username string) bool {
	id, _, ok := cm.bot.FindPlayer(username)
	if !ok || id == 0 {
		return false
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.inCombat && cm.mode == ModeDuel {
		return cm.pvpTarget == username
	}

	if !cm.inCombat || cm.pvpTarget != username || cm.mode != ModeDefend {
		cm.targetID = id
		cm.pvpTarget = username
		cm.inCombat = true
		cm.swingsInFight = 0
		// A fresh answer gets a fresh budget. A hit arriving inside an answer
		// that is already running only extends the window: a continuous attacker
		// must not be able to refill the swing budget with every punch, or the
		// "three swings at most" cap means nothing.
		cm.defendSwings = 0
		cm.logger.Info("Defensive response: hitting back from where the bot stands",
			"username", username, "id", id)
	}
	cm.mode = ModeDefend
	cm.defendUntil = time.Now().Add(defendDuration)
	return true
}

// ModeForTest reports the current engagement mode, so a test can prove a
// single hit produced a defensive answer rather than a duel.
func (cm *CombatManager) ModeForTest() CombatMode {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.mode
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
	cm.mode = ModeMob
	cm.defendUntil = time.Time{}
	cm.defendSwings = 0
	cm.swingsInFight = 0
	cm.bot.StopMovement()
}
