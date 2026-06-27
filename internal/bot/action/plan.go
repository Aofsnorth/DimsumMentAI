package action

import (
	"strings"
	"time"

	"bedrock-ai/internal/bot"
)

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

// ExecuteAndWait runs a single action synchronously and blocks until the
// action has settled (movement idle, gathering complete, craft processed,
// etc.). Used by the planner's agentic loop so each step completes before
// re-evaluating with the LLM.
func ExecuteAndWait(b *bot.Bot, label, param, user string) {
	Execute(b, label, param, user)
	waitForActionSettled(b, strings.ToLower(strings.TrimSpace(label)))
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
