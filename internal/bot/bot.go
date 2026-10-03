package bot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot/affordance"
	"bedrock-ai/internal/bot/building/coordinator"
	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/dimension"
	"bedrock-ai/internal/bot/durability"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/exploration"
	"bedrock-ai/internal/bot/farming"
	"bedrock-ai/internal/bot/fishing"
	"bedrock-ai/internal/bot/gathering"
	"bedrock-ai/internal/bot/husbandry"
	"bedrock-ai/internal/bot/interact"
	"bedrock-ai/internal/bot/inventory"
	"bedrock-ai/internal/bot/inventory/trading"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/bot/protect"
	"bedrock-ai/internal/bot/storage"
	"bedrock-ai/internal/bot/survival"
	"bedrock-ai/internal/bot/world"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/evidence"
	"bedrock-ai/internal/handler"
	"bedrock-ai/internal/memory"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// RecipeInfo holds the ingredients and output for a crafting recipe, keyed by
// RecipeNetworkID. Populated from the server's CraftingData packet.
type RecipeInfo struct {
	Ingredients []protocol.ItemDescriptorCount
	Output      protocol.ItemStack
	Block       string // e.g. "crafting_table", "" for inventory recipes
	// Shapeless is true for shapeless recipes; shaped recipes use Width/Height.
	Shapeless bool
	Width     int32 // only meaningful for shaped recipes
	Height    int32 // only meaningful for shaped recipes
}

// Function pointers for dependency injection (resolving circular dependencies)
var (
	// PublishActionStatusFunc hands a verdict to whoever is waiting on this
	// action — the plan executor's step, most often.
	//
	// It exists because ReportActionStatus is the chat path and the chat path
	// alone. Anything that reported only through it left the planner waiting for
	// a verdict that had already been decided, so the step sat out its full
	// ninety-second timeout and failed on work that had worked. The eight-label
	// interact family reported this way and every one of its steps timed out.
	//
	// The indirection is the same one every other hook here uses: the action
	// package imports this one, so it cannot be called directly.
	PublishActionStatusFunc   func(b *Bot, status event.ActionStatus)
	SendInputLoopFunc         func(ctx context.Context, b *Bot, gd minecraft.GameData)
	PacketLoopFunc            func(ctx context.Context, b *Bot) error
	ChunkRequesterLoopFunc    func(ctx context.Context, b *Bot)
	VenityCompatLoopFunc      func(ctx context.Context, b *Bot)
	SendPlayerSkinFunc        func(b *Bot)
	SendLoadingScreenDoneFunc func(b *Bot)
	RecalculatePathFunc       func(b *Bot)
	NavigateToFunc            func(b *Bot, pos mgl32.Vec3)
	StopMovementFunc          func(b *Bot)
	NavigateToBlockFunc       func(b *Bot, x, y, z int32, tolerance float32) bool
	LookAtFunc                func(b *Bot, pos mgl32.Vec3)

	// Chat listener and action execution hooks
	InitChatListenerFunc func(ctx context.Context, b *Bot)
	ExecuteActionFunc    func(b *Bot, label, param, user string)

	// Proactive conversation loop hook. bot/chat imports bot, so we can't
	// import it back here — the loop is started via this function pointer.
	StartProactiveLoopFunc func(ctx context.Context, b *Bot)

	// StartAGILoopFunc starts the autonomy brain. bot/agi imports bot for the
	// same reason bot/chat does, so it is injected rather than imported.
	StartAGILoopFunc func(ctx context.Context, b *Bot)
	// RequestChunkRadiusFunc asks the server to stream chunks around the bot.
	// Injected because bot imports network, so network cannot import bot back.
	RequestChunkRadiusFunc func(b *Bot)

	// Planner initialization hook. bot/planner imports bot, so we can't
	// import it back here — the concrete planner is constructed via this
	// function pointer and stored as PlannerInterface.
	NewPlannerFunc func(b *Bot, client *ai.NvidiaClient) PlannerInterface
)

