package gathering

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

type BlockMiner struct {
	rg     *ResourceGatherer
	logger *slog.Logger
}

func NewBlockMiner(rg *ResourceGatherer, logger *slog.Logger) *BlockMiner {
	return &BlockMiner{
		rg:     rg,
		logger: logger,
	}
}

func (bm *BlockMiner) GatherBlock(ctx context.Context, blockName string, targetCount int) {
	bot := bm.rg.bot
	if targetCount <= 0 {
		targetCount = 1
	}

	resolvedName := bm.resolveFuzzyName(blockName)
	bm.logger.Debug("Starting block gathering", "name", blockName, "resolved", resolvedName, "target", targetCount)

	startCount := bm.inventoryCount(resolvedName)
	currentCount := startCount
	minedBlocks := 0
	failedAttempts := 0
	dugPositions := make(map[string]bool)

	for currentCount-startCount < targetCount && failedAttempts < 8 {
		select {
		case <-ctx.Done():
			return
		default:
		}

		step, minedName, foundCandidate := bm.findBestMineStep(resolvedName, dugPositions)
		if !foundCandidate {
			bm.logger.Warn("No matching block found nearby", "name", resolvedName)
			break
		}

		botPos := bot.GetCoords()
		dist := bm.distance(botPos, step.Aim)
		if dist > 4.0 {
			reached := bot.NavigateToBlock(step.Position.X(), step.Position.Y(), step.Position.Z(), 3.0)
			if !reached {
				dugPositions[mineKey(step.Position)] = true
				failedAttempts++
				continue
			}
			bot.StopMovement()
		}

		beforeCount := bm.inventoryCount(resolvedName)
		if !bm.breakBlock(ctx, step, minedName) {
			dugPositions[mineKey(step.Position)] = true
			failedAttempts++
			continue
		}

		dugPositions[mineKey(step.Position)] = true
		minedBlocks++
		failedAttempts = 0

		// Program-based timing: brief best-effort sweep that exits the instant
		// inventory count rises. No waiting for server pickup confirmation.
		bm.rg.looter.CollectMatchingDropsUntil(ctx, 6.0, resolvedName, beforeCount, 900*time.Millisecond)
		currentCount = bm.inventoryCount(resolvedName)
		if currentCount <= beforeCount && step.CountsTowardTarget {
			failedAttempts++
			bm.logger.Debug("inventory did not rise after sweep, moving on", "name", resolvedName, "pos", step.Position)
		}
	}

	collected := currentCount - startCount
	if collected < 0 {
		collected = 0
	}
	bm.logger.Info("block gathering complete", "requested", resolvedName, "mined_blocks", minedBlocks, "collected", collected)
	bot.ReportActionStatus("", event.ActionStatus{
		Action:  "mine",
		Item:    resolvedName,
		Count:   collected,
		Success: true,
	})
}

func (bm *BlockMiner) findBestMineStep(resolvedName string, dugPositions map[string]bool) (mineStep, string, bool) {
	bot := bm.rg.bot
	botPos := bot.GetCoords()
	world := bot.GetLocalWorldModel()
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	var bestStep mineStep
	bestBlockName := ""
	bestScore := float32(math.MaxFloat32)
	foundCandidate := false

	for dx := int32(-12); dx <= 12; dx++ {
		for dy := int32(-3); dy <= 5; dy++ {
			for dz := int32(-12); dz <= 12; dz++ {
				tx, ty, tz := bx+dx, by+dy, bz+dz
				target := protocol.BlockPos{tx, ty, tz}
				if dugPositions[mineKey(target)] {
					continue
				}
				if tx == bx && tz == bz && (ty == by || ty == by-1) {
					continue
				}
				if !world.IsSolid(tx, ty, tz) {
					continue
				}
				name, ok := bot.GetBlockName(tx, ty, tz)
				if !ok || !blockNameMatches(name, resolvedName) {
					continue
				}

				step, ok := planMineStep(world, botPos, target)
				if !ok || dugPositions[mineKey(step.Position)] {
					continue
				}
				stepName := name
				if step.Position != target {
					var stepNameOK bool
					stepName, stepNameOK = bot.GetBlockName(step.Position.X(), step.Position.Y(), step.Position.Z())
					if !stepNameOK || strings.EqualFold(stepName, "minecraft:bedrock") {
						continue
					}
				}

				dist := bm.distance(botPos, mgl32.Vec3{float32(tx) + 0.5, float32(ty) + 0.5, float32(tz) + 0.5})
				score := dist
				if !step.CountsTowardTarget {
					score += 18
				}
				if score < bestScore {
					bestScore = score
					bestStep = step
					bestBlockName = stepName
					foundCandidate = true
				}
			}
		}
	}

	return bestStep, bestBlockName, foundCandidate
}

func (bm *BlockMiner) breakBlock(ctx context.Context, step mineStep, blockName string) bool {
	// Clear any solid block between bot's eye and the target aim point first.
	// The server rejects break requests that aren't in line-of-sight, so digging
	// "through" something silently fails. This also mimics how a human player
	// would naturally clear the path.
	for depth := 0; depth < 5; depth++ {
		obs, obsName, ok := bm.findReachObstruction(step.Position, step.Aim)
		if !ok {
			break
		}
		bm.logger.Info("clearing obstruction before target", "obstruction_pos", obs, "name", obsName, "target_pos", step.Position)
		obsStep, planned := planMineStep(bm.rg.bot.GetLocalWorldModel(), bm.rg.bot.GetCoords(), obs)
		if !planned || obsStep.Position != obs {
			obsStep = mineStep{
				Position:           obs,
				Face:               0,
				Aim:                mgl32.Vec3{float32(obs.X()) + 0.5, float32(obs.Y()) + 0.5, float32(obs.Z()) + 0.5},
				CountsTowardTarget: false,
			}
		}
		if !bm.mineSingle(ctx, obsStep, obsName) {
			break
		}
	}

	return bm.mineSingle(ctx, step, blockName)
}

