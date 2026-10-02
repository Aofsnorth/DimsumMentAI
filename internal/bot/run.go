// Package bot implements the bot core, including connection lifecycle,
// subsystem initialization, and main run loop.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

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
	"bedrock-ai/internal/bot/inventory/trading"
	"bedrock-ai/internal/bot/survival"
	"bedrock-ai/internal/debuglog"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft"
)

// ServerDisconnect reports that the connection ended from the far side rather
// than from our own shutdown. PacketLoop returns it so the run loop can tell
// "the world went away" apart from "the user pressed Ctrl+C" and decide whether
// reconnecting is worth trying.
//
// Reason is empty when the peer vanished without a goodbye, which is exactly
// what a single-player host does when it closes the world or leaves.
type ServerDisconnect struct {
	Reason string
}

func (e *ServerDisconnect) Error() string {
	if e.Reason == "" {
		return "server closed the connection"
	}
	return e.Reason
}

// HostClosedWorld reports whether the disconnect carries no reason at all, the
// signature of a host that stopped without a Disconnect packet.
func (e *ServerDisconnect) HostClosedWorld() bool {
	return strings.TrimSpace(e.Reason) == ""
}

const (
	// MaxReconnectAttempts is how many times the bot tries to rejoin a world
	// that disappeared under it. Enough to ride out a host restart, small enough
	// that a genuinely gone server does not leave the process hanging.
	MaxReconnectAttempts = 5

	// ReconnectBaseDelay is the first backoff step; it doubles per attempt up to
	// ReconnectMaxDelay.
	ReconnectBaseDelay = 2 * time.Second
	ReconnectMaxDelay  = 20 * time.Second
)

// ReconnectDelay returns the wait before rejoin attempt n (1-based).
func ReconnectDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := ReconnectBaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= ReconnectMaxDelay {
			return ReconnectMaxDelay
		}
	}
	if delay > ReconnectMaxDelay {
		return ReconnectMaxDelay
	}
	return delay
}

