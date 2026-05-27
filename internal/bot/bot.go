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
	"bedrock-ai/internal/bot/gathering"
	"bedrock-ai/internal/bot/inventory"
	"bedrock-ai/internal/bot/pathfinder"
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
)

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
	ProtoSkin         protocol.Skin
	PlayerUUID        uuid.UUID

	// AI and configuration
	AiClient  *ai.NvidiaClient
	Throttler *ai.MessageThrottler
	AiCfg     config.AIConfig

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

	// Movement & Steering
	MovementState    string // "idle", "walk_to", "follow"
	TargetPos        mgl32.Vec3
	TargetPlayerName string
	LookTargetName   string
	LookTargetUntil  time.Time
	IsOnLadder       bool // shared ladder state between movement and network systems
	IsGrounded       bool
	ParkourUntil     time.Time

	// Look angles
	Yaw                 float32
	Pitch               float32
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
	InventoryMap   map[uint32]protocol.ItemStack
	ItemNames      map[int32]string
	Recipes        map[string]uint32
	HeldSlot       uint32
	StackRequestID int32

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
		ItemNames:           make(map[int32]string),
		Recipes:             make(map[string]uint32),
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
