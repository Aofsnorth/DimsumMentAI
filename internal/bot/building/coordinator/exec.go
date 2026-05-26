package coordinator

import (
	"context"
	"fmt"
	"math"
	"time"

	"bedrock-ai/internal/bot/building/common"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// executeBlockList runs through the sorted block entries, placing each.
func (ba *BuilderAgent) executeBlockList(ctx context.Context, blocks []common.BlockEntry, cx, cz int) bool {
	ba.logger.Info("Starting execution of block list layout", "total", len(blocks))

	var failedList []common.BlockEntry

	for idx, entry := range blocks {
		select {
		case <-ctx.Done():
			ba.logger.Warn("Block placement loop cancelled by context")
			return false
		default:
		}

		ba.mu.Lock()
		ba.blocksPlaced = idx + 1
		ba.status = fmt.Sprintf("Building (%d/%d)", ba.blocksPlaced, ba.totalBlocks)
		ba.mu.Unlock()

		ok := ba.placer.PlaceBlockAt(ctx, entry.X, entry.Y, entry.Z, entry.Block, cx, cz, entry.Metadata)
		if ok {
			ba.mu.Lock()
			ba.placedHistory = append(ba.placedHistory, entry)
			ba.mu.Unlock()
		} else {
			ba.logger.Warn("Failed to place block, queued for retry pass", "block", entry.Block, "x", entry.X, "y", entry.Y, "z", entry.Z)
			failedList = append(failedList, entry)
		}

		if (idx+1)%15 == 0 {
			ba.bot.SendSafeChat(fmt.Sprintf("Progress pembangunan: %d%% (%d/%d)", int(float32(idx+1)/float32(len(blocks))*100), idx+1, len(blocks)))
		}
	}

	if len(failedList) > 0 {
		ba.logger.Info("Starting retry pass for failed blocks", "count", len(failedList))
		for _, entry := range failedList {
			select {
			case <-ctx.Done():
				return false
			default:
			}
			ok := ba.placer.PlaceBlockAt(ctx, entry.X, entry.Y, entry.Z, entry.Block, cx, cz, entry.Metadata)
			if ok {
				ba.mu.Lock()
				ba.placedHistory = append(ba.placedHistory, entry)
				ba.mu.Unlock()
			}
		}
	}

	return true
}

// UndoBuild removes the last N blocks placed by the bot.
func (ba *BuilderAgent) UndoBuild(ctx context.Context, count int) {
	ba.mu.Lock()
	if ba.isBuilding {
		ba.mu.Unlock()
		ba.bot.SendSafeChat("Aku lagi sibuk membangun. Hentikan dulu pake 'stopbuild' sebelum undo!")
		return
	}

	if len(ba.placedHistory) == 0 {
		ba.mu.Unlock()
		ba.bot.SendSafeChat("Belum ada blok yang aku tempatkan untuk dibatalkan.")
		return
	}

	if count <= 0 || count > len(ba.placedHistory) {
		count = len(ba.placedHistory)
	}

	ba.isBuilding = true
	ba.status = "Undoing..."
	ba.mu.Unlock()

	go func() {
		defer func() {
			ba.mu.Lock()
			ba.isBuilding = false
			ba.status = "Ready"
			ba.mu.Unlock()
		}()

		ba.bot.SendSafeChat(fmt.Sprintf("Membatalkan %d blok terakhir...", count))
		ba.executeUndoLoop(ctx, count)
	}()
}

func (ba *BuilderAgent) executeUndoLoop(ctx context.Context, count int) {
	for i := 0; i < count; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ba.mu.Lock()
		idx := len(ba.placedHistory) - 1
		entry := ba.placedHistory[idx]
		ba.placedHistory = ba.placedHistory[:idx]
		ba.mu.Unlock()

		botPos := ba.bot.GetCoords()
		dx := float32(entry.X) + 0.5 - botPos.X()
		dy := float32(entry.Y) + 0.5 - botPos.Y()
		dz := float32(entry.Z) + 0.5 - botPos.Z()
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

		if dist > 4.5 {
			ba.bot.NavigateToBlock(int32(entry.X), int32(entry.Y), int32(entry.Z), 3.0)
			time.Sleep(300 * time.Millisecond)
		}

		pos := protocol.BlockPos{int32(entry.X), int32(entry.Y), int32(entry.Z)}
		ba.bot.LookAt(mgl32.Vec3{float32(entry.X) + 0.5, float32(entry.Y) + 0.5, float32(entry.Z) + 0.5})
		time.Sleep(100 * time.Millisecond)

		_ = ba.bot.WritePacket(&packet.Animate{
			ActionType:      packet.AnimateActionSwingArm,
			EntityRuntimeID: ba.bot.GetEntityRuntimeID(),
		})
		_ = ba.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: ba.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStartBreak,
			BlockPosition:   pos,
			BlockFace:       1,
		})
		time.Sleep(300 * time.Millisecond)
		_ = ba.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: ba.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionCrackBreak,
			BlockPosition:   pos,
			BlockFace:       1,
		})
		_ = ba.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: ba.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionPredictDestroyBlock,
			BlockPosition:   pos,
			BlockFace:       1,
		})

		ba.bot.GetLocalWorldModel().SetSolid(int32(entry.X), int32(entry.Y), int32(entry.Z), false)
		time.Sleep(150 * time.Millisecond)
	}
	ba.bot.SendSafeChat("Undo selesai!")
}
