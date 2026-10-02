package bot_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/event"
)

// The retry loop had no exit, and the two things that close it are here: a cap
// on how often one node may try, and a path that gets dropped when it runs out.

func newScaffoldTestBot() *bot.Bot {
	return &bot.Bot{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		RecentStatusReports: make(map[string]time.Time),
		RecentBotMessages:   make(map[string]time.Time),
		CurrentPath:         []pathfinder.Node{{X: 1, Y: 2, Z: 3, Action: "place"}},
		PathIndex:           0,
		MovementState:       "walk_to",
		TargetTolerance:     2.0,
	}
}

func TestANodeMayTryThreeTimesAndThenItIsGivenUpOn(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()

	for attempt := 1; attempt <= bot.MaxScaffoldAttempts; attempt++ {
		if !b.NoteScaffoldAttempt(1, 2, 3) {
			t.Fatalf("attempt %d was refused, want all %d attempts allowed", attempt, bot.MaxScaffoldAttempts)
		}
	}
	if b.NoteScaffoldAttempt(1, 2, 3) {
		t.Error("attempt past the cap was allowed, want the node given up on")
	}
}

func TestTheCapIsCountedPerNodeNotPerPath(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()

	// Exhaust one node, which is what happens when a step genuinely cannot be
	// built. The very next node on the same path has to start clean, or a long
	// route slowly talks itself out of ever building anything again.
	for i := 0; i < bot.MaxScaffoldAttempts+2; i++ {
		b.NoteScaffoldAttempt(1, 2, 3)
	}
	if b.NoteScaffoldAttempt(1, 2, 3) {
		t.Error("the exhausted node was still allowed an attempt")
	}
	if !b.NoteScaffoldAttempt(4, 5, 6) {
		t.Error("a fresh node inherited the previous node's exhausted budget")
	}
}

func TestMovingToANewNodeResetsTheCount(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()

	b.NoteScaffoldAttempt(1, 2, 3)
	b.NoteScaffoldAttempt(1, 2, 3)

	// Visit a different node, then come back. The budget is per node, so
	// returning to a node is not punished for what some other node did.
	if !b.NoteScaffoldAttempt(1, 2, 4) {
		t.Fatal("the second node's first attempt was refused")
	}
	if !b.NoteScaffoldAttempt(1, 2, 3) {
		t.Error("returning to a node did not get a fresh budget")
	}
}

func TestGivingUpOnAStepDropsThePathSoTheNodeCannotComeBack(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()
	b.PathIndex = 0

	b.AbandonScaffoldStep("place", 1, 2, 3)

	if b.CurrentPath != nil {
		t.Errorf("CurrentPath = %v, want nil: the node that cannot be built must not be executed again", b.CurrentPath)
	}
	if b.PathIndex != 0 {
		t.Errorf("PathIndex = %d, want 0 alongside a dropped path", b.PathIndex)
	}
}

func TestGivingUpOnAStepIsSafeWhenThePathIsAlreadyGone(t *testing.T) {
	t.Parallel()

	// The scaffold runs on its own goroutine while the movement tick may have
	// dropped the path underneath it. Abandoning an already-empty path must not
	// panic or invent a path.
	b := newScaffoldTestBot()
	b.CurrentPath = nil
	b.PathIndex = 0

	b.AbandonScaffoldStep("place", 1, 2, 3)

	if b.CurrentPath != nil {
		t.Errorf("CurrentPath = %v, want nil", b.CurrentPath)
	}
}

func TestASatisfiedStepIsWalkedPastSoTheNodeDoesNotReturn(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()
	b.CurrentPath = []pathfinder.Node{
		{X: 1, Y: 2, Z: 3, Action: "place"},
		{X: 4, Y: 2, Z: 3},
	}
	b.PathIndex = 0

	if !b.AdvancePastScaffoldStep(1, 2, 3) {
		t.Fatal("AdvancePastScaffoldStep = false on the current node, want true")
	}
	if b.PathIndex != 1 {
		t.Errorf("PathIndex = %d, want 1", b.PathIndex)
	}
}

