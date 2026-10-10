package gathering

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Treefarm tuning. The bone-meal budget mirrors how a player force-grows: a
// sapling usually takes 1-4 meals, and overshooting the budget wastes the
// stack on a host that ate the packets without growing anything.
const (
	// treeFarmMaxCycles is the hard safety bound on laps; the bone meal supply
	// ends the job long before this in any normal run.
	treeFarmMaxCycles = 64
	// treeFarmBoneBudget is how many meals one sapling may be given.
	treeFarmBoneBudget = 12
	// treeFarmPlantTimeout is how long the world gets to show the planted
	// sapling before the plant is written off.
	treeFarmPlantTimeout = 2500 * time.Millisecond
	// treeFarmGrowTimeout is how long one meal gets to advance the sapling.
	treeFarmGrowTimeout = 2 * time.Second
	// treeFarmPlantRadius is the soil scan radius when the stump column is not
	// plantable.
	treeFarmPlantRadius int32 = 4
	// faceTop is the block face the sapling and the meals are clicked on.
	faceTop = int32(1)
	// noSlot marks "no matching stack in the inventory".
	noSlot = ^uint32(0)
)

// TreeFarm fells a tree, replants a sapling, force-grows it with bone meal,
// and repeats until the bone meal or the saplings run out.
//
// One lap = one tree. The chop's own sweep already collects the sapling drops,
// so the inventory is the cycle's fuel gauge: every lap spends one sapling and
// a few meals, and the loop stops the moment either runs dry — which is exactly
// the order the player gave ("sampe bone meal-nya habis").
//
// Both halves of the replant are confirmed against the world before anything
// is counted or spent: an unconfirmed plant is never meal'd, and a sapling
// that never grows is written off after the budget instead of draining the
// stack on a host that eats the packets.
func (tc *TreeChopper) TreeFarm(ctx context.Context, maxCycles int, preferred string) (trees, logs int) {
	if maxCycles <= 0 || maxCycles > treeFarmMaxCycles {
		maxCycles = treeFarmMaxCycles
	}

	for cycle := 0; cycle < maxCycles; cycle++ {
		if ctx.Err() != nil {
			return trees, logs
		}
		if tc.rg.looter.currentItemCount("bone_meal") <= 0 {
			tc.logger.Info("treefarm: bone meal ran out", "trees", trees, "logs", logs)
			return trees, logs
		}
		if tc.findItemSlotByName("sapling") == noSlot {
			tc.logger.Info("treefarm: no sapling in the inventory", "trees", trees, "logs", logs)
			return trees, logs
		}

		// Fell the nearest tree. The full gather machinery (pending lists,
		// rescan walks, attempt budgets) belongs to the gather job; the farm
		// only ever needs one tree, and the next one grows on demand at the
		// spot the last lap planted.
		base, ok := tc.nearestTreeBase(preferred)
		if !ok {
			tc.logger.Info("treefarm: no reachable tree to fell", "trees", trees, "logs", logs)
			return trees, logs
		}
		reached, got := tc.ChopTreeAt(ctx, base, 1)
		if !reached {
			continue
		}
		trees++
		logs += got

		if !tc.plantAndGrow(ctx, base) {
			continue
		}
	}
	return trees, logs
}

// nearestTreeBase picks the best-scoring tree around the current position.
func (tc *TreeChopper) nearestTreeBase(preferred string) (protocol.BlockPos, bool) {
	skip := make(map[protocol.BlockPos]bool)
	candidates, _ := tc.findTreeCandidates(tc.rg.bot.GetCoords(), preferred, skip)
	if len(candidates) == 0 {
		return protocol.BlockPos{}, false
	}
	return candidates[0].base, true
}

// plantAndGrow plants the carried sapling near where the tree stood and
// force-grows it with bone meal. The stump column is tried first — the tree
// regrows where it stood, which keeps the farm in one place.
func (tc *TreeChopper) plantAndGrow(ctx context.Context, stump protocol.BlockPos) bool {
	bot := tc.rg.bot
	spot, ok := tc.findPlantSpot(stump)
	if !ok {
		tc.logger.Warn("treefarm: no soil with headroom to plant on")
		return false
	}
	if !bot.NavigateToBlock(spot.x, spot.y, spot.z, 2.5) {
		tc.logger.Warn("treefarm: could not walk to the planting spot", "spot", spot)
		return false
	}
	haltWalkOnly(bot)

	saplingSlot := tc.findItemSlotByName("sapling")
	if saplingSlot == noSlot {
		return false
	}
	saplingsBefore := tc.rg.looter.currentItemCount("sapling")
	if err := bot.EquipItem(saplingSlot); err != nil {
		tc.logger.Warn("treefarm: could not equip the sapling", "err", err)
		return false
	}
	bot.LookAt(cellCentre(spot.x, spot.y, spot.z))
	if !sleepContext(ctx, 150*time.Millisecond) {
		return false
	}
	tc.useItemOn(spot.x, spot.y, spot.z)

	if !tc.waitForCell(ctx, spot.x, spot.y+1, spot.z, treeFarmPlantTimeout, isSaplingName) {
		if tc.rg.looter.currentItemCount("sapling") < saplingsBefore {
			// The server consumed the sapling without showing the block; the
			// inventory is the truth, so keep going — the meal loop re-checks
			// the cell and stops honestly if it never appears.
			tc.logger.Warn("treefarm: sapling consumed but no block appeared")
		} else {
			tc.logger.Warn("treefarm: the server never planted the sapling")
			return false
		}
	}

	meals := 0
	for meals < treeFarmBoneBudget {
		if ctx.Err() != nil {
			return false
		}
		if tc.rg.looter.currentItemCount("bone_meal") <= 0 {
			tc.logger.Info("treefarm: bone meal ran out mid-growth", "meals", meals)
			return false
		}
		name, known := bot.GetBlockName(spot.x, spot.y+1, spot.z)
		if !known || !isSaplingName(name) {
			// Grown, or the sapling is gone for another reason. Either way the
			// lap is done.
			return true
		}
		mealSlot := tc.findItemSlotByName("bone_meal")
		if mealSlot == noSlot {
			tc.logger.Warn("treefarm: bone meal vanished between the count and the slot")
			return false
		}
		if err := bot.EquipItem(mealSlot); err != nil {
			tc.logger.Warn("treefarm: could not equip bone meal", "err", err)
			return false
		}
		bot.LookAt(cellCentre(spot.x, spot.y+1, spot.z))
		if !sleepContext(ctx, 100*time.Millisecond) {
			return false
		}
		tc.useItemOn(spot.x, spot.y+1, spot.z)
		meals++
		// A meal that does nothing is retried while the budget lasts — real
		// players overshoot meals too, and one silent packet must not end the
		// whole lap.
		_ = tc.waitForCell(ctx, spot.x, spot.y+1, spot.z, treeFarmGrowTimeout, func(name string) bool {
			return !isSaplingName(name)
		})
	}
	tc.logger.Warn("treefarm: bone meal budget spent, sapling still standing", "meals", meals)
	return false
}