func (b *Bot) Run(ctx context.Context) error {
	attempts := 0
	switches := 0
	connectedOnce := false

	for {
		// A pending switch (from the join action) is applied before the session
		// starts, and gets a fresh retry budget: deliberately leaving a server is
		// not a failure to recover from.
		if target, ok := b.TakeJoinRequest(); ok {
			switches++
			if switches > MaxServerSwitches {
				b.Logger.Error("too many server switches, giving up",
					slog.Int("switches", switches),
					slog.String("address", target),
				)
				return errors.New("terlalu banyak perpindahan server")
			}
			b.logSwitchResult(target, b.applyJoin(target))
			attempts = 0
		}

		result := b.runSession(ctx)

		// Our own shutdown: the user asked to stop, so never fight them.
		if ctx.Err() != nil {
			return nil
		}
		if result.Connected {
			// A session that got in resets the ladder: the next drop starts fresh.
			connectedOnce = true
			attempts = 0
		}

		var dropped *ServerDisconnect
		switch {
		case errors.As(result.Err, &dropped):
			// The server said why. A kick or a ban is not something to retry.
			if !dropped.HostClosedWorld() {
				b.Logger.Error("disconnected by server, not retrying",
					slog.String("reason", dropped.Reason))
				return result.Err
			}
		case !connectedOnce:
			// Never got in at all: a bad address, a refused connection, a spawn
			// failure. Retrying with the same settings cannot fix any of those, so
			// report it instead of looping.
			return result.Err
		}
		// Anything else past the first session means the world was lost and the
		// host is not answering yet. That is the case worth waiting out.

		if attempts >= MaxReconnectAttempts {
			b.Logger.Error("world closed and the bot could not get back in",
				slog.Int("attempts", attempts),
				slog.String("host", b.ServerHost),
				slog.String("last_error", ErrText(result.Err)),
				slog.String("hint", "start the world again on the host, or check that it is still open to LAN"),
			)
			return result.Err
		}
		attempts++

		delay := ReconnectDelay(attempts)
		b.Logger.Warn("world closed by the host, waiting to rejoin",
			slog.Int("attempt", attempts),
			slog.Int("max_attempts", MaxReconnectAttempts),
			slog.Duration("retry_in", delay),
			slog.String("last_error", ErrText(result.Err)),
		)

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

// applyJoin redirects the dialer at a new address.
func (b *Bot) applyJoin(address string) error {
	b.join.mu.Lock()
	hook := b.join.hook
	b.join.mu.Unlock()

	if hook == nil {
		return errors.New("bot ini tidak mendukung pindah server")
	}
	return hook(address)
}

// runSession is one connection lifetime: connect, spawn, run the loops, and
// block until the connection ends.
//
// Each session gets its own context so its goroutines (input loop, chunk
// requests, chat listener, tickers) die with the session. Without that, a
// rejoin would leave the previous session's loops running alongside the new
// ones, each writing movement packets for a connection that no longer exists.
func (b *Bot) runSession(ctx context.Context) sessionResult {
	sessionCtx, endSession := context.WithCancel(ctx)
	defer endSession()
	// Registered so a join request can end this session; cleared on the way out
	// so a request that arrives late cannot cancel a session already gone.
	b.SetSessionCancel(endSession)
	defer b.SetSessionCancel(nil)

	conn, gd, err := b.connect(sessionCtx)
	if err != nil {
		return sessionResult{Err: err}
	}
	b.Conn = conn
	defer func() {
		// Mirror the connect-goroutine's stderr print so any exit path
		// (panic, error return, normal shutdown) leaves a clear breadcrumb.
		fmt.Fprintln(os.Stderr, "DEBUG: runSession() returning, defer closing conn")
		_ = b.Conn.Close()
	}()

	// A rejoin is a different world: the host may have closed and reopened it,
	// with a new seed. Keeping the old chunks would let pathfinding walk through
	// terrain that does not exist any more.
	b.ResetSessionState()
	b.noteServer(b.ServerHost)

	b.initSpawn(gd)
	defer b.SaveLastStandingPosition()
	b.initSubsystems(gd)
	b.startLoops(sessionCtx, gd)

	// Runs after the loops start on purpose: the first line waits out a settle
	// delay, and the chat path borrows the tick loop, so both have to be alive.
	// Goroutine, not a direct call — this would otherwise sit in front of the
	// packet loop and stall every incoming packet for the whole sequence.
	go b.RunJoinMessages(sessionCtx)

	if PacketLoopFunc == nil {
		return sessionResult{Connected: true}
	}
	err = PacketLoopFunc(sessionCtx, b)
	fmt.Fprintln(os.Stderr, "DEBUG: PacketLoopFunc returned err:", err)
	return sessionResult{Connected: true, Err: err}
}

// ErrText renders an error for a log field without panicking on a nil.
func ErrText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ResetSessionState drops the per-world state a new session must not inherit.
func (b *Bot) ResetSessionState() {
	if b.WorldCache != nil {
		b.WorldCache.Reset()
	}

	b.Mu.Lock()
	b.CurrentPath = nil
	b.PathIndex = 0
	b.LastJumpPathIndex = -1
	b.TicksStuck = 0
	// The idle-nudge schedule is per-session. Carrying it across a reconnect
	// would delay the first step of the new session by whatever remained of the
	// old window, which on a server that drops silent clients is long enough to
	// get kicked before the bot has moved at all.
	b.LastIdleNudgeAt = time.Time{}
	b.ConsecutiveStuckCount = 0
	b.StuckWindowStart = time.Time{}
	b.MovementState = "idle"

	// Entity and player tracking is per-world. When a host closes the world the
	// entities do not send RemoveActor first — they just stop existing. Keeping
	// them means the next session sees ghosts: GetEntities reports mobs that are
	// no longer there, FindPlayer returns stale positions of players who left,
	// and "interact with what is in front" can pick a phantom target. A new
	// session re-populates all of these from AddActor/AddPlayer/PlayerList.
	b.Actors = make(map[uint64]*entity.Info)
	b.UniqueIDToRuntimeID = make(map[int64]uint64)
	b.PlayerTracker = NewPlayerTracker()

	// ItemNames is re-seeded from the new session's game data. Cross-server
	// joins (the join action) get a different item palette, and an un-overwritten
	// runtime ID would keep naming the previous server's item.
	b.ItemNames = make(map[int32]string)
	b.Mu.Unlock()

	// Enchantment options are a recipe network ID from the world that issued
	// them, so they cannot outlive it. A rejoin that kept them would let the
	// manager ask a server it has just joined to craft recipe numbers it never
	// sent. This is deliberately outside the b.Mu section: the enchanting state
	// has its own lock, and the order between the two is not something to depend on.
	ResetEnchantState(b)

	if b.WorldModel != nil {
		b.WorldModel.PurgeFalseSolidOverrides()
	}
}

func (b *Bot) connect(ctx context.Context) (*minecraft.Conn, minecraft.GameData, error) {
	conn, err := b.Dialer()
	if err != nil {
		return nil, minecraft.GameData{}, fmt.Errorf("dial: %w", err)
	}

	go func() {
		<-ctx.Done()
		// Use fmt.Fprintln so the message reaches stderr immediately even
		// if the slog handler is buffered or the process is about to exit.
		// Without this, intermittent shutdowns (e.g. context canceled mid-craft)
		// looked like crashed bots because the slog "shutdown requested" line
		// had no time to flush before os.Exit(1).
		fmt.Fprintln(os.Stderr, "DEBUG: ctx.Done() fired, closing connection")
		b.Logger.Info("shutdown requested, closing connection")
		_ = conn.Close()
		fmt.Fprintln(os.Stderr, "DEBUG: conn.Close() returned")
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
	b.serverAuthBlockBreaking = gd.PlayerMovementSettings.ServerAuthoritativeBlockBreaking
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
		"clientCacheEnabled":      b.Conn.ClientCacheEnabled(),
		"chunkRadius":             b.Conn.ChunkRadius(),
		"venityCompat":            b.VenityCompat,
		"netherGamesCompat":       b.NetherGamesCompat,
		"rewindHistorySize":       gd.PlayerMovementSettings.RewindHistorySize,
		"rewindMovement":          b.RewindMovement,
		"serverAuthBlockBreaking": b.serverAuthBlockBreaking,
		"worldTime":               gd.Time,
		"serverTickInit":          0,
		"runId":                   "tick-fix",
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
	b.Interactor = interact.New(b, b.Logger)
	b.InventoryMgr = inventory.NewInventoryManager(b, b.Logger)
	b.BuilderAgent = coordinator.NewBuilderAgent(b, b.Logger, b.AiClient)
	b.SurvivalMgr = survival.NewManager(b, b.Logger)
	b.Farmer = farming.NewFarmer(b, b.Logger)
	b.Fisher = fishing.NewFisher(b, b.Logger)
	b.HusbandryMgr = husbandry.NewManager(b, b.Logger)
	b.Explorer = exploration.NewExplorer(b, b.Logger)

	// Wire the observation seam. Each of these managers refuses to report
	// success without a server signal, and with nothing wired they refuse
	// always: a fishing rod that never sees a bite, a wolf that can never be
	// confirmed collared, a crop whose age cannot be read. The adapters exist
	// because the three packages each declare their own event type, so one
	// recorded ring is presented three ways.
	b.Fisher.SetActorEventSource(FisherEvents{B: b})
	b.HusbandryMgr.SetActorEventSource(HusbandryEvents{B: b})
	b.HusbandryMgr.SetEntityMetaSource(HusbandryEntityMeta{B: b})
	b.Farmer.SetBlockStateSource(BlockStates{B: b})

	// Trading needs two observation seams, both of which only exist because the
	// bot now records them: the server's per-villager offer list, and the XP
	// level that pays for the offers that cost XP. Without the first the manager
	// has no prices; without the second it cannot know whether an XP-costing
	// offer is affordable, and it refuses rather than guessing.
	b.Trading = trading.NewManager(b, b.Logger)
	b.Trading.SetTradeObserver(TradeWindows{B: b})
	b.Trading.SetXPObserver(Levels{})

	// The enchanting table needs the same two things trading does, from the same
	// place. The server's offers arrive on packet.PlayerEnchantOptions and are
	// recorded by the handler in network/player; the XP level that pays for them
	// arrives on UpdateAttributes and is folded in by the same path trading uses.
	// Without this the manager refuses every enchant with ErrNoEnchanter, which
	// is the intended behaviour for a missing seam and the wrong behaviour for a
	// wired one — the table is useless, and silently so, which is worse.
	b.InventoryMgr.Station().SetEnchanter(EnchantTable{B: b})

	b.Bus.Publish(event.SpawnEvent{GameData: gd})

	if SendLoadingScreenDoneFunc != nil {
		SendLoadingScreenDoneFunc(b)
	}

	// Ask for chunks explicitly. Without this the bot can spawn into a world the
	// server never streams to it, and proxies like Geyser have been observed to
	// drop a client that never requests a radius.
	if RequestChunkRadiusFunc != nil {
		RequestChunkRadiusFunc(b)
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
	if StartAGILoopFunc != nil {
		go StartAGILoopFunc(ctx, b)
	}
	if SendInputLoopFunc != nil {
		go SendInputLoopFunc(ctx, b, gd)
	}
	go b.StartPositionSaver(ctx.Done())

	b.runTickerLoop(ctx, 200*time.Millisecond, func() { b.CombatMgr.Tick(ctx) })
	b.runTickerLoop(ctx, 1200*time.Millisecond, func() { b.ThreatDet.Scan(ctx) })
	b.runTickerLoop(ctx, 500*time.Millisecond, func() { b.SurvivalMgr.Tick() })

	// Sub-chunk requesting is skipped on Geyser-fronted servers.
	//
	// Measured against play.hansprojects.my.id (Geyser + Floodgate): a client
	// that sends SubChunkRequest goes silent for ~25 seconds and is dropped,
	// while the same client without it stays connected and runs commands fine.
	// The server pushes chunks anyway once a chunk radius has been requested,
	// so nothing is lost by not asking for them.
	if ChunkRequesterLoopFunc != nil && !b.GeyserNoSubChunks {
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
