// Package player handles player, entity, and inventory-related packets.
package player

import (
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/debuglog"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// teleportAckDistance is how far the server's reported feet position may sit
// from the bot's own before an unflagged MovePlayer is still treated as a
// teleport. A Geyser front-end relays ordinary movement as a stream of
// MoveModeNormal echoes, so distance is the only signal left for the host that
// moves the player without saying so.
const teleportAckDistance = 2.2

// ShouldAckTeleport reports whether a server-sent MovePlayer for the local
// player must be acknowledged with InputFlagHandledTeleport on the next
// PlayerAuthInput tick.
//
// A real client acknowledges teleports, not every position echo. Geyser
// forwards each ordinary client position update as an unflagged MovePlayer, and
// calling all of them teleports froze the body: the next tick skips steering and
// physics to hold the acked position, so as soon as the server echoed at 20Hz
// there was never a tick that steered, and the bot stood still with a full set
// of walk inputs on the wire.
func ShouldAckTeleport(mode byte, serverPos, localPos mgl32.Vec3) bool {
	if mode == packet.MoveModeTeleport || mode == packet.MoveModeReset {
		return true
	}
	dx := serverPos.X() - localPos.X()
	dy := serverPos.Y() - localPos.Y()
	dz := serverPos.Z() - localPos.Z()
	return dx*dx+dy*dy+dz*dz > teleportAckDistance*teleportAckDistance
}

// serverSupportMaxLift bounds how far up a correction may push the body and
// still read as "the server is holding me on a surface" (a stair step or slab
// edge) rather than a teleport.
const serverSupportMaxLift = 0.75

// NoteServerSupport records a small upward server correction as proof of a
// surface under the feet. Caller MUST hold b.Mu.
func NoteServerSupport(b *bot.Bot, localY, serverY float32) {
	lift := serverY - localY
	if lift > 0.02 && lift <= serverSupportMaxLift {
		b.ServerSupportY = serverY
		b.ServerSupportAt = time.Now()
	}
}

// probeResetColumn describes the blocks around a position so a server
// teleport can be explained: the cell the host refused and the cell it put the
// bot back in. Info-level on purpose, like the other movement probes.
func probeResetColumn(b *bot.Bot, p mgl32.Vec3) []string {
	if b.WorldCache == nil {
		return nil
	}
	x, y, z := int32(math.Floor(float64(p.X()))), int32(math.Floor(float64(p.Y()))), int32(math.Floor(float64(p.Z())))
	out := make([]string, 0, 5)
	for cy := y - 2; cy <= y+2; cy++ {
		rid, loaded := b.WorldCache.GetBlockRID(x, cy, z)
		if !loaded {
			out = append(out, fmt.Sprintf("%d:unloaded", cy))
			continue
		}
		name, _ := b.WorldCache.BlockName(rid)
		out = append(out, fmt.Sprintf("%d,%d,%d:%s:solid=%v:top=%.2f", x, cy, z, name, b.WorldCache.IsRIDSolid(rid), b.WorldCache.TopOffsetAt(x, cy, z)))
	}
	return out
}

var (
	resetLogMu   sync.Mutex
	resetLogLast time.Time
)

// resetLogDue rate-limits the reset log: a pinned body is reset every ~150ms
// and the same line twenty times a second buries everything else.
func resetLogDue() bool {
	resetLogMu.Lock()
	defer resetLogMu.Unlock()
	if time.Since(resetLogLast) < 2*time.Second {
		return false
	}
	resetLogLast = time.Now()
	return true
}

// missing-support reconciliation constants. The signature is narrow on
// purpose: a body the server repeatedly stands a full block above the decoded
// ground, while it is quiescent, means the model is missing a block. Anything
// slabbier (feet mid-cell) stays with the support-hold machinery.
// maxLift 2.2 covers the Y-fight's second resting level — the body standing on
// the model's own ground one-two blocks under the pin (live run: rest at
// 121.0, pin at 123.06, lift 2.06 — discarded by the old 1.6 cap, so the
// strongest evidence never counted).
const (
	missingSupportMinLift     = 0.5
	missingSupportMaxLift     = 2.2
	missingSupportMaxFrac     = 0.2
	missingSupportJitter      = 0.3
	missingSupportHitsNeeded  = 3
	missingSupportHold        = 30 * time.Second
	missingSupportLogInterval = 5 * time.Second
)

// reconcilePinnedSupportCell writes the server's pinned support cell into the
// world model when the local model demonstrably lacks it: the cell under the
// pinned feet is loaded and reads non-solid. Returns whether a cell was
// marked. Caller MUST hold b.Mu.
func reconcilePinnedSupportCell(b *bot.Bot, localPos, serverPos mgl32.Vec3) bool {
	if b.WorldCache == nil || b.WorldModel == nil {
		return false
	}
	cellX := int32(math.Floor(float64(serverPos.X())))
	cellY := int32(math.Floor(float64(serverPos.Y()))) - 1
	cellZ := int32(math.Floor(float64(serverPos.Z())))
	rid, loaded := b.WorldCache.GetBlockRID(cellX, cellY, cellZ)
	if !loaded {
		// Undecoded cells abstain: the model cannot outvote the host about
		// ground it has not seen, but there is also nothing to reconcile.
		return false
	}
	if b.WorldCache.IsRIDSolid(rid) {
		return false // the model already agrees — nothing is missing
	}
	// No floor-depth check here on purpose: the live cases had the server's
	// block floating over model-air too (a tree trunk the model never decoded,
	// with air beneath it in the model's view). A decoded-air cell the server
	// demonstrably stands the body on IS the decode gap, however deep the
	// model's next floor is. The freshness, displacement and fraction gates on
	// the callers are what keep a transient echo from conjuring ground.
	b.WorldModel.SetTempSolid(cellX, cellY, cellZ, missingSupportHold)
	if time.Since(b.ServerSupportReconciledAt) > missingSupportLogInterval {
		b.ServerSupportReconciledAt = time.Now()
		if b.Logger != nil {
			b.Logger.Info("server-pinned support reconciled into the world model",
				"cell", fmt.Sprintf("%d,%d,%d", cellX, cellY, cellZ),
				"server_y", serverPos.Y(),
				"local_y", localPos.Y(),
				"lift", serverPos.Y()-localPos.Y())
		}
	}
	return true
}

// NoteMissingSupport watches persistent server corrections that stand the body
// a full block above the decoded ground, and after enough of them agree,
// writes the missing support cell into the world model as a temporary solid.
//
// The live run that shaped this: a tree two blocks up a slope. The host held
// the feet at ~101.1 while the model had air from 100 up; every physics tick
// sank the body, every server echo pushed it back, the step-jump never landed,
// and the scaffolder burned twenty placements on a cell the server already
// considered occupied. Trusting the server's world here is also the fair-play
// answer: the bot stops clipping into a block that is really there.
// Caller MUST hold b.Mu.
func NoteMissingSupport(b *bot.Bot, localPos, serverPos mgl32.Vec3) {
	lift := serverPos.Y() - localPos.Y()
	if lift < -0.05 {
		// The server put the body BELOW the local view — the opposite of a
		// missing floor (a pit, a real fall). Contradicts the hypothesis.
		b.ServerSupportHits = 0
		return
	}
	if lift < missingSupportMinLift || lift > missingSupportMaxLift {
		// Ordinary echo (the ground agrees), or the small-lift stair/slab case
		// the support hold already covers. Neither confirms nor contradicts.
		return
	}
	frac := serverPos.Y() - float32(math.Floor(float64(serverPos.Y())))
	if frac > missingSupportMaxFrac {
		// Feet land mid-cell: a partial block top, not a missing full cube.
		return
	}

	if math.Abs(float64(serverPos.Y()-b.ServerSupportHitsY)) > missingSupportJitter {
		b.ServerSupportHitsY = serverPos.Y()
		b.ServerSupportHits = 1
		return
	}
	b.ServerSupportHits++
	if b.ServerSupportHits < missingSupportHitsNeeded {
		return
	}
	b.ServerSupportHits = 0
	reconcilePinnedSupportCell(b, localPos, serverPos)
}

// ReconcileServerPinnedSupportNow immediately writes the server's pinned
// support cell into the world model when the host is standing the body a full
// block above what the model decoded. The echo path (NoteMissingSupport) needs
// three agreeing corrections to fire, and a scaffolder whose placements keep
// being refused gets exactly one attempt per cell — the live tower burned all
// of them on a cell the server already had a tree trunk in. Returns whether a
// cell was reconciled and the caller may retry.
//
// The lift is measured against the ground the MODEL can stand the body on
// under the pin column, not against the falling body: during a Y-fight the
// body's two quiescent levels sit exactly at a body-lift window's edges (at
// the pin, lift ~0; on the model ground, lift ~2) and the window interior is
// only ever traversed mid-fall, so a body-lift gate misses deterministically.
func ReconcileServerPinnedSupportNow(b *bot.Bot) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if time.Since(b.ServerSupportReconciledAt) < 2*time.Second {
		return false // just reconciled; give the placement a plain retry first
	}
	// The pin must be fresh: a stale ServerGroundY says nothing about the
	// column the tower is fighting right now.
	if b.ServerGroundAt.IsZero() || time.Since(b.ServerGroundAt) > 2*time.Second {
		return false
	}
	frac := b.ServerGroundY - float32(math.Floor(float64(b.ServerGroundY)))
	if frac > missingSupportMaxFrac {
		return false
	}
	// Only reject gross displacement (a real teleport far from the pin); the
	// Y-fight's two resting levels — held at the pin (lift ~0) and standing on
	// the model ground one-two blocks down (lift ~2) — must both pass.
	if d := b.ServerGroundY - b.Pos.Y(); d < -0.5 || d > 2.3 {
		return false
	}
	return reconcilePinnedSupportCell(b, b.Pos, mgl32.Vec3{b.Pos.X(), b.ServerGroundY, b.Pos.Z()})
}

