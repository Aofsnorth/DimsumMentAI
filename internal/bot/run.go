package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"bedrock-ai/internal/bot/building/coordinator"
	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/exploration"
	"bedrock-ai/internal/bot/farming"
	"bedrock-ai/internal/bot/fishing"
	"bedrock-ai/internal/bot/gathering"
	"bedrock-ai/internal/bot/husbandry"
	"bedrock-ai/internal/bot/inventory"
	"bedrock-ai/internal/bot/survival"
	"bedrock-ai/internal/debuglog"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
)

func (b *Bot) Run(ctx context.Context) error {
	conn, err := b.Dialer()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	b.Conn = conn
	defer b.Conn.Close()

	go func() {
		<-ctx.Done()
		b.Logger.Info("shutdown requested, closing connection")
		_ = b.Conn.Close()
	}()

	if parsedUUID, err := uuid.Parse(conn.IdentityData().Identity); err == nil {
		b.PlayerUUID = parsedUUID
	}

	b.Logger.Info("connected to server",
		slog.String("address", conn.RemoteAddr().String()),
		slog.String("server_host", b.ServerHost),
		slog.Bool("venity_compat", b.VenityCompat),
		slog.Bool("nether_games_compat", b.NetherGamesCompat),
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
	b.HeadYaw = gd.Yaw
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
	b.IsGrounded = true
	b.RewindMovement = gd.PlayerMovementSettings.RewindHistorySize > 0
	// gd.Time is world day-time, not the server tick used by PlayerAuthInput / rewind.
	b.ServerTick = 0
	b.Mu.Unlock()
	b.Logger.Info("spawned in world",
		slog.String("name", b.Name),
		slog.Float64("x", float64(actualPos.X())),
		slog.Float64("y", float64(actualPos.Y())),
		slog.Float64("z", float64(actualPos.Z())),
		slog.Bool("client_cache_enabled", b.Conn.ClientCacheEnabled()),
	)
	// #region agent log
	debuglog.Log("F", "run.go:spawned", "bot spawned", map[string]any{
		"clientCacheEnabled": b.Conn.ClientCacheEnabled(),
		"chunkRadius":        b.Conn.ChunkRadius(),
		"venityCompat":       b.VenityCompat,
		"netherGamesCompat":  b.NetherGamesCompat,
		"rewindHistorySize":  gd.PlayerMovementSettings.RewindHistorySize,
		"rewindMovement":     b.RewindMovement,
		"worldTime":          gd.Time,
		"serverTickInit":     0,
		"runId":              "tick-fix",
	})
	// #endregion
	if lastPos, ok := b.LoadLastStandingPosition(); ok {
		b.Logger.Debug("loaded last standing position",
			slog.Float64("x", float64(lastPos.X())),
			slog.Float64("y", float64(lastPos.Y())),
			slog.Float64("z", float64(lastPos.Z())),
		)
	}
	b.SaveLastStandingPosition()
	defer b.SaveLastStandingPosition()

	// Initialize subsystems
	b.WorldCache.SetLogger(b.Logger)
	b.WorldModel.SetChunkQuerier(b.WorldCache)

	b.CombatMgr = combat.NewCombatManager(b, b.Logger)
	b.ThreatDet = combat.NewThreatDetector(b, b.CombatMgr, b.Logger)
	b.Gatherer = gathering.NewResourceGatherer(b, b.Logger)
	b.InventoryMgr = inventory.NewInventoryManager(b, b.Logger)
	b.BuilderAgent = coordinator.NewBuilderAgent(b, b.Logger, b.AiClient)
	b.SurvivalMgr = survival.NewManager(b, b.Logger)
	b.Farmer = farming.NewFarmer(b, b.Logger)
	b.Fisher = fishing.NewFisher(b, b.Logger)
	b.HusbandryMgr = husbandry.NewManager(b, b.Logger)
	b.Explorer = exploration.NewExplorer(b, b.Logger)

	b.Bus.Publish(event.SpawnEvent{GameData: gd})

	// Tell the server we finished loading
	if SendLoadingScreenDoneFunc != nil {
		SendLoadingScreenDoneFunc(b)
	}
	if b.VenityCompat && VenityCompatLoopFunc != nil {
		go VenityCompatLoopFunc(ctx, b)
	}

	// Register chat listener via registered function pointer
	if InitChatListenerFunc != nil {
		InitChatListenerFunc(ctx, b)
	}

	// Start proactive conversation loop (AGI layer — bot can initiate
	// conversation autonomously without player trigger).
	if StartProactiveLoopFunc != nil {
		go StartProactiveLoopFunc(ctx, b)
	}

	// Start sending PlayerAuthInput so the server registers
	// the bot's physical position and broadcasts it to other players.
	if SendInputLoopFunc != nil {
		go SendInputLoopFunc(ctx, b, gd)
	}
	go b.StartPositionSaver(ctx.Done())

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

	// Survival automation loop: auto-eat, auto-armor, time-based actions
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				b.SurvivalMgr.Tick()
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
