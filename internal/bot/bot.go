package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot/building"
	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/gathering"
	"bedrock-ai/internal/bot/inventory"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Function pointers for dependency injection (resolving circular dependencies)
var (
	SendInputLoopFunc      func(ctx context.Context, b *Bot, gd minecraft.GameData)
	PacketLoopFunc         func(ctx context.Context, b *Bot) error
	ChunkRequesterLoopFunc func(ctx context.Context, b *Bot)
	SendPlayerSkinFunc     func(b *Bot)
	SendLoadingScreenDoneFunc func(b *Bot)
	RecalculatePathFunc    func(b *Bot)
	NavigateToFunc         func(b *Bot, pos mgl32.Vec3)
	StopMovementFunc       func(b *Bot)
	NavigateToBlockFunc    func(b *Bot, x, y, z int32, tolerance float32) bool
	LookAtFunc             func(b *Bot, pos mgl32.Vec3)
)

type Bot struct {
	Logger     *slog.Logger
	Conn       *minecraft.Conn
	Dialer     DialerFunc
	Registry   *handler.Registry
	Bus        *event.Bus
	Name       string
	ProtoSkin  protocol.Skin
	PlayerUUID uuid.UUID

	// AI and configuration
	AiClient  *ai.NvidiaClient
	Throttler *ai.MessageThrottler
	AiCfg     config.AIConfig

	// Player Tracking
	PlayerEntityIDs map[string]uint64
	PlayerUsernames map[uint64]string
	PlayerPositions map[uint64]mgl32.Vec3
	PlayerUUIDs     map[uuid.UUID]string

	// Actor Tracking
	Actors              map[uint64]*entity.Info
	UniqueIDToRuntimeID map[int64]uint64

	// Subsystems
	CombatMgr    *combat.CombatManager
	ThreatDet    *combat.ThreatDetector
	Gatherer     *gathering.ResourceGatherer
	InventoryMgr *inventory.InventoryManager
	BuilderAgent *building.BuilderAgent

	// Movement & Steering
	MovementState    string // "idle", "walk_to", "follow"
	TargetPos        mgl32.Vec3
	TargetPlayerName string
	IsOnLadder       bool // shared ladder state between movement and network systems

	// Look angles
	Yaw   float32
	Pitch float32

	// A* Pathfinding
	WorldModel            *pathfinder.LocalWorldModel
	WorldCache            *WorldCache
	CurrentPath           []pathfinder.Node
	PathIndex             int
	TicksStuck            int
	LastTickPos           mgl32.Vec3
	LastPathRecalcTime    time.Time
	ConsecutiveStuckCount int

	// Health & Hunger tracking
	Health int
	Hunger int

	// Inventory tracking
	InventoryMap map[uint32]protocol.ItemStack
	ItemNames    map[int32]string
	Recipes      map[string]uint32
	HeldSlot     uint32

	// Emotes / Animations state
	EmoteState string
	EmoteTicks int

	// Internal bot messages tracked to prevent loops
	RecentBotMessages map[string]time.Time

	Mu  sync.Mutex
	Pos mgl32.Vec3
	VelY float32
}

type DialerFunc func() (*minecraft.Conn, error)

