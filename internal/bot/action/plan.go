package action

import (
	"fmt"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
)

// statusTimeout bounds how long a plan step waits for its handler to say how it
// went. It is generous on purpose — a chest search or a stronghold sweep is
// real work — but it is a bound, because a handler that never reports is
// indistinguishable from one that is still working, and a plan must not hang
// on either. When it expires the step fails: silence is not a result.
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

	go func() {
		b.Logger.Debug("executing action plan", "steps", len(steps), "user", user)
		for _, step := range steps {
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
//
// It used to return a hard-coded success for every non-craft action once the
// bot had stopped moving, which told the planner that steps it could not
// perform — or that had failed — were done. The planner re-plans from these
// answers, so an optimistic one walks it further away from the goal on every
// retry. Now the verdict is the handler's own: a reported success is a success,
// a reported failure is a failure carrying its reason, and a handler that says
// nothing within the timeout fails the step rather than passing it.
//
// Craft keeps its structured path, which already returns the real outcome.
func ExecuteAndWait(b *bot.Bot, label, param, user string) event.ActionStatus {
	return executeAndWait(b, label, param, user, statusTimeout)
}

// executeAndWait is ExecuteAndWait with the report timeout passed in, so the
// expiry path can be tested without sitting out the production bound.
func executeAndWait(b *bot.Bot, label, param, user string, timeout time.Duration) event.ActionStatus {
	normalized := strings.ToLower(strings.TrimSpace(label))

	// Rest and wait are satisfied by standing still. That is the instruction,
	// not a shortcut around it, so it succeeds without dispatching anything and
	// without waiting for a report that will never come.
	if normalized == "" {
		return event.ActionStatus{Action: normalized, Success: true}
	}

	if normalized == "craft" {
		return executeCraftActionSilent(b, param, user)
	}

	// An unhandled label must fail loudly. Letting it through as a no-op
	// success is how a plan used to "complete" steps the bot never performed.
	if _, ok := lookupHandler(normalized); !ok {
		return event.ActionStatus{
			Action:  normalized,
			Success: false,
			Error:   fmt.Sprintf("unknown action label %q", normalized),
		}
	}

	// Subscribe before dispatching: handlers that report synchronously do so
	// from inside Execute, and a subscription installed afterwards would miss
	// the very report it is waiting for.
	statuses := subscribeStatus(b)
	defer unsubscribeStatus(b, statuses)

	Execute(b, normalized, param, user)

	// Still wait for the bot to settle before reading the verdict. A handler
	// that reported on dispatch (navigation, toggles) does so while the bot is
	// still moving, and the action is not done until the motion is.
	waitForActionSettled(b, normalized)

	select {
	case status := <-statuses:
		// Keep the requested label when a handler reports under a different one
		// (an alias such as "remember" reporting as "recall"), so the step the
		// planner asked for stays identifiable in its own transcript.
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
	case "come", "goto":
		waitForMovementIdle(b, 35*time.Second)
	case "gather", "mine", "automine", "loot", "clear", "scan":
		waitForGatheringIdle(b, 90*time.Second)
	case "craft":
		time.Sleep(900 * time.Millisecond)
	case "follow":
		time.Sleep(600 * time.Millisecond)
	default:
		time.Sleep(250 * time.Millisecond)
	}
}

func waitForMovementIdle(b *bot.Bot, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b.Mu.Lock()
		state := b.MovementState
		b.Mu.Unlock()
		if state == "idle" {
			return
		}
		time.Sleep(250 * time.Millisecond)
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
