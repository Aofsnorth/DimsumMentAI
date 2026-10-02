package agi_test

import (
	"encoding/json"
	"testing"

	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/jev"
)

// The affordance layer is only worth building if the model actually receives it.
//
// A package that derives a correct set, passes every test, and is never
// rendered into the question batch is the worst outcome available: it looks
// finished and changes nothing. These tests close that gap by reading the batch
// that would go to the server.

// TestTheAffordanceQuestionCarriesTheDerivedSet is the wiring. The batch must
// contain the affordance question, and its options must be the ones the world
// allows rather than the ones a programmer wrote down.
func TestTheAffordanceQuestionCarriesTheDerivedSet(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	batch := r.QuestionsForTest(agi.Snapshot{})
	raw, ok := batch[jev.QAffordance]
	if !ok {
		t.Fatal("the batch carries no affordance question: the derived set is computed and then dropped")
	}

	var question struct {
		Criteria map[string]string `json:"criteria"`
	}
	if err := json.Unmarshal(raw, &question); err != nil {
		t.Fatalf("the affordance question is not valid JSON: %v", err)
	}
	if len(question.Criteria) == 0 {
		t.Fatal("the affordance question carries no options: the model cannot choose from an empty set")
	}

	// The idle verbs have to be in there, or a bot with nothing to do is a bot
	// with nothing offered.
	for _, want := range []string{"rest", "wander"} {
		if _, ok := question.Criteria[want]; !ok {
			t.Errorf("%q is not offered; a bot with nothing to do must still be able to idle", want)
		}
	}
}

// TestEveryOfferedOptionIsDescribed stops the model being handed bare names,
// which is the flat list this work replaced.
func TestEveryOfferedOptionIsDescribed(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	batch := r.QuestionsForTest(agi.Snapshot{})
	raw := batch[jev.QAffordance]

	var question struct {
		Criteria map[string]string `json:"criteria"`
	}
	if err := json.Unmarshal(raw, &question); err != nil {
		t.Fatalf("the affordance question is not valid JSON: %v", err)
	}

	for label, summary := range question.Criteria {
		if summary == "" {
			t.Errorf("option %q carries no description; the model would be choosing a bare name", label)
		}
	}
}

// TestTheRiskQuestionIsAlwaysAsked is the disposition's wiring. A disposition
// only asked when something scary is on screen is a panic response with a
// longer fuse, not a personality.
func TestTheRiskQuestionIsAlwaysAsked(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	batch := r.QuestionsForTest(agi.Snapshot{})
	raw, ok := batch[jev.QRisk]
	if !ok {
		t.Fatal("the batch carries no risk question: the disposition can never move")
	}

	var question struct {
		Criteria map[string]string `json:"criteria"`
	}
	if err := json.Unmarshal(raw, &question); err != nil {
		t.Fatalf("the risk question is not valid JSON: %v", err)
	}
	for _, want := range []string{jev.RiskCareful, jev.RiskBold, jev.RiskReckless} {
		if _, ok := question.Criteria[want]; !ok {
			t.Errorf("the risk question does not offer %q; a dial with one position is a constant", want)
		}
	}
}

// TestEveryQuestionInTheBatchIsValidJSON is a guard on the whole assembly. A
// malformed question is silently dropped by the client, which would look exactly
// like a feature that is wired but never fires.
func TestEveryQuestionInTheBatchIsValidJSON(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	for name, raw := range r.QuestionsForTest(agi.Snapshot{}) {
		var probe map[string]any
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Errorf("question %q is not valid JSON and would be dropped silently: %v", name, err)
		}
	}
}

// TestTheRiskQuestionIsAskedOnBusyTicks guards a contradiction between a comment
// and the code.
//
// The disposition was documented as "asked on every tick, unprompted and
// ungated", and the code sent it after the busy early-return. Every tick the bot
// spent doing something — which is most of them — went without it, so a
// disposition could only ever move while the body was idle. That is precisely
// backwards: a risk dial that only answers when nothing is happening cannot
// answer when something does.
func TestTheRiskQuestionIsAskedOnBusyTicks(t *testing.T) {
	t.Parallel()

	b := newLocomotionBot()
	r := agi.NewRunnerForTest(b, agi.Config{})

	busy := agi.Snapshot{Busy: true, GoalSummary: "gather wood"}
	if _, ok := r.QuestionsForTest(busy)[jev.QRisk]; !ok {
		t.Error("no risk question on a busy tick: the disposition can only move while the bot is idle")
	}

	exploring := agi.Snapshot{Exploring: true, GoalSummary: "gather wood"}
	if _, ok := r.QuestionsForTest(exploring)[jev.QRisk]; !ok {
		t.Error("no risk question while exploring: a bot walking somewhere cannot choose to be careful")
	}
}
