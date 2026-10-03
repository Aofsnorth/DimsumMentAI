package movement_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// The bot is placed at x=0.9, straddling the block edge between cell 0 and
// cell 1, which is what puts a neighbouring cell inside the ground check's
// +/-0.3 sweep. The floor the body stands on is (0,66,0). The wall that must
// never vote is a single block at (1,66,0): on the floor's own layer, under
// the feet's sweep but beside them -- never under, never in the climb path.
// A full jump rises 1.25 blocks, which puts the whole body a full block above
// that wall's top face, so no skin window can ever touch it.
//
// This one geometry produces both reported symptoms from a single defect:
//
//	"pas aku suruh dia lompat dia malah lompat di udara naik terus lagi"
//	-- the wall is read as ground at every height the body rises through, so
//	   each airborne tick looks grounded and buys another 0.42 impulse.
//
//	"lompatnya tingginya ngak sama sama player normal (lebih pendek)"
//	-- the same false grounding zeroes VelY mid-arc and truncates the jump.
const (
	testBotX  = 0.9
	testBotZ  = 0.5
	testFloor = 66
)

func newJumpTestBot() *bot.Bot {
	world := pathfinder.NewLocalWorldModel()
	world.SetSolid(0, testFloor, 0, true) // the floor the body really stands on
	world.SetSolid(1, testFloor, 0, true) // the wall it is only ever beside
	return &bot.Bot{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:       "TestBot",
		WorldModel: world,
	}
}

// groundedAt asks the tick's own question at a given feet height.
func groundedAt(t *testing.T, feetY float32) bool {
	t.Helper()
	tc := &movement.TickContext{
		B:       newJumpTestBot(),
		CurrPos: mgl32.Vec3{testBotX, feetY, testBotZ},
	}
	tc.UpdateGroundedState()
	return tc.IsGrounded
}

// A block beside the feet is not a floor. Reading it as one is what let the
// body keep buying jump impulses while airborne.
func TestGroundedIsNotTakenFromABlockBesideTheFeet(t *testing.T) {
	t.Parallel()

	// From half a block up, the sweep's cell-floor combos are (0..1, 66..67):
	// cell 66's top faces sit 0.2-0.6 below the feet, both well outside the
	// skin. The ONLY way these read grounded is a sweep that brushes any
	// solid at all, which is exactly the old bug.
	for _, feetY := range []float32{67.20, 67.42, 67.60, 68.05} {
		if groundedAt(t, feetY) {
			t.Fatalf("feet at y=%.2f were reported grounded; every solid in the "+
				"sweep is 0.2-0.6 below the feet, far outside any resting skin. "+
				"This false floor is the mid-air jump and the truncated arc", feetY)
		}
	}
}

// The anti-vacuous half: genuinely resting on the block underneath must still
// read as grounded, or the fix above is just "never grounded", which breaks
// every jump request and the whole scaffold climb.
func TestGroundedIsStillReportedWhenRestingOnTheBlockBelow(t *testing.T) {
	t.Parallel()

	for _, feetY := range []float32{67.0, 66.98, 67.02} {
		if !groundedAt(t, feetY) {
			t.Fatalf("feet at y=%.2f resting on the solid floor block (0,66,0) were "+
				"not reported grounded; the fix would make the bot permanently airborne",
				feetY)
		}
	}
}

// End to end through the physics: the arc must reach full vanilla height even
// with that wall alongside. Before the fix the wall clipped it partway up.
func TestJumpArcIsNotTruncatedByABlockBesideTheBody(t *testing.T) {
	t.Parallel()

	tc := &movement.TickContext{
		B:          newJumpTestBot(),
		CurrPos:    mgl32.Vec3{testBotX, 67.0, testBotZ},
		IsGrounded: true,
		ShouldJump: true,
	}

	apex := float32(0)
	for i := 0; i < 25; i++ {
		tc.ApplyVerticalVelocity()
		tc.CurrPos = mgl32.Vec3{testBotX, tc.NextY, testBotZ}
		if rise := tc.CurrPos.Y() - 67.0; rise > apex {
			apex = rise
		}
		tc.IsGrounded = false
		tc.UpdateGroundedState()
		tc.ShouldJump = false // a request is one tick, never a held key
	}

	// Vanilla jump reaches 1.25 blocks. The unfixed physics integrates to about
	// 1.32; either is a normal jump. A wall clipped arc lands nearer half a
	// block, which is what the player sees as "lebih pendek".
	if apex < 1.20 || apex > 1.40 {
		t.Fatalf("jump reached %.2f blocks, want 1.20-1.40 (vanilla 1.25). "+
			"Too low means an airborne tick read the wall beside the body as floor "+
			"and zeroed VelY mid-arc", apex)
	}
}

