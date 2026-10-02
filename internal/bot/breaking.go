package bot

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Server-authoritative block breaking.
//
// Servers that set PlayerMovementSettings.ServerAuthoritativeBlockBreaking in
// the Start packet (Geyser and vanilla 1.20.10+ hosts both do) require block
// breaking to travel inside PlayerAuthInput.BlockActions. A vanilla
// client-hosted LAN world closes the connection ~4ms after receiving a
// standalone PlayerAction StartBreak — no disconnect reason, the session is
// simply torn down — while the PlayerAuthInput form is accepted and the block
// breaks. The reverse is not true, so the routing is decided by the negotiated
// flag and never by a guess: legacy servers keep receiving the packets they
// expect, unchanged.
//
// The mapping mirrors what a real client sends (verified against Geyser's
// BlockBreakHandler and a live probe against the LAN host):
//
//	StartBreak          → BlockActions [StartBreak] + per-tick ContinueDestroy
//	CrackBreak          → no-op (progress is implicit; the server tracks time)
//	PredictDestroy      → BlockActions [PredictDestroy], mining state cleared
//	StopBreak           → BlockActions [PredictDestroy] if not already sent
//	AbortBreak          → BlockActions [AbortBreak], mining state cleared

// ServerAuthBlockBreaking reports whether the current server negotiated
// server-authoritative block breaking. It is captured from the Start packet in
// initSpawn and re-captured on every reconnect.
func (b *Bot) ServerAuthBlockBreaking() bool {
	return b.serverAuthBlockBreaking
}

// RouteBreakAction converts a legacy break PlayerAction into the
// PlayerAuthInput block-action form. It reports whether the packet was
// consumed; when false the caller must write the packet to the connection
// as-is.
func (b *Bot) RouteBreakAction(pk *packet.PlayerAction) bool {
	switch pk.ActionType {
	case protocol.PlayerActionStartBreak:
		b.BeginServerAuthBreak(pk.BlockPosition, pk.BlockFace)
		return true
	case protocol.PlayerActionCrackBreak, protocol.PlayerActionContinueDestroyBlock:
		// Progress is carried by the per-tick ContinueDestroy actions that
		// TakeBlockTickActions attaches while the mining state is active.
		return true
	case protocol.PlayerActionPredictDestroyBlock, protocol.PlayerActionCreativePlayerDestroyBlock:
		b.FinishServerAuthBreak(pk.BlockPosition, pk.BlockFace)
		return true
	case protocol.PlayerActionStopBreak:
		// Legacy call sites end their sequence with StopBreak. Under
		// server-auth breaking the equivalent finish is PredictDestroy; if one
		// already passed through this is a no-op.
		b.FinishServerAuthBreak(pk.BlockPosition, pk.BlockFace)
		return true
	case protocol.PlayerActionAbortBreak:
		b.AbortServerAuthBreak(pk.BlockPosition, pk.BlockFace)
		return true
	default:
		return false
	}
}

// BeginServerAuthBreak starts breaking the block at pos: it queues a StartBreak
// block action for the next PlayerAuthInput and marks the bot as mining, which
// makes the movement loop attach ContinueDestroy on every subsequent tick.
func (b *Bot) BeginServerAuthBreak(pos protocol.BlockPos, face int32) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.miningActive = true
	b.finishing = false
	b.miningPos = pos
	b.miningFace = face
	b.pendingBlockActions = append(b.pendingBlockActions, protocol.PlayerBlockAction{
		Action:   protocol.PlayerActionStartBreak,
		BlockPos: pos,
		Face:     face,
	})
}

// FinishServerAuthBreak finishes the break in progress by queueing
// PredictDestroy. A real client pairs it with ContinueDestroy on the same tick,
// which TakeBlockTickActions reproduces while the mining state is still set —
// so the state is only cleared once the composed actions are drained.
func (b *Bot) FinishServerAuthBreak(pos protocol.BlockPos, face int32) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if !b.miningActive || b.finishing {
		// A PredictDestroy already ended this break (miner and scaffold send
		// PredictDestroy and then StopBreak; the second finish is a no-op).
		return
	}
	b.finishing = true
	b.pendingBlockActions = append(b.pendingBlockActions, protocol.PlayerBlockAction{
		Action:   protocol.PlayerActionPredictDestroyBlock,
		BlockPos: pos,
		Face:     face,
	})
}

// AbortServerAuthBreak cancels the break in progress.
func (b *Bot) AbortServerAuthBreak(pos protocol.BlockPos, face int32) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.miningActive = false
	b.finishing = false
	b.pendingBlockActions = append(b.pendingBlockActions, protocol.PlayerBlockAction{
		Action:   protocol.PlayerActionAbortBreak,
		BlockPos: pos,
		Face:     face,
	})
}

// TakeBlockTickActions composes the block actions for the next
// PlayerAuthInput: the one-shot actions queued since the last tick plus the
// per-tick ContinueDestroy a real client sends while it keeps mining.
// ContinueDestroy is skipped on the tick that carries StartBreak, matching the
// vanilla client's first breaking tick, and on the tick that carries
// PredictDestroy the two are paired — exactly the shape Geyser documents.
func (b *Bot) TakeBlockTickActions() []protocol.PlayerBlockAction {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	var actions []protocol.PlayerBlockAction
	if len(b.pendingBlockActions) > 0 {
		actions = append(actions, b.pendingBlockActions...)
		b.pendingBlockActions = nil
	}

	started := false
	finishIdx := -1
	for i, a := range actions {
		switch a.Action {
		case protocol.PlayerActionStartBreak:
			started = true
		case protocol.PlayerActionPredictDestroyBlock, protocol.PlayerActionAbortBreak:
			if finishIdx < 0 {
				finishIdx = i
			}
		}
	}
	if b.miningActive && !started {
		cont := protocol.PlayerBlockAction{
			Action:   protocol.PlayerActionContinueDestroyBlock,
			BlockPos: b.miningPos,
			Face:     b.miningFace,
		}
		if finishIdx >= 0 {
			// Final tick: the vanilla client pairs ContinueDestroy with the
			// finish action and sends it first — Geyser processes them in that
			// order, so keep it.
			actions = append(actions[:finishIdx], append([]protocol.PlayerBlockAction{cont}, actions[finishIdx:]...)...)
		} else {
			actions = append(actions, cont)
		}
	}
	if finishIdx >= 0 {
		b.miningActive = false
		b.finishing = false
	}
	return actions
}
