package movement_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/bot/movement"
)

// A walking human's head nods once per stride pair, so the bob is not decoration
// to be dialled in by eye: it has to run at a human frequency, and it has to be
// driven by travel rather than by the clock. A clock-driven sine keeps
// oscillating while the bot stands still and jumps to a random phase when it
// starts walking, both of which read as a body vibrating rather than walking.

// walkSpeed and sprintSpeed are the vanilla speeds the bob is tuned against:
// 4.317 m/s walking and 5.612 m/s sprinting.
const (
	walkSpeed   = 4.317
	sprintSpeed = 5.612
)

// oneWalkStride is a whole stride, which is phase zero again. Sampling the bob
// at distance zero is blocked by the no-travel guard, and the quarter-cycle
// relationships have to be read at a real phase.
//
// These mirror the unexported lengths in the package. They are restated here on
// purpose: a test that read them from the implementation would agree with any
// value it was handed, and the point is to pin the numbers the human band was
// derived from.
const (
	oneWalkStride       = 1.10
	sprintStrideForTest = 1.45
)

// bobFrequencyAt counts the sign changes in the pitch over a fixed distance,
// which is the bob's frequency expressed without needing a time base.
func bobFrequencyAt(distance, speed float32) float64 {
	const step = 0.002
	crossings := 0
	prev := signOf(StrideBobAt(0, speed))
	for d := float32(step); d < distance; d += float32(step) {
		cur := signOf(StrideBobAt(d, speed))
		if cur != 0 && cur != prev {
			crossings++
			prev = cur
		}
	}
	// Two crossings is one full cycle.
	// Hz is cycles per second: cycles divided by distance, times the speed that
	// distance was covered at.
	return float64(crossings) / 2 / float64(distance) * float64(speed)
}

func signOf(v float32) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	default:
		return 0
	}
}

// StrideBobAt calls the package function through the test-only seam below.
func StrideBobAt(distance, speed float32) float32 {
	pitch, _ := movement.StrideBob(distance, speed, false)
	return pitch
}

func TestStrideBobRunsAtAHumanFrequency(t *testing.T) {
	t.Parallel()

	// Measured human walking head bob is about 3.3-3.9 Hz. At the vanilla walk
	// speed a 1.10 m stride puts the cycle right in that band; a bob an order
	// of magnitude off would be obvious in third person.
	for _, tc := range []struct {
		name   string
		speed  float32
		lo, hi float64
	}{
		{"walk", walkSpeed, 3.0, 4.4},
		{"sprint", sprintSpeed, 2.6, 4.4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := bobFrequencyAt(20, tc.speed)
			if got < tc.lo || got > tc.hi {
				t.Fatalf("bob frequency %.2f Hz at %s is outside the human band %.1f-%.1f Hz",
					got, tc.name, tc.lo, tc.hi)
			}
		})
	}
}

func TestStrideBobIsZeroWhenNotMoving(t *testing.T) {
	t.Parallel()

	// Standing still must produce no bob at all. A clock-driven sine fails here;
	// a distance-driven one cannot.
	pitch, yaw := movement.StrideBob(0, 0, false)
	if pitch != 0 || yaw != 0 {
		t.Fatalf("a stationary body must not bob, got pitch %.3f yaw %.3f", pitch, yaw)
	}
	// Accumulated phase with no travel is still no travel.
	s := &movement.StridePhase{}
	_, _, walking := s.Bob(false)
	if walking {
		t.Fatal("a stride phase that never advanced must report no walking")
	}
}

// A stride switch must change how fast the phase advances, never where it is.
//
// If the phase were recomputed as travelled divided by the current stride, the
// switch from a 1.10 m walk stride to a 1.45 m sprint stride would teleport it
// by the fraction of a cycle those two lengths differ by -- visible as a head
// snap the instant the bot breaks into a run. Accumulated cycles cannot do that,
// so the increment a sprint tick adds is the same whatever phase it starts from.
func TestStrideBobStrideSwitchAddsTheSameCycleFromAnyPhase(t *testing.T) {
	t.Parallel()

	sprintingStep := float32(sprintSpeed / 20)

	run := func(walkTicks int) float64 {
		s := &movement.StridePhase{}
		walkingStep := float32(walkSpeed / 20)
		for i := 0; i < walkTicks; i++ {
			s.Advance(walkingStep, 0)
		}
		before := s.Cycles()
		s.Advance(sprintingStep, 0)
		return s.Cycles() - before
	}

	// Three starting phases an eighth of a cycle apart.
	fromEarly := run(1)
	fromMiddle := run(5)
	fromLate := run(9)

	want := float64(sprintingStep) / sprintStrideForTest
	for name, got := range map[string]float64{"early": fromEarly, "middle": fromMiddle, "late": fromLate} {
		if math.Abs(got-want) > 1e-6 {
			t.Fatalf("a sprint tick from the %s phase advanced %.6f cycles, want %.6f; "+
				"an increment that depends on the starting phase is a recomputed phase",
				name, got, want)
		}
	}
}