// PlannerInterface is implemented by bot/planner.Planner. Defined here to
// break the circular dependency (bot/planner imports bot, bot can't import
// bot/planner). The interface exposes only what the bot and chat handler
// need: running plans, canceling, and rendering the todo list.
type PlannerInterface interface {
	Run(goal, user string, actions []string)
	RunFromChat(user, request string)
	Cancel()
	IsRunning() bool
	TodoRenderForPrompt() string
	TodoRenderForChat() string
	TodoIsActive() bool
	TodoClear()
}

type Bot struct {
	Logger *slog.Logger

	// BeginEpisodeFunc, SuspendFunc and EndEpisodeFunc let the chat layer drive
	// a recording brief without importing the brain, for the same reason as
	// StartAGILoopFunc: the brain imports the bot, so a field of the brain's own
	// type on the bot would close the cycle.
	//
	// All of them are nil whenever AGI is off or never started, and every caller
	// must read nil as "autonomy is not running" rather than as a failure — a
	// bot with no brain is a perfectly ordinary configuration.
	//
	// BeginEpisodeFunc answers with primitives rather than the brain's own
	// Episode type for the same reason, and with just enough of it for the chat
	// layer to confirm out loud what it has just started.
	BeginEpisodeFunc func(line string, now time.Time) (number int, budget time.Duration, objective string, ok bool)
	SuspendFunc      func(who string)
	EndEpisodeFunc   func(reason string)
	// AutonomyContextFunc renders the same goal and plan used by the motor loop.
	// Publish and snapshot this callback under Mu; invoke it outside Mu.
	AutonomyContextFunc func() string
	// OneBlockFunc reports how the brain reads the world, so a caller outside
	// the brain can ask the same question it asks itself.
	OneBlockFunc      func(nearBlocks string) string
	Conn              *minecraft.Conn
	Dialer            DialerFunc
	Registry          *handler.Registry
	Bus               *event.Bus
	Name              string
	ServerHost        string
	VenityCompat      bool // play.venity.net hub: aggressive chunk flood + ~30s session checks
	NetherGamesCompat bool // play.nethergames.org/net: stricter login compatibility
	RewindMovement    bool // server uses RewindHistorySize / CorrectPlayerMovePrediction

	// join holds the pending server-switch request and the cancel func of the
	// live session. Switching servers is a new connection, not a packet, so it
	// has to be handed to the run loop between sessions.
	join joinState
	// command holds the last server command's output. Separate mutex from Mu so
	// the packet loop never waits on the bot lock.
	command commandState
	// JoinMessages are replayed after every successful join. A leading "/" means
	// a server command, anything else is chat.
	JoinMessages        []string
	JoinMessageDelay    time.Duration
	JoinMessageInterval time.Duration
	// SearchRadiusBlocks and SearchRadiusPortal bound how far the navigation
	// actions look for a named block and for a portal, in blocks. Zero uses the
	// package defaults.
	SearchRadiusBlocks int
	SearchRadiusPortal int

	// GeyserNoSubChunks disables the sub-chunk requester. Measured on
	// play.hansprojects.my.id: a client that sends SubChunkRequest is dropped
	// after ~25s of silence, while one that only requests a chunk radius stays
	// connected. Set from the detected server profile, not from config, so it
	// only ever applies to a Geyser front-end.
	GeyserNoSubChunks bool

	// GeyserNoHeldItemEcho suppresses the held-item MobEquipment echo after a
	// server-driven inventory update. Measured on play.nexusone.fun: echoing a
	// server-pushed Geyser custom item (GeyserHash NBT) back in a client-side
	// MobEquipment makes the session go permanently silent, while Geyser
	// servers that never receive the echo stay connected. Set from the detected
	// server profile, not from config, so it only ever applies to a Geyser
	// front-end.
	GeyserNoHeldItemEcho bool

	// IdleNudge enables the small idle steps that keep a Geyser-fronted server
	// from timing the bot out. It is set from the detected server profile, NOT
	// from config: a server the bot already sits fine on (Hans, the origin
	// servers) must keep behaving exactly as before, because the nudge is only
	// a fix for servers that drop silent clients, not a general improvement.
	IdleNudge bool

	// Idle-nudge fields. LastIdleNudgeAt schedules the next small idle step;
	// the gap and reach bound how often and how far. Zero uses package defaults.
	LastIdleNudgeAt time.Time
	IdleNudgeMinSec int
	IdleNudgeMaxSec int
	IdleNudgeReach  float32

	// CommandOutputWindow is how long a command's reply stays reportable.
	// Zero uses the package default.
	CommandOutputWindow time.Duration
	// AutoRetryWait is how long the bare command form gets to produce a reply
	// before the slash form is tried. Zero uses the package default.
	AutoRetryWait time.Duration
	// CommandPrefix selects the wire form of a server command: auto, slash or
	// none. See config.ServerConfig.CommandPrefix.
	CommandPrefix string
	// GeyserOverride is the operator's explicit `is_geyser_server` setting, or
	// nil when detection should decide. It is stored because noteServer runs
	// again on every session and must not undo an explicit choice.
	GeyserOverride *bool
	// CommandPrefixOrder decides which form "auto" tries first. It is set from
	// the detected server profile because the two orders are not
	// interchangeable: a Geyser front-end answers the slash form and ignores
	// the bare one, while other servers do the opposite. Empty means
	// bare-first, which is what the origin servers were verified against.
	CommandPrefixOrder string
	Language           string
	StatePath          string
	Memory             *memory.Store // curated long-term facts + named places (MinePal-style memory)
	Debug              bool
	ProtoSkin          protocol.Skin
	PlayerUUID         uuid.UUID

	// AI and configuration
	AiClient  *ai.NvidiaClient
	Throttler *ai.MessageThrottler
	AiCfg     config.AIConfig
	// Agicfg holds the autonomy settings. Read when the AGI loop starts, so
	// changing them takes effect on the next session rather than needing a
	// restart mid-game.
	Agicfg  config.AGIConfig
	Planner PlannerInterface

	// Player Tracking
	PlayerTracker

	// Actor Tracking
	Actors              map[uint64]*entity.Info
	UniqueIDToRuntimeID map[int64]uint64

	// Server observation. Both are written by the network goroutine and read by
	// the action goroutines, so every access goes through Mu.
	//
	// ActorEvents is a bounded ring of the server's own verdicts — a bite, a
	// tame, a rejection. EntityMetas caches the metadata that says whether a mob
	// is actually collared. Neither could be answered before: the survival
	// actions reported from what they had attempted, which is how a cast that
	// never got a bite came to read as a catch.
	ActorEvents []ActorEvent
	EntityMetas map[uint64]EntityMetaState
	// TradeWindows is the server's own offer list per villager, and xpLevel is
	// the level that pays for the ones that cost XP. Neither is derivable from
	// anything else the bot knows, so both are recorded rather than assumed.
	// See trade_state.go.
	TradeWindows map[uint64]trading.TradeWindow
	xpLevel      int32
	// xpLevelSeen distinguishes "level zero" from "no level ever observed". An
	// XP-costing trade must be refused in the second case rather than attempted
	// as if it were free.
	xpLevelSeen bool

	// Subsystems
	CombatMgr    *combat.CombatManager
	ThreatDet    *combat.ThreatDetector
	Gatherer     *gathering.ResourceGatherer
	Interactor   *interact.Interactor
	InventoryMgr *inventory.InventoryManager
	BuilderAgent *coordinator.BuilderAgent
	SurvivalMgr  *survival.Manager
	Farmer       *farming.Farmer
	Fisher       *fishing.Fisher
	HusbandryMgr *husbandry.Manager
	// Trading holds the villager trade manager. It is the one subsystem that
	// cannot act at all without the server first sending a price list, so it is
	// built here alongside the others and reports "no offers observed" until
	// the UpdateTrade handler has run at least once.
	Trading *trading.Manager
	// Protection decides which blocks the bot may break or place. It is nil
	// until configured, and a nil policy permits everything — see
	// protect.New's documented default. A protected block is refused, not
	// silently skipped, so the caller can tell a refusal from a failure.
	Protection *protect.Policy
	Explorer   *exploration.Explorer

	// Movement & Steering
	MovementState    string // "idle", "walk_to", "follow"
	TargetPos        mgl32.Vec3
	TargetPlayerName string
	TargetTolerance  float32 // arrival tolerance for walk_to (default 2.0; tightened for item pickup)
	// FollowMoving is the hysteresis-latched walk/stop state for follow mode.
	// A single distance threshold made the state flap at the boundary.
	FollowMoving bool
	// sprintHint is Jev's travel style for the current trip, set by the AGI
	// layer and read by the movement tick. Nil means no opinion: the movement
	// rules sprint on their own. Non-nil latches walk-off (false) or run
	// (true) until the trip ends or the layer clears it. A pointer, so "run
	// this trip" and "no opinion" are different states and a stale sprint
	// can never leak into a walk the model chose.
	sprintHint *bool
	// sprintHop latches with sprintHint and adds a jump to the run — the
	// bunny-hop. Stored beside the hint rather than inside it so clearing one
	// clears both and they can never disagree.
	sprintHop bool

	// LastChatPartner is the most recent player the bot had a conversation
	// with. Used by action status reports to know whom to address when the
	// action handler doesn't have a specific user.
	LastChatPartner string
	// ImplicitFollowOff is the set of players who have told the bot to stop
	// drifting toward them. It is a set rather than a flag because the opt-out
	// is per player: one person saying "berhenti ikutin aku" says nothing about
	// whether the bot should still come when somebody else says hello.
	//
	// It only gates the unprompted behaviour. An explicit "come here" still
	// works, because being told to do something and quietly doing it is not the
	// same as being followed around.
	ImplicitFollowOff map[string]bool
	LookTargetName    string
	LookTargetUntil   time.Time
	IsOnLadder        bool // shared ladder state between movement and network systems
	IsGrounded        bool
	// emoteJumpSpent bounds an emote "jump" to one physical hop: the first
	// grounded tick inside the emote's 80-tick window buys the impulse and the
	// rest of the window is visual only. Without it the physics re-buys the
	// impulse on every landing inside the window, which reads on the wire as
	// a held jump key and climbs the air. Exported for the movement packet
	// writer and its tests; treat it as movement-internal.
	EmoteJumpSpent bool
	// jumpRequestedAt latches a request to leave the ground, for the movement
	// loop to collect. See jump.go — the jump emote is not a jump, and the
	// scaffolder cannot make one without the loop's help.
	jumpRequestedAt time.Time
	// dropOK is one spent permission to leave a ledge on purpose. See
	// RequestDrop in query.go; it is spent on the first movement tick that uses
	// it, because an unspent authority is a permanent one.
	dropOK bool
	// appetite is how much risk the bot takes on purpose, and appetiteSet
	// separates "careful because it was decided" from "careful because nothing
	// has written the field yet". See SetAppetite in query.go.
	//
	// It lives here rather than in the combat manager because the disposition is
	// not a combat setting: it decides whether the bot walks a cliff or jumps
	// it, and the movement layer has to be able to read it too.
	appetite     affordance.Appetite
	appetiteSet  bool
	ParkourUntil time.Time

	// ServerGroundY is the last vertical position the server confirmed for the
	// bot, together with when it was confirmed. The server is authoritative for
	// position, so a stationary bot adopts this Y instead of free-falling.
	//
	// Without it the bot fell every tick whenever the local world model had no
	// decoded floor under it, and the server snapped it back on the next
	// CorrectPlayerMovePrediction. That fall/snap cycle made the body Y
	// oscillate by more than a block, which the look code turned into a visible
	// head tremor (and an upward aim bias) when watching a nearby player.
	ServerGroundY   float32
	ServerGroundAt  time.Time
	LastServerPosAt time.Time

	// Look angles
	Yaw                 float32
	Pitch               float32
	HeadYaw             float32 // decoupled head yaw — leads body Yaw during turns for natural motion
	IdleLookTargetYaw   float32
	IdleLookTargetPitch float32
	IdleLookTargetType  string
	IdleLookTargetID    uint64
	IdleLookTargetPos   mgl32.Vec3
	NextIdleLookChange  time.Time

	// World loading: true only after server sends LevelChunk in sub-chunk request mode.
	// Since protocol 800 (1.21.100+) Bedrock never inlines sub-chunk data into
	// LevelChunk, so this is the normal case on current servers and LAN worlds.
	SubChunkRequestMode bool
	// ChunkDimension is the dimension ID the server reported in LevelChunk.
	// SubChunkRequest must echo it or the server replies
	// SubChunkResultInvalidDimension and no terrain ever arrives.
	ChunkDimension int32
	// Dimension is the same thing in words: which of the three worlds the bot is
	// standing in. Kept alongside the raw ID because the ID only ever appears in
	// packet plumbing, and a brain that has to translate 1 into "the Nether"
	// every time it asks a question will eventually ask it wrong.
	Dimension dimension.Dimension
	// Durability is the shared count of how much life the bot's tools have left.
	// It lives on the bot rather than inside the combat manager because two
	// subsystems wear the same tools — the miner breaks pickaxes and combat
	// breaks swords — and a pickaxe that tracked its life in one place and was
	// ignored in the other would be a pickaxe that breaks mid-vein.
	Durability *durability.Tracker
	// Evidence is the durable, structured record of what the bot actually did.
	//
	// It is separate from the console logger because a console line is gone when
	// the process ends, and the questions worth asking afterwards are all about
	// sequence: which plan was it on, which steps had already completed, how many
	// times did this one fail. Those need a file, and they need a sequence
	// number, and neither is something slog gives you.
	//
	// It may be nil, and every method on it tolerates that, so a bot that never
	// opened a log behaves exactly like one that did — with nothing recorded.
	Evidence *evidence.Logger
	// SubChunkLimit is the server's advertised cap on how many sub-chunks one
	// SubChunkRequest may carry. Zero or negative means the server set no limit,
	// so the full world column is requested.
	SubChunkLimit int32

	// A* Pathfinding
	WorldModel            *pathfinder.LocalWorldModel
	WorldCache            *world.WorldCache
	CurrentPath           []pathfinder.Node
	PathIndex             int
	LastJumpPathIndex     int
	LastJumpTime          time.Time
	TicksStuck            int
	LastTickPos           mgl32.Vec3
	LastPathRecalcTime    time.Time
	ConsecutiveStuckCount int

	// StuckWindowStart/StuckWindowPos measure net displacement over a short
	// window instead of per tick. The per-tick counter (TicksStuck) cannot see
	// the two failure modes that actually strand the bot: the host rubberbanding
	// us back every few ticks, and sliding sideways along a wall. In both cases
	// the position changes on most ticks, so the per-tick test says "moving"
	// while the bot gains no ground at all — and no recovery ever ran.
	StuckWindowStart time.Time
	StuckWindowPos   mgl32.Vec3

	// WalkToRepathFailures counts consecutive walk_to re-paths that produced no
	// route, so the re-path interval can back off instead of re-running A* every
	// tick against terrain it cannot solve.
	WalkToRepathFailures int

	// Health & Hunger tracking
	Health int
	Hunger int

	// Inventory tracking
	InventoryMap    map[uint32]protocol.ItemStack
	StackNetworkIDs map[uint32]int32
	ItemNames       map[int32]string
	Recipes         map[string]uint32

	// ContainerWatch tracks the one container window the bot currently has
	// open (chest, barrel). Nil means no container is open or expected.
	ContainerWatch *ContainerWatchState
	// UnreadableContainers remembers chests whose contents failed to arrive,
	// so the multi-chest search does not re-open the same silent window in a
	// tight loop. Keyed by "x,y,z" with the time it was recorded.
	UnreadableContainers map[string]time.Time
	// SpeechOwner is whichever loop is entitled to start unprompted
	// conversations. See speech_owner.go for why exactly one may hold it.
	SpeechOwner string

	// storageSvc is the container search/open service, built on first use.
	storageSvc *storage.Service
	// RecipeCandidates maps an output item name to ALL recipe network IDs that
	// produce it. Many items (e.g. "stick") have one recipe per wood variant;
	// keeping every candidate lets the crafter pick the one whose ingredients
	// the bot actually has, instead of whichever recipe happened to be written
	// to Recipes last.
	RecipeCandidates map[string][]uint32
	RecipesByNetID   map[uint32]RecipeInfo
	HeldSlot         uint32
	StackRequestID   int32

	// craftMu serializes CraftItem so request IDs, stack-ID snapshots, and the
	// per-request response channel cannot race planner/evaluation actions.
	craftMu sync.Mutex
	placeMu sync.Mutex

	blockUpdateMu         sync.Mutex
	blockUpdateWaiters    map[protocol.BlockPos]map[uint64]chan uint32
	nextBlockUpdateWaiter uint64

	// Pending craft requests: maps ItemStackRequest.RequestID to a pending
	// craft entry. Used by CraftItem to synchronously wait for the server's
	// ItemStackResponse instead of fire-and-forget. The outputNetworkID is
	// the item type NetworkID of the recipe's output, used to fill in the
	// item type when the server creates a new slot (the response only carries
	// the stack instance ID, not the item type).
	pendingCrafts              map[int32]pendingCraft
	pendingItemStackRequest    *protocol.ItemStackRequest
	pendingItemInteractionData *protocol.UseItemTransactionData

	// Server-auth block breaking state (see breaking.go). Break actions are
	// converted to PlayerAuthInput.BlockActions when the server negotiated
	// server-authoritative block breaking.
	serverAuthBlockBreaking bool
	pendingBlockActions     []protocol.PlayerBlockAction
	miningActive            bool
	finishing               bool
	miningPos               protocol.BlockPos
	miningFace              int32

	// Emotes / Animations state
	EmoteState string
	EmoteTicks int

	// Internal bot messages tracked to prevent loops
	RecentBotMessages map[string]time.Time
	// RecentStatusReports maps a status-report signature to when it was last
	// spoken, so a retry loop does not repeat itself to the player. See
	// status_reporter.go.
	RecentStatusReports map[string]time.Time

	Mu                  sync.Mutex
	Pos                 mgl32.Vec3
	VelY                float32
	ServerTick          uint64 // monotonic input tick; synced from server packets when rewind
	TickSynced          bool   // true after first server tick reference (UpdateAttributes/MovePlayer/etc.)
	LastSentInputYaw    float32
	LastSentInputPitch  float32
	MovementSyncPending bool // send ClientMovementPredictionSync after next correction
	ScaffoldingActive   bool
	// ScaffoldStep* and ScaffoldAttempts count how many times the path node at
	// this position has tried to build its block. See scaffold_progress.go.
	ScaffoldStepX, ScaffoldStepY, ScaffoldStepZ int32
	ScaffoldAttempts                            int
	// HeadroomNode* and HeadroomDetours count how many times a node has sent the
	// planner looking for a way around a block above it. See
	// scaffold_progress.go.
	HeadroomNodeX, HeadroomNodeY, HeadroomNodeZ int32
	HeadroomDetours                             int
	// RepathInFlight and RepathTarget gate re-planning so a destination is not
	// searched for twice at once. See repath.go.
	RepathInFlight bool
	RepathTarget   mgl32.Vec3
	// GazeHint and GazeUntil are Jev's latched attention: what the head should
	// settle on while the bot is standing still. See query.go.
	GazeHint  string
	GazeUntil time.Time
}

