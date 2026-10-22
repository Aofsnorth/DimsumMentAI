package action

import (
	"fmt"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
)

// statusTimeout bounds how long a plan step waits for its handler to say how it
// went.
const statusTimeout = 90 * time.Second

type Step struct {
	Label string
	Param string
}

func ExecutePlan(b *bot.Bot, steps []Step, user string) {
	if len(steps) == 0 {
		return
	}
	if len(steps) == 1 {
		Execute(b, steps[0].Label, steps[0].Param, user)
		return
	}

	ctx := b.ActivityContext()
	go func() {
		b.Logger.Debug("executing action plan", "steps", len(steps), "user", user)
		for _, step := range steps {
			if ctx != nil && ctx.Err() != nil {
				return
			}
			label := strings.ToLower(strings.TrimSpace(step.Label))
			if label == "" {
				continue
			}
			Execute(b, label, step.Param, user)
			waitForActionSettled(b, label)
		}
	}()
}

// ExecuteAndWait runs one action synchronously and returns what actually
// happened.
func ExecuteAndWait(b *bot.Bot, label, param, user string) event.ActionStatus {
	return ExecuteAndWaitWithTimeout(b, label, param, user, actionReportTimeout(label))
}

// actionReportTimeout resolves how long a step may run before "no result"
// means failure.
func actionReportTimeout(label string) time.Duration {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "gather", "chop", "treefarm", "automine", "fish", "harvest", "explore":
		return 45 * time.Minute
	}
	return statusTimeout
}

// ExecuteAndWaitWithTimeout is ExecuteAndWait with the report timeout passed in, so the
// expiry path can be tested without sitting out the production bound.
func ExecuteAndWaitWithTimeout(b *bot.Bot, label, param, user string, timeout time.Duration) event.ActionStatus {
	normalized := strings.ToLower(strings.TrimSpace(label))

	if normalized == "" {
		return event.ActionStatus{Action: normalized, Success: true}
	}

	if normalized == "craft" {
		return executeCraftActionSilent(b, param, user)
	}

	if _, ok := lookupHandler(normalized); !ok {
		return event.ActionStatus{
			Action:  normalized,
			Success: false,
			Error:   fmt.Sprintf("unknown action label %q", normalized),
		}
	}

	statuses := SubscribeStatus(b)
	defer UnsubscribeStatus(b, statuses)

	Execute(b, normalized, param, user)

	waitForActionSettled(b, normalized)

	select {
	case status := <-statuses:
		if status.Action == "" {
			status.Action = normalized
		}
		return status
	case <-time.After(timeout):
		return event.ActionStatus{
			Action:  normalized,
			Success: false,
			Error:   fmt.Sprintf("action %q reported no result within %s", normalized, timeout),
		}
	}
}

func waitForActionSettled(b *bot.Bot, label string) {
	switch label {
	case "come", "goto", "move", "walk", "force_walk", "gotoblock", "standon", "enterportal":
		waitForMovementIdle(b, 65*time.Second)
	case "emote", "jump", "dance", "twerk", "floss", "naenae", "robot", "breakdance",
		"spinforever", "spinfast", "spinslow", "spinlookup", "spinlookdown",
		"jumpforever", "jumpinplace", "jumpspincombo", "nod", "shake", "headbang", "wave", "dab":
		waitForEmoteSettled(b, 5*time.Second)
	case "gather", "chop", "treefarm", "mine", "automine", "loot", "clear", "scan":
		waitForGatheringIdle(b, actionReportTimeout(label))
	case "craft":
		time.Sleep(900 * time.Millisecond)
	case "follow":
		time.Sleep(600 * time.Millisecond)
	default:
		time.Sleep(250 * time.Millisecond)
	}
}

// waitForEmoteSettled waits for the active emote window to end (EmoteTicks
// reaches zero, driven by the movement loop's applyEmote), so a staged routine
// does not start its next clause while the body is still hopping or spinning.
func waitForEmoteSettled(b *bot.Bot, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b.Mu.Lock()
		ticks := b.EmoteTicks
		b.Mu.Unlock()
		if ticks == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForMovementIdle(b *bot.Bot, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	startDeadline := time.Now().Add(350 * time.Millisecond)
	seenMoving := false
	for time.Now().Before(deadline) {
		now := time.Now()
		b.Mu.Lock()
		state := b.MovementState
		forceWalking := !b.ForceWalkUntil.IsZero() && now.Before(b.ForceWalkUntil)
		b.Mu.Unlock()

		moving := (state != "" && state != "idle") || forceWalking
		if moving {
			seenMoving = true
		}
		if seenMoving && !moving {
			return
		}
		if !seenMoving && now.After(startDeadline) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForGatheringIdle(b *bot.Bot, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	startDeadline := time.Now().Add(2 * time.Second)
	seenActive := false
	for time.Now().Before(deadline) {
		active := b.Gatherer != nil && b.Gatherer.IsGathering()
		if active {
			seenActive = true
		}
		if seenActive && !active {
			return
		}
		if !seenActive && time.Now().After(startDeadline) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}
