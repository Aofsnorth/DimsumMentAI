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
	// The sweep walks to the drops, which re-aims navigation mid-journey. The
	// journey that called this is saved here and put back afterwards: without
	// that, the sweep overwrites the caller's destination and the bot stands
	// still where the interrupted journey died.
	savedJourney := l.rg.bot.SnapshotJourney()
	defer l.rg.bot.RestoreJourney(savedJourney)
	deadline := time.Now().Add(timeout)
	attempted := make(map[uint64]bool)
	pollInv := beforeCount >= 0 && itemName != ""

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
		if time.Now().After(deadline) {
			break
		}
		if l.collectDrop(ctx, &collected, deadline, maxDist, itemName, attempted, beforeCount, pollInv) {
			break
		}
	}

	return collected
}

func (l *Looter) collectDrop(ctx context.Context, collected *int, deadline time.Time, maxDist float32, itemName string, attempted map[uint64]bool, beforeCount int, pollInv bool) bool {
	closestItem := l.closestDrop(maxDist, itemName, attempted)
	if closestItem == nil {
		if time.Now().After(deadline) {
			return true
		}
		return !sleepContext(ctx, 80*time.Millisecond)
	}

	l.logger.Info("Looter: heading to item drop", "id", closestItem.ID, "pos", closestItem.Position)

	// Navigate to the item's EXACT position (not the floor block) so the bot
	// actually stands on top of the drop. Bedrock only pulls items within ~1
	// block; a loose tolerance left the bot just out of pickup range and it
	// counted "reached" without ever collecting the item.
	//
	// Tighten arrival tolerance to ~0.6 blocks so the bot closes into pickup
	// range before the steering loop declares "arrived" and idles.
	l.rg.bot.SetTargetTolerance(0.6)
	defer l.rg.bot.SetTargetTolerance(2.0)

	// Poll toward the item: re-aim and re-issue navigation only while the
	// entity still exists, giving the steering loop time to actually walk
	// there instead of thrashing the path every tick.
	for i := 0; i < 40; i++ {
		if time.Now().After(deadline) {
			return true
		}
		if pollInv && l.currentItemCount(itemName) > beforeCount {
			return true
		}

		// Item gone → picked up.
		if !l.itemExists(closestItem.ID) {
			attempted[closestItem.ID] = true
			if pollInv {
				return l.currentItemCount(itemName) > beforeCount
			}
			*collected++
			return false
		}

		// Refresh the drop's position (items drift on spawn) and walk to it.
		cur := l.entityByID(closestItem.ID)
		if cur == nil {
			attempted[closestItem.ID] = true
			return false
		}
		l.rg.bot.LookAt(cur.Position)
		l.rg.bot.NavigateTo(cur.Position)
		if !sleepContext(ctx, 250*time.Millisecond) {
			return true
		}
	}
	// Couldn't collect within the poll window; abandon this drop so the sweep
	// can try another instead of looping forever on an unreachable item.
	attempted[closestItem.ID] = true
	return false
}

// entityByID returns the tracked entity with the given ID, or nil.
func (l *Looter) entityByID(id uint64) *entity.Info {
	for _, e := range l.rg.bot.GetEntities() {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// itemExists reports whether an entity with the given ID is still tracked.
func (l *Looter) itemExists(id uint64) bool {
	for _, e := range l.rg.bot.GetEntities() {
		if e.ID == id {
			return true
		}
	}
	return false
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

		// DropWithinReach is the vertical window, and it used to be a hardcoded
		// three blocks up and four down. That is where every dropped log went.
		//
		// A trunk is chopped by towering: the bot climbs, breaks the logs high
		// up, and the drops fall to the ground at the base — five, six, seven
		// blocks below the body. The window rejected every one of them before
		// the distance check ran, so the sweep logged "Starting item sweep",
		// found nothing, and spun for its full timeout. The log showed exactly
		// that: a sweep that starts and then never says it is heading anywhere.
		if !DropWithinReach(botPos, e.Position, maxDist) {
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

// DropWithinReach reports whether a drop at target is close enough to the body
// for a sweep to go and get it.
//
// The vertical bound is derived from the same radius as the horizontal one, so
// a sweep cannot reject a drop it is plainly able to walk to. That pairing is
// the whole point: the two used to be independent, and a fixed three-block
// vertical window against a caller-chosen horizontal radius is what made a
// tower-chopped log invisible to the bot standing directly above it.
func DropWithinReach(from, target mgl32.Vec3, maxDist float32) bool {
	dy := target.Y() - from.Y()
	return dy <= maxDist && dy >= -maxDist
}