// PlayerTracker holds the player position, orientation, name, and UUID maps
// the bot maintains from AddPlayer/MovePlayer/RemoveActor/PlayerList packets.
// It is embedded in Bot so the existing b.PlayerEntityIDs / b.PlayerPositions
// (etc.) accesses keep working via field promotion; grouping the six maps here
// is the first step of breaking up the Bot god-object by cohesive state.
type PlayerTracker struct {
	PlayerEntityIDs map[string]uint64
	PlayerUsernames map[uint64]string
	PlayerPositions map[uint64]mgl32.Vec3
	PlayerYaws      map[uint64]float32
	PlayerPitches   map[uint64]float32
	PlayerUUIDs     map[uuid.UUID]string
}

// NewPlayerTracker returns a PlayerTracker with all six maps initialized to
// empty, ready for population by the network handlers.
func NewPlayerTracker() PlayerTracker {
	return PlayerTracker{
		PlayerEntityIDs: make(map[string]uint64),
		PlayerUsernames: make(map[uint64]string),
		PlayerPositions: make(map[uint64]mgl32.Vec3),
		PlayerYaws:      make(map[uint64]float32),
		PlayerPitches:   make(map[uint64]float32),
		PlayerUUIDs:     make(map[uuid.UUID]string),
	}
}

