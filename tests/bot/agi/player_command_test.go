package agi_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/bot/world"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/go-gl/mathgl/mgl32"
)

// A player who says "come here" is not making a suggestion, and the natural
// loop is not allowed to renegotiate it once a second. These pin the rule that
// the body, while it is committed, belongs to whatever already claimed it.

// newMotorRunner builds a runner whose bot is real enough for the motor
// decisions: it has a logger and a world cache, so the free-play branch can be
// entered, and no explorer, so "wander" is a no-op and cannot be mistaken for a
// movement it started.
func newMotorRunner(t *testing.T) *agi.Runner {
	t.Helper()
	b := &bot.Bot{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		WorldCache: world.NewWorldCache(0, cube.Range{-64, 319}, slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
	return agi.New(b, agi.Config{Enabled: true, Mode: "natural", Wander: true})
}

// normalBlocks is a block list that reads as ordinary. An empty one reads as a
// single-block world twice in a row, which sends free play down the one-block
// branch instead of the resting one.
const normalBlocks = "stone, dirt, copper_ore, oak_log"

// TestACommittedBodyIsTheOnlyBusySignalThereIs. The whole rule reduces to this
// predicate, so it is worth pinning on its own: the two ways a body can already
// be in use are a walk in progress and an exploration drift, and neither of them
// is negotiable.
func TestACommittedBodyIsTheOnlyBusySignalThereIs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		snap agi.Snapshot
		want bool
	}{
		{"an idle bot is free", agi.Snapshot{}, false},
		{"a bot walking to a player is committed", agi.Snapshot{Busy: true}, true},
		{"a bot out exploring is committed", agi.Snapshot{Exploring: true}, true},
	}
	for _, tc := range cases {
		if got := agi.BodyCommitted(tc.snap); got != tc.want {
			t.Errorf("%s: bodyCommitted = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestFreePlayLeavesAWalkAlone is the core regression. The player asked the bot
// to come over; the bot is on its way; and the free-play branch opened a rest
// period, which calls StopMovement. The result on camera is a bot that walks a
// few blocks toward somebody, stops, and stands there — reading as a bot that
// changed its mind mid-stride.
func TestFreePlayLeavesAWalkAlone(t *testing.T) {
	t.Parallel()

	r := newMotorRunner(t)
	now := time.Now()
	// The bot has just moved, so resting is the honest next move and the guard
	// below is the only thing standing between it and a fresh rest period.
	r.SetLastMoved(now)

	walking := agi.Snapshot{Now: now, HP: 20, Coords: "X:10 Y:64 Z:0", NearBlocks: normalBlocks, Busy: true}
	r.FreePlay(walking, agi.Judgement{})
	if r.ShouldIdle(walking) {
		t.Error("free play opened a rest period while the bot was walking to a player")
	}

	// The same runner with a free body does rest, so the assertion above is
	// about the commitment and not about free play being broken.
	resting := agi.Snapshot{Now: now, HP: 20, Coords: "X:10 Y:64 Z:0", NearBlocks: normalBlocks}
	r.FreePlay(resting, agi.Judgement{})
	if !r.ShouldIdle(resting) {
		t.Fatal("a free bot no longer rests; the guard above would pass for the wrong reason")
	}
}

// TestAnExplorationDriftIsNotCancelledByIdle. The same argument one branch
// further on: an exploratory walk is a long commitment, and a rest period
// landing on top of it stops the bot in the middle of nowhere.
func TestAnExplorationDriftIsNotCancelledByIdle(t *testing.T) {
	t.Parallel()

	r := newMotorRunner(t)
	now := time.Now()
	snap := agi.Snapshot{Now: now, HP: 20, Coords: "X:10 Y:64 Z:0", NearBlocks: normalBlocks, Exploring: true}
	r.FreePlay(snap, agi.Judgement{})
	if r.ShouldIdle(snap) {
		t.Error("free play rested the bot in the middle of an exploration")
	}
}

// TestAFreeBotRestsAndThenGetsUp. The rest branch used to be unreachable: free
// play asked whether a rest period was already running, which is only ever true
// once something has opened one, and free play was the only thing that opened
// one. The result was a bot that acted or drifted on every single tick — the
// "never still" failure this whole mode exists to avoid, and the one the direct
// idle tests could not see because they called idle themselves.
func TestAFreeBotRestsAndThenGetsUp(t *testing.T) {
	t.Parallel()

	r := newMotorRunner(t)
	start := time.Now()
	world := agi.Snapshot{HP: 20, Coords: "X:10 Y:64 Z:0", NearBlocks: normalBlocks, Now: start}
	world.FreeSlots = 20

	// It has just arrived somewhere, so it stays.
	r.SetLastMoved(start)
	r.FreePlay(world, agi.Judgement{})
	if !r.ShouldIdle(world) {
		t.Fatal("a bot with nothing to do and no reason to move did not rest")
	}

	// Still there long after the rest window has closed, it gets up. A bot that
	// rested and then never moved again is not a companion, it is furniture.
	stale := world
	stale.Now = start.Add(2 * agi.DriftAfterStill)
	if !r.ShouldDrift(stale) {
		t.Fatal("the bot stood still indefinitely; a rest is not a retirement")
	}

	// And it does not snap upright the moment a rest period ends: the window
	// closing is not the same as having been still for too long. Even the
	// longest rest the bot can take has to end well before the drift threshold.
	justAfter := world
	justAfter.Now = start.Add(agi.MaxStillIdleSec * time.Second)
	if r.ShouldDrift(justAfter) {
		t.Error("the bot set off the instant its rest ended rather than when it had been still too long")
	}
}

// TestTheAttentionWindowIsNotTheOnlyThingHoldingTheBot. Ninety seconds of
// silence from the player is enough time for a long walk to still be running, so
// the human-attention window cannot be what protects the walk. This drives the
// real tick, with the attention already lapsed, which is where the interruption
// used to happen.
func TestTheAttentionWindowIsNotTheOnlyThingHoldingTheBot(t *testing.T) {
	t.Parallel()

	r := newMotorRunner(t)
	r.Suspend("Arthenyxx")

	late := agi.Snapshot{
		Now:        time.Now().Add(2 * agi.SuspendAfter),
		HP:         20,
		Coords:     "X:10 Y:64 Z:0",
		NearBlocks: normalBlocks,
		Busy:       true,
	}
	if r.AttendingToPlayer(late.Now) {
		t.Fatal("precondition: the attention should have lapsed by now")
	}

	r.NaturalTick(context.Background(), late, agi.Judgement{})
	if r.ShouldIdle(late) {
		t.Error("the bot rested in the middle of a walk, two attention windows after the player spoke")
	}
}

// TestPlayAlongDoesNotSteerAWalkThatIsAlreadyHappening. Aiming the head is
// harmless when the bot is standing still and harmful when it is walking a
// path, because the head turn is applied on top of the movement the path
// follower is still steering.
func TestPlayAlongDoesNotSteerAWalkThatIsAlreadyHappening(t *testing.T) {
	t.Parallel()

	seed := func(r *agi.Runner) {
		r.Bot().PlayerEntityIDs = map[string]uint64{"Arthenyxx": 7}
		r.Bot().PlayerPositions = map[uint64]mgl32.Vec3{7: {4, 64, 4}}
	}

	walking := newMotorRunner(t)
	seed(walking)
	walking.SetSuspendedBy("Arthenyxx")
	walking.PlayAlong(agi.Snapshot{Busy: true}, agi.Judgement{})
	if got := walking.Bot().LookTargetName; got != "" {
		t.Errorf("the bot turned its head while walking to that same player: %q", got)
	}

	standing := newMotorRunner(t)
	seed(standing)
	standing.SetSuspendedBy("Arthenyxx")
	standing.PlayAlong(agi.Snapshot{}, agi.Judgement{})
	if got := standing.Bot().LookTargetName; got != "Arthenyxx" {
		t.Errorf("a standing bot did not look at the player it was talking to: %q", got)
	}
}
