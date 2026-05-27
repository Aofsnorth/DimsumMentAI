package gathering

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

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

		bm.rg.looter.CollectMatchingDrops(ctx, 6.0, resolvedName)
		currentCount = bm.waitForInventoryCount(ctx, resolvedName, beforeCount, 2*time.Second)
		if currentCount <= beforeCount && step.CountsTowardTarget {
			failedAttempts++
			bm.logger.Warn("mined target block but inventory did not increase", "name", resolvedName, "pos", step.Position)
		}
	}

	collected := currentCount - startCount
	if collected < 0 {
		collected = 0
	}
	bm.logger.Info("block gathering complete", "requested", resolvedName, "mined_blocks", minedBlocks, "collected", collected)
	bot.SendChat(fmt.Sprintf("Selesai ngumpulin %s! Aku dapet %d block.", blockName, collected))
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
	bot := bm.rg.bot
	bm.equipBestTool(blockName)

	bot.LookAt(step.Aim)
	if !sleepContext(ctx, 120*time.Millisecond) {
		return false
	}

	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   step.Position,
		BlockFace:       step.Face,
	})

	breakTime := 850 * time.Millisecond
	lowerName := strings.ToLower(blockName)
	if strings.Contains(lowerName, "stone") || strings.Contains(lowerName, "ore") {
		breakTime = 1500 * time.Millisecond
	}

	elapsed := time.Duration(0)
	swingInterval := 200 * time.Millisecond
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

	changed := bm.waitForBlockChanged(ctx, step.Position, blockName, 1600*time.Millisecond)
	if changed {
		bot.GetLocalWorldModel().SetSolid(step.Position.X(), step.Position.Y(), step.Position.Z(), false)
	} else {
		bm.logger.Warn("server did not confirm block break", "name", blockName, "pos", step.Position)
	}
	return changed
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