func newBot(opts ...Option) (*Bot, error) {
	b := &Bot{
		PlayerEntityIDs:     make(map[string]uint64),
		PlayerUsernames:     make(map[uint64]string),
		PlayerPositions:     make(map[uint64]mgl32.Vec3),
		PlayerUUIDs:         make(map[uuid.UUID]string),
		RecentBotMessages:   make(map[string]time.Time),
		MovementState:       "idle",
		InventoryMap:        make(map[uint32]protocol.ItemStack),
		ItemNames:           make(map[int32]string),
		Recipes:             make(map[string]uint32),
		Health:              20,
		Hunger:              20,
		WorldModel:          pathfinder.NewLocalWorldModel(),
		WorldCache:          NewWorldCache(0, cube.Range{-64, 319}, nil), // Air is runtime ID 0 in most cases, but we will fix logger in Run
		Actors:              make(map[uint64]*entity.Info),
		UniqueIDToRuntimeID: make(map[int64]uint64),
		VelY:                0.0,
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

func (b *Bot) Run(ctx context.Context) error {
	conn, err := b.Dialer()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	b.Conn = conn
	defer b.Conn.Close()

	if parsedUUID, err := uuid.Parse(conn.IdentityData().Identity); err == nil {
		b.PlayerUUID = parsedUUID
	}

	b.Logger.Info("connected to server",
		slog.String("address", conn.RemoteAddr().String()),
		slog.Bool("UseBlockNetworkIDHashes", conn.GameData().UseBlockNetworkIDHashes),
	)

	if err := b.Conn.DoSpawn(); err != nil {
		return fmt.Errorf("spawn: %w", err)
	}

	gd := b.Conn.GameData()
	b.WorldCache.SetUseBlockNetworkIDHashes(gd.UseBlockNetworkIDHashes)
	b.Mu.Lock()
	b.Pos = gd.PlayerPosition.Sub(mgl32.Vec3{0, 1.62, 0})
	b.Yaw = gd.Yaw
	b.Pitch = gd.Pitch
	// Initialize item names map from StartGame packet
	for _, entry := range gd.Items {
		b.ItemNames[int32(entry.RuntimeID)] = entry.Name
	}
	b.Mu.Unlock()

	// Validate spawn position - if in void (y > 320 or y < -64), set to safe height
	spawnY := gd.PlayerPosition.Y() - 1.62
	if spawnY > 320 || spawnY < -64 {
		b.Logger.Warn("Bot spawned in void, setting to safe height",
			slog.Float64("y", float64(spawnY)),
		)
		b.Mu.Lock()
		b.Pos = mgl32.Vec3{gd.PlayerPosition.X(), 100, gd.PlayerPosition.Z()}
		b.Mu.Unlock()
	} else {
		b.Mu.Lock()
		b.Pos = gd.PlayerPosition.Sub(mgl32.Vec3{0, 1.62, 0})
		b.Mu.Unlock()
	}

	b.Mu.Lock()
	actualPos := b.Pos
	b.Mu.Unlock()
	b.Logger.Info("spawned in world",
		slog.String("name", b.Name),
		slog.Float64("x", float64(actualPos.X())),
		slog.Float64("y", float64(actualPos.Y())),
		slog.Float64("z", float64(actualPos.Z())),
	)

	// Initialize subsystems
	b.WorldCache.logger = b.Logger // Set logger since it was nil in newBot
	b.WorldModel.SetChunkQuerier(b.WorldCache)

	b.CombatMgr = combat.NewCombatManager(b, b.Logger)
	b.ThreatDet = combat.NewThreatDetector(b, b.CombatMgr, b.Logger)
	b.Gatherer = gathering.NewResourceGatherer(b, b.Logger)
	b.InventoryMgr = inventory.NewInventoryManager(b, b.Logger)
	b.BuilderAgent = building.NewBuilderAgent(b, b.Logger, b.AiClient)

	b.Bus.Publish(event.SpawnEvent{GameData: gd})

	// Tell the server we finished loading
	if SendLoadingScreenDoneFunc != nil {
		SendLoadingScreenDoneFunc(b)
	}

	// Register chat listener
	b.InitChatListener(ctx)

	// Start sending PlayerAuthInput so the server registers
	// the bot's physical position and broadcasts it to other players.
	if SendInputLoopFunc != nil {
		go SendInputLoopFunc(ctx, b, gd)
	}

	// Start combat and threat detector loops
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.CombatMgr.Tick(ctx)
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(1200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.ThreatDet.Scan(ctx)
			}
		}
	}()

	if ChunkRequesterLoopFunc != nil {
		go ChunkRequesterLoopFunc(ctx, b)
	}

	if PacketLoopFunc != nil {
		return PacketLoopFunc(ctx, b)
	}
	return nil
}

