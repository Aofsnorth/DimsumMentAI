package gathering

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

// TestGatherOutcomeReportsFullHaulOnlyWhenTargetMet is the guard for the bug
// behind "take 200 logs and it stops at one tree": a partial haul used to be
// announced as success, so the model believed the order was complete.
func TestGatherOutcomeReportsFullHaulOnlyWhenTargetMet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		collected int
		target    int
		felled    int
		wantOK    bool
		wantErr   bool
	}{
		{name: "target met", collected: 200, target: 200, felled: 14, wantOK: true},
		{name: "overshoot still ok", collected: 213, target: 200, felled: 15, wantOK: true},
		{name: "short by a lot", collected: 12, target: 200, felled: 1, wantErr: true},
		{name: "short by one", collected: 199, target: 200, felled: 14, wantErr: true},
		{name: "nothing felled", collected: 0, target: 200, felled: 0, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ok, errMsg := gatherOutcome(tc.collected, tc.target, tc.felled)
			if ok != tc.wantOK {
				t.Fatalf("gatherOutcome(%d, %d, %d) success = %v, want %v", tc.collected, tc.target, tc.felled, ok, tc.wantOK)
			}
			if tc.wantErr && errMsg == "" {
				t.Fatal("shortfall reported with empty error, want a reason the LLM can act on")
			}
			if tc.wantOK && errMsg != "" {
				t.Fatalf("success reported with error %q, want empty", errMsg)
			}
			// The message must name the real numbers, not just "failed".
			if tc.collected > 0 && tc.collected < tc.target {
				if !strings.Contains(errMsg, "200") {
					t.Errorf("error %q does not mention the target", errMsg)
				}
			}
		})
	}
}

// TestMaxGatherAttemptsIsBounded keeps a huge request from turning one command
// into a whole-forest tour, while still allowing a pile far bigger than a
// single trunk.
func TestMaxGatherAttemptsIsBounded(t *testing.T) {
	t.Parallel()

	if maxGatherAttempts < 2 {
		t.Fatalf("maxGatherAttempts = %d, want room for more than one trunk", maxGatherAttempts)
	}
	if maxGatherAttempts > 32 {
		t.Fatalf("maxGatherAttempts = %d, want a bound that stops a single request from touring the forest", maxGatherAttempts)
	}
}

// TestChopCadenceVariesLikeAHand covers the "natural" side of the fix: a fixed
// 100 ms tick between swings is the clearest tell of a bot. The rhythm must
// vary, stay inside its bounds, and still pause longer between bursts than
// inside them.
func TestChopCadenceVariesLikeAHand(t *testing.T) {
	t.Parallel()

	seen := map[time.Duration]int{}
	for swing := 0; swing < 24; swing++ {
		wait := chopCadence(swing)
		if wait < chopSwingMin || wait > chopRecoveryMax {
			t.Fatalf("swing %d: cadence %v outside [%v, %v]", swing, wait, chopSwingMin, chopRecoveryMax)
		}
		seen[wait]++

		isRecoverySlot := swing%chopBurstLength == chopBurstLength-1
		if isRecoverySlot && wait < chopRecoveryMin {
			t.Fatalf("swing %d: recovery slot cadence %v shorter than the recovery floor %v", swing, wait, chopRecoveryMin)
		}
		if !isRecoverySlot && wait > chopRecoveryMax {
			t.Fatalf("swing %d: burst cadence %v longer than the burst ceiling", swing, wait)
		}
	}

	if len(seen) < 4 {
		t.Fatalf("cadence produced only %d distinct values over 24 swings, want a varied rhythm", len(seen))
	}
}

// TestChopWindUpStaysInBounds checks the tool raise before the first swing:
// long enough to read as a wind-up, short enough not to eat the break time.
func TestChopWindUpStaysInBounds(t *testing.T) {
	t.Parallel()

	for i := 0; i < 20; i++ {
		w := chopWindUp()
		if w < chopWindUpMin || w > chopWindUpMax {
			t.Fatalf("wind-up %v outside [%v, %v]", w, chopWindUpMin, chopWindUpMax)
		}
	}
	if chopWindUpMin >= chopSwingMin {
		t.Fatal("wind-up floor should stay under the swing floor so it reads as a separate beat")
	}
}

// TestChopAimJittersAroundBlockCentre checks the aim drifts a little instead of
// being welded to the exact block centre — but never far enough to miss.
func TestChopAimJittersAroundBlockCentre(t *testing.T) {
	t.Parallel()

	center := mgl32.Vec3{10.5, 64.5, -3.5}
	moved := false

	for i := 0; i < 40; i++ {
		aim := chopAim(center)
		if math.Abs(float64(aim.X()-center.X())) > chopAimJitter {
			t.Fatalf("aim X drifted %v, want at most %v", aim.X()-center.X(), chopAimJitter)
		}
		if math.Abs(float64(aim.Y()-center.Y())) > chopAimJitter {
			t.Fatalf("aim Y drifted %v, want at most %v", aim.Y()-center.Y(), chopAimJitter)
		}
		if math.Abs(float64(aim.Z()-center.Z())) > chopAimJitter {
			t.Fatalf("aim Z drifted %v, want at most %v", aim.Z()-center.Z(), chopAimJitter)
		}
		if aim != center {
			moved = true
		}
	}

	if !moved {
		t.Fatal("aim never moved off the block centre, want visible jitter")
	}
}
