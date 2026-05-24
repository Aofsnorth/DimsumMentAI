package gathering

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"github.com/go-gl/mathgl/mgl32"
)

type Looter struct {
	rg     *ResourceGatherer
	logger *slog.Logger
}

func NewLooter(rg *ResourceGatherer, logger *slog.Logger) *Looter {
	return &Looter{
		rg:     rg,
		logger: logger,
	}
}

// CollectAllDrops sweeps nearby item drops and navigates to them
func (l *Looter) CollectAllDrops(ctx context.Context, maxDist float32) int {
	collected := 0
	l.logger.Info("Starting item sweep", "max_distance", maxDist)

	for {
		select {
		case <-ctx.Done():
			return collected
		default:
		}

		botPos := l.rg.bot.GetCoords()
		entities := l.rg.bot.GetEntities()

		var closestItem *entity.Info
		closestDist := float32(math.MaxFloat32)

		for _, entity := range entities {
			// Check if item drop
			isItem := strings.Contains(strings.ToLower(entity.Type), "item") || 
				strings.Contains(strings.ToLower(entity.Name), "item")
			if !isItem {
				continue
			}

			dy := entity.Position.Y() - botPos.Y()
			if dy > 3 || dy < -3 {
				continue // skip high/low drops
			}

			dist := l.distance(botPos, entity.Position)
			if dist <= maxDist {
				if dist < closestDist {
					closestDist = dist
					closestItem = entity
				}
			}
		}

		if closestItem == nil {
			break
		}

		l.logger.Info("Looter: heading to item drop", "id", closestItem.ID, "pos", closestItem.Position)
		l.rg.bot.LookAt(closestItem.Position)
		time.Sleep(100 * time.Millisecond)

		// Navigate close enough
		reached := l.rg.bot.NavigateToBlock(
			int32(math.Floor(float64(closestItem.Position.X()))),
			int32(math.Floor(float64(closestItem.Position.Y()))),
			int32(math.Floor(float64(closestItem.Position.Z()))),
			1.5,
		)

		if reached {
			collected++
			time.Sleep(500 * time.Millisecond) // wait for server pickup registration
		} else {
			// If not reached, stop searching this item to avoid loop
			break
		}
	}

	l.rg.bot.StopMovement()
	return collected
}

func (l *Looter) distance(a, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
