package gathering

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/movement/animation"
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

// GatherBlock mines a block type and reports the outcome as an action the
// player can see.
func (bm *BlockMiner) GatherBlock(ctx context.Context, blockName string, targetCount int) {
	collected := bm.gatherBlocks(ctx, blockName, targetCount)
	bm.rg.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "mine",
		Item:    bm.resolveFuzzyName(blockName),
		Count:   collected,
		Success: true,
	})
}

// gatherBlocks does the mining and returns how much was collected, without
// reporting an action status.
//
// Internal detours use this — stocking scaffold blocks mid-chop, for example —
// because a "mine: dirt" status would show up in the conversation for work the
// player never asked for and derail the answer to what they did ask for.
func (bm *BlockMiner) gatherBlocks(ctx context.Context, blockName string, targetCount int) int {
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
			collected := currentCount - startCount
			if collected < 0 {
				collected = 0
			}
			return collected
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
	return collected
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
				step, stepName, ok := bm.evaluateMineCandidate(target, botPos, bx, by, bz, resolvedName, dugPositions, world)
				if !ok {
					continue
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

func (bm *BlockMiner) evaluateMineCandidate(target protocol.BlockPos, botPos mgl32.Vec3, bx, by, bz int32, resolvedName string, dugPositions map[string]bool, world entity.WorldModel) (mineStep, string, bool) {
	if dugPositions[mineKey(target)] {
		return mineStep{}, "", false
	}
	if target.X() == bx && target.Z() == bz && (target.Y() == by || target.Y() == by-1) {
		return mineStep{}, "", false
	}
	if !world.IsSolid(target.X(), target.Y(), target.Z()) {
		return mineStep{}, "", false
	}

	name, ok := bm.rg.bot.GetBlockName(target.X(), target.Y(), target.Z())
	if !ok || !blockNameMatches(name, resolvedName) {
		return mineStep{}, "", false
	}

	step, ok := planMineStep(botMineWorld{bot: bm.rg.bot, model: world}, botPos, target)
	if !ok || dugPositions[mineKey(step.Position)] {
		return mineStep{}, "", false
	}

	stepName := name
	if step.Position != target {
		var stepNameOK bool
		stepName, stepNameOK = bm.rg.bot.GetBlockName(step.Position.X(), step.Position.Y(), step.Position.Z())
		if !stepNameOK || strings.EqualFold(stepName, "minecraft:bedrock") {
			return mineStep{}, "", false
		}
	}

	return step, stepName, true
}

func (bm *BlockMiner) breakBlock(ctx context.Context, step mineStep, blockName string) bool {
	bot := bm.rg.bot
	visibleStep, visible := planMineStep(
		botMineWorld{bot: bot, model: bot.GetLocalWorldModel()},
		bot.GetCoords(),
		step.Position,
	)
	if !visible {
		return false
	}
	return bm.mineSingle(ctx, visibleStep, blockName)
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

	breakTime := sabdBreakDuration(serverAuthBreaking(bot), blockName, bm.equippedToolName())

	// Same human rhythm as the chopper (chopWindUp/chopCadence live in
	// chop_action.go): a fixed 150 ms tick restarts the viewer's arm-swing
	// animation before it finishes, which reads as machine twitching.
	if !sleepContext(ctx, chopWindUp()) {
		return false
	}
	elapsed := time.Duration(chopWindUpMin) // conservative: never overrun the break time
	for swing := 0; elapsed < breakTime; swing++ {
		_ = bot.WritePacket(animation.MineSwing(bot.GetEntityRuntimeID()))
		bot.LookAt(step.Aim)

		wait := chopCadence(swing)
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
	// Explicitly stop breaking so the server clears destroy-progress at this
	// position. Without this the server still thinks we're mid-break there and
	// a freshly placed block gets insta-broken.
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStopBreak,
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
