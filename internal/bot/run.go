package bot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"bedrock-ai/internal/bot/building/coordinator"
	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/bot/gathering"
	"bedrock-ai/internal/bot/inventory"
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
	b.WorldCache.SetLogger(b.Logger)
	b.WorldModel.SetChunkQuerier(b.WorldCache)

	b.CombatMgr = combat.NewCombatManager(b, b.Logger)
	b.ThreatDet = combat.NewThreatDetector(b, b.CombatMgr, b.Logger)
	b.Gatherer = gathering.NewResourceGatherer(b, b.Logger)
	b.InventoryMgr = inventory.NewInventoryManager(b, b.Logger)
	b.BuilderAgent = coordinator.NewBuilderAgent(b, b.Logger, b.AiClient)

	b.Bus.Publish(event.SpawnEvent{GameData: gd})

	// Tell the server we finished loading
	if SendLoadingScreenDoneFunc != nil {
		SendLoadingScreenDoneFunc(b)
	}

	// Register chat listener via registered function pointer
	if InitChatListenerFunc != nil {
		InitChatListenerFunc(ctx, b)
	}

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