// ApplySelfMovePlayer updates bot position and teleport acknowledgement state
// for a confirmed local player movement packet. Caller MUST hold b.Mu.
func ApplySelfMovePlayer(b *bot.Bot, mode byte, newPos mgl32.Vec3) bool {
	ack := ShouldAckTeleport(mode, newPos, b.Pos)
	if ack && b.MovementState == "idle" && b.Logger != nil {
		b.Logger.Info("server teleported idle bot",
			slog.Any("local", b.Pos),
			slog.Any("server_feet", newPos),
			slog.Int("mode", int(mode)),
			slog.Any("server_column", probeResetColumn(b, newPos)),
		)
	}
	if ack && b.MovementState != "idle" && b.Logger != nil && resetLogDue() {
		b.Logger.Info("server moved bot (teleport/reset)",
			slog.Any("local", b.Pos),
			slog.Any("server", newPos),
			slog.Int("mode", int(mode)),
			slog.String("movement_state", b.MovementState),
			slog.Any("local_column", probeResetColumn(b, b.Pos)),
			slog.Any("server_column", probeResetColumn(b, newPos)),
		)
	}
	// Flight state decides both the adoption below and which of the two
	// support notes may record anything: a body mid-jump or mid-knockback is
	// integrating assigned motion, and its echoes are stale arc floors, not
	// ground truth.
	dxD, dyD, dzD := newPos.X()-b.Pos.X(), newPos.Y()-b.Pos.Y(), newPos.Z()-b.Pos.Z()
	realTeleport := dxD*dxD+dyD*dyD+dzD*dzD > teleportAckDistance*teleportAckDistance
	inFlight := b.VelY > 0.05 || b.VelY < -0.05 || b.KnockbackX != 0 || b.KnockbackZ != 0 || time.Now().Before(b.KnockbackUntil)
	if ack {
		b.ServerResets++
		// A host that resets the body a little ABOVE where it stood is holding it
		// on a surface the local model cannot represent (a carpet, a stair step).
		// Geyser delivers that as a MovePlayer reset, not a correction packet.
		NoteServerSupport(b, b.Pos.Y(), newPos.Y())
		// In-flight corrections are stale arc floors, not ground truth — EXCEPT
		// an idle body falling through model-air: that fall is the Y-fight's own
		// signature (the server holds the body on a block the model lacks), and
		// its echoes are precisely the ones that land inside the lift window.
		// Rising echoes stay skipped, so a tower jump's stale pre-arc echo can
		// never re-arm the counter; knockback keeps full protection.
		idleFalling := b.MovementState == "idle" && b.VelY < -0.05 &&
			b.KnockbackX == 0 && b.KnockbackZ == 0 && !time.Now().Before(b.KnockbackUntil)
		if !inFlight || idleFalling {
			NoteMissingSupport(b, b.Pos, newPos)
			// A reset that actually moves the body while a route is active
			// invalidates that route: it is anchored to the pre-reset position,
			// and resuming it walks the body straight back to where the server
			// just refused it — the live drop-chase-into-the-dug-shaft loop
			// fought the server for seventeen seconds this way.
			// handleCorrectPrediction drops its route the same way.
			if b.MovementState != "idle" && dxD*dxD+dyD*dyD+dzD*dzD > 0.25 {
				b.CurrentPath = nil
				b.PathIndex = 0
			}
		}
	}
	if (ack && realTeleport) || (!inFlight && (ack || b.MovementState == "idle")) {
		b.Pos = newPos
		b.VelY = 0.0
		if ack && realTeleport {
			b.TeleportAckPending = true
			b.TeleportAckPos = newPos
		}
	}
	b.ServerGroundY = newPos.Y()
	b.ServerGroundAt = time.Now()
	b.LastServerPosAt = b.ServerGroundAt
	// TeleportAckPending is set inside the adopt branch only: a correction we
	// skipped mid-flight must not arm the ack, or the next tick claims the
	// skipped position and the arc dies anyway.
	return ack
}