// mineSingle performs one break (no recursion, no obstruction check). Used
// internally by breakBlock and by the obstruction-clearing loop.
func (bm *BlockMiner) mineSingle(ctx context.Context, step mineStep, blockName string) bool {
	bot := bm.rg.bot
	bm.equipBestTool(blockName)

	bot.LookAt(step.Aim)
	if !sleepContext(ctx, 30*time.Millisecond) {
		return false
	}

	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})

	breakTime := blockBreakDuration(blockName, bm.equippedToolName())

	elapsed := time.Duration(0)
	swingInterval := 150 * time.Millisecond
	for elapsed < breakTime {
		_ = bot.WritePacket(&packet.Animate{
			ActionType:      packet.AnimateActionSwingArm,
			EntityRuntimeID: bot.GetEntityRuntimeID(),
		})
		bot.LookAt(step.Aim)

		wait := swingInterval
		if elapsed+wait > breakTime {
			wait = breakTime - elapsed
		}
		if !sleepContext(ctx, wait) {
			return false
		}
		elapsed += wait
	}

	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionCrackBreak,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionPredictDestroyBlock,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})

	changed := bm.waitForBlockChanged(ctx, step.Position, blockName, 400*time.Millisecond)
	// Program-based: optimistically clear the block in our world model so the
	// next pathfind doesn't try to step through it. If the server actually
	// kept it, the world cache update from server will resolidify on the
	// next chunk diff.
	bot.GetLocalWorldModel().SetSolid(step.Position.X(), step.Position.Y(), step.Position.Z(), false)
	if !changed {
		bm.logger.Debug("server did not confirm block break (assuming success)", "name", blockName, "pos", step.Position)
	}
	return true
}

// findReachObstruction walks a ray from the bot's eye position toward aim and
// returns the first solid block (other than the target itself) it hits. Empty
// blocks, the target block, and blocks outside reach (~5 blocks) are skipped.
func (bm *BlockMiner) findReachObstruction(target protocol.BlockPos, aim mgl32.Vec3) (protocol.BlockPos, string, bool) {
	bot := bm.rg.bot
	pos := bot.GetCoords()
	eye := mgl32.Vec3{pos.X(), pos.Y() + 1.62, pos.Z()}

	dx := aim.X() - eye.X()
	dy := aim.Y() - eye.Y()
	dz := aim.Z() - eye.Z()
	dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
	if dist <= 0.001 {
		return protocol.BlockPos{}, "", false
	}

	// Sample every 0.2 blocks along the ray, up to reach. Track which block
	// cells we've already inspected so we don't probe the same cell twice.
	const reach float32 = 5.5
	const stepSize float32 = 0.2
	rayLen := reach
	if dist < reach {
		rayLen = dist
	}
	maxSteps := int(math.Floor(float64(rayLen / stepSize)))
	seen := make(map[[3]int32]bool, maxSteps)
	world := bot.GetLocalWorldModel()
	for i := 1; i <= maxSteps; i++ {
		t := float32(i) * stepSize / dist
		if t > 1 {
			t = 1
		}
		x := eye.X() + dx*t
		y := eye.Y() + dy*t
		z := eye.Z() + dz*t
		bx := int32(math.Floor(float64(x)))
		by := int32(math.Floor(float64(y)))
		bz := int32(math.Floor(float64(z)))
		key := [3]int32{bx, by, bz}
		if seen[key] {
			continue
		}
		seen[key] = true
		if bx == target.X() && by == target.Y() && bz == target.Z() {
			continue
		}
		if !world.IsSolid(bx, by, bz) {
			continue
		}
		name, _ := bot.GetBlockName(bx, by, bz)
		if strings.EqualFold(name, "minecraft:bedrock") {
			return protocol.BlockPos{}, "", false
		}
		return protocol.BlockPos{bx, by, bz}, name, true
	}
	return protocol.BlockPos{}, "", false
}

func (bm *BlockMiner) waitForBlockChanged(ctx context.Context, pos protocol.BlockPos, oldName string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		world := bm.rg.bot.GetLocalWorldModel()
		name, ok := bm.rg.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
		if !world.IsSolid(pos.X(), pos.Y(), pos.Z()) || !ok || !strings.EqualFold(name, oldName) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		if !sleepContext(ctx, 50*time.Millisecond) {
			return false
		}
	}
}

func (bm *BlockMiner) inventoryCount(resolvedName string) int {
	return inventoryCountMatching(bm.rg.bot.GetInventorySlots(), bm.rg.bot.GetItemNames(), resolvedName)
}

func (bm *BlockMiner) waitForInventoryCount(ctx context.Context, resolvedName string, previousCount int, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	latest := bm.inventoryCount(resolvedName)
	for latest <= previousCount && time.Now().Before(deadline) {
		if !sleepContext(ctx, 100*time.Millisecond) {
			return latest
		}
		latest = bm.inventoryCount(resolvedName)
	}
	return latest
}

func mineKey(pos protocol.BlockPos) string {
	return fmt.Sprintf("%d,%d,%d", pos.X(), pos.Y(), pos.Z())
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