func (b *Bot) sendLoadingScreenDone() {
	if SendLoadingScreenDoneFunc != nil {
		SendLoadingScreenDoneFunc(b)
	}
}

func (b *Bot) sendPlayerSkin() {
	if SendPlayerSkinFunc != nil {
		SendPlayerSkinFunc(b)
	}
}

// FindPlayer returns the runtime ID and current position of a player by username (case-insensitive)
func (b *Bot) FindPlayer(username string) (uint64, mgl32.Vec3, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	for name, id := range b.PlayerEntityIDs {
		if strings.EqualFold(name, username) {
			if pos, ok := b.PlayerPositions[id]; ok {
				return id, pos, true
			}
		}
	}
	return 0, mgl32.Vec3{}, false
}

// RecalculatePath computes the shortest path to targetPos using A* search.
func (b *Bot) RecalculatePath() {
	if RecalculatePathFunc != nil {
		RecalculatePathFunc(b)
	}
}

func (b *Bot) Close() error {
	if b.Conn != nil {
		return b.Conn.Close()
	}
	return nil
}

func (b *Bot) GetEntities() map[uint64]*entity.Info {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.Actors
}

func (b *Bot) GetHeldItemSlot() uint32 {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.HeldSlot
}

func (b *Bot) GetInventorySlots() map[uint32]protocol.ItemStack {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.InventoryMap
}

func (b *Bot) GetItemNames() map[int32]string {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.ItemNames
}

func (b *Bot) SendChat(msg string) {
	b.SendSafeChat(msg)
}

func (b *Bot) GetEntityRuntimeID() uint64 {
	return b.Conn.GameData().EntityRuntimeID
}

func (b *Bot) GetLocalWorldModel() entity.WorldModel {
	return b.WorldModel
}

func (b *Bot) NavigateTo(pos mgl32.Vec3) {
	if NavigateToFunc != nil {
		NavigateToFunc(b, pos)
	}
}

func (b *Bot) StopMovement() {
	if StopMovementFunc != nil {
		StopMovementFunc(b)
	}
}

func (b *Bot) NavigateToBlock(x, y, z int32, tolerance float32) bool {
	if NavigateToBlockFunc != nil {
		return NavigateToBlockFunc(b, x, y, z, tolerance)
	}
	return false
}

func (b *Bot) WritePacket(pk packet.Packet) error {
	return b.Conn.WritePacket(pk)
}

func (b *Bot) EquipItem(slot uint32) error {
	b.Mu.Lock()
	b.HeldSlot = slot
	item := b.InventoryMap[slot]
	b.Mu.Unlock()

	pk := &packet.MobEquipment{
		EntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
		NewItem:         protocol.ItemInstance{Stack: item},
		InventorySlot:   byte(slot),
		HotBarSlot:      byte(slot),
		WindowID:        0,
	}
	return b.Conn.WritePacket(pk)
}

func (b *Bot) UnequipItem() error {
	pk := &packet.MobEquipment{
		EntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
		NewItem:         protocol.ItemInstance{},
		InventorySlot:   0,
		HotBarSlot:      0,
		WindowID:        0,
	}
	return b.Conn.WritePacket(pk)
}

func (b *Bot) LookAt(pos mgl32.Vec3) {
	if LookAtFunc != nil {
		LookAtFunc(b, pos)
	}
}

