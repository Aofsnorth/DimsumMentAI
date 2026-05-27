package player

import (
	"log/slog"
	"math"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/debuglog"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

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
			fell := b.Pos.Y()-newPos.Y() > 0.4
			if fell {
				feetX := int32(math.Floor(float64(b.Pos.X())))
				feetY := int32(math.Floor(float64(b.Pos.Y())))
				feetZ := int32(math.Floor(float64(b.Pos.Z())))
				b.WorldModel.SetSolid(feetX, feetY-1, feetZ, false)
				if b.MovementState != "idle" {
					b.CurrentPath = nil
				}
			}
			b.Pos = newPos
			b.VelY = 0.0
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
	correctedPos := p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
	if correctedPos.Y() <= 320 && correctedPos.Y() >= -64 {
		posDiff := float64(b.Pos.X()-correctedPos.X())*float64(b.Pos.X()-correctedPos.X()) +
			float64(b.Pos.Y()-correctedPos.Y())*float64(b.Pos.Y()-correctedPos.Y()) +
			float64(b.Pos.Z()-correctedPos.Z())*float64(b.Pos.Z()-correctedPos.Z())

		if posDiff > 4.0 {
			if b.Pos.Y()-correctedPos.Y() > 0.4 {
				feetX := int32(math.Floor(float64(b.Pos.X())))
				feetY := int32(math.Floor(float64(b.Pos.Y())))
				feetZ := int32(math.Floor(float64(b.Pos.Z())))
				b.WorldModel.SetSolid(feetX, feetY-1, feetZ, false)
			}
			if b.MovementState != "idle" {
				b.CurrentPath = nil
			}
		}

		b.Pos = correctedPos
		if !b.IsOnLadder {
			b.VelY = 0.0
		}
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
		"runId":      "tick-fix-v2",
	})
	// #endregion
	if b.RewindMovement && !b.VenityCompat {
		sendMovementPredictionSync(b, entityID)
	}
}

func sendMovementPredictionSync(b *bot.Bot, entityUniqueID int64) {
	pk := &packet.ClientMovementPredictionSync{
		ActorFlags:            protocol.NewBitset(protocol.EntityDataFlagCount),
		EntityUniqueID:        entityUniqueID,
		BoundingBoxWidth:      0.6,
		BoundingBoxHeight:     1.8,
		MovementSpeed:         0.1,
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