// pendingCraft tracks a single in-flight item stack request. The response
// channel receives a craftResult when the server's ItemStackResponse arrives.
// outputNetID tags newly-created inventory slots when the response omits their
// item type.
type pendingCraft struct {
	ch          chan craftResult
	outputNetID int32
}

// StackResponseUpdate carries an authoritative stack ID for a request-local
// container slot.
type StackResponseUpdate struct {
	ContainerID    byte
	Slot           byte
	StackNetworkID int32
}

// craftResult is sent to a pending request channel when the server's
// ItemStackResponse arrives. accepted=false means rejection.
type craftResult struct {
	accepted bool
	updates  []StackResponseUpdate
}

// CraftResult creates a result without slot updates.
func CraftResult(accepted bool) craftResult {
	return craftResultWithUpdates(accepted, nil)
}

// CraftResultWithUpdates creates a result with authoritative request-local IDs.
func CraftResultWithUpdates(accepted bool, updates []StackResponseUpdate) craftResult {
	return craftResultWithUpdates(accepted, updates)
}

func craftResultWithUpdates(accepted bool, updates []StackResponseUpdate) craftResult {
	return craftResult{accepted: accepted, updates: updates}
}

func (r craftResult) stackNetworkID(containerID, slot byte) int32 {
	for _, update := range r.updates {
		if update.ContainerID == containerID && update.Slot == slot {
			return update.StackNetworkID
		}
	}
	return 0
}

