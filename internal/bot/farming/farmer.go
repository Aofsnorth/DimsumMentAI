// Package farming implements bot farming operations: planting, harvesting,
// and tilling.
//
// The rule this package is written around is that a crop is not harvested
// because it looks like a crop. Wheat is harvested at stage 7 and not at 3, a
// pumpkin stem is never harvested at all, and a crop whose growth stage cannot
// be read is left standing. The previous version of this file broke anything
// whose name contained "wheat" or "pumpkin" at any age, never replanted what it
// took, and had no bone meal — so a "harvest" destroyed a freshly planted field
// and reported success anyway.
//
// Maturity is read through the BlockStateSource seam in confirm.go, which
// *bot.Bot does not satisfy today. Until it does, every staged crop reads as an
// unknown stage and is left alone. That is deliberate: an unreadable age must
// not be guessed into a mature one.
package farming

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	// scanRadius is how far around the bot a field is looked for.
	scanRadius = int32(24)

	// scanBelow and scanAbove bound the vertical part of the scan. A farm is
	// roughly level, so a shallow band is enough and keeps the scan off the
	// cave systems that would otherwise dominate the search.
	scanBelow = int32(-3)
	scanAbove = int32(3)

	// harvestReach is the distance the bot walks to before clicking a crop.
	harvestReach = 3.0

	// plantRadius is how far around the bot bare farmland is looked for. It is
	// narrower than the harvest scan on purpose: planting is an action with a
	// cost, and a field across the map is not one to cross a village for.
	plantRadius = int32(16)

	// boneMealBudget is how many meals one cell may be given in a single visit.
	// A wheat field takes eight; a single crop is usually done in one. The
	// bound stops a field that refuses to mature from being fertilised forever
	// on the same visit.
	boneMealBudget = 8

	// hoeRadiusFloor is the smallest hoeing scan, so a zero radius still does
	// the block the bot is standing on rather than nothing.
	hoeRadiusFloor = int32(1)

	// topFace is the face a seed is used on. A crop is planted on the top of
	// the farmland, so the click targets the farmland and hits its top face.
	topFace = int32(1)
)

// waterNeighbours are the six cells around a kelp block that prove it is in
// water. Kelp that is not in water is not a harvest; breaking it produces
// nothing and the bot reports litter.
var waterNeighbours = [6][3]int32{
	{0, -1, 0}, {0, 1, 0},
	{1, 0, 0}, {-1, 0, 0},
	{0, 0, 1}, {0, 0, -1},
}

// Timings is every wait in a farming operation.
type Timings struct {
	// Aim is the pause between turning toward the cell and clicking it.
	Aim time.Duration
	// Settle is the pause after a click before the first confirmation read.
	Settle time.Duration
	// Confirm is how long the world gets to reflect an action. Expiry is a
	// failure, never a success.
	Confirm time.Duration
	// Poll is the sampling interval while confirming.
	Poll time.Duration
}

// DefaultTimings returns the production timings.
func DefaultTimings() Timings {
	return Timings{
		Aim:     120 * time.Millisecond,
		Settle:  250 * time.Millisecond,
		Confirm: 2 * time.Second,
		Poll:    50 * time.Millisecond,
	}
}

// Compressed divides every wait by factor, so a test does not sit through the
// production confirmation budget.
func (t Timings) Compressed(factor int) Timings {
	if factor <= 0 {
		return t
	}
	d := time.Duration(factor)
	return Timings{
		Aim:     t.Aim / d,
		Settle:  t.Settle / d,
		Confirm: t.Confirm / d,
		Poll:    t.Poll / d,
	}
}

// Bot is the slice of the bot this package needs.
//
// Every method here already exists on *bot.Bot, so wiring the package costs
// nothing outside it. Block state is a separate interface for exactly the
// reason the furnace package has one: *bot.Bot cannot supply it today.
//
// The methods the previous version declared and never called — GetEntities,
// NavigateTo, SendChat, GetEntityRuntimeID, GetLocalWorldModel, DropItem — are
// gone. An interface that promises capabilities its implementation does not use
// is a fake it looks like it needs.
type Bot interface {
	GetCoords() mgl32.Vec3
	GetBlockName(x, y, z int32) (string, bool)
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	ResetLook()
	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	EquipItem(slot uint32) error
	WritePacket(pk packet.Packet) error
	ReportActionStatus(user string, status event.ActionStatus)
}