// One request is one hop: an airborne body must leave the request latched so
// landing can honour it, and must not buy an impulse in the meantime.
func TestRequestedJumpIsNotConsumedWhileAirborne(t *testing.T) {
	t.Parallel()

	b := newJumpTestBot()
	b.RequestJump()

	tc := &movement.TickContext{
		B:       b,
		CurrPos: mgl32.Vec3{testBotX, 67.42, testBotZ}, // already airborne, rising
		VelY:    0.42,
	}
	tc.SetSyncedGroundedForTest(false) // previous tick's physics: not on the floor

	tc.TakeRequestedJumpForTest()
	if tc.ShouldJump {
		t.Fatal("a jump request fired on a body already rising at 0.42 blocks/tick; " +
			"the second impulse is what turns one hop into climbing the air")
	}
	if !b.JumpRequested() {
		t.Fatal("the request must stay latched while airborne so landing can honour " +
			"it; consuming it here silently drops the hop the player asked for")
	}
}

// The reported "lompat di udara naik terus lagi". Steering re-asserts
// ShouldJump every tick while it still wants to climb (it sets the flag in
// four places), which is right on the ground and is a rocket in the air. The
// airborne tick must refuse the impulse no matter how often it is asked.
func TestHeldJumpIntentDoesNotClimbTheAir(t *testing.T) {
	t.Parallel()

	tc := &movement.TickContext{
		B:          newJumpTestBot(),
		CurrPos:    mgl32.Vec3{testBotX, 67.0, testBotZ},
		IsGrounded: true,
	}

	apex := float32(0)
	for i := 0; i < 25; i++ {
		tc.ShouldJump = true // steering asks again, every single tick
		tc.ApplyVerticalVelocity()
		tc.CurrPos = mgl32.Vec3{testBotX, tc.NextY, testBotZ}
		if rise := tc.CurrPos.Y() - 67.0; rise > apex {
			apex = rise
		}
		tc.IsGrounded = false
		tc.UpdateGroundedState()
	}

	if apex > 1.40 {
		t.Fatalf("body climbed %.2f blocks over 25 ticks of held jump intent; "+
			"that is the bot flying. An airborne tick must never re-apply the "+
			"jump impulse however many times steering asks for it", apex)
	}
}

// A descending body at high speed (e.g. -0.46 blocks/tick) crosses the floor
// into the block below (67.22 - 0.46 = 66.76). The landing physics must catch
// this swept collision and snap NextY to exactly 67.0, rather than penetrating
// into the block and relying on server rubberbanding, which is what the player
// saw as "turunnya suka lag/jatuhnya".
func TestFallingBodyLandsCleanlyOnFloorWithoutPenetration(t *testing.T) {
	t.Parallel()

	tc := &movement.TickContext{
		B:       newJumpTestBot(),
		CurrPos: mgl32.Vec3{testBotX, 67.22, testBotZ},
		VelY:    -0.46,
		NextY:   66.76,
	}

	tc.ApplyGroundLanding()

	if !tc.IsGrounded {
		t.Fatal("falling body crossing floor was not detected as landing")
	}
	if tc.NextY != 67.0 {
		t.Fatalf("landing position NextY = %.4f, want exactly 67.0000; floor penetration causes lag and rubberbanding", tc.NextY)
	}
	if tc.VelY != 0.0 {
		t.Fatalf("landing VelY = %.4f, want 0.0", tc.VelY)
	}
}