// QueueItemStackRequest schedules an inventory request for the next
// PlayerAuthInput tick. Modern Bedrock sends these requests inline with player
// input instead of as standalone ItemStackRequest packets.
func (b *Bot) QueueItemStackRequest(request protocol.ItemStackRequest) error {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.pendingItemStackRequest != nil {
		return fmt.Errorf("item stack request already queued")
	}
	b.pendingItemStackRequest = &request
	return nil
}

// TakeItemStackRequest removes and returns the request queued for the next
// PlayerAuthInput tick.
func (b *Bot) TakeItemStackRequest() (protocol.ItemStackRequest, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.pendingItemStackRequest == nil {
		return protocol.ItemStackRequest{}, false
	}
	request := *b.pendingItemStackRequest
	b.pendingItemStackRequest = nil
	return request, true
}

// QueueItemInteractionData schedules an item interaction for the next
// PlayerAuthInput tick. Modern Bedrock sends these interactions inline with player
// input instead of as standalone InventoryTransaction packets.
func (b *Bot) QueueItemInteractionData(data protocol.UseItemTransactionData) error {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.pendingItemInteractionData != nil {
		return fmt.Errorf("item interaction data already queued")
	}
	b.pendingItemInteractionData = &data
	return nil
}

// TakeItemInteractionData removes and returns the interaction queued for the next
// PlayerAuthInput tick.
func (b *Bot) TakeItemInteractionData() (protocol.UseItemTransactionData, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.pendingItemInteractionData == nil {
		return protocol.UseItemTransactionData{}, false
	}
	data := *b.pendingItemInteractionData
	b.pendingItemInteractionData = nil
	return data, true
}

