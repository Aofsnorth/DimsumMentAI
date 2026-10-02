package movement_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
)

// newGazeTestBot builds a bot walking a straight +Z route at feet height.
func newGazeTestBot(feet mgl32.Vec3, path []pathfinder.Node) *bot.Bot {
	model := pathfinder.NewLocalWorldModel()
	model.SetPathBounds(pathfinder.Node{X: 500, Y: 120, Z: 500}, pathfinder.Node{X: 520, Y: 120, Z: 520})
	return &bot.Bot{
		MovementState: "walk_to",
		WorldModel:    model,
		Pos:           feet,
		CurrentPath:   path,
		PathIndex:     0,
	}
}

// straightPath builds n nodes marching +Z from the given start block.
func straightPath(startX, startY, startZ, n int) []pathfinder.Node {
	path := make([]pathfinder.Node, 0, n)
	for i := 0; i < n; i++ {
		path = append(path, pathfinder.Node{X: int32(startX), Y: int32(startY), Z: int32(startZ + i)})
	}
	return path
}

// TestWalkingGazeIsStableAlongAStraightPath is the core of the natural-camera
// change. Aiming the head at the single next waypoint — one block away — meant
// the aim swung back and forth with the bot's sub-block position and jumped
// outright whenever PathIndex advanced. Neither may happen: along a straight
// corridor the head must hold one steady heading.
func TestWalkingGazeIsStableAlongAStraightPath(t *testing.T) {
	t.Parallel()

	b := newGazeTestBot(mgl32.Vec3{0.5, 64, 0.2}, straightPath(0, 64, 0, 40))
	tc := &movement.TickContext{B: b, HasPath: true, CurrPos: mgl32.Vec3{0.5, 64, 0.2}, TargetYaw: 0}

	firstYaw, _, ok := tc.WalkingGazeAngles()
	if !ok {
		t.Fatal("WalkingGazeAngles returned no aim for an active path")
	}
	if offset := math.Abs(float64(movement.AngleDifference(firstYaw, 0))); offset > 1 {
		t.Fatalf("straight +Z route aims at yaw %.2f, want ~0", firstYaw)
	}

	for tick := 1; tick <= 60; tick++ {
		// Walk 0.2 blocks per tick, advancing the waypoint index as a real run
		// would, so both sources of jitter are exercised.
		z := float32(0.2 + 0.2*float64(tick))
		b.PathIndex = int(z - 0.8)
		tc.CurrPos = mgl32.Vec3{0.5, 64, z}
		tc.TargetYaw = 0

		yaw, _, ok := tc.WalkingGazeAngles()
		if !ok {
			t.Fatalf("tick %d: no aim", tick)
		}
		if offset := math.Abs(float64(movement.AngleDifference(yaw, firstYaw))); offset > 1 {
			t.Fatalf("tick %d: aim moved %.2f deg along a straight path (yaw %.2f, first %.2f)",
				tick, offset, yaw, firstYaw)
		}
	}
}

// TestWalkingGazeLeadsIntoCorners is the second half of the change: the head has
// to arrive at the new heading before the body does, which is what a human head
// does on a corner and what a body-locked camera cannot do.
func TestWalkingGazeLeadsIntoCorners(t *testing.T) {
	t.Parallel()

	// Route runs +Z for three blocks, then turns to +X.
	path := []pathfinder.Node{
		{X: 0, Y: 64, Z: 0}, {X: 0, Y: 64, Z: 1}, {X: 0, Y: 64, Z: 2},
		{X: 0, Y: 64, Z: 3},
		{X: 1, Y: 64, Z: 3}, {X: 2, Y: 64, Z: 3}, {X: 3, Y: 64, Z: 3},
	}

	// Stand on the +Z leg, heading +Z (yaw 0), with the corner just ahead.
	tc := &movement.TickContext{
		B:         newGazeTestBot(mgl32.Vec3{0.5, 64, 0.5}, path),
		HasPath:   true,
		CurrPos:   mgl32.Vec3{0.5, 64, 0.5},
		TargetYaw: 0,
	}

	yaw, _, ok := tc.WalkingGazeAngles()
	if !ok {
		t.Fatal("no aim on a route with a corner")
	}

	lead := movement.AngleDifference(yaw, 0)
	// The corner turns toward +X, which is negative yaw in this convention, so
	// the head must lead that way rather than staying locked on +Z.
	if lead >= -5 {
		t.Fatalf("aim yaw %.2f did not lead the +Z body heading into the corner (lead %.2f)", yaw, lead)
	}
	if lead < -movement.WalkingGazeMaxLeadYaw {
		t.Fatalf("lead %.2f exceeds cap %.2f", lead, movement.WalkingGazeMaxLeadYaw)
	}
}