func TestStrideBobStopsCleanlyAndResumes(t *testing.T) {
	t.Parallel()

	s := &movement.StridePhase{}
	step := float32(walkSpeed / 20)
	for i := 0; i < 10; i++ {
		s.Advance(step, 0)
	}
	if _, _, walking := s.Bob(false); !walking {
		t.Fatal("precondition: the phase should report walking")
	}

	// A stopped tick is an Advance with no travel, which is exactly what the
	// movement loop produces when the bot halts.
	for i := 0; i < 40; i++ {
		s.Advance(0, 0)
	}
	pitchStopped, yawStopped, walking := s.Bob(false)
	if walking {
		t.Fatal("a stopped body must not report walking")
	}
	if pitchStopped != 0 || yawStopped != 0 {
		t.Fatalf("a stopped body must not bob, got pitch %.4f yaw %.4f", pitchStopped, yawStopped)
	}

	// The phase is held, not discarded, so the first stride after a pause
	// continues the stride the body was in rather than restarting at zero.
	held := s.Cycles()
	if held <= 0 {
		t.Fatal("standing still discarded the phase instead of holding it")
	}
	s.Advance(step, 0)
	if resumed, _, _ := s.Bob(false); resumed == 0 {
		t.Fatal("resuming produced no bob")
	}
	if s.Cycles() <= held {
		t.Fatal("resuming reset the phase; a restart at zero is the clock-driven tell")
	}
}

func TestStrideBobMeasuresDiagonalTravelByItsLength(t *testing.T) {
	t.Parallel()

	// Advance takes the two horizontal axes, and the step length it derives is
	// the hypot of them. So walking diagonally at 45 degrees for one block has
	// to advance the phase exactly as far as walking straight for the same
	// length. Summing the axes instead would make a diagonal gait 1.41 times too
	// fast, which reads as the bob quickening the instant a bot turns a corner.
	diagonal := &movement.StridePhase{}
	diagonal.Advance(1.0, 1.0)

	axial := &movement.StridePhase{}
	axial.Advance(float32(math.Hypot(1, 1)), 0)

	dp, _, _ := diagonal.Bob(false)
	ap, _, _ := axial.Bob(false)
	if math.Abs(float64(dp-ap)) > 1e-4 {
		t.Fatalf("a diagonal step advanced %.5f but the same length on one axis advanced %.5f", dp, ap)
	}
}

func TestStrideBobDampsInAirRatherThanStopping(t *testing.T) {
	t.Parallel()

	ground, _ := movement.StrideBob(0.37, sprintSpeed, false)
	air, _ := movement.StrideBob(0.37, sprintSpeed, true)

	if air == 0 {
		t.Fatal("the bob must collapse to a trace in air, not switch off; " +
			"a hard cut is its own tell")
	}
	if air >= ground {
		t.Fatalf("air bob %.4f must be damped below the ground bob %.4f", air, ground)
	}
	if air > ground*0.5 {
		t.Fatalf("air bob %.4f is not damped enough against ground %.4f", air, ground)
	}
}

func TestStrideBobAmplitudeStaysHuman(t *testing.T) {
	t.Parallel()

	// Human walking head pitch nod measures roughly 1.5-3.5 degrees. Much
	// larger and it reads as a seizure rather than a walk.
	var peak float32
	for d := float32(0); d < 4; d += 0.005 {
		p, _ := movement.StrideBob(d, sprintSpeed, false)
		if p > peak {
			peak = p
		}
	}
	if peak < 0.8 || peak > 3.6 {
		t.Fatalf("sprint bob peak %.2f degrees is outside the human 0.8-3.6 band", peak)
	}
}

func TestStrideBobLateralLeadsTheVerticalByAQuarterCycle(t *testing.T) {
	t.Parallel()

	// The head is at its furthest side as it passes through the vertical middle
	// of the stride, so the two components sit a quarter turn apart rather than
	// in phase. In phase looks like a single axis shaking.
	pitchAtZero, yawAtZero := movement.StrideBob(oneWalkStride, walkSpeed, false)
	if math.Abs(float64(pitchAtZero)) > 1e-6 {
		t.Fatalf("expected the vertical component to cross zero at phase zero, got %.4f", pitchAtZero)
	}
	if math.Abs(float64(yawAtZero)) < 0.5 {
		t.Fatalf("expected the lateral component at its extreme at phase zero, got %.4f", yawAtZero)
	}

	// A quarter of a stride later the roles are exchanged.
	pitchQuarter, yawQuarter := movement.StrideBob(oneWalkStride+oneWalkStride/4, walkSpeed, false)
	if math.Abs(float64(yawQuarter)) > 1e-6 {
		t.Fatalf("expected the lateral component to cross zero a quarter cycle later, got %.4f", yawQuarter)
	}
	if math.Abs(float64(pitchQuarter)) < math.Abs(float64(yawAtZero))*0.9 {
		t.Fatal("expected the vertical component at its extreme a quarter cycle later")
	}
}

func TestStrideBobIsBoundedByConstruction(t *testing.T) {
	t.Parallel()

	// Pure trigonometry bounds it, but the accumulator is the part that can go
	// wrong, so drive it hard and confirm nothing runs away.
	s := &movement.StridePhase{}
	for i := 0; i < 200000; i++ {
		s.Advance(0.3, 0.3)
	}
	pitch, yaw, walking := s.Bob(false)
	if !walking {
		t.Fatal("a long walk should still report walking")
	}
	if pitch > 4 || pitch < -4 || yaw > 2 || yaw < -2 {
		t.Fatalf("bob escaped its amplitude after a long walk: pitch %.3f yaw %.3f", pitch, yaw)
	}
}

func TestStrideBobSurvivesANilPhase(t *testing.T) {
	t.Parallel()

	// A context assembled without a stride accumulator must not panic; this is
	// the shape every test-built TickContext has.
	var s *movement.StridePhase
	s.Advance(1, 1)
	pitch, yaw, walking := s.Bob(false)
	if walking || pitch != 0 || yaw != 0 {
		t.Fatal("a nil stride phase must report no bob")
	}
}
