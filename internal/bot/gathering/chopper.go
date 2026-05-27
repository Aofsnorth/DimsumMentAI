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

func (tc *TreeChopper) GatherWood(ctx context.Context, targetCount int, preferred string) {
	bot := tc.rg.bot
	botPos := bot.GetCoords()

	tc.logger.Debug("Starting wood gathering", "target", targetCount)
	tc.rg.bot.SendChat("Aku cari pohon dulu ya!")

	var logPos protocol.BlockPos
	found := false
	var fallbackLog protocol.BlockPos
	hasFallback := false

	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	// Search horizontal area for actual logs only. Solid blocks such as dirt or
	// grass are deliberately ignored so tree chopping never turns into digging.
	for r := int32(1); r <= 16; r++ {
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if math.Abs(float64(dx)) != float64(r) && math.Abs(float64(dz)) != float64(r) {
					continue
				}
				for dy := int32(-1); dy <= 5; dy++ {
					tx, ty, tz := bx+dx, by+dy, bz+dz
					name, ok := tc.rg.bot.GetBlockName(tx, ty, tz)
					if ok && isLogBlockName(name) {
						if !hasFallback {
							fallbackLog = protocol.BlockPos{tx, ty, tz}
							hasFallback = true
						}
						if preferred != "" && !matchesPreferredLog(name, preferred) {
							continue
						}
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
		if hasFallback {
			logPos = fallbackLog
			found = true
		}
	}

	if !found {
		tc.logger.Warn("No log blocks found nearby")
		tc.rg.bot.SendChat("Aku belum nemu pohon yang kebaca di sekitar sini.")
		return
	}

	tc.ChopTreeAt(ctx, logPos, targetCount)
}

func (tc *TreeChopper) ChopTreeAt(ctx context.Context, startPos protocol.BlockPos, targetCount int) {
	tc.logger.Debug("Directed to chop tree", "pos", startPos)
	if targetCount <= 0 {
		targetCount = 1
	}

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
	tc.chopTree(ctx, basePos, targetCount)
}

func (tc *TreeChopper) traceToBase(pos protocol.BlockPos) protocol.BlockPos {
	current := pos

	for i := 0; i < 15; i++ {
		below := protocol.BlockPos{current.X(), current.Y() - 1, current.Z()}
		name, ok := tc.rg.bot.GetBlockName(below.X(), below.Y(), below.Z())
		if ok && isLogBlockName(name) {
			current = below
		} else {
			break
		}
	}
	return current
}
