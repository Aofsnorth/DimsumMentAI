package player

import (
	"log/slog"
	"math"

	"bedrock-ai/internal/bot"
	"github.com/go-gl/mathgl/mgl32"
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
		b.PlayerPositions[p.EntityRuntimeID] = p.Position
	}
	b.Mu.Unlock()
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
}