// Farmer runs farming operations against a field: reading crop age, harvesting
// only what is ripe, putting the seed back, and accelerating the new planting.
type Farmer struct {
	bot    Bot
	logger *slog.Logger

	mu      sync.Mutex
	timings Timings
	states  BlockStateSource
	active  bool
	results []HarvestResult
}

// NewFarmer creates a farmer.
//
// If the bot also satisfies BlockStateSource the seam is picked up
// automatically, so adding GetBlockState to *bot.Bot is all the wiring the
// maturity check needs — no call site changes.
func NewFarmer(bot Bot, logger *slog.Logger) *Farmer {
	f := &Farmer{bot: bot, logger: logger, timings: DefaultTimings()}
	if src, ok := bot.(BlockStateSource); ok {
		f.states = src
	}
	return f
}

// SetTimings overrides the operation timings. Production leaves them at
// DefaultTimings.
func (f *Farmer) SetTimings(t Timings) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timings = t
}

// SetBlockStateSource wires the block-state seam that crop age is read from.
//
// The wiring it wants is one method on *bot.Bot —
// GetBlockState(x, y, z) (name string, props map[string]any, ok bool) — backed
// by chunk.RuntimeIDToState(rid) in internal/bot/world, which already returns
// the property map that WorldCache.BlockName throws away today.
//
// With no source wired, StageAt reports every stage as unknown and the harvest
// refuses every staged crop. Pass a nil source to go back to that.
func (f *Farmer) SetBlockStateSource(src BlockStateSource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = src
}

func (f *Farmer) stateSource() BlockStateSource {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states
}

// BlockStateWired reports whether a block-state source is available, and so
// whether staged crops can be aged at all. The action layer reports it so a
// "harvest did nothing" answer can say why.
func (f *Farmer) BlockStateWired() bool { return f.stateSource() != nil }

func (f *Farmer) currentTimings() Timings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.timings
}

func (f *Farmer) setFarmingState(active bool) {
	f.mu.Lock()
	f.active = active
	f.mu.Unlock()
}

// IsFarming reports whether the farmer is mid-operation.
func (f *Farmer) IsFarming() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

// LastHarvests returns the per-cell results of the most recent HarvestCrops or
// BoneMealCrops call, so a caller can say which cells were taken and which were
// refused, and why.
func (f *Farmer) LastHarvests() []HarvestResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]HarvestResult(nil), f.results...)
}

func (f *Farmer) record(results []HarvestResult) {
	f.mu.Lock()
	f.results = results
	f.mu.Unlock()
}

// Stop stops a farming operation.
func (f *Farmer) Stop() {
	f.setFarmingState(false)
	f.bot.StopMovement()
}

// --- Reading crop age ---------------------------------------------------

// StageAt returns the growth stage of the cell and whether it could be read.
//
// The three refusals are all deliberate and all mean "no reading": no state
// source wired, the cell not in the world cache, and a block that either is not
// a crop or is a crop with no age to read. A false is never silently turned
// into a zero, because a zero is "just planted" and a mature default would be
// the opposite mistake.
func (f *Farmer) StageAt(x, y, z int32) (int, bool) {
	src := f.stateSource()
	if src == nil {
		return 0, false
	}
	name, props, ok := src.GetBlockState(x, y, z)
	if !ok {
		return 0, false
	}
	crop := CropOf(name)
	if crop == CropUnknown {
		return 0, false
	}
	rule, known := RuleOf(crop)
	if !known || !rule.Staged {
		return 0, false
	}
	return ReadStage(props)
}

// --- Harvest ------------------------------------------------------------

