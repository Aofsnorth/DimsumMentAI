package gathering

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type TreeChopper struct {
	rg     *ResourceGatherer
	logger *slog.Logger
}

func NewTreeChopper(rg *ResourceGatherer, logger *slog.Logger) *TreeChopper {
	return &TreeChopper{
		rg:     rg,
		logger: logger,
	}
}

func (tc *TreeChopper) GatherWood(ctx context.Context, targetCount int) {
	bot := tc.rg.bot
	botPos := bot.GetCoords()

	tc.logger.Info("Starting wood gathering", "target", targetCount)
	tc.rg.bot.SendChat("Aku cari pohon dulu ya!")

	var logPos protocol.BlockPos
	found := false

	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	// Search horizontal area
	for r := int32(1); r <= 16; r++ {
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if math.Abs(float64(dx)) != float64(r) && math.Abs(float64(dz)) != float64(r) {
					continue
				}
				for dy := int32(-1); dy <= 5; dy++ {
					tx, ty, tz := bx+dx, by+dy, bz+dz
					world := tc.rg.bot.GetLocalWorldModel()
					if world.IsSolid(tx, ty, tz) {
						logPos = protocol.BlockPos{tx, ty, tz}
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		tc.logger.Warn("No solid blocks registered as logs nearby. Guessing coordinate in front of bot.")
		logPos = protocol.BlockPos{bx + 2, by, bz}
	}

	tc.ChopTreeAt(ctx, logPos)
}

func (tc *TreeChopper) ChopTreeAt(ctx context.Context, startPos protocol.BlockPos) {
	tc.logger.Info("Directed to chop tree", "pos", startPos)

	targetVec := mgl32.Vec3{float32(startPos.X()) + 0.5, float32(startPos.Y()), float32(startPos.Z()) + 0.5}
	tc.rg.bot.LookAt(targetVec)
	time.Sleep(100 * time.Millisecond)

	reached := tc.rg.bot.NavigateToBlock(startPos.X(), startPos.Y(), startPos.Z(), 2.5)
	if !reached {
		tc.logger.Warn("Could not reach tree base")
		return
	}
	tc.rg.bot.StopMovement()

	basePos := tc.traceToBase(startPos)
	tc.chopTree(ctx, basePos)
}

func (tc *TreeChopper) traceToBase(pos protocol.BlockPos) protocol.BlockPos {
	current := pos
	world := tc.rg.bot.GetLocalWorldModel()

	for i := 0; i < 15; i++ {
		below := protocol.BlockPos{current.X(), current.Y() - 1, current.Z()}
		if world.IsSolid(below.X(), below.Y(), below.Z()) {
			current = below
		} else {
			break
		}
	}
	return current
}