// TestWalkingGazeLeadStaysBounded guards a right-angle corner. The raw
// look-ahead aim there sits about 45 deg off the direction of travel, which would
// point the view at a wall the bot is not walking toward. The lead cap has to
// trim it back to something a human head would actually do.
func TestWalkingGazeLeadStaysBounded(t *testing.T) {
	t.Parallel()

	path := []pathfinder.Node{
		{X: 0, Y: 64, Z: 0}, {X: 0, Y: 64, Z: 1}, {X: 0, Y: 64, Z: 2},
		{X: 1, Y: 64, Z: 2}, {X: 2, Y: 64, Z: 2}, {X: 3, Y: 64, Z: 2},
	}
	tc := &movement.TickContext{
		B:         newGazeTestBot(mgl32.Vec3{0.5, 64, 0.5}, path),
		HasPath:   true,
		CurrPos:   mgl32.Vec3{0.5, 64, 0.5},
		TargetYaw: 0,
	}

	yaw, _, ok := tc.WalkingGazeAngles()
	if !ok {
		t.Fatal("no aim on a right-angle corner")
	}
	// The raw diagonal aim is about -45 deg off travel; it must land exactly on
	// the cap, proving the trim actually fired rather than the geometry
	// happening to be gentle.
	lead := movement.AngleDifference(yaw, 0)
	if math.Abs(float64(lead+movement.WalkingGazeMaxLeadYaw)) > 0.5 {
		t.Fatalf("lead %.2f not trimmed to the cap %.2f", lead, movement.WalkingGazeMaxLeadYaw)
	}
}

// TestWalkingGazeFallsBackOnDegenerateAim covers a route that doubles back on
// itself, where accumulating the look-ahead distance can land the aim point on
// top of the bot. The horizontal distance is then near zero and the angle is
// pure noise, so the caller must fall back to the travel direction rather than
// swinging the view.
func TestWalkingGazeFallsBackOnDegenerateAim(t *testing.T) {
	t.Parallel()

	path := []pathfinder.Node{
		{X: 0, Y: 64, Z: 5}, {X: 0, Y: 64, Z: 6}, {X: 0, Y: 64, Z: 7},
		{X: 0, Y: 64, Z: 6}, {X: 0, Y: 64, Z: 5}, {X: 0, Y: 64, Z: 4},
	}
	tc := &movement.TickContext{
		B:           newGazeTestBot(mgl32.Vec3{0.5, 64, 6.5}, path),
		HasPath:     true,
		CurrPos:     mgl32.Vec3{0.5, 64, 6.5},
		TargetYaw:   180,
		TargetPitch: 3,
	}

	if _, _, ok := tc.WalkingGazeAngles(); ok {
		t.Fatal("hairpin produced an aim instead of a clean fallback")
	}
	yaw, pitch := tc.WalkingScanBase()
	if yaw != 180 || pitch != 3 {
		t.Fatalf("fallback aim = (%.2f, %.2f), want the travel direction (180, 3)", yaw, pitch)
	}
}

// TestWalkingGazePitchFollowsTerrain covers the vertical half. Pitch used to be
// pinned to exactly 0 while walking, so the view never reacted to the ground the
// bot was about to step onto. Climbing must tip the view up, descending down.
func TestWalkingGazePitchFollowsTerrain(t *testing.T) {
	t.Parallel()

	up := []pathfinder.Node{
		{X: 0, Y: 64, Z: 0}, {X: 0, Y: 64, Z: 1}, {X: 0, Y: 65, Z: 2},
		{X: 0, Y: 66, Z: 3}, {X: 0, Y: 67, Z: 4},
	}
	upTC := &movement.TickContext{
		B:         newGazeTestBot(mgl32.Vec3{0.5, 64, 0.5}, up),
		HasPath:   true,
		CurrPos:   mgl32.Vec3{0.5, 64, 0.5},
		TargetYaw: 0,
	}
	_, upPitch, ok := upTC.WalkingGazeAngles()
	if !ok {
		t.Fatal("no aim on a climbing route")
	}
	if upPitch > -3 {
		t.Fatalf("climbing pitch %.2f, want clearly upward (negative)", upPitch)
	}

	down := []pathfinder.Node{
		{X: 0, Y: 64, Z: 4}, {X: 0, Y: 64, Z: 3}, {X: 0, Y: 63, Z: 2},
		{X: 0, Y: 62, Z: 1}, {X: 0, Y: 61, Z: 0},
	}
	downTC := &movement.TickContext{
		B:         newGazeTestBot(mgl32.Vec3{0.5, 64, 3.5}, down),
		HasPath:   true,
		CurrPos:   mgl32.Vec3{0.5, 64, 3.5},
		TargetYaw: 0,
	}
	_, downPitch, ok := downTC.WalkingGazeAngles()
	if !ok {
		t.Fatal("no aim on a descending route")
	}
	if downPitch < 3 {
		t.Fatalf("descending pitch %.2f, want clearly downward (positive)", downPitch)
	}
	if downPitch > movement.WalkingGazeMaxPitch {
		t.Fatalf("descending pitch %.2f exceeds cap %.2f", downPitch, movement.WalkingGazeMaxPitch)
	}
}

