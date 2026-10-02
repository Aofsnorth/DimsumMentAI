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
// for the arm cycle, so the last thing a viewer sees is the exact vibration this
// rhythm exists to prevent.
//
// What is left over is handed to the last swing instead of being dropped.
// Dropping it was worse than a runt swing: the shortfall is not a rounding error
// but a whole unused pause, up to a full recovery. A 650ms break laid down 380ms
// of swings and then stopped, so the last quarter of the break had a raised arm
// and no strike — the block simply vanished between beats, and the swing read as
// disconnected from the thing it was hitting. Stretching the last beat keeps
// every pause at or above the swing floor, because it only ever adds time to one
// that already cleared it.
//
// The exception is a break too short to carry a swing at all, where the last beat
// is still the wind-up. Stretching that would turn a tool raise into a stall, and
// the time cannot be spent on anything: there is no swing that fits. An instant
// block is better served by no swing than by a strained one.
func Beats(breakTime time.Duration, aim mgl32.Vec3) []Beat {
	beats := []Beat{{Wait: WindUp(), Aim: aim}}

	// One aim for the whole break, not one per swing. The jitter is there so the
	// head is not welded to a single pixel, and re-rolling it on every swing does
	// not do that: the look ease converges at 0.22 a tick and needs the better
	// part of a second to settle, while a fresh random target arrived every three
	// hundred milliseconds. The head chased a target it could never reach, in
	// step with the arm, which reads as the swing itself being wrong because the
	// body it hangs off is twitching through every strike. This repository has
	// already ruled that shape a bug once, in the movement loop's pitch easing.
	swingAim := JitteredAim(aim)

	// Assume the wind-up took its floor: never overrun the break time.
	elapsed := WindUpMin
	for swing := 0; ; swing++ {
		wait := Cadence(swing)
		if elapsed+wait >= breakTime {
			if remainder := breakTime - elapsed; remainder > 0 && len(beats) > 1 {
				beats[len(beats)-1].Wait += remainder
			}
			return beats
		}
		beats = append(beats, Beat{Wait: wait, Aim: swingAim})
		elapsed += wait
	}
}

// Chain is a break rhythm that keeps running across several blocks.
//
// Beats is the right shape for one block and the wrong shape for a tree. A trunk
// is not a stack of independent breaks: a player fells it with one continuous
// swing cycle and the arm never drops between logs. Laying a fresh Beats out per
// log put a new wind-up in front of every one of them, and since the chopper only
// waited 20ms between logs the gap between the last swing of one log and the
// first swing of the next came out at 120-240ms — under SwingMin, so the viewer's
// client restarted the arm cycle before the previous one finished. On a six-log
// trunk that happened five times, and it is what a stuttering arm looks like.
//
// Carrying the rhythm across the logs is the fix. The wind-up is owed once, the
// swing counter keeps counting so the burst-and-recovery pattern spans the whole
// tree rather than restarting inside every log, and the gap at a log boundary is
// an ordinary cadence pause.
//
// The aim is re-rolled on Reaim rather than per beat, for the same reason Beats
// uses one aim per break: the look ease cannot follow a target that changes every
// few hundred milliseconds, and the head is what the arm hangs off.
type Chain struct {
	centre  mgl32.Vec3
	aim     mgl32.Vec3
	swings  int
	started bool
}

// NewChain starts a rhythm aimed at a block centre.
func NewChain(centre mgl32.Vec3) *Chain {
	return &Chain{centre: centre, aim: JitteredAim(centre)}
}

// Next reports how long to wait before the next action and whether a swing is due
// when it elapses.
//
// The first call of a chain is the wind-up and sends no swing; every call after
// that is a cadence pause and sends one.
func (c *Chain) Next() (wait time.Duration, swing bool) {
	if !c.started {
		c.started = true
		return WindUp(), false
	}
	wait = Cadence(c.swings)
	c.swings++
	return wait, true
}

// Started reports whether the wind-up has already been served.
func (c *Chain) Started() bool { return c.started }

// Swings reports how many swings the chain has sent.
func (c *Chain) Swings() int { return c.swings }

// Aim is where the head should be looking during the next swing.
func (c *Chain) Aim() mgl32.Vec3 { return c.aim }

// Reaim moves the chain onto a new block, keeping the swing count and the fact
// that the wind-up is already spent.
func (c *Chain) Reaim(centre mgl32.Vec3) {
	c.centre = centre
	c.aim = JitteredAim(centre)
}
