// Idle nudge: the bot fidgets while standing still.
//
// This file exists because of a concrete disconnect. On the Geyser-fronted
// server the bot spawned, stood perfectly still, and was kicked at a
// near-identical ~26 seconds, every session. The bot already "looked around"
// while idle (see applyIdleLook), but a server-side AFK kicker watches for
// POSITION change, not head rotation — so a bot that only turns its head is
// still a statue from the server's point of view.
//
// The fix is the least surprising one: a standing player fidgets. The bot now
// takes an occasional small idle step to a nearby point, at a randomised
// interval, using the exact same walk_to machinery as any other destination. No
// new movement primitive is introduced, so there is nothing to get out of sync
// with the pathfinder.
//
// It is also, independently, more human. A character that never moves while
// "idle" is the single most obvious tell that it is not a person.
package movement

import (
	"log/slog"
	"math"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/rand"

	"github.com/go-gl/mathgl/mgl32"
)

// The first nudge is scheduled a randomised interval out. The window is
// deliberately short: on servers that drop a silent client, the connection can
// be gone within ~15-20 seconds, so a nudge that first fires at 30s would never
// run in time to prove the bot is alive.
const (
	defaultNudgeMinGap = 5 * time.Second
	defaultNudgeMaxGap = 9 * time.Second
	defaultNudgeReach  = 6.0
)

// maybeIdleNudge issues a short idle walk when the bot has been standing still
// long enough. Every "genuinely idle" condition is checked before it fires, so
// it can never interrupt a real task.
//
// The whole behaviour is gated on b.IdleNudge, which comes from the detected
// server profile. It exists for servers that drop a client which goes quiet
// (Geyser today). On any other server this function returns immediately and the
// bot stands exactly as still as it always did — the fix is scoped, not global.
func (tc *TickContext) maybeIdleNudge() {
	b := tc.B

	if !b.IdleNudge {
		return
	}
	// Only when idle with nothing to do. Any active state, or a player tracking
	// us, means the bot already has a reason to be where it is.
	if tc.MState != "idle" || tc.HasPath || tc.ShouldMove {
		return
	}
	if b.MovementState != "idle" {
		return
	}
	// Never nudge while something else is driving the bot around.
	if b.Explorer != nil && b.Explorer.IsExploring() {
		return
	}
	if b.Planner != nil && b.Planner.IsRunning() {
		return
	}

	now := time.Now()
	minGap, maxGap := idleNudgeGap(b)
	radius := idleNudgeReach(b)

	b.Mu.Lock()
	last := b.LastIdleNudgeAt
	origin := b.Pos
	b.Mu.Unlock()

	// Schedule the first nudge a randomised interval out, so the bot does not
	// step the instant it joins — but short enough to fire well before a
	// ~15-second connection timeout on a server that drops silent clients.
	if last.IsZero() {
		b.Mu.Lock()
		b.LastIdleNudgeAt = now.Add(time.Duration(rand.Intn(int(maxGap/time.Second))+2) * time.Second)
		b.Mu.Unlock()
		return
	}
	if now.Before(last) || now.Sub(last) < minGap {
		return
	}

	// Aim a few blocks away in a random direction, biased toward level ground.
	// A short distance is deliberate: the bot should read as shifting its
	// weight, not as setting out somewhere.
	angle := rand.Float64() * 2 * math.Pi
	dist := radius * float32(0.4+0.6*rand.Float64())
	target := origin.Add(mgl32.Vec3{
		float32(math.Cos(angle)) * dist,
		0,
		float32(math.Sin(angle)) * dist,
	})

	// Re-arm before walking so the next window starts from now even if the walk
	// itself takes a while.
	b.Mu.Lock()
	b.LastIdleNudgeAt = now.Add(minGap + time.Duration(rand.Intn(int(maxGap-minGap)))*time.Second)
	b.Mu.Unlock()

	b.WalkTo(target)
	b.Logger.Debug("idle nudge",
		slog.Float64("x", float64(target.X())),
		slog.Float64("z", float64(target.Z())),
	)
}

// idleNudgeGap resolves the wait window between nudges, falling back to the
// defaults so a zero-valued Bot still behaves.
func idleNudgeGap(b *bot.Bot) (time.Duration, time.Duration) {
	minSec := b.IdleNudgeMinSec
	if minSec <= 0 {
		minSec = int(defaultNudgeMinGap / time.Second)
	}
	maxSec := b.IdleNudgeMaxSec
	if maxSec <= 0 {
		maxSec = int(defaultNudgeMaxGap / time.Second)
	}
	if maxSec <= minSec {
		maxSec = minSec + 1
	}
	return time.Duration(minSec) * time.Second, time.Duration(maxSec) * time.Second
}

// idleNudgeReach resolves how far a single idle nudge may reach.
func idleNudgeReach(b *bot.Bot) float32 {
	if b.IdleNudgeReach > 0 {
		return b.IdleNudgeReach
	}
	return defaultNudgeReach
}
