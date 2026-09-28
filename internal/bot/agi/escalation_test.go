package agi

import (
	"strings"
	"testing"
	"time"
)

// There was a gap, found by reading the code rather than by watching the bot:
// the two models never spoke to each other, and when the big one was consulted
// it was told the present moment and nothing else. A bot asked "what do you
// want to do" while holding a plan it knew nothing about answers as if it had no
// plan — and a viewer watching the body do one thing while the mouth says
// another concludes the bot is broken.
//
// These tests pin the fix on both sides: the fast model opens the door, and
// when it does the slow model is handed the whole picture.

func testSnap() Snapshot {
	v := NewVocabulary()
	v.NoteBlock("netherrack")
	v.NoteBlock("netherrack")
	v.NoteBlock("copper_ore")
	v.NoteItem("copper_ingot")

	return Snapshot{
		Now:         time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC),
		HP:          18,
		Hunger:      15,
		Coords:      "X:10 Y:64 Z:-20",
		HeldItem:    "copper_pickaxe",
		VisibleMob:  "zombie, skeleton",
		NearBlocks:  "netherrack, copper_ore",
		GoalSummary: "stock_up",
		PlanSummary: "Objective: gather copper\n  > 1. [mine] dig the copper seam @ 10,64,-20",
		EpisodeText: "episode 1: build a house. 18 min of recording time left.",
		Vocabulary:  v,
	}
}

// TestTheBigModelIsToldWhatTheBotIsDoing is the regression. This is the exact
// gap that was found: the plan, the goal, the mobs, the blocks and the clock
// were all real and all absent from the prompt.
func TestTheBigModelIsToldWhatTheBotIsDoing(t *testing.T) {
	t.Parallel()

	_, prompt := (&Runner{}).buildDecisionPrompt(testSnap())

	for _, want := range []string{
		"gather copper", // the plan
		"stock_up",      // the goal
		"netherrack",    // what this world is made of
		"copper_ore",
		"zombie", // what is in it
		"18 min", // how much recording time is left
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the decision prompt does not mention %q:\n%s", want, prompt)
		}
	}
}

// TestThePlanTellsTheModelToGetOutOfTheWay. Showing a model a plan without
// telling it to respect one produces a bot that proposes a fresh task every
// tick while its body is three steps into something else.
func TestThePlanTellsTheModelToGetOutOfTheWay(t *testing.T) {
	t.Parallel()

	_, prompt := (&Runner{}).buildDecisionPrompt(testSnap())
	if !strings.Contains(prompt, "IKUTI") {
		t.Errorf("the prompt shows a plan without telling the model to follow it:\n%s", prompt)
	}
}

// TestTheModelIsToldNotToInventActions. The action list is survival-shaped, so
// on a server with different blocks in it half those actions cannot work. The
// prompt says so explicitly, because a model handed a menu it cannot use will
// pick from it anyway.
func TestTheModelIsToldNotToInventActions(t *testing.T) {
	t.Parallel()

	_, prompt := (&Runner{}).buildDecisionPrompt(testSnap())
	if !strings.Contains(prompt, "jangan karang aksi") {
		t.Errorf("the prompt does not tell the model to refuse actions the world cannot support:\n%s", prompt)
	}
}

// TestAPromptWithNothingGoingOnStaysShort. Blocks that render nothing when there
// is nothing to render are the difference between a prompt that says "no plan"
// and a prompt that stays silent about it.
func TestAPromptWithNothingGoingOnStaysShort(t *testing.T) {
	t.Parallel()

	_, prompt := (&Runner{}).buildDecisionPrompt(Snapshot{
		Now: time.Now(), HP: 20, Hunger: 20, Coords: "X:0 Y:64 Z:0",
	})
	if strings.Contains(prompt, "yang lagi bot kerjain") {
		t.Errorf("the prompt announced an empty plan:\n%s", prompt)
	}
	if strings.Contains(prompt, "Isi dunia ini") {
		t.Errorf("the prompt announced an empty world:\n%s", prompt)
	}
}

// TestTheFastModelDecidesWhoGetsTheExpensiveOne. Jev never spends the big model
// on its own judgement about whether the big model is needed; it hands back a
// probability and the program does the cutting.
func TestTheFastModelDecidesWhoGetsTheExpensiveOne(t *testing.T) {
	t.Parallel()

	threshold := escalateThreshold

	// No answer at all means no, even if the threshold were zero. A missing
	// answer must never read as a confident yes — that would spend the
	// expensive tier on every tick of a session where Jev is down.
	if WantsBigBrain(Judgement{Escalate: 1.0}, threshold) {
		t.Error("escalated on a judgement that never came")
	}
	if !WantsBigBrain(Judgement{Escalate: 0.99, Known: true}, threshold) {
		t.Error("a near-certain escalation was refused")
	}
	if WantsBigBrain(Judgement{Escalate: 0.2, Known: true}, threshold) {
		t.Error("escalated on a low probability")
	}
}

// TestTheEscalationCarriesTheSamePictureThePromptDoes. Two code paths building
// two different pictures of the same moment is how the fast model's decision and
// the slow model's answer drift apart.
func TestTheEscalationCarriesTheSamePictureThePromptDoes(t *testing.T) {
	t.Parallel()

	snap := testSnap()
	plan := Plan{
		Objective: "gather copper",
		Steps:     []PlanStep{{Kind: StepMine, Description: "dig the copper seam", State: StepActive}},
	}

	ctx := buildEscalationContext(snap, plan)
	prompt := ctx.Prompt()

	for _, want := range []string{
		"gather copper", // the plan
		"netherrack",    // the world
		"18 min",        // the clock
		"episode 1",     // the recording
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the escalation prompt does not carry %q:\n%s", want, prompt)
		}
	}
	// It is not a task list. Asking a big model to think and then handing it a
	// menu gets a menu back instead of a thought.
	if strings.Contains(prompt, "PILIHAN:") {
		t.Errorf("the escalation prompt handed the model a menu to pick from:\n%s", prompt)
	}
}

// TestDoingNothingSurvivesTheEscalation. The expensive tier is the one most
// likely to fill silence with something, and a bot that is asked to think
// every few minutes and is never told that nothing is an answer will invent
// something every few minutes.
func TestDoingNothingSurvivesTheEscalation(t *testing.T) {
	t.Parallel()

	prompt := buildEscalationContext(testSnap(), Plan{}).Prompt()
	if !strings.Contains(prompt, "doing nothing is a real answer") {
		t.Errorf("the escalation prompt does not say that doing nothing is allowed:\n%s", prompt)
	}
}
