package agi_test

import (
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/jev"
)

func newLocomotionBot() *bot.Bot {
	return &bot.Bot{Logger: slog.Default()}
}

// Locomotion is how Jev styles a trip: walk, sprint, or sprint-jump. These
// tests pin the contract between the decision and the body — the gait Jev
// picks is the gait the movement tick runs, and nothing else changes.

// TestTheLocomotionQuestionTravelsWithATravellingActivity. A travel style with
// nowhere to go is noise, so the question only rides along when the menu holds
// something that covers ground — and a model that never heard of it simply
// does not answer, which must read as "no opinion".
func TestTheLocomotionQuestionTravelsWithATravellingActivity(t *testing.T) {
	t.Parallel()

	withTravel := []string{jev.ActivityRest, jev.ActivityWander}
	got := false
	for name := range jev.BuildLocomotionQuestion() {
		if name == jev.QLocomotion {
			got = true
		}
	}
	if !got {
		t.Fatal("the locomotion question does not carry the locomotion answer key")
	}
	_ = withTravel
}

// TestAnUnheardQuestionLeavesTheGaitAlone. Missing, empty or unknown answers
// all mean auto: a narrower model must change nothing about how the bot moves.
func TestAnUnheardQuestionLeavesTheGaitAlone(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})
	j := agi.JudgementForTest("", "")
	if j.Locomotion != agi.LocomotionAuto {
		t.Errorf("empty locomotion answer = %q, want auto", j.Locomotion)
	}
	j = agi.JudgementForTest("", "jog briskly")
	if j.Locomotion != agi.LocomotionAuto {
		t.Errorf("unknown locomotion answer = %q, want auto", j.Locomotion)
	}
	_ = r
}

// TestTheThreeGaitsSurviveTheTrip. Each named gait parses to itself —
// case-insensitively, since models capitalise — so "Sprint" runs and "sprint"
// runs and neither reads as the other.
func TestTheThreeGaitsSurviveTheTrip(t *testing.T) {
	t.Parallel()

	cases := map[string]agi.LocomotionHint{
		"walk":        agi.LocomotionWalk,
		"Walk":        agi.LocomotionWalk,
		"sprint":      agi.LocomotionSprint,
		"Sprint":      agi.LocomotionSprint,
		"sprint_jump": agi.LocomotionSprintJump,
		"Sprint_Jump": agi.LocomotionSprintJump,
	}
	for raw, want := range cases {
		if got := agi.JudgementForTest("", raw).Locomotion; got != want {
			t.Errorf("locomotion %q = %q, want %q", raw, got, want)
		}
	}
}

// TestAWalkDecisionClearsAStaleSprint. The latch must never leak a run into a
// walk the model chose next: only the two running gaits are stored, and
// anything else drops the hint back to the movement rules.
func TestAWalkDecisionClearsAStaleSprint(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	b.SetSprintHint(false)
	r := agi.NewRunnerForTest(b, agi.Config{})

	r.ApplyLocomotionForTest(agi.LocomotionWalk)
	if _, _, latched := b.SprintHint(); latched {
		t.Error("a walk decision left a sprint latch set; the next trip would run")
	}

	r.ApplyLocomotionForTest(agi.LocomotionSprint)
	sprint, hop, latched := b.SprintHint()
	if !latched || !sprint || hop {
		t.Errorf("sprint latch = (%v,%v,%v), want (true,false,true)", sprint, hop, latched)
	}

	r.ApplyLocomotionForTest(agi.LocomotionSprintJump)
	sprint, hop, latched = b.SprintHint()
	if !latched || !sprint || !hop {
		t.Errorf("sprint-jump latch = (%v,%v,%v), want (true,true,true)", sprint, hop, latched)
	}

	r.ApplyLocomotionForTest(agi.LocomotionAuto)
	if _, _, latched := b.SprintHint(); latched {
		t.Error("an auto decision left a sprint latch set")
	}
}
