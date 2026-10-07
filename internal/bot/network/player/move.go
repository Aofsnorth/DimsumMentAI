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
	if ack {
		// A host that resets the body a little ABOVE where it stood is holding it
		// on a surface the local model cannot represent (a carpet, a stair step).
		// Geyser delivers that as a MovePlayer reset, not a correction packet.
		NoteServerSupport(b, b.Pos.Y(), newPos.Y())
	}
	// During a live knockback the server has assigned this body motion and a
	// real client integrates it — rising on the hop, sliding on the push. The
	// echo still reflects where the server saw us a couple of ticks ago, so
	// adopting it (and zeroing the hop velocity it came with) is what erased
	// every "kok ga kena knockback" before the slide could ever be seen:
	// claimed, overwritten within 200ms, corrected back to the stale spot.
	// Once the carried motion has decayed, the echo is authority again.
	if ack || (b.MovementState == "idle" && b.KnockbackX == 0 && b.KnockbackZ == 0) {
		b.Pos = newPos
		b.VelY = 0.0
	}
	b.ServerGroundY = newPos.Y()
	b.ServerGroundAt = time.Now()
	b.LastServerPosAt = b.ServerGroundAt
	if ack {
		b.TeleportAckPending = true
		b.TeleportAckPos = newPos
	}
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
	if correctedPos.Y() <= 320 && correctedPos.Y() >= -64 {
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