// TestWalkingGazeFallsBackWithoutAPath keeps the no-route case honest: with
// nothing to look along, the aim must be the movement direction itself, so
// head-tracked follow and idle poses are unaffected.
func TestWalkingGazeFallsBackWithoutAPath(t *testing.T) {
	t.Parallel()

	tc := &movement.TickContext{HasPath: false, TargetYaw: 42, TargetPitch: -7}
	yaw, pitch := tc.WalkingScanBase()
	if yaw != 42 || pitch != -7 {
		t.Fatalf("fallback aim = (%.2f, %.2f), want the movement target (42, -7)", yaw, pitch)
	}
}

// TestWalkingHeadLeadsBodyInsteadOfSnapping is the regression guard for the
// removed head snap. With the snap in place the head was reset to the movement
// direction every tick, so head-trunk separation could never build up and the
// head visibly swung sideways as the body turned.
func TestWalkingHeadLeadsBodyInsteadOfSnapping(t *testing.T) {
	t.Parallel()

	path := []pathfinder.Node{
		{X: 0, Y: 64, Z: 0}, {X: 0, Y: 64, Z: 1}, {X: 0, Y: 64, Z: 2},
		{X: 0, Y: 64, Z: 3}, {X: 1, Y: 64, Z: 3}, {X: 2, Y: 64, Z: 3},
	}
	b := newGazeTestBot(mgl32.Vec3{0.5, 64, 0.5}, path)

	// Warm the eased state at the start-of-route heading so the first ticks
	// measure the turn, not the initial convergence.
	tc := &movement.TickContext{
		B: b, HasPath: true, Tick: 100,
		CurrPos: mgl32.Vec3{0.5, 64, 0.5}, Yaw: 0, HeadYaw: 0, Pitch: 0,
		TargetYaw: 0, TargetPitch: 0, HasHorizontalMove: true,
	}
	tc.ApplyEasedLook(true, 40, 28)

	maxSeparation := 0.0
	for tick := 0; tick < 40; tick++ {
		// Advance the bot toward the corner and keep the body heading on the
		// +Z leg while the route ahead bends to +X.
		z := float32(0.5 + 0.15*float64(tick))
		b.PathIndex = int(z - 0.8)
		tc.Tick = uint64(101 + tick)
		tc.CurrPos = mgl32.Vec3{0.5, 64, z}
		tc.Dx, tc.Dz, tc.Dist = 0, 1, 1
		tc.ApplyMoveLookTarget()
		tc.ApplyEasedLook(true, 40, 28)

		if sep := math.Abs(float64(movement.AngleDifference(tc.HeadYaw, tc.Yaw))); sep > maxSeparation {
			maxSeparation = sep
		}
		// The head may lead the travel direction by the lead cap and then add
		// the bounded gaze scan on top. 0.5 deg of slack absorbs float32
		// accumulation across the two clamps.
		maxOffset := float64(movement.WalkingGazeMaxLeadYaw+movement.WalkingGazeMaxYawOffset) + 0.5
		if offset := math.Abs(float64(movement.AngleDifference(tc.HeadYaw, tc.TargetYaw))); offset > maxOffset {
			t.Fatalf("tick %d: head offset %.2f from travel direction exceeds the total cap", tick, offset)
		}
	}

	if maxSeparation < 3 {
		t.Fatalf("head never separated from the body (max %.2f deg); the head is still locked to travel", maxSeparation)
	}
}