// HarvestCrops harvests ripe crops of a type, replants what it took, and
// accelerates the new planting when bone meal is to hand.
//
// The return value counts harvests that the world confirmed: the cell was
// readable before the break and no longer holds the crop after it. A break that
// was sent and not honoured is not counted, and a replant that was planned but
// not observed is not claimed.
//
// An empty cropType means every crop this package grows. A cropType it does not
// recognise harvests nothing at all, rather than falling back to "anything
// that looks like a crop".
func (f *Farmer) HarvestCrops(ctx context.Context, cropType string, maxCount int) Harvest {
	f.setFarmingState(true)
	defer f.setFarmingState(false)

	filter := CropsFor(cropType)
	results := make([]HarvestResult, 0, 8)
	confirmed := 0

	if len(filter) == 0 {
		f.logger.Warn("harvest: unknown crop type", "crop", cropType)
		f.record(results)
		f.bot.ReportActionStatus("", event.ActionStatus{
			Action: "harvest", Item: cropType, Count: 0, Success: false,
			Error: "tidak dikenal tanaman " + cropType,
		})
		return Harvest{Count: 0, Results: results}
	}

	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	for dx := -scanRadius; dx <= scanRadius && confirmed < maxCount; dx++ {
		for dy := scanBelow; dy <= scanAbove && confirmed < maxCount; dy++ {
			for dz := -scanRadius; dz <= scanRadius && confirmed < maxCount; dz++ {
				if ctx.Err() != nil {
					f.report("harvest", cropType, confirmed)
					return Harvest{Count: confirmed, Results: results}
				}
				x, y, z := bx+dx, by+dy, bz+dz
				name, known := f.bot.GetBlockName(x, y, z)
				if !known {
					continue
				}
				crop := CropOf(name)
				if crop == CropUnknown || !cropIn(filter, crop) {
					continue
				}
				result := f.harvestCell(ctx, x, y, z, crop)
				results = append(results, result)
				if result.Confirmed() {
					confirmed++
				}
			}
		}
	}

	f.record(results)
	f.report("harvest", cropType, confirmed)
	return Harvest{Count: confirmed, Results: results}
}

// harvestCell runs the whole cycle on one cell: read the age, refuse if it is
// not ripe or is not safe, break it, confirm the world changed, replant, and
// feed the new planting.
//
// The order is the point. Nothing is clicked before the age is read, nothing is
// counted before the cell is re-read, and nothing is claimed about a replant or
// a meal before it has been observed in the world.
func (f *Farmer) harvestCell(ctx context.Context, x, y, z int32, crop Crop) HarvestResult {
	result := HarvestResult{Crop: crop, CellReadable: true, SupportIntact: true}

	rule, known := RuleOf(crop)
	if !known {
		return result
	}

	// Kelp out of water is not a harvest. Checked before the age because kelp
	// has no age, so the age check below would wave it straight through.
	if rule.NeedsWater && !f.inWater(x, y, z) {
		f.logger.Debug("harvest: crop is not in water", "crop", crop, "pos", [3]int32{x, y, z})
		return result
	}

	// A stack is taken from the top. Breaking a lower block of a bamboo column
	// drops less and leaves a stump the bot then walks past.
	if rule.Stackable {
		above, ok := f.bot.GetBlockName(x, y+1, z)
		if ok && CropOf(above) == crop {
			return result
		}
	}

	// A support-bearing crop is only taken while its support is really there.
	// Breaking the pod is not enough: if the log is gone, the pod would have
	// dropped on its own and the harvest is really a cleanup.
	if rule.GrowsFromSupport {
		support, ok := f.bot.GetBlockName(x, y-1, z)
		if !ok || !IsCocoaSupport(support) {
			f.logger.Debug("harvest: crop has no support block", "crop", crop, "pos", [3]int32{x, y, z})
			return result
		}
	}

	stage, stageKnown := f.StageAt(x, y, z)
	if refusal := HarvestRefusal(crop, stage, stageKnown); refusal != "" {
		f.logger.Debug("harvest: refused", "crop", crop, "pos", [3]int32{x, y, z}, "reason", refusal)
		return result
	}

	before := NewInventory(f.bot.GetInventorySlots(), f.bot.GetItemNames())

	if !f.approach(x, y, z) {
		return result
	}
	f.breakBlock(protocol.BlockPos{x, y, z})

	result.CellCleared = f.waitForCellCleared(crop, x, y, z)
	if !result.CellCleared {
		f.logger.Warn("harvest: cell still holds the crop after the break", "crop", crop, "pos", [3]int32{x, y, z})
		return result
	}

	after := NewInventory(f.bot.GetInventorySlots(), f.bot.GetItemNames())
	result.DropsGained = after.Gained(before, func(name string) bool { return produceMatches(crop, name) })

	// A harvest that took the pod but dropped the log took the crop with it.
	// The pod is gone either way, so the support is re-read and reported apart.
	if rule.GrowsFromSupport {
		support, ok := f.bot.GetBlockName(x, y-1, z)
		result.SupportIntact = ok && IsCocoaSupport(support)
		if !result.SupportIntact {
			f.logger.Warn("harvest: crop support did not survive", "crop", crop, "pos", [3]int32{x, y, z})
		}
	}

	f.replantCell(ctx, crop, rule, x, y, z, &result)
	f.boneMealCell(ctx, crop, x, y, z, &result)

	return result
}

