package acquisition

import (
	"context"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
)

// GatherDroppedItems scans for item entities on ground and drives bot towards them.
func (ia *InventoryAcquisition) GatherDroppedItems(ctx context.Context, radius float32) {
	if ia.bot == nil {
		return
	}

	botPos := ia.bot.GetCoords()
	entities := ia.bot.GetEntities()

	var closestItem *entity.Info
	closestDist := float32(math.MaxFloat32)

	for _, ent := range entities {
		if ent.Type == "item" || strings.Contains(strings.ToLower(ent.Type), "item") || strings.Contains(strings.ToLower(ent.Name), "item") {
			dx := ent.Position.X() - botPos.X()
			dy := ent.Position.Y() - botPos.Y()
			dz := ent.Position.Z() - botPos.Z()
			dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

			if dist <= radius && dist < closestDist {
				closestDist = dist
				closestItem = ent
			}
		}
	}

	if closestItem != nil {
		ia.logger.Info("Walking to pick up dropped item on ground", "name", closestItem.Name, "dist", closestDist)
		
		reached := ia.bot.NavigateToBlock(
			int32(math.Floor(float64(closestItem.Position.X()))),
			int32(math.Floor(float64(closestItem.Position.Y()))),
			int32(math.Floor(float64(closestItem.Position.Z()))),
			1.2,
		)
		if reached {
			ia.bot.StopMovement()
			time.Sleep(500 * time.Millisecond)
		}
	}
}
