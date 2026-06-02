package bot

import (
	"strings"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// BreakObstacleAt asynchronously breaks the block at pos so the bot can
// continue along its current path. Used by the steering loop when the bot is
// detected as stuck against a wall.
//
// Safety: bedrock/barrier blocks are skipped to avoid infinite loops on
// unbreakable terrain. The world model is optimistically cleared on send.
func (b *Bot) BreakObstacleAt(pos protocol.BlockPos) {
	name, _ := b.GetBlockName(pos.X(), pos.Y(), pos.Z())
	lower := strings.ToLower(name)
	if strings.Contains(lower, "bedrock") || strings.Contains(lower, "barrier") || strings.Contains(lower, "command_block") {
		return
	}
	if !b.WorldModel.IsSolid(pos.X(), pos.Y(), pos.Z()) {
		return
	}

	go func() {
		runtimeID := b.Conn.GameData().EntityRuntimeID
		_ = b.Conn.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: runtimeID,
			ActionType:      protocol.PlayerActionStartBreak,
			BlockPosition:   pos,
			BlockFace:       1,
		})

		// Generous fixed wait covers most hand-breakable blocks. The bot is
		// already stuck so a few extra ms here is irrelevant.
		breakMs := 1200
		if strings.Contains(lower, "stone") || strings.Contains(lower, "ore") || strings.Contains(lower, "cobble") || strings.Contains(lower, "deepslate") {
			breakMs = 1800
		}
		swingInterval := 150 * time.Millisecond
		elapsed := time.Duration(0)
		total := time.Duration(breakMs) * time.Millisecond
		for elapsed < total {
			_ = b.Conn.WritePacket(&packet.Animate{
				ActionType:      packet.AnimateActionSwingArm,
				EntityRuntimeID: runtimeID,
			})
			wait := swingInterval
			if elapsed+wait > total {
				wait = total - elapsed
			}
			time.Sleep(wait)
			elapsed += wait
		}

		_ = b.Conn.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: runtimeID,
			ActionType:      protocol.PlayerActionCrackBreak,
			BlockPosition:   pos,
			BlockFace:       1,
		})
		_ = b.Conn.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: runtimeID,
			ActionType:      protocol.PlayerActionPredictDestroyBlock,
			BlockPosition:   pos,
			BlockFace:       1,
		})

		b.WorldModel.SetSolid(pos.X(), pos.Y(), pos.Z(), false)
		b.Logger.Info("broke obstacle to unstick path", "pos", pos, "name", name)
	}()
}