// approach walks into reach, turns to look at the cell, and settles the aim.
func (f *Farmer) approach(x, y, z int32) bool {
	if !f.bot.NavigateToBlock(x, y, z, harvestReach) {
		return false
	}
	f.bot.StopMovement()
	f.bot.LookAt(blockCenter(x, y, z))
	sleep(f.currentTimings().Aim)
	return true
}

// waitForCellCleared polls the world until the cell no longer holds the crop.
//
// The poll is what makes this honest. The old code wrote a break transaction
// and counted the harvest; a server that ignored it produced a count and no
// crop. Expiry is a failure — an unconfirmed break is never rounded up.
func (f *Farmer) waitForCellCleared(crop Crop, x, y, z int32) bool {
	t := f.currentTimings()
	sleep(t.Settle)

	deadline := time.Now().Add(t.Confirm)
	for {
		name, known := f.bot.GetBlockName(x, y, z)
		if CellCleared(crop, name, known) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		sleep(t.Poll)
	}
}

// inWater reports whether any cell adjacent to (x, y, z) is water.
func (f *Farmer) inWater(x, y, z int32) bool {
	for _, off := range waterNeighbours {
		name, ok := f.bot.GetBlockName(x+off[0], y+off[1], z+off[2])
		if ok && IsWater(name) {
			return true
		}
	}
	return false
}

// --- Replant ------------------------------------------------------------

// replantCell puts the seed back into the cell a harvest came out of.
//
// The ground is the crop's own. PlanReplant refuses anything else, so a
// harvest on sand does not put a wheat seed on sand and call it a replant.
func (f *Farmer) replantCell(ctx context.Context, crop Crop, rule CropRule, x, y, z int32, result *HarvestResult) {
	groundY := y
	if rule.GroundBelow {
		groundY = y - 1
	}
	groundName, groundKnown := f.bot.GetBlockName(x, groundY, z)
	inv := NewInventory(f.bot.GetInventorySlots(), f.bot.GetItemNames())

	plan := PlanReplant(crop, groundName, groundKnown, inv.hasItem(rule.Seeds...))
	result.ReplantReason = plan.Reason
	if !plan.Do {
		f.logger.Debug("harvest: no replant", "crop", crop, "pos", [3]int32{x, y, z}, "reason", plan.Reason)
		return
	}

	slot, found := f.findItemSlot(inv, rule.Seeds)
	if !found {
		// The inventory moved between the plan and the lookup — a stack merged
		// away, or a pickup landed. Not a replant.
		result.ReplantReason = "seed slot vanished between plan and use"
		return
	}
	if err := f.bot.EquipItem(slot); err != nil {
		result.ReplantReason = "could not equip " + plan.Seed + ": " + err.Error()
		f.logger.Warn("harvest: could not equip seed", "crop", crop, "err", err)
		return
	}

	if !f.approach(x, groundY, z) {
		result.ReplantReason = "could not walk to the planting cell"
		return
	}
	f.useItem(x, groundY, z, slot)
	// The seed lands on the top face of the ground, which is the cell the crop
	// was harvested from.
	result.Replanted = f.waitForPlanted(crop, x, y, z)
	if !result.Replanted {
		result.ReplantReason = "seed was used but no crop appeared"
		f.logger.Warn("harvest: replant not observed", "crop", crop, "pos", [3]int32{x, y, z})
	}
}

