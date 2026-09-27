package bot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot/building/coordinator"
	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/exploration"
	"bedrock-ai/internal/bot/farming"
	"bedrock-ai/internal/bot/fishing"
	"bedrock-ai/internal/bot/gathering"
	"bedrock-ai/internal/bot/husbandry"
	"bedrock-ai/internal/bot/interact"
	"bedrock-ai/internal/bot/inventory"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/bot/storage"
	"bedrock-ai/internal/bot/survival"
	"bedrock-ai/internal/bot/world"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/event"
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
	Logger            *slog.Logger
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
	Explorer     *exploration.Explorer

	// Movement & Steering
	MovementState    string // "idle", "walk_to", "follow"
	TargetPos        mgl32.Vec3
	TargetPlayerName string
	TargetTolerance  float32 // arrival tolerance for walk_to (default 2.0; tightened for item pickup)
	// FollowMoving is the hysteresis-latched walk/stop state for follow mode.
	// A single distance threshold made the state flap at the boundary.
	FollowMoving bool

	// LastChatPartner is the most recent player the bot had a conversation
	// with. Used by action status reports to know whom to address when the
	// action handler doesn't have a specific user.
	LastChatPartner string
	LookTargetName  string
	LookTargetUntil time.Time
	IsOnLadder      bool // shared ladder state between movement and network systems
	IsGrounded      bool
	ParkourUntil    time.Time

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

	Mu                  sync.Mutex
	Pos                 mgl32.Vec3
	VelY                float32
	ServerTick          uint64 // monotonic input tick; synced from server packets when rewind
	TickSynced          bool   // true after first server tick reference (UpdateAttributes/MovePlayer/etc.)
	LastSentInputYaw    float32
	LastSentInputPitch  float32
	MovementSyncPending bool // send ClientMovementPredictionSync after next correction
	ScaffoldingActive   bool
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