func (b *Bot) DropItem(name string, count int) error {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	var targetSlot uint32
	var foundItem protocol.ItemStack
	found := false

	// Find the item by name
	for slot, item := range b.InventoryMap {
		if item.Count <= 0 {
			continue
		}
		itemName := b.ItemNames[item.NetworkID]
		if strings.Contains(strings.ToLower(itemName), strings.ToLower(name)) {
			targetSlot = slot
			foundItem = item
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("item %s not found in inventory", name)
	}

	if count <= 0 || count > int(foundItem.Count) {
		count = int(foundItem.Count)
	}

	// Create dropped item transaction
	dropItem := foundItem
	dropItem.Count = uint16(count)

	tx := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				WindowID:      0,
				InventorySlot: targetSlot,
				OldItem:       protocol.ItemInstance{Stack: foundItem},
				NewItem: protocol.ItemInstance{
					Stack: protocol.ItemStack{
						ItemType:       foundItem.ItemType,
						BlockRuntimeID: foundItem.BlockRuntimeID,
						Count:          foundItem.Count - uint16(count),
						NBTData:        foundItem.NBTData,
						CanBePlacedOn:  foundItem.CanBePlacedOn,
						CanBreak:       foundItem.CanBreak,
						HasNetworkID:   foundItem.HasNetworkID,
					},
				},
			},
			{
				SourceType:    protocol.InventoryActionSourceWorld,
				SourceFlags:   1, // Drop item flag
				InventorySlot: 0,
				OldItem:       protocol.ItemInstance{},
				NewItem:       protocol.ItemInstance{Stack: dropItem},
			},
		},
		TransactionData: &protocol.NormalTransactionData{},
	}

	return b.Conn.WritePacket(tx)
}

func (b *Bot) InjectAIEvent(msg string) {
	b.Logger.Info("AI Event injected", "msg", msg)
	if b.AiClient == nil {
		return
	}

	// Query Nvidia Client asynchronously with the system message
	go func() {
		hp, hunger, botCoords := b.GetStatusDetails()
		heldItem := b.GetHeldItem()
		invSummary := b.GetInventorySummary()

		b.Mu.Lock()
		mainPlayer := b.AiCfg.MainPlayer
		botName := b.Name
		b.Mu.Unlock()

		if mainPlayer == "" {
			return
		}

		playerCoordsStr := ""
		if pCoords, ok := b.GetPlayerCoords(mainPlayer); ok {
			playerCoordsStr = fmt.Sprintf("X:%.0f Y:%.0f Z:%.0f", pCoords.X(), pCoords.Y(), pCoords.Z())
		}

		botStatusText := fmt.Sprintf("HP: %d/20, Hunger: %d/20", hp, hunger)
		systemPrompt := b.AiClient.BuildSystemPrompt(
			botName,
			botCoords+" ("+botStatusText+")",
			playerCoordsStr,
			heldItem,
			invSummary,
		)

		reply, err := b.AiClient.Ask(mainPlayer, systemPrompt, msg)
		if err != nil {
			b.Logger.Error("Failed to ask Nvidia LLM for injected event", "error", err.Error())
			return
		}

		parsed := ai.Parse(reply)
		if parsed.CleanReply != "" {
			b.SendSafeChat(parsed.CleanReply)
		}

		for _, act := range parsed.Actions {
			b.ExecuteAction(act.Label, act.Param, mainPlayer)
		}
	}()
}

func (b *Bot) CraftItem(recipeNetID uint32, count int) error {
	req := protocol.ItemStackRequest{
		RequestID: 1, // standard ID
		Actions: []protocol.StackRequestAction{
			&protocol.AutoCraftRecipeStackRequestAction{
				RecipeNetworkID: recipeNetID,
				TimesCrafted:    byte(count),
				NumberOfCrafts:  byte(count),
			},
		},
	}
	pk := &packet.ItemStackRequest{
		Requests: []protocol.ItemStackRequest{req},
	}
	return b.Conn.WritePacket(pk)
}

func (b *Bot) GetRecipes() map[string]uint32 {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	// Create a shallow copy to be thread-safe
	copyMap := make(map[string]uint32, len(b.Recipes))
	for k, v := range b.Recipes {
		copyMap[k] = v
	}
	return copyMap
}
