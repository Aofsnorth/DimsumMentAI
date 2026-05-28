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
	return l.collectDrops(ctx, maxDist, "", -1, 4500*time.Millisecond)
}

func (l *Looter) CollectMatchingDrops(ctx context.Context, maxDist float32, itemName string) int {
	return l.collectDrops(ctx, maxDist, itemName, -1, 4500*time.Millisecond)
}

// CollectMatchingDropsUntil sweeps for matching drops, exiting as soon as the
// bot's inventory count for itemName rises above beforeCount OR timeout elapses.
// Used by the mining loop for program-based fast pickup without waiting for
// server confirmation.
func (l *Looter) CollectMatchingDropsUntil(ctx context.Context, maxDist float32, itemName string, beforeCount int, timeout time.Duration) int {
	return l.collectDrops(ctx, maxDist, itemName, beforeCount, timeout)
}

func (l *Looter) collectDrops(ctx context.Context, maxDist float32, itemName string, beforeCount int, timeout time.Duration) int {
	collected := 0
	l.logger.Info("Starting item sweep", "max_distance", maxDist, "item", itemName)
	deadline := time.Now().Add(timeout)
	attempted := make(map[uint64]bool)

	pollInv := beforeCount >= 0 && itemName != ""

	// Tight early-exit check: if inventory has already risen (server-side
	// pickup beat us to the sweep), bail immediately.
	if pollInv && l.currentItemCount(itemName) > beforeCount {
		return 0
	}

	for {
		select {
		case <-ctx.Done():
			return collected
		default:
		}

		if pollInv && l.currentItemCount(itemName) > beforeCount {
			return collected
		}

		closestItem := l.closestDrop(maxDist, itemName, attempted)

		if closestItem == nil {
			if time.Now().After(deadline) {
				break
			}
			if !sleepContext(ctx, 80*time.Millisecond) {
				return collected
			}
			continue
		}

		l.logger.Info("Looter: heading to item drop", "id", closestItem.ID, "pos", closestItem.Position)
		l.rg.bot.LookAt(closestItem.Position)
		if !sleepContext(ctx, 80*time.Millisecond) {
			return collected
		}

		if pollInv && l.currentItemCount(itemName) > beforeCount {
			return collected
		}

		// Navigate close enough
		reached := l.rg.bot.NavigateToBlock(
			int32(math.Floor(float64(closestItem.Position.X()))),
			int32(math.Floor(float64(closestItem.Position.Y()))),
			int32(math.Floor(float64(closestItem.Position.Z()))),
			1.5,
		)

		if reached {
			attempted[closestItem.ID] = true
			collected++
			if pollInv && l.currentItemCount(itemName) > beforeCount {
				return collected
			}
		} else {
			attempted[closestItem.ID] = true
		}
	}

	l.rg.bot.StopMovement()
	return collected
}

// currentItemCount counts items in the bot's inventory matching itemName.
func (l *Looter) currentItemCount(itemName string) int {
	return inventoryCountMatching(l.rg.bot.GetInventorySlots(), l.rg.bot.GetItemNames(), itemName)
}

func (l *Looter) closestDrop(maxDist float32, itemName string, attempted map[uint64]bool) *entity.Info {
	botPos := l.rg.bot.GetCoords()
	entities := l.rg.bot.GetEntities()

	var closestItem *entity.Info
	closestDist := float32(math.MaxFloat32)

	for _, e := range entities {
		if attempted[e.ID] {
			continue
		}
		isItem := strings.Contains(strings.ToLower(e.Type), "item") ||
			strings.Contains(strings.ToLower(e.Name), "item")
		if !isItem {
			continue
		}
		if itemName != "" && !itemNameMatches(e.Name, itemName) {
			continue
		}

		dy := e.Position.Y() - botPos.Y()
		if dy > 3 || dy < -4 {
			continue
		}

		dist := l.distance(botPos, e.Position)
		if dist <= maxDist && dist < closestDist {
			closestDist = dist
			closestItem = e
		}
	}
	return closestItem
}

func (l *Looter) distance(a, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
