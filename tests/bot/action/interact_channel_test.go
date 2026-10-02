package action_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/event"
)

// A step that timed out on work that had worked.
//
// The interact family reported through ReportActionStatus, which is the chat
// path: it told the player what happened and told the planner nothing. The step
// then sat out its full ninety-second timeout and failed — every time, including
// when the interaction had genuinely succeeded.
//
// These tests drive a real subscriber, because the whole defect is that the
// subscriber was never reached.

func newChatOnlyBot() *bot.Bot {
	return &bot.Bot{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		RecentBotMessages:   make(map[string]time.Time),
		RecentStatusReports: make(map[string]time.Time),
	}
}

// TestAChatReportedVerdictReachesTheStepSubscriber is the fix.
//
// It is deliberately routed the way the interact family routes it — through the
// bot's chat report — rather than through the action package's own ReportStatus,
// because that distinction is the entire bug.
func TestAChatReportedVerdictReachesTheStepSubscriber(t *testing.T) {
	t.Parallel()

	b := newChatOnlyBot()
	// The channel is the one SubscribeStatus hands back, not a fresh one: the
	// subscriber list is keyed by the channel it was given, so reading from a
	// different channel waits for a message that was delivered somewhere else.
	ch := action.SubscribeStatus(b)

	// This is the path the interact family used: the chat report, and nothing
	// else.
	b.ReportActionStatus("Arthenyxx", event.ActionStatus{
		Action: "interact", Item: "stone_button", Success: true,
	})

	select {
	case status := <-ch:
		if !status.Success || status.Item != "stone_button" {
			t.Errorf("subscriber got %+v, want the stone_button success", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the step subscriber never heard the verdict: " +
			"the step would sit out its ninety-second timeout and fail on work that had worked")
	}

	action.UnsubscribeStatus(b, ch)
}

// TestTheVerdictIsPublishedWithNoChatClientAtAll is the harder case and the one
// that actually bites. The narration path returns early without an AI client, so
// a bot that is running headless — a build server, a probe, anything without a
// player to talk to — used to publish nothing at all.
func TestTheVerdictIsPublishedWithNoChatClientAtAll(t *testing.T) {
	t.Parallel()

	b := newChatOnlyBot() // no AiClient
	ch := action.SubscribeStatus(b)

	b.ReportActionStatus("Arthenyxx", event.ActionStatus{
		Action: "interact", Success: false, Error: "no such block",
	})

	select {
	case status := <-ch:
		if status.Success || status.Error != "no such block" {
			t.Errorf("subscriber got %+v, want the failure", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a headless bot published no verdict at all")
	}

	action.UnsubscribeStatus(b, ch)
}

// TestPublishingDoesNotBlockTheReporter keeps the hook from becoming a new way to
// wedge a goroutine. Delivery is non-blocking and a subscriber that has already
// resolved must not be able to stall whatever is reporting.
func TestPublishingDoesNotBlockTheReporter(t *testing.T) {
	t.Parallel()

	b := newChatOnlyBot()
	ch := action.SubscribeStatus(b)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Nobody is reading. A second publish on an unread subscriber must not
		// block, or every subsystem that reports would eventually park here.
		for range 50 {
			b.ReportActionStatus("Arthenyxx", event.ActionStatus{Action: "interact", Success: true})
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("publishing blocked with an unread subscriber")
	}

	action.UnsubscribeStatus(b, ch)
}