// waitForPlanted polls the cell until the crop is back in it.
func (f *Farmer) waitForPlanted(crop Crop, x, y, z int32) bool {
	t := f.currentTimings()
	sleep(t.Settle)

	deadline := time.Now().Add(t.Confirm)
	for {
		if name, ok := f.bot.GetBlockName(x, y, z); ok && CropOf(name) == crop {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		sleep(t.Poll)
	}
}

// --- Bone meal ----------------------------------------------------------

// boneMealCell accelerates the crop now in the cell, if it is a crop that
// responds to bone meal, is not already ripe, and the stage is actually known.
//
// A meal is only reported as applied once the stage has been seen to advance.
// Spending one and watching nothing happen is the case the old code could not
// express, and reporting it as a success is the bug this replaces.
func (f *Farmer) boneMealCell(ctx context.Context, crop Crop, x, y, z int32, result *HarvestResult) {
	for applied := 0; applied < boneMealBudget; applied++ {
		if ctx.Err() != nil {
			return
		}
		name, ok := f.bot.GetBlockName(x, y, z)
		if !ok || CropOf(name) != crop {
			return
		}
		inv := NewInventory(f.bot.GetInventorySlots(), f.bot.GetItemNames())
		before, beforeKnown := f.StageAt(x, y, z)
		plan := PlanBoneMeal(crop, before, beforeKnown, hasBoneMeal(inv))
		result.BoneMealReason = plan.Reason
		if !plan.Do {
			return
		}
		slot, found := f.findItemSlot(inv, []string{boneMealName})
		if !found {
			// The inventory moved between the plan and the lookup.
			result.BoneMealReason = "bone meal slot vanished between plan and use"
			return
		}
		if err := f.bot.EquipItem(slot); err != nil {
			result.BoneMealReason = "could not equip bone meal: " + err.Error()
			f.logger.Warn("harvest: could not equip bone meal", "err", err)
			return
		}
		if !f.approach(x, y, z) {
			result.BoneMealReason = "could not walk to the crop"
			return
		}
		f.useItem(x, y, z, slot)

		if !f.waitForStageAdvance(crop, before, beforeKnown, x, y, z) {
			f.logger.Warn("harvest: bone meal spent but stage did not advance", "crop", crop, "pos", [3]int32{x, y, z})
			result.BoneMealReason = "bone meal was used but the stage did not advance"
			return
		}
		result.BoneMealApplied = true
	}
}

// waitForStageAdvance polls until the crop's stage is seen to move forward
// from the reading taken before the meal was used.
func (f *Farmer) waitForStageAdvance(crop Crop, before int, beforeKnown bool, x, y, z int32) bool {
	t := f.currentTimings()
	sleep(t.Settle)

	deadline := time.Now().Add(t.Confirm)
	for {
		after, afterKnown := f.StageAt(x, y, z)
		if StageAdvanced(before, after, beforeKnown, afterKnown) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		sleep(t.Poll)
	}
}

// BoneMealCrops walks a field and accelerates the crops that are planted but
// not yet ripe. It is the second half of 5.2, and the half that makes the
// wheat cycle close: harvest, replant, meal, wait, harvest again.
func (f *Farmer) BoneMealCrops(ctx context.Context, cropType string, maxCount int) Harvest {
	f.setFarmingState(true)
	defer f.setFarmingState(false)

	filter := CropsFor(cropType)
	results := make([]HarvestResult, 0, 8)
	fertilised := 0

	if len(filter) == 0 {
		f.logger.Warn("bone meal: unknown crop type", "crop", cropType)
		f.record(results)
		return Harvest{Count: 0, Results: results}
	}

	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	for dx := -scanRadius; dx <= scanRadius && fertilised < maxCount; dx++ {
		for dy := scanBelow; dy <= scanAbove && fertilised < maxCount; dy++ {
			for dz := -scanRadius; dz <= scanRadius && fertilised < maxCount; dz++ {
				if ctx.Err() != nil {
					f.record(results)
					return Harvest{Count: fertilised, Results: results}
				}
				x, y, z := bx+dx, by+dy, bz+dz
				name, known := f.bot.GetBlockName(x, y, z)
				if !known {
					continue
				}
				crop := CropOf(name)
				if crop == CropUnknown || !cropIn(filter, crop) {
					continue
				}
				stage, stageKnown := f.StageAt(x, y, z)
				inv := NewInventory(f.bot.GetInventorySlots(), f.bot.GetItemNames())
				if plan := PlanBoneMeal(crop, stage, stageKnown, hasBoneMeal(inv)); !plan.Do {
					continue
				}

				result := HarvestResult{Crop: crop, CellReadable: true, SupportIntact: true}
				f.boneMealCell(ctx, crop, x, y, z, &result)
				results = append(results, result)
				if result.BoneMealApplied {
					fertilised++
				}
			}
		}
	}

	f.record(results)
	if fertilised > 0 {
		f.bot.ReportActionStatus("", event.ActionStatus{
			Action: "bone_meal", Item: cropType, Count: fertilised, Success: true,
		})
	}
	return Harvest{Count: fertilised, Results: results}
}

// --- Planting -----------------------------------------------------------

// PlantSeeds sows seeds on bare farmland near the bot and counts only the
// plantings the world confirmed.
//
// The old version returned true as soon as it had written the transaction, so
// a full inventory or a server that refused the click still counted.
func (f *Farmer) PlantSeeds(ctx context.Context, cropType string, maxCount int) int {
	f.setFarmingState(true)
	defer f.setFarmingState(false)

	report := f.PlantSeedsDetailed(ctx, cropType, maxCount)
	return report.Count
}

// Plant is the per-cell result of a sowing attempt.
type Plant struct {
	Crop    Crop
	Planted bool
	// GroundReadable is false when the farmland cell was never in the cache.
	GroundReadable bool
	// Reason explains a refusal.
	Reason string
}

// PlantSeedsDetailed is PlantSeeds with the per-cell detail kept.
func (f *Farmer) PlantSeedsDetailed(ctx context.Context, cropType string, maxCount int) Planting {
	filter := CropsFor(cropType)
	plants := make([]Plant, 0, 8)
	planted := 0

	if len(filter) == 0 {
		f.logger.Warn("plant: unknown crop type", "crop", cropType)
		f.bot.ReportActionStatus("", event.ActionStatus{
			Action: "plant", Item: cropType, Count: 0, Success: false,
			Error: "tidak dikenal tanaman " + cropType,
		})
		return Planting{Count: 0, Plants: plants}
	}

	// One crop is sown per call. A field of mixed crops has no single seed, and
	// planting the first filter entry's seed on every bare cell would put
	// carrots on the wheat half of a rotation.
	crop := filter[0]
	rule, ok := RuleOf(crop)
	if !ok || len(rule.Seeds) == 0 {
		f.logger.Warn("plant: crop cannot be sown", "crop", crop)
		return Planting{Count: 0, Plants: plants}
	}

	inv := NewInventory(f.bot.GetInventorySlots(), f.bot.GetItemNames())
	slot, hasSeed := f.findItemSlot(inv, rule.Seeds)
	if !hasSeed {
		f.bot.ReportActionStatus("", event.ActionStatus{
			Action: "plant", Item: string(crop), Count: 0, Success: false,
			Error: "gak punya benih " + string(crop),
		})
		return Planting{Count: 0, Plants: plants}
	}
	if err := f.bot.EquipItem(slot); err != nil {
		return Planting{Count: 0, Plants: plants}
	}

	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	for dx := -plantRadius; dx <= plantRadius && planted < maxCount; dx++ {
		for dz := -plantRadius; dz <= plantRadius && planted < maxCount; dz++ {
			if ctx.Err() != nil {
				return Planting{Count: planted, Plants: plants}
			}
			x, y, z := bx+dx, by-1, bz+dz
			plant := f.plantAtFarmland(ctx, crop, x, y, z, slot)
			plants = append(plants, plant)
			if plant.Planted {
				planted++
			}
		}
	}

	if planted > 0 {
		f.bot.ReportActionStatus("", event.ActionStatus{
			Action: "plant", Item: string(crop), Count: planted, Success: true,
		})
	}
	return Planting{Count: planted, Plants: plants}
}

// plantAtFarmland sows one seed on one cell of farmland and confirms it.
func (f *Farmer) plantAtFarmland(ctx context.Context, crop Crop, x, y, z int32, seedSlot uint32) Plant {
	plant := Plant{Crop: crop, GroundReadable: true}

	groundName, known := f.bot.GetBlockName(x, y, z)
	plant.GroundReadable = known
	inv := NewInventory(f.bot.GetInventorySlots(), f.bot.GetItemNames())
	plan := PlanReplant(crop, groundName, known, inv.hasItem(ruleSeeds(crop)...))
	plant.Reason = plan.Reason
	if !plan.Do {
		return plant
	}

	// Something already on top of the farmland: a crop, a snow layer, a
	// pumpkin. Planting into it wastes the seed.
	if above, ok := f.bot.GetBlockName(x, y+1, z); ok && !isAir(above) {
		plant.Reason = "cell above the farmland is occupied by " + above
		return plant
	}
	if ctx.Err() != nil {
		plant.Reason = "cancelled"
		return plant
	}

	if !f.approach(x, y+1, z) {
		plant.Reason = "could not walk to the farmland"
		return plant
	}
	f.useItem(x, y, z, seedSlot)
	plant.Planted = f.waitForPlanted(crop, x, y+1, z)
	if !plant.Planted {
		plant.Reason = "seed was used but no crop appeared"
	}
	return plant
}

// ruleSeeds returns the seed list for a crop, or nothing.
func ruleSeeds(crop Crop) []string {
	rule, ok := RuleOf(crop)
	if !ok {
		return nil
	}
	return rule.Seeds
}

// --- Hoeing -------------------------------------------------------------

// HoeGround tills dirt and grass blocks into farmland.
func (f *Farmer) HoeGround(ctx context.Context, radius int32) int {
	if radius < hoeRadiusFloor {
		radius = hoeRadiusFloor
	}

	hoeSlot, found := f.findItemSlot(NewInventory(f.bot.GetInventorySlots(), f.bot.GetItemNames()), hoeTypes)
	if !found {
		f.bot.ReportActionStatus("", event.ActionStatus{
			Action: "hoe", Item: "hoe", Count: 0, Success: false, Error: "gak punya cangkul",
		})
		return 0
	}
	if err := f.bot.EquipItem(hoeSlot); err != nil {
		return 0
	}

	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	hoed := 0
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			if ctx.Err() != nil {
				f.report("hoe", "hoe", hoed)
				return hoed
			}
			if f.hoeAtBlock(ctx, bx+dx, by-1, bz+dz, hoeSlot) {
				hoed++
			}
		}
	}

	if hoed > 0 {
		f.bot.ReportActionStatus("", event.ActionStatus{
			Action: "hoe", Item: "farmland", Count: hoed, Success: true,
		})
	}
	return hoed
}

