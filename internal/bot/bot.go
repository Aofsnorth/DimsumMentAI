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
	"bedrock-ai/internal/bot/inventory"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/bot/survival"
	"bedrock-ai/internal/bot/world"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"

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

	// Planner initialization hook. bot/planner imports bot, so we can't
	// import it back here — the concrete planner is constructed via this
	// function pointer and stored as PlannerInterface.
	NewPlannerFunc func(b *Bot, client *ai.NvidiaClient) PlannerInterface
)

// PlannerInterface is implemented by bot/planner.Planner. Defined here to
// break the circular dependency (bot/planner imports bot, bot can't import
// bot/planner). The interface exposes only what the bot and chat handler
// need: running plans, cancelling, and rendering the todo list.
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
	Language          string
	StatePath         string
	Debug             bool
	ProtoSkin         protocol.Skin
	PlayerUUID        uuid.UUID

	// AI and configuration
	AiClient  *ai.NvidiaClient
	Throttler *ai.MessageThrottler
	AiCfg     config.AIConfig
	Planner   PlannerInterface

	// Player Tracking
	PlayerEntityIDs map[string]uint64
	PlayerUsernames map[uint64]string
	PlayerPositions map[uint64]mgl32.Vec3
	PlayerYaws      map[uint64]float32
	PlayerPitches   map[uint64]float32
	PlayerUUIDs     map[uuid.UUID]string

	// Actor Tracking
	Actors              map[uint64]*entity.Info
	UniqueIDToRuntimeID map[int64]uint64

	// Subsystems
	CombatMgr    *combat.CombatManager
	ThreatDet    *combat.ThreatDetector
	Gatherer     *gathering.ResourceGatherer
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

	// LastChatPartner is the most recent player the bot had a conversation
	// with. Used by action status reports to know whom to address when the
	// action handler doesn't have a specific user.
	LastChatPartner string
	LookTargetName  string
	LookTargetUntil time.Time
	IsOnLadder      bool // shared ladder state between movement and network systems
	IsGrounded      bool
	ParkourUntil    time.Time

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
	SubChunkRequestMode bool

	// A* Pathfinding
	WorldModel            *pathfinder.LocalWorldModel
	WorldCache            *world.WorldCache
	CurrentPath           []pathfinder.Node
	PathIndex             int
	LastJumpPathIndex     int
	TicksStuck            int
	LastTickPos           mgl32.Vec3
	LastPathRecalcTime    time.Time
	ConsecutiveStuckCount int

	// Health & Hunger tracking
	Health int
	Hunger int

	// Inventory tracking
	InventoryMap    map[uint32]protocol.ItemStack
	StackNetworkIDs map[uint32]int32
	ItemNames       map[int32]string
	Recipes         map[string]uint32
	RecipesByNetID  map[uint32]RecipeInfo
	HeldSlot        uint32
	StackRequestID  int32

	// Pending craft requests: maps ItemStackRequest.RequestID to a pending
	// craft entry. Used by CraftItem to synchronously wait for the server's
	// ItemStackResponse instead of fire-and-forget. The outputNetworkID is
	// the item type NetworkID of the recipe's output, used to fill in the
	// item type when the server creates a new slot (the response only carries
	// the stack instance ID, not the item type).
	pendingCrafts map[int32]pendingCraft

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

// pendingCraft tracks a single in-flight CraftItem request. The channel
// receives a craftResult when the server's ItemStackResponse arrives.
// outputNetID is the recipe output's item type NetworkID, used by the
// response handler to tag newly-created inventory slots.
type pendingCraft struct {
	ch          chan craftResult
	outputNetID int32
}

// craftResult is sent to a pending craft's channel when the server's
// ItemStackResponse arrives. accepted=false means the server rejected the
// request (non-zero status).
type craftResult struct {
	accepted bool
}

// CraftResult creates a craftResult value. Exported so the network/player
// package can send results to pending craft channels.
func CraftResult(accepted bool) craftResult {
	return craftResult{accepted: accepted}
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
		PlayerEntityIDs:     make(map[string]uint64),
		PlayerUsernames:     make(map[uint64]string),
		PlayerPositions:     make(map[uint64]mgl32.Vec3),
		PlayerYaws:          make(map[uint64]float32),
		PlayerPitches:       make(map[uint64]float32),
		PlayerUUIDs:         make(map[uuid.UUID]string),
		RecentBotMessages:   make(map[string]time.Time),
		MovementState:       "idle",
		Language:            "Indonesian",
		StatePath:           "data/bot_state.json",
		InventoryMap:        make(map[uint32]protocol.ItemStack),
		StackNetworkIDs:     make(map[uint32]int32),
		ItemNames:           make(map[int32]string),
		Recipes:             make(map[string]uint32),
		RecipesByNetID:      make(map[uint32]RecipeInfo),
		pendingCrafts:       make(map[int32]pendingCraft),
		StackRequestID:      -1,
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
