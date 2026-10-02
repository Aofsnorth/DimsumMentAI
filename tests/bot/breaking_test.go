package bot_test

import (
	"testing"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// TestRouteBreakActionSequence pins the full server-auth breaking sequence the
// chopper produces today: StartBreak … StopBreak, translated into
// PlayerAuthInput block actions. A standalone PlayerAction StartBreak tears
// down vanilla 1.20.10+ hosts, so this mapping is what keeps the bot connected.
func TestRouteBreakActionSequence(t *testing.T) {
	b := &bot.Bot{}
	pos := protocol.BlockPos{10, 64, -20}

	if !b.RouteBreakAction(&packet.PlayerAction{ActionType: protocol.PlayerActionStartBreak, BlockPosition: pos, BlockFace: 1}) {
		t.Fatal("routeBreakAction(StartBreak) reported not handled")
	}
	actions := b.TakeBlockTickActions()
	if len(actions) != 1 || actions[0].Action != protocol.PlayerActionStartBreak || actions[0].BlockPos != pos {
		t.Fatalf("first tick actions = %+v, want a single StartBreak at %v", actions, pos)
	}

	// While mining, every tick attaches ContinueDestroy — except the tick that
	// carried StartBreak itself, matching the vanilla client's first tick.
	actions = b.TakeBlockTickActions()
	if len(actions) != 1 || actions[0].Action != protocol.PlayerActionContinueDestroyBlock {
		t.Fatalf("mid-mining actions = %+v, want a single ContinueDestroy", actions)
	}

	// The chopper ends with CrackBreak then StopBreak; both must collapse into
	// one PredictDestroy paired with the final ContinueDestroy.
	if !b.RouteBreakAction(&packet.PlayerAction{ActionType: protocol.PlayerActionCrackBreak, BlockPosition: pos, BlockFace: 1}) {
		t.Fatal("routeBreakAction(CrackBreak) reported not handled")
	}
	if !b.RouteBreakAction(&packet.PlayerAction{ActionType: protocol.PlayerActionStopBreak, BlockPosition: pos, BlockFace: 1}) {
		t.Fatal("routeBreakAction(StopBreak) reported not handled")
	}
	actions = b.TakeBlockTickActions()
	if len(actions) != 2 ||
		actions[0].Action != protocol.PlayerActionContinueDestroyBlock ||
		actions[1].Action != protocol.PlayerActionPredictDestroyBlock {
		t.Fatalf("final tick actions = %+v, want ContinueDestroy+PredictDestroy", actions)
	}

	// The break is over: no further actions, no stray ContinueDestroy.
	if actions := b.TakeBlockTickActions(); len(actions) != 0 {
		t.Fatalf("post-break actions = %+v, want none", actions)
	}
}

// TestRouteBreakActionPredictThenStopIsNoop covers the miner/scaffold shape:
// PredictDestroy followed by StopBreak must queue exactly one PredictDestroy.
func TestRouteBreakActionPredictThenStopIsNoop(t *testing.T) {
	b := &bot.Bot{}
	pos := protocol.BlockPos{1, 2, 3}

	b.RouteBreakAction(&packet.PlayerAction{ActionType: protocol.PlayerActionStartBreak, BlockPosition: pos, BlockFace: 1})
	b.RouteBreakAction(&packet.PlayerAction{ActionType: protocol.PlayerActionPredictDestroyBlock, BlockPosition: pos, BlockFace: 1})
	b.RouteBreakAction(&packet.PlayerAction{ActionType: protocol.PlayerActionStopBreak, BlockPosition: pos, BlockFace: 1})

	actions := b.TakeBlockTickActions()
	predicts := 0
	for _, a := range actions {
		if a.Action == protocol.PlayerActionPredictDestroyBlock {
			predicts++
		}
	}
	if predicts != 1 {
		t.Fatalf("queued PredictDestroy actions = %d (actions: %+v), want exactly 1", predicts, actions)
	}
	if actions := b.TakeBlockTickActions(); len(actions) != 0 {
		t.Fatalf("post-break actions = %+v, want none", actions)
	}
}

// TestRouteBreakActionNonBreakActionsPassThrough guards the routing boundary:
// non-break PlayerActions must not be swallowed.
func TestRouteBreakActionNonBreakActionsPassThrough(t *testing.T) {
	b := &bot.Bot{}
	cases := []int32{
		protocol.PlayerActionStartItemUseOn,
		protocol.PlayerActionStopItemUseOn,
		protocol.PlayerActionJump,
		protocol.PlayerActionStartSneak,
	}
	for _, actionType := range cases {
		if b.RouteBreakAction(&packet.PlayerAction{ActionType: actionType}) {
			t.Fatalf("routeBreakAction(action %d) reported handled, want pass-through", actionType)
		}
	}
	if actions := b.TakeBlockTickActions(); len(actions) != 0 {
		t.Fatalf("actions = %+v, want none queued", actions)
	}
}

// TestAbortServerAuthBreakStopsContinueDestroy ensures an abort ends the
// per-tick ContinueDestroy stream instead of leaving a phantom mining state.
func TestAbortServerAuthBreakStopsContinueDestroy(t *testing.T) {
	b := &bot.Bot{}
	pos := protocol.BlockPos{5, 65, 5}

	b.BeginServerAuthBreak(pos, 1)
	b.AbortServerAuthBreak(pos, 1)

	actions := b.TakeBlockTickActions()
	if len(actions) != 2 ||
		actions[0].Action != protocol.PlayerActionStartBreak ||
		actions[1].Action != protocol.PlayerActionAbortBreak {
		t.Fatalf("abort tick actions = %+v, want StartBreak+AbortBreak", actions)
	}
	if actions := b.TakeBlockTickActions(); len(actions) != 0 {
		t.Fatalf("post-abort actions = %+v, want none", actions)
	}
}
