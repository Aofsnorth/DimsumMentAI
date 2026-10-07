// The three body-level behaviours a duel and a blocked route both lean on:
// taking a hit (knockback), pushing through when the pathfinder gives up
// (force walk), and a look emote that actually turns the head (spin).
//
// The knockback contract here is the fair-play one: the server assigned the
// body motion, so the claim must slide by it and bleed it off like the
// vanilla client — never discard it, never amplify it.
package movement_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

func newCombatMotionBot() *bot.Bot {
	world := pathfinder.NewLocalWorldModel()
	world.SetSolid(0, 66, 0, true)
	return &bot.Bot{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:       "TestBot",
		WorldModel: world,
	}
}

func TestKnockbackSlidesTheClaimAndDecays(t *testing.T) {
	t.Parallel()
	b := newCombatMotionBot()
	b.KnockbackX = 0.4
	b.KnockbackZ = 0.2

	// A grounded hit uses ground friction (0.546/tick): the whole slide is
	// about one velocity-length over a handful of ticks — the jolt. The old
	// air-drag decay stretched the same hit into a multi-second glide, which
	// read as the bot walking in the direction of the punch.
	tc := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{0, 66, 0}, IsGrounded: true}
	if !tc.ApplyKnockbackForTest() {
		t.Fatal("carried knockback must be claimed on the tick it arrives")
	}
	if got := tc.CurrPos.X(); got < 0.39 || got > 0.41 {
		t.Fatalf("claimed X = %.3f, want the 0.4 the server assigned", got)
	}
	if got := tc.CurrPos.Z(); got < 0.19 || got > 0.21 {
		t.Fatalf("claimed Z = %.3f, want the 0.2 the server assigned", got)
	}

	// The remainder is carried, not dropped: 0.4 × 0.546 = 0.218 keeps a
	// short slide going, then stops — one jolt, not a stroll.
	b.Mu.Lock()
	carriedX, carriedZ := b.KnockbackX, b.KnockbackZ
	b.Mu.Unlock()
	if carriedX < 0.21 || carriedX > 0.22 {
		t.Fatalf("carried X after one tick = %.4f, want ~0.218 (ground friction)", carriedX)
	}
	if carriedZ < 0.105 || carriedZ > 0.115 {
		t.Fatalf("carried Z after one tick = %.4f, want ~0.109", carriedZ)
	}

	// The shove is not a movement decision: the body must not turn to face it
	// (a hit player keeps facing what they were facing), so no horizontal-move
	// claim may come out of the fold.
	if tc.HasHorizontalMove {
		t.Fatal("knockback claimed as intentional movement; that hands the head to the walk look and the body turns into the push")
	}

	// Airborne bodies keep the lighter drag: 0.4 × 0.91 = 0.364.
	b.KnockbackX = 0.4
	b.KnockbackZ = 0.2
	tcAir := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{0, 66, 0}}
	tcAir.ApplyKnockbackForTest()
	b.Mu.Lock()
	carriedX = b.KnockbackX
	b.Mu.Unlock()
	if carriedX < 0.36 || carriedX > 0.37 {
		t.Fatalf("airborne carried X = %.4f, want ~0.364 (air drag)", carriedX)
	}

	// Below the claim threshold the carried motion stops, and with nothing
	// left the tick reports it had no motion at all.
	b.KnockbackX = 0.004
	b.KnockbackZ = 0
	tc2 := &movement.TickContext{B: b, CurrPos: mgl32.Vec3{1, 66, 1}}
	if !tc2.ApplyKnockbackForTest() {
		t.Fatal("sub-threshold knockback still moves the body once before stopping")
	}
	if got := tc2.CurrPos.X(); got < 1.003 || got > 1.005 {
		t.Fatalf("final slide X = %.4f, want the last 0.004", got)
	}
	b.Mu.Lock()
	carriedX = b.KnockbackX
	b.Mu.Unlock()
	if carriedX != 0 {
		t.Fatalf("carried X = %.5f, want fully stopped below the threshold", carriedX)
	}
	if tc2.ApplyKnockbackForTest() {
		t.Fatal("a body with no carried knockback must report no motion")
	}
}