// findPlantSpot looks near the bot for soil with air above it. The stump's own
// column wins when it is plantable.
func (tc *TreeChopper) findPlantSpot(stump protocol.BlockPos) (spot, bool) {
	bot := tc.rg.bot
	if name, ok := bot.GetBlockName(stump.X(), stump.Y()-1, stump.Z()); ok && isSoilName(name) {
		if above, ok2 := bot.GetBlockName(stump.X(), stump.Y(), stump.Z()); ok2 && isAirName(above) {
			return spot{stump.X(), stump.Y() - 1, stump.Z()}, true
		}
	}

	pos := bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))
	for dx := -treeFarmPlantRadius; dx <= treeFarmPlantRadius; dx++ {
		for dz := -treeFarmPlantRadius; dz <= treeFarmPlantRadius; dz++ {
			for dy := int32(-2); dy <= 2; dy++ {
				x, y, z := bx+dx, by+dy, bz+dz
				name, ok := bot.GetBlockName(x, y, z)
				if !ok || !isSoilName(name) {
					continue
				}
				above, ok2 := bot.GetBlockName(x, y+1, z)
				if !ok2 || !isAirName(above) {
					continue
				}
				return spot{x, y, z}, true
			}
		}
	}
	return spot{}, false
}

// findItemSlotByName returns the first slot whose item name contains `want`.
func (tc *TreeChopper) findItemSlotByName(want string) uint32 {
	inv := tc.rg.bot.GetInventorySlots()
	names := tc.rg.bot.GetItemNames()
	want = strings.ToLower(want)
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		if strings.Contains(strings.ToLower(names[item.NetworkID]), want) {
			return slot
		}
	}
	return noSlot
}

// useItemOn uses the held stack on the top face of a block — the same
// transaction the farmer plants and fertilises with, with the interactor's
// vanilla field set. The caller settles the look first: the server aims the
// interaction by the bot's view.
func (tc *TreeChopper) useItemOn(x, y, z int32) {
	bot := tc.rg.bot
	slot := bot.GetHeldItemSlot()
	inv := bot.GetInventorySlots()
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			TriggerType:     protocol.TriggerTypePlayerInput,
			BlockPosition:   protocol.BlockPos{x, y, z},
			BlockFace:       faceTop,
			HotBarSlot:      safecast.To[int32](slot),
			HeldItem:        protocol.ItemInstance{Stack: inv[slot]},
			Position:        bot.GetCoords().Add(mgl32.Vec3{0, mineEyeHeight, 0}),
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	_ = bot.WritePacket(tx)
}

// waitForCell polls a block cell until its name satisfies `want`, or the
// timeout passes. False means the cell never looked right in time.
func (tc *TreeChopper) waitForCell(ctx context.Context, x, y, z int32, timeout time.Duration, want func(name string) bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		name, ok := tc.rg.bot.GetBlockName(x, y, z)
		if ok && want(name) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		if !sleepContext(ctx, 150*time.Millisecond) {
			return false
		}
	}
}

// spot is a plantable soil cell (the block the sapling's cell stands on).
type spot struct{ x, y, z int32 }

func cellCentre(x, y, z int32) mgl32.Vec3 {
	return mgl32.Vec3{float32(x) + 0.5, float32(y) + 0.5, float32(z) + 0.5}
}

func isSaplingName(name string) bool {
	return strings.Contains(strings.ToLower(name), "sapling")
}

func isSoilName(name string) bool {
	n := strings.ToLower(strings.TrimPrefix(strings.ToLower(name), "minecraft:"))
	switch n {
	case "grass_block", "dirt", "coarse_dirt", "podzol":
		return true
	}
	return false
}

func isAirName(name string) bool {
	n := strings.ToLower(strings.TrimPrefix(strings.ToLower(name), "minecraft:"))
	switch n {
	case "", "air", "cave_air", "void_air":
		return true
	}
	return false
}

// TreeFarmCount reports the human-readable outcome of a farm run, the same way
// GatherOutcome does for a gather: a run that felled nothing is a failure with
// the reason, everything else is a success the narrator can quote.
func TreeFarmCount(trees, logs int) (success bool, summary string) {
	if trees == 0 && logs == 0 {
		return false, "gagal: butuh bone meal dan sapling di inventory, plus pohon yang bisa ditebang"
	}
	return true, fmt.Sprintf("%d pohon, %d log", trees, logs)
}