func handleMovePlayer(b *bot.Bot, p *packet.MovePlayer) {
	b.Mu.Lock()
	isSelf := p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID
	b.Logger.Debug("MovePlayer packet received",
		slog.Uint64("runtime_id", p.EntityRuntimeID),
		slog.Uint64("self_runtime_id", b.Conn.GameData().EntityRuntimeID),
		slog.Bool("is_self", isSelf),
		slog.Float64("x", float64(p.Position.X())),
		slog.Float64("y", float64(p.Position.Y())),
		slog.Float64("z", float64(p.Position.Z())),
	)
	if isSelf {
		newPos := p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
		if newPos.Y() <= 320 && newPos.Y() >= -64 {
			// H2 (Venity): detect the server snapping us back while we are
			// actively walking. A large jump from our locally-simulated Pos to
			// the server's MovePlayer position during walk_to/follow is the
			// signature of anticheat rejecting movement (rubberband/pin).
			if b.VenityCompat && b.MovementState != "idle" && debuglog.Enabled() {
				dx := b.Pos.X() - newPos.X()
				dy := b.Pos.Y() - newPos.Y()
				dz := b.Pos.Z() - newPos.Z()
				snap := dx*dx + dy*dy + dz*dz
				if snap > 0.25 {
					// #region agent log
					debuglog.Log("V", "move.go:serverSnap", "venity server overrode position during walk", map[string]any{
						"mState":  b.MovementState,
						"snapSq":  snap,
						"localX":  b.Pos.X(),
						"localZ":  b.Pos.Z(),
						"srvX":    newPos.X(),
						"srvZ":    newPos.Z(),
						"srvTick": p.Tick,
						"runId":   "venity-walk-v1",
					})
					// #endregion
				}
			}
			// Read the ack decision against the pre-update Pos: once b.Pos is
			// overwritten every echo looks like a zero-distance no-op.
			ApplySelfMovePlayer(b, p.Mode, newPos)
		}
	} else {
		b.PlayerPositions[p.EntityRuntimeID] = trackedPlayerFeetPosition(p.Position)
		b.PlayerYaws[p.EntityRuntimeID] = p.Yaw
		b.PlayerPitches[p.EntityRuntimeID] = p.Pitch
	}
	b.Mu.Unlock()
	if isSelf {
		// #region agent log
		if p.Tick > 0 {
			debuglog.Log("O", "move.go:handleMovePlayer", "self MovePlayer tick", map[string]any{
				"serverTick": p.Tick,
				"runId":      "tick-fix-v3",
			})
		}
		// #endregion
		if p.Tick > 0 {
			syncServerTick(b, p.Tick, "MovePlayer")
		}
	}
}