func TestForceWalkOwnsTheBodyUntilTheDeadline(t *testing.T) {
	t.Parallel()
	b := newCombatMotionBot()
	b.ForceWalk(0, 2)

	// The push also clears whatever route A* had produced: the whole point is
	// that the route refused to work.
	b.Mu.Lock()
	b.CurrentPath = []pathfinder.Node{{X: 1, Y: 66, Z: 0, LinkType: pathfinder.LinkWalk}}
	b.Mu.Unlock()

	tc := &movement.TickContext{B: b, MState: "idle"}
	if !tc.ApplyForceWalkForTest() {
		t.Fatal("an open ForceWalk window must own the body")
	}
	if tc.MState != "force_walk" {
		t.Fatalf("MState = %q, want force_walk — the look pipeline turns toward it only under that state", tc.MState)
	}
	if !tc.HasHorizontalMove || tc.Dist <= 0 {
		t.Fatal("force walk must claim horizontal motion, or the body never leaves the cell")
	}
	if tc.Dz != 1 {
		t.Fatalf("push Z = %.3f, want 1 (yaw 0 pushes along +Z)", tc.Dz)
	}
	b.Mu.Lock()
	pathCleared := b.CurrentPath == nil
	b.Mu.Unlock()
	if !pathCleared {
		t.Fatal("force walk must clear the refused route instead of negotiating with it")
	}

	// Past the deadline the raw push lets go and regular steering resumes.
	b.Mu.Lock()
	b.ForceWalkUntil = time.Now().Add(-time.Second)
	b.Mu.Unlock()
	tc2 := &movement.TickContext{B: b, MState: "idle"}
	if tc2.ApplyForceWalkForTest() {
		t.Fatal("an expired ForceWalk window must hand the body back to steering")
	}
}

func TestLookEmoteSpinOwnsTheHeadAcrossTicks(t *testing.T) {
	t.Parallel()
	b := newCombatMotionBot()
	b.TriggerEmoteFor("spin", 40)

	// The old bug: the spin wrote its angle at packet time, after the eased
	// look had already run, and the next tick's ease pulled the head straight
	// back — the player watched a shiver, not a spin. Owning the look
	// pipeline means each tick steps the angle and holds it.
	tc := &movement.TickContext{
		B: b, Gait: movement.NewGaitState(), IsGrounded: true,
		MState: "idle",
		Yaw:    0, Pitch: 0,
		TargetYaw: 10, TargetPitch: 5,
		SmoothedLookYaw: 10, SmoothedLookPitch: 5,
	}
	tc.SetSyncedGroundedForTest(true)
	if !tc.ApplyLookEmoteForTest() {
		t.Fatal("a look emote must own the head on an idle body")
	}
	if tc.Yaw < 17 || tc.Yaw > 19 {
		t.Fatalf("spin Yaw = %.1f, want ~18 for one step", tc.Yaw)
	}
	if tc.TargetYaw != tc.Yaw || tc.SmoothedLookYaw != tc.Yaw || tc.HeadYaw != tc.Yaw {
		t.Fatalf("spin step left the eased state behind: target %.1f, smoothed %.1f, head %.1f, body %.1f — "+
			"any mismatch is the ease pulling the head back next tick", tc.TargetYaw, tc.SmoothedLookYaw, tc.HeadYaw, tc.Yaw)
	}

	// Next tick, carrying the persisted angle the way the input loop does:
	// the spin steps FURTHER, never back toward the idle gaze.
	b.Mu.Lock()
	b.Yaw = tc.Yaw
	b.Mu.Unlock()
	tc2 := &movement.TickContext{
		B: b, Gait: movement.NewGaitState(), IsGrounded: true,
		MState: "idle",
		Yaw:    tc.Yaw, Pitch: tc.Pitch,
		TargetYaw: tc.Yaw, TargetPitch: tc.Pitch,
		SmoothedLookYaw: tc.Yaw, SmoothedLookPitch: tc.Pitch,
	}
	tc2.SetSyncedGroundedForTest(true)
	if !tc2.ApplyLookEmoteForTest() {
		t.Fatal("the emote window is still open; the second tick must keep stepping")
	}
	if tc2.Yaw < 35 || tc2.Yaw > 37 {
		t.Fatalf("second spin step Yaw = %.1f, want ~36 (continuing the rotation, not easing back)", tc2.Yaw)
	}

	// A body walking a route keeps its head for steering.
	b.TriggerEmoteFor("spin", 40)
	tc3 := &movement.TickContext{B: b, Gait: movement.NewGaitState(), MState: "follow"}
	if tc3.ApplyLookEmoteForTest() {
		t.Fatal("a follow-state body must keep its head; the walk needs it")
	}
}
