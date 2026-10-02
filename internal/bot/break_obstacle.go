package bot

import (
	"log/slog"
	"strings"
	"time"

	"bedrock-ai/internal/bot/movement/animation"
	"bedrock-ai/internal/bot/protect"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// ObstacleBreakFace is the face an unstick break claims. The bot is already
// wedged against the cell, so the top face is the one it can be looking at.
const ObstacleBreakFace int32 = 1

// ObstacleBreakDuration is how long an unstick break waits. The bot is already
// stuck, so a deliberate margin over the vanilla time is free: the block going
// a beat later is invisible, a rejected destroy leaves the bot pushing the same
// wall forever.
const (
	ObstacleBreakDuration = 1800 * time.Millisecond
	obstacleHardBreak     = 2600 * time.Millisecond
)

// HardToBreak reports whether a block needs the longer unstick break.
func HardToBreak(lowerName string) bool {
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

	// The unstick break is the most dangerous break the bot performs, and the
	// reason the protection policy matters most here. It fires precisely when the
	// bot is wedged, which is exactly when a player is most likely to have built
	// something worth keeping, and it was previously a filter on the *terrain*
	// only: bedrock and barriers were spared and everything else, including the
	// diamond block someone built a wall out of, was not.
	//
	// A refusal is logged rather than silent, because "the bot did not move" and
	// "the bot decided not to break that" look identical from outside and only
	// one of them is a bug.
	if b.Protection != nil {
		if decision := b.Protection.Allowed(protect.Break, pos, lower); !decision.OK {
			b.Logger.Info("AGI: refusing to break a protected block",
				slog.String("reason", decision.Reason),
				slog.String("block", lower),
				slog.Any("pos", pos))
			return
		}
	}

	go func() {
		runtimeID := b.Conn.GameData().EntityRuntimeID
		// Routed through b.WritePacket so the break actions reach
		// server-auth-block-breaking hosts in PlayerAuthInput form.
		_ = b.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: runtimeID,
			ActionType:      protocol.PlayerActionStartBreak,
			BlockPosition:   pos,
			BlockFace:       ObstacleBreakFace,
		})

		breakTime := ObstacleBreakDuration
		if HardToBreak(lower) {
			breakTime = obstacleHardBreak
		}
		// The shared rhythm, for the same reason the chopper and the miner use
		// it: a fixed 300 ms metronome restarts the viewer's arm-swing cycle
		// before it finishes, so the arm reads as vibrating rather than
		// swinging, and the dig sound it drives comes out as a rattle.
		aim := ObstacleAim(pos)
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
			BlockFace:       ObstacleBreakFace,
		})
		_ = b.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: runtimeID,
			ActionType:      protocol.PlayerActionPredictDestroyBlock,
			BlockPosition:   pos,
			BlockFace:       ObstacleBreakFace,
		})
		_ = b.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: runtimeID,
			ActionType:      protocol.PlayerActionStopBreak,
			BlockPosition:   pos,
			BlockFace:       ObstacleBreakFace,
		})

		b.WorldModel.SetSolid(pos.X(), pos.Y(), pos.Z(), false)
		b.Logger.Info("broke obstacle to unstick path", "pos", pos, "name", name)
	}()
}
