package animation

import (
	"math/rand"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

// Swing rhythm for block breaking.
//
// Two separate tells made a breaking bot look automated, and both live here
// because every break path in the bot needs them:
//
//  1. Pace faster than the animation. Every Animate packet makes the viewer's
//     client replay the full arm-swing cycle (~300ms). Swinging again every
//     70-150ms restarts that cycle before it finishes, so the arm reads as a
//     vibration instead of a swing — and because each swing is also what makes
//     a client emit its dig sound, the same mistake is heard as a machine-gun
//     rattle. A human lands a tool around 2.5-4 times a second: 260-400ms.
//  2. A metronome. Even at the right speed, an unvarying interval is a bot. A
//     hand works in short bursts with a longer recovery between them, and the
//     aim drifts a little inside the block rather than welding to one pixel.
//
// These are shared by the chopper, the miner, the scaffold and the obstacle
// unstick, so a single policy governs every break the bot performs. The values
// are the ones the chopper shipped with; the other paths were still on their
// own fixed 300-400ms ticks.
const (
	// WindUpMin/WindUpMax is the tool raise before the first swing. Starting
	// instantly looks automated, but it must stay under the swing floor so it
	// reads as a separate beat rather than a delay.
	WindUpMin = 100 * time.Millisecond
	WindUpMax = 220 * time.Millisecond

	// SwingMin/SwingMax is the pause between swings inside a burst.
	SwingMin = 260 * time.Millisecond
	SwingMax = 400 * time.Millisecond

	// RecoveryMin/RecoveryMax is the longer pause between bursts.
	RecoveryMin = 460 * time.Millisecond
	RecoveryMax = 760 * time.Millisecond

	// BurstLength is how many swings run before a recovery pause.
	BurstLength = 3

	// AimJitter is how far (in blocks) the aim may wander from the block
	// centre. Enough to read as a hand, never enough to miss.
	AimJitter = 0.12
)

// WindUp returns the pause before the first swing of a break.
func WindUp() time.Duration {
	return WindUpMin + time.Duration(rand.Int63n(int64(WindUpMax-WindUpMin)))
}

// Cadence returns the pause after the nth swing: quick inside a burst, a longer
// recovery between them.
func Cadence(swing int) time.Duration {
	if swing%BurstLength == BurstLength-1 {
		return RecoveryMin + time.Duration(rand.Int63n(int64(RecoveryMax-RecoveryMin)))
	}
	return SwingMin + time.Duration(rand.Int63n(int64(SwingMax-SwingMin)))
}

// JitteredAim drifts the aim point around the block centre so the head does not
// sit perfectly still on one pixel for the whole break.
func JitteredAim(center mgl32.Vec3) mgl32.Vec3 {
	return mgl32.Vec3{
		center.X() + AimJitter*(rand.Float32()*2-1),
		center.Y() + AimJitter*(rand.Float32()*2-1),
		center.Z() + AimJitter*(rand.Float32()*2-1),
	}
}

// Beat describes one swing's timing: how long to wait before it and the aim to
// use for it. Extracted so every break path paces itself identically instead of
// each re-deriving the loop.
type Beat struct {
	Wait time.Duration
	Aim  mgl32.Vec3
}

// Beats lays out the whole rhythm for a break of the given duration: the
// wind-up, then one Beat per swing until the duration runs out.
//
// The caller still has to do the waiting — beats are only meaningful relative
// to a clock the break can actually be cancelled on — but the beats are
// arranged so the total never overshoots the break, which is what an early
// PredictDestroy on a server-auth host is silently rejected for.
//
// A swing is only emitted when there is room for a full pause after it. The
// naive version trimmed the last beat to whatever time was left, which on a
// 3-second break produced a final swing 86ms after the previous one: too fast
// for the arm cycle, so the last thing a viewer sees is the vibration this
// rhythm exists to avoid. A remainder too short to swing is given to the
// previous beat instead, so the break still ends when it should.
func Beats(breakTime time.Duration, aim mgl32.Vec3) []Beat {
	beats := []Beat{{Wait: WindUp(), Aim: aim}}

	// Assume the wind-up took its floor: never overrun the break time.
	elapsed := WindUpMin
	for swing := 0; ; swing++ {
		wait := Cadence(swing)
		if elapsed+wait >= breakTime {
			remainder := breakTime - elapsed
			if remainder < SwingMin && len(beats) > 1 {
				beats[len(beats)-1].Wait += remainder
			}
			return beats
		}
		beats = append(beats, Beat{Wait: wait, Aim: JitteredAim(aim)})
		elapsed += wait
	}
}
