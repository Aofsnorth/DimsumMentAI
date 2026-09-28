package bot

import (
	"strings"
	"time"

	"bedrock-ai/internal/bot/movement/animation"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// obstacleBreakFace is the face an unstick break claims. The bot is already
// wedged against the cell, so the top face is the one it can be looking at.
const obstacleBreakFace int32 = 1

// obstacleBreakDuration is how long an unstick break waits. The bot is already
// stuck, so a deliberate margin over the vanilla time is free: the block going
// a beat later is invisible, a rejected destroy leaves the bot pushing the same
// wall forever.
const (
	obstacleBreakDuration = 1800 * time.Millisecond
	obstacleHardBreak     = 2600 * time.Millisecond
)

// hardToBreak reports whether a block needs the longer unstick break.
func hardToBreak(lowerName string) bool {
	for _, keyword := range []string{"stone", "ore", "cobble", "deepslate", "obsidian"} {
		if strings.Contains(lowerName, keyword) {
			return true
		}
	}
	return false
}

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
		// Routed through b.WritePacket so the break actions reach
		// server-auth-block-breaking hosts in PlayerAuthInput form.
		_ = b.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: runtimeID,
			ActionType:      protocol.PlayerActionStartBreak,
			BlockPosition:   pos,
			BlockFace:       obstacleBreakFace,
		})

		breakTime := obstacleBreakDuration
		if hardToBreak(lower) {
			breakTime = obstacleHardBreak
		}
		// The shared rhythm, for the same reason the chopper and the miner use
		// it: a fixed 300 ms metronome restarts the viewer's arm-swing cycle
		// before it finishes, so the arm reads as vibrating rather than
		// swinging, and the dig sound it drives comes out as a rattle.
		aim := obstacleAim(pos)
		for i, beat := range animation.Beats(breakTime, aim) {
			time.Sleep(beat.Wait)
			if i == 0 {
				// The first beat is the wind-up; the arm is still being raised.
				continue
			}
			_ = b.WritePacket(animation.MineSwing(runtimeID))
			b.LookAt(beat.Aim)
		}

		_ = b.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: runtimeID,
			ActionType:      protocol.PlayerActionCrackBreak,
			BlockPosition:   pos,
			BlockFace:       obstacleBreakFace,
		})
		_ = b.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: runtimeID,
			ActionType:      protocol.PlayerActionPredictDestroyBlock,
			BlockPosition:   pos,
			BlockFace:       obstacleBreakFace,
		})
		_ = b.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: runtimeID,
			ActionType:      protocol.PlayerActionStopBreak,
			BlockPosition:   pos,
			BlockFace:       obstacleBreakFace,
		})

		b.WorldModel.SetSolid(pos.X(), pos.Y(), pos.Z(), false)
		b.Logger.Info("broke obstacle to unstick path", "pos", pos, "name", name)
	}()
}