func trackedPlayerFeetPosition(pos mgl32.Vec3) mgl32.Vec3 {
	return pos.Sub(mgl32.Vec3{0, 1.62, 0})
}

func handleCorrectPrediction(b *bot.Bot, p *packet.CorrectPlayerMovePrediction) {
	b.Mu.Lock()
	correctedPos := p.Position.Sub(mgl32.Vec3{0, 1.62})
	localY := b.Pos.Y()
	// A body in vertical flight — rising from a jump or a hit-hop, falling
	// from a ledge — owns its arc, and a live knockback extends that to the
	// whole slide. A prediction correction arriving inside the window is the
	// move-checker re-sending the pre-arc position; adopting it mid-rise is
	// what cut jumps to a fraction of their height, and zeroing the fall
	// velocity on each one is what rendered drops as slow motion. Past the
	// arc, the correction is authority again.
	kbLive := b.KnockbackX != 0 || b.KnockbackZ != 0 || time.Now().Before(b.KnockbackUntil)
	inFlight := kbLive || b.VelY > 0.05 || b.VelY < -0.05
	inRange := correctedPos.Y() <= 320 && correctedPos.Y() >= -64

	if inRange && inFlight {
		// The decisive flight diagnostic. During a hit it tells whether the
		// server refused the slide (correction near the PRE-hit spot) or
		// confirms it (near the slid spot). During a jump or fall it is the
		// move-checker re-sending the pre-arc position. Either way the
		// correction is NOT adopted — the arc owns the body — but it is
		// recorded.
		b.Logger.Info("server correction during flight ignored",
			slog.Any("local", b.Pos),
			slog.Any("server", correctedPos),
			slog.Float64("vel_y", float64(b.VelY)),
			slog.Bool("kb_live", kbLive),
		)
	} else if inRange {
		posDiff := float64(b.Pos.X()-correctedPos.X())*float64(b.Pos.X()-correctedPos.X()) +
			float64(b.Pos.Y()-correctedPos.Y())*float64(b.Pos.Y()-correctedPos.Y()) +
			float64(b.Pos.Z()-correctedPos.Z())*float64(b.Pos.Z()-correctedPos.Z())

		if posDiff > 4.0 && b.MovementState != "idle" {
			// Our route is anchored to a position the server just rejected, so
			// drop it. PathIndex goes with it: the tick loop treats a nil path as
			// "no route" only when the index is also back at zero.
			b.CurrentPath = nil
			b.PathIndex = 0
		}

		if b.MovementState != "idle" {
			b.Logger.Info("server corrected position",
				slog.Any("local", b.Pos),
				slog.Any("server", correctedPos),
				slog.String("movement_state", b.MovementState),
			)
		}
		NoteServerSupport(b, b.Pos.Y(), correctedPos.Y())

		b.Pos = correctedPos
		if !b.IsOnLadder {
			b.VelY = 0.0
		}
		// The server owns the authoritative position. Remember the Y it just
		// confirmed so the movement loop can anchor to it instead of free-falling
		// on a world model that may not have the floor decoded yet.
		b.ServerGroundY = correctedPos.Y()
		b.ServerGroundAt = time.Now()
		b.LastServerPosAt = b.ServerGroundAt
	}
	b.Mu.Unlock()

	syncServerTick(b, p.Tick, "CorrectPlayerMovePrediction")
	b.Mu.Lock()
	prevTick := b.ServerTick
	b.MovementSyncPending = b.RewindMovement
	entityID := b.Conn.GameData().EntityUniqueID
	b.Mu.Unlock()
	// #region agent log
	debuglog.Log("H", "move.go:handleCorrectPrediction", "synced server tick", map[string]any{
		"prevTick":   prevTick,
		"serverTick": p.Tick,
		"newTick":    p.Tick + 1,
		"srvY":       correctedPos.Y(),
		"localY":     localY,
		"srvRawY":    p.Position.Y(),
		"runId":      "tick-fix-v2",
	})
	// #endregion
	if b.RewindMovement {
		sendMovementPredictionSync(b, entityID)
	}
}

func sendMovementPredictionSync(b *bot.Bot, entityUniqueID int64) {
	pk := &packet.ClientMovementPredictionSync{
		ActorFlags:        protocol.NewBitset(protocol.EntityDataFlagCount),
		EntityUniqueID:    entityUniqueID,
		BoundingBoxWidth:  0.6,
		BoundingBoxHeight: 1.8,
		MovementSpeed:     0.1,
	}
	if err := b.Conn.WritePacket(pk); err != nil {
		b.Logger.Warn("ClientMovementPredictionSync write failed", slog.String("error", err.Error()))
		return
	}
	b.Mu.Lock()
	b.MovementSyncPending = false
	b.Mu.Unlock()
	// #region agent log
	debuglog.Log("M", "move.go:sendMovementPredictionSync", "sent movement prediction sync", map[string]any{
		"entityUniqueID": entityUniqueID,
		"runId":          "tick-fix",
	})
	// #endregion
}