// PendingCraftLookup returns the channel and output NetworkID for a pending
// craft request. Returns ok=false if no pending craft exists for requestID.
// Caller MUST hold b.Mu.
func (b *Bot) PendingCraftLookup(requestID int32) (chan craftResult, int32, bool) {
	pc, ok := b.pendingCrafts[requestID]
	if !ok {
		return nil, 0, false
	}
	return pc.ch, pc.outputNetID, true
}

// PendingCraftDelete removes a pending craft entry. Caller MUST hold b.Mu.
func (b *Bot) PendingCraftDelete(requestID int32) {
	delete(b.pendingCrafts, requestID)
}

type DialerFunc func() (*minecraft.Conn, error)

func newBot(opts ...Option) (*Bot, error) {
	b := &Bot{
		PlayerTracker:       NewPlayerTracker(),
		RecentBotMessages:   make(map[string]time.Time),
		RecentStatusReports: make(map[string]time.Time),
		MovementState:       "idle",
		TargetTolerance:     2.0,
		Language:            "Indonesian",
		StatePath:           "data/bot_state.json",
		InventoryMap:        make(map[uint32]protocol.ItemStack),
		StackNetworkIDs:     make(map[uint32]int32),
		ItemNames:           make(map[int32]string),
		Recipes:             make(map[string]uint32),
		RecipeCandidates:    make(map[string][]uint32),
		RecipesByNetID:      make(map[uint32]RecipeInfo),
		pendingCrafts:       make(map[int32]pendingCraft),
		blockUpdateWaiters:  make(map[protocol.BlockPos]map[uint64]chan uint32),
		StackRequestID:      0,
		Health:              20,
		Hunger:              20,
		WorldModel:          pathfinder.NewLocalWorldModel(),
		WorldCache:          world.NewWorldCache(0, cube.Range{-64, 319}, nil),
		Actors:              make(map[uint64]*entity.Info),
		UniqueIDToRuntimeID: make(map[int64]uint64),
		VelY:                0.0,
		LastJumpPathIndex:   -1,
		Durability:          durability.NewTracker(),
		Evidence:            evidence.Open("logs/events.jsonl"),
	}

	for _, opt := range opts {
		opt(b)
	}

	if err := b.validate(); err != nil {
		return nil, fmt.Errorf("validate bot options: %w", err)
	}

	return b, nil
}

func (b *Bot) validate() error {
	if b.Logger == nil {
		return fmt.Errorf("logger is required")
	}
	if b.Dialer == nil {
		return fmt.Errorf("dialer is required")
	}
	if b.Registry == nil {
		return fmt.Errorf("registry is required")
	}
	if b.Bus == nil {
		return fmt.Errorf("event bus is required")
	}
	return nil
}

// DurabilityTracker exposes the shared tool-life counter.
//
// The combat manager and the gatherer both wear tools and both need to know how
// much life is left in them, but neither of them owns them. This is the seam:
// an optional method on the bot, rather than a field on either subsystem's
// interface, because a subsystem that has to be handed a tracker it does not use
// is a subsystem coupled to a concern that is not its own.
func (b *Bot) DurabilityTracker() *durability.Tracker {
	return b.Durability
}
