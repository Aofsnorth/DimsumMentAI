// Package bot implements the bot core, including connection lifecycle,
// subsystem initialization, and main run loop.
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
	"github.com/sandertv/gophertunnel/minecraft"
)

func (b *Bot) Run(ctx context.Context) error {
	conn, gd, err := b.connect(ctx)
	if err != nil {
		return err
	}
	b.Conn = conn
	defer b.Conn.Close()

	b.initSpawn(gd)
	defer b.SaveLastStandingPosition()
	b.initSubsystems(gd)
	b.startLoops(ctx, gd)

	if PacketLoopFunc != nil {
		return PacketLoopFunc(ctx, b)
	}
	return nil
}

func (b *Bot) connect(ctx context.Context) (*minecraft.Conn, minecraft.GameData, error) {
	conn, err := b.Dialer()
	if err != nil {
		return nil, minecraft.GameData{}, fmt.Errorf("dial: %w", err)
	}

	go func() {
		<-ctx.Done()
		b.Logger.Info("shutdown requested, closing connection")
		_ = conn.Close()
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

	if err := conn.DoSpawn(); err != nil {
		return nil, minecraft.GameData{}, fmt.Errorf("spawn: %w", err)
	}
	gd := conn.GameData()
	return conn, gd, nil
}

func (b *Bot) initSpawn(gd minecraft.GameData) {
	b.WorldCache.SetUseBlockNetworkIDHashes(gd.UseBlockNetworkIDHashes)
	b.Mu.Lock()
	b.Pos = gd.PlayerPosition.Sub(mgl32.Vec3{0, 1.62, 0})
	b.Yaw = gd.Yaw
	b.HeadYaw = gd.Yaw
	b.Pitch = gd.Pitch
	for _, entry := range gd.Items {
		b.ItemNames[int32(entry.RuntimeID)] = entry.Name
	}
	b.Mu.Unlock()

	b.clampSpawnPosition(gd)

	b.Mu.Lock()
	actualPos := b.Pos
	b.IsGrounded = true
	b.RewindMovement = gd.PlayerMovementSettings.RewindHistorySize > 0
	b.ServerTick = 0
	b.Mu.Unlock()

	b.Logger.Info("spawned in world",
		slog.String("name", b.Name),
		slog.Float64("x", float64(actualPos.X())),
		slog.Float64("y", float64(actualPos.Y())),
		slog.Float64("z", float64(actualPos.Z())),
		slog.Bool("client_cache_enabled", b.Conn.ClientCacheEnabled()),
	)

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

	if lastPos, ok := b.LoadLastStandingPosition(); ok {
		b.Logger.Debug("loaded last standing position",
			slog.Float64("x", float64(lastPos.X())),
			slog.Float64("y", float64(lastPos.Y())),
			slog.Float64("z", float64(lastPos.Z())),
		)
	}
	b.SaveLastStandingPosition()
}

func (b *Bot) clampSpawnPosition(gd minecraft.GameData) {
	spawnY := gd.PlayerPosition.Y() - 1.62
	if spawnY > 320 || spawnY < -64 {
		b.Logger.Warn("Bot spawned in void, setting to safe height",
			slog.Float64("y", float64(spawnY)),
		)
		b.Mu.Lock()
		b.Pos = mgl32.Vec3{gd.PlayerPosition.X(), 100, gd.PlayerPosition.Z()}
		b.Mu.Unlock()
		return
	}
	b.Mu.Lock()
	b.Pos = gd.PlayerPosition.Sub(mgl32.Vec3{0, 1.62, 0})
	b.Mu.Unlock()
}

func (b *Bot) initSubsystems(gd minecraft.GameData) {
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

	if SendLoadingScreenDoneFunc != nil {
		SendLoadingScreenDoneFunc(b)
	}
}

func (b *Bot) startLoops(ctx context.Context, gd minecraft.GameData) {
	if b.VenityCompat && VenityCompatLoopFunc != nil {
		go VenityCompatLoopFunc(ctx, b)
	}
	if InitChatListenerFunc != nil {
		InitChatListenerFunc(ctx, b)
	}
	if StartProactiveLoopFunc != nil {
		go StartProactiveLoopFunc(ctx, b)
	}
	if SendInputLoopFunc != nil {
		go SendInputLoopFunc(ctx, b, gd)
	}
	go b.StartPositionSaver(ctx.Done())

	b.runTickerLoop(ctx, 200*time.Millisecond, func() { b.CombatMgr.Tick(ctx) })
	b.runTickerLoop(ctx, 1200*time.Millisecond, func() { b.ThreatDet.Scan(ctx) })
	b.runTickerLoop(ctx, 500*time.Millisecond, func() { b.SurvivalMgr.Tick() })

	if ChunkRequesterLoopFunc != nil {
		go ChunkRequesterLoopFunc(ctx, b)
	}
}

func (b *Bot) runTickerLoop(ctx context.Context, interval time.Duration, tick func()) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tick()
			}
		}
	}()
}
