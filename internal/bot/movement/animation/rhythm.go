package animation

import (
	"math/rand"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

// Swing rhythm for block breaking.
//
// A break is not a series of chops with rests in between: a player holds the
// mine button and the arm works the whole time the block is cracking. The Mojang
// block-breaking design doc says so directly — `ClientInstance::tickDestroyBlock`
// calls `continueDestroyBlock` every simulation tick while the input is held, and
// it "continues even if the player is swinging at air after having broken a
// block. It only stops if the input is raised". Mining has no rests in it.
//
// The swing rate is the visible half of that, and it is set by how the bot
// looks on a live host rather than by theory about how the client renders an
// arc. An item's swing animation is 0.3s by default
// (minecraft:swing_duration), and re-sending Animate faster than that used to be
// justified as restarting the arc mid-flight, which would read as a stutter
// rather than a chop. It does not. Asked what the bot looked like swinging at
// 230ms, the answer was that the movement is smooth and only too slow, and
// 100ms was the number asked for. The arc-restart worry was reasoned from the
// animation's length rather than watched, and observation outranks the argument.
//
// The upper bound matters just as much, for the opposite reason: a pause wider
// than the swing animation parks the arm at rest mid-break, and a viewer reads
// that as a bot that stopped mining while the block is still cracking. That was
// the shipped bug — a burst-and-recovery cadence whose recovery ran 460-760ms,
// one and a half to two and a half times the swing it was spacing out.
//
// So the floor only has to stay clear of a stutter — a swing restarted before
// the previous one finished reads as a vibration, and since each swing also
// drives the dig sound, as a rattle.
//
// These values are shared by the chopper, the miner, the scaffold and the
// obstacle unstick, so one policy governs every break the bot performs.
const (
	// WindUpMin/WindUpMax is the tool raise before the first swing. It is small
	// because the swing it precedes is small: at a 100ms cadence a 90ms wind-up
	// would be almost a whole beat of raised arm, which is the stall this rhythm
	// exists to avoid.
	WindUpMin = 30 * time.Millisecond
	WindUpMax = 60 * time.Millisecond

	// SwingMin/SwingMax is the pause between swings, centred on 100ms — about
	// ten a second, which is how fast held-button mining is supposed to read.
	// The band is narrow because the ceiling is a real constraint and the floor
	// is chosen rather than derived; a fixed interval would be a metronome, so it
	// jitters by a few milliseconds either side of the target.
	SwingMin = 90 * time.Millisecond
	SwingMax = 110 * time.Millisecond
	SwingMid = 100 * time.Millisecond

	// AimJitter is how far (in blocks) the aim may wander from the block
	// centre. Enough to read as a hand, never enough to miss.
	AimJitter = 0.12
)

// WindUp returns the pause before the first swing of a break.
func WindUp() time.Duration {
	return WindUpMin + time.Duration(rand.Int63n(int64(WindUpMax-WindUpMin)))
}

// Cadence returns the pause after a swing. The interval is jittered so a long
// break is not a metronome, but it stays inside one swing animation's width:
// mining is continuous work, not a burst pattern.
func Cadence() time.Duration {
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
// Swing count first, pause length second. How many swings fit is decided from the
// break, and the break's leftover time is then shared evenly between them, so every
// pause lands near the reference rate instead of the last one absorbing the slack.
//
// Two bounds shape the count. The swing floor caps how many fit: N past
// remaining/SwingMin means a pause under the floor, which restarts the viewer's arm
// cycle mid-flight. The reference rate sets how many are wanted, so a two-second break
// swings about nine times rather than four.
//
// Dividing with floor (not ceil) is what keeps both guarantees at once: swings <=
// remaining/SwingMin makes floor(remaining/swings) >= SwingMin, and the leftover
// swings*share <= remaining, so the correction below can only ever lengthen the last
// pause. An earlier version rounded the share up and then subtracted the overshoot,
// which is how the last swing came out at 190ms — under the floor — on a 450ms break.
//
// No jitter is invented here, and that is deliberate: whatever jitter a break
// gets comes from the headroom the break time actually leaves above the swing
// floor, so a break that barely fits swings stays clean rather than having noise
// pushed through it.
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
	remaining := breakTime - WindUpMin

	// A break too short to carry a swing gets none. There is no honest way to
	// spend the time — stretching the wind-up would turn a tool raise into a
	// stall, and a strained swing is worse than an instant block simply breaking.
	swings := (remaining + SwingMid/2) / SwingMid
	if fits := remaining / SwingMin; swings > fits {
		swings = fits
	}
	if swings < 1 {
		return beats
	}

	// Split the break's time between the swings. Every pause clears the swing
	// floor, the pauses vary so a long break is not a metronome, and they total
	// the break exactly.
	//
	// The split is a random partition with a floor, not N identical slices plus
	// noise: each swing draws from the slack left above the floor and divided by
	// how many swings remain, so no single pause can be starved by the ones before
	// it. Two earlier attempts failed this in opposite directions. Laying down
	// fixed cadence steps and stretching the last one to absorb the remainder left
	// the arm parked mid-break (438ms on a 1.4s log). Jittering every slice and
	// correcting the total on the last swing drove that one pause to 163ms — under
	// the floor, which is the vibration the floor exists to prevent.
	for i := time.Duration(0); i < swings-1; i++ {
		left := swings - i
		slack := remaining - left*SwingMin
		wait := SwingMin
		if slack > 0 {
			extra := slack / left
			if extra > 0 {
				// Take at least half the fair share, so the swings ahead cannot
				// crowd the last one and leave it holding a pause long enough to
				// read as a rest.
				wait += extra/2 + time.Duration(rand.Int63n(int64(extra-extra/2)+1))
			}
		}
		beats = append(beats, Beat{Wait: wait, Aim: swingAim})
		remaining -= wait
	}

	// Whatever is left is the final swing, and it is at least SwingMin by
	// construction: the draws above never took more than their even share.
	beats = append(beats, Beat{Wait: remaining, Aim: swingAim})
	return beats
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
// Carrying the rhythm across the logs is the fix: the wind-up is owed once, and
// the gap at a log boundary is an ordinary cadence pause rather than a stall.
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
	c.swings++
	return Cadence(), true
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