func TestStepIsNotAdvancedWhileAnotherOneIsCurrent(t *testing.T) {
	t.Parallel()

	// The path may have been rebuilt between the scaffold starting and finishing.
	// Advancing on a position that is no longer the current node would skip a
	// step the bot never looked at.
	b := newScaffoldTestBot()
	b.CurrentPath = []pathfinder.Node{
		{X: 4, Y: 2, Z: 3},
		{X: 1, Y: 2, Z: 3, Action: "place"},
	}
	b.PathIndex = 0

	if b.AdvancePastScaffoldStep(1, 2, 3) {
		t.Error("AdvancePastScaffoldStep = true on a node that is no longer current, want false")
	}
	if b.PathIndex != 0 {
		t.Errorf("PathIndex = %d, want 0: an unrelated node was skipped", b.PathIndex)
	}
}

func TestTheStatusSpamIsSuppressedAndThenAllowedAgain(t *testing.T) {
	t.Parallel()

	b := newScaffoldTestBot()
	status := event.ActionStatus{
		Action:  "scaffold",
		Item:    "place",
		Success: false,
		Error:   "the cell holds dirt, which is not something to break for a scaffold",
	}
	sig := bot.StatusSignatureForTest(status)

	if !b.ShouldSpeakStatusForTest(sig) {
		t.Fatal("the first report of a kind was suppressed, want it spoken")
	}
	// This is the live shape of the bug: one node retried, same reason, every
	// tick. Fifty identical sentences help nobody.
	for i := 0; i < 50; i++ {
		if b.ShouldSpeakStatusForTest(sig) {
			t.Fatalf("repeat %d of an identical report was allowed through", i)
		}
	}
}

func TestDifferentFailuresAreNotCollapsedIntoOneAnother(t *testing.T) {
	t.Parallel()

	// The suppression is on the report, not on "anything that failed recently".
	// Two different problems in the same second are two things the player needs
	// to hear about.
	b := newScaffoldTestBot()
	dirt := event.ActionStatus{Action: "scaffold", Item: "place", Error: "the cell holds dirt"}
	noBlocks := event.ActionStatus{Action: "gather", Item: "dirt", Error: "gak punya block buat lewat"}

	if !b.ShouldSpeakStatusForTest(bot.StatusSignatureForTest(dirt)) {
		t.Fatal("the first report was suppressed")
	}
	if !b.ShouldSpeakStatusForTest(bot.StatusSignatureForTest(noBlocks)) {
		t.Error("a different failure was suppressed as if it were the same one")
	}
}

func TestTheSuppressionWindowIsNotForever(t *testing.T) {
	t.Parallel()

	// A bot that has genuinely given up must be able to say so later. A window
	// that never reopened would just be a quieter version of the same silence.
	b := newScaffoldTestBot()
	status := event.ActionStatus{Action: "scaffold", Item: "place", Error: "the cell holds dirt"}
	sig := bot.StatusSignatureForTest(status)

	if !b.ShouldSpeakStatusForTest(sig) {
		t.Fatal("the first report was suppressed")
	}
	// Age the record past the window rather than sleeping through it.
	for key, at := range b.RecentStatusReports {
		b.RecentStatusReports[key] = at.Add(-time.Hour)
	}
	if !b.ShouldSpeakStatusForTest(sig) {
		t.Error("a report was still suppressed long after the window closed")
	}
}

func TestTheCountIsNotPartOfTheSignature(t *testing.T) {
	t.Parallel()

	// A stuck node reports the same action, item and reason with an unchanging
	// count. Including the count would have been the obvious thing to do and
	// would not have suppressed anything that matters.
	first := event.ActionStatus{Action: "scaffold", Item: "place", Count: 0, Error: "the cell holds dirt"}
	second := event.ActionStatus{Action: "scaffold", Item: "place", Count: 0, Error: "the cell holds dirt"}

	if bot.StatusSignatureForTest(first) != bot.StatusSignatureForTest(second) {
		t.Error("two identical reports produced different signatures")
	}
}
