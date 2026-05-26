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
	botPos := bot.GetCoords()
	world := bot.GetLocalWorldModel()

	resolvedName := bm.resolveFuzzyName(blockName)
	bm.logger.Info("Starting block gathering", "name", blockName, "resolved", resolvedName, "target", targetCount)

	mined := 0
	failedAttempts := 0
	dugPositions := make(map[string]bool)

	for mined < targetCount && failedAttempts < 5 {
		select {
		case <-ctx.Done():
			return
		default:
		}

		botPos = bot.GetCoords()
		bx := int32(math.Floor(float64(botPos.X())))
		by := int32(math.Floor(float64(botPos.Y())))
		bz := int32(math.Floor(float64(botPos.Z())))

		var bestBlock protocol.BlockPos
		bestScore := float32(math.MaxFloat32)
		foundCandidate := false

		for dx := int32(-12); dx <= 12; dx++ {
			for dy := int32(-3); dy <= 5; dy++ {
				for dz := int32(-12); dz <= 12; dz++ {
					tx, ty, tz := bx+dx, by+dy, bz+dz
					key := fmt.Sprintf("%d,%d,%d", tx, ty, tz)
					if dugPositions[key] {
						continue
					}

					if tx == bx && tz == bz && ty == by-1 {
						continue
					}
					if tx == bx && tz == bz && ty == by {
						continue
					}

					if !world.IsSolid(tx, ty, tz) {
						continue
					}

					dist := bm.distance(botPos, mgl32.Vec3{float32(tx), float32(ty), float32(tz)})
					isExposed := !world.IsSolid(tx, ty+1, tz)
					exposureBonus := float32(0.0)
					if !isExposed {
						exposureBonus = 100.0
					}

					score := dist + exposureBonus

					if score < bestScore {
						bestScore = score
						bestBlock = protocol.BlockPos{tx, ty, tz}
						foundCandidate = true
					}
				}
			}
		}

		if !foundCandidate {
			bestBlock = protocol.BlockPos{bx + 2, by - 1, bz}
			tcName := fmt.Sprintf("%d,%d,%d", bestBlock.X(), bestBlock.Y(), bestBlock.Z())
			if dugPositions[tcName] {
				break
			}
			foundCandidate = true
		}

		dist := bm.distance(botPos, mgl32.Vec3{float32(bestBlock.X()), float32(bestBlock.Y()), float32(bestBlock.Z())})
		if dist > 4.0 {
			reached := bot.NavigateToBlock(bestBlock.X(), bestBlock.Y(), bestBlock.Z(), 3.0)
			if !reached {
				dugPositions[fmt.Sprintf("%d,%d,%d", bestBlock.X(), bestBlock.Y(), bestBlock.Z())] = true
				failedAttempts++
				continue
			}
			bot.StopMovement()
		}

		bm.equipBestTool(resolvedName)

		targetCenter := mgl32.Vec3{float32(bestBlock.X()) + 0.5, float32(bestBlock.Y()) + 0.5, float32(bestBlock.Z()) + 0.5}
		bot.LookAt(targetCenter)
		time.Sleep(100 * time.Millisecond)

		_ = bot.WritePacket(&packet.Animate{
			ActionType:      packet.AnimateActionSwingArm,
			EntityRuntimeID: bot.GetEntityRuntimeID(),
		})

		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStartBreak,
			BlockPosition:   bestBlock,
			BlockFace:       1,
		})

		breakTime := 800 * time.Millisecond
		if strings.Contains(resolvedName, "stone") || strings.Contains(resolvedName, "ore") {
			breakTime = 1500 * time.Millisecond
		}
		time.Sleep(breakTime)

		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionCrackBreak,
			BlockPosition:   bestBlock,
			BlockFace:       1,
		})
		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionPredictDestroyBlock,
			BlockPosition:   bestBlock,
			BlockFace:       1,
		})

		world.SetSolid(bestBlock.X(), bestBlock.Y(), bestBlock.Z(), false)
		dugPositions[fmt.Sprintf("%d,%d,%d", bestBlock.X(), bestBlock.Y(), bestBlock.Z())] = true
		mined++
		failedAttempts = 0

		time.Sleep(150 * time.Millisecond)
	}

	bm.rg.looter.CollectAllDrops(ctx, 6.0)
	bot.SendChat(fmt.Sprintf("Selesai ngumpulin %s! Aku dapet %d block.", blockName, mined))
}