// hoeTypes are the hoe item names, most valuable first, so the bot keeps the
// good one in hand when the inventory holds several.
var hoeTypes = []string{
	"netherite_hoe", "diamond_hoe", "iron_hoe", "stone_hoe", "golden_hoe", "wooden_hoe",
}

// hoeAtBlock uses the hoe on one block and waits for it to become farmland.
func (f *Farmer) hoeAtBlock(ctx context.Context, x, y, z int32, hoeSlot uint32) bool {
	name, ok := f.bot.GetBlockName(x, y, z)
	if !ok || !isTillable(name) {
		return false
	}
	if above, ok := f.bot.GetBlockName(x, y+1, z); ok && !isAir(above) {
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	if !f.approach(x, y+1, z) {
		return false
	}
	f.useItem(x, y, z, hoeSlot)
	return f.waitForFarmland(x, y, z)
}

// waitForFarmland polls until the tilled cell is farmland.
func (f *Farmer) waitForFarmland(x, y, z int32) bool {
	t := f.currentTimings()
	sleep(t.Settle)

	deadline := time.Now().Add(t.Confirm)
	for {
		if name, ok := f.bot.GetBlockName(x, y, z); ok && isFarmland(name) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		sleep(t.Poll)
	}
}

// --- Packet helpers -----------------------------------------------------

// useItem sends a UseItem-on-block transaction for the held stack.
func (f *Farmer) useItem(x, y, z int32, slot uint32) {
	inv := f.bot.GetInventorySlots()
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{x, y, z},
			BlockFace:       topFace,
			HotBarSlot:      safecast.To[int32](slot),
			HeldItem:        protocol.ItemInstance{Stack: inv[slot]},
			Position:        f.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	_ = f.bot.WritePacket(tx)
}

// breakBlock sends a UseItem break transaction for the cell.
//
// HeldItem carries the real stack rather than an empty instance: the server
// validates the held item against the break, and an empty instance is not what
// the bot is actually holding. A rejected break then shows up as a cell that
// never clears, which waitForCellCleared reports honestly.
func (f *Farmer) breakBlock(pos protocol.BlockPos) {
	inv := f.bot.GetInventorySlots()
	held := f.bot.GetHeldItemSlot()
	f.bot.LookAt(blockCenter(pos.X(), pos.Y(), pos.Z()))
	sleep(50 * time.Millisecond)

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:    protocol.UseItemActionBreakBlock,
			BlockPosition: pos,
			BlockFace:     topFace,
			HotBarSlot:    safecast.To[int32](held),
			HeldItem:      protocol.ItemInstance{Stack: inv[held]},
			Position:      f.bot.GetCoords(),
			// On the top face, matching useItem a few lines above for the same
			// face. The block centre is inside the block and on none of its
			// faces.
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	_ = f.bot.WritePacket(tx)
}

// findItemSlot locates a slot holding any of names, preferring an exact name
// match before falling back to a substring one.
//
// The exact-first order matters for wheat: "wheat_seeds" is what plants the
// crop, and a substring-only search would also accept a renamed stack whose
// name merely contains "wheat".
func (f *Farmer) findItemSlot(inv Inventory, names []string) (uint32, bool) {
	slots := f.bot.GetInventorySlots()
	lookup := f.bot.GetItemNames()

	var substringHit uint32
	foundSubstring := false
	for slot, stack := range slots {
		if stack.Count <= 0 {
			continue
		}
		name := Normalise(lookup[stack.NetworkID])
		if name == "" {
			continue
		}
		for _, want := range names {
			if name == Normalise(want) {
				return slot, true
			}
			if !foundSubstring && strings.Contains(name, Normalise(want)) {
				substringHit, foundSubstring = slot, true
			}
		}
	}
	return substringHit, foundSubstring
}

// --- Small shared helpers ----------------------------------------------

// isAir reports whether a block name is empty space.
func isAir(name string) bool {
	switch Normalise(name) {
	case "air", "cave_air", "void_air", "structure_void", "":
		return true
	default:
		return false
	}
}

// isTillable reports whether a hoe turns a block into farmland.
func isTillable(name string) bool {
	switch Normalise(name) {
	case "dirt", "coarse_dirt", "rooted_dirt", "dirt_with_roots", "grass_block", "podzol", "mycelium", "mud", "muddy_mangrove_roots":
		return true
	default:
		return false
	}
}

func blockCenter(x, y, z int32) mgl32.Vec3 {
	return mgl32.Vec3{float32(x) + 0.5, float32(y) + 0.5, float32(z) + 0.5}
}

// sleep waits for d, tolerating a non-positive wait so a compressed test
// timing of zero really is zero rather than an instant tick of work.
func sleep(d time.Duration) {
	if d <= 0 {
		return
	}
	time.Sleep(d)
}
