package affordance_test

import (
	"testing"

	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/bot/affordance"
)

// Every verb the catalogue promises must actually resolve in the registry.
//
// This exists because the hand-maintained label list it replaced drifted twenty
// labels behind reality without anything noticing, and because the catalogue is
// hand-maintained too. A verb the model is offered and the body cannot perform
// is worse than a verb the model is never offered: the bot says it did something
// and did not.

// TestEveryOfferedVerbIsReallyRegistered is the drift check.
func TestEveryOfferedVerbIsReallyRegistered(t *testing.T) {
	t.Parallel()

	registered := action.SupportedLabels()
	if missing := affordance.Check(registered); len(missing) > 0 {
		t.Errorf("the catalogue promises verbs the registry does not resolve: %v", missing)
	}
}

// TestTheCatalogueIsNotEmpty guards the opposite failure: a catalogue that
// silently lost its contents still satisfies every check above, and the bot is
// left with nothing to choose from.
func TestTheCatalogueIsNotEmpty(t *testing.T) {
	t.Parallel()

	if len(affordance.Catalogue) < 10 {
		t.Errorf("catalogue has %d verbs, want a usable set", len(affordance.Catalogue))
	}
}

// TestEveryVerbIsDescribed keeps the model from being handed bare names. A list
// of 169 labels with no effect attached is the flat menu this work replaced.
func TestEveryVerbIsDescribed(t *testing.T) {
	t.Parallel()

	for _, v := range affordance.Catalogue {
		if v.Summary == "" {
			t.Errorf("verb %q has no description; the model would be choosing a bare name", v.Label)
		}
	}
}

// TestLabelsAreUnique catches a copy-paste duplicate, which would silently make
// one verb reachable under two names with only one described.
func TestLabelsAreUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, len(affordance.Catalogue))
	for _, v := range affordance.Catalogue {
		if seen[v.Label] {
			t.Errorf("verb %q appears twice in the catalogue", v.Label)
		}
		seen[v.Label] = true
	}
}

// TestEveryActionKindVerbIsReallyRegistered checks the direction that can kill
// the bot: an entry marked Action promises a registry label, and a label the
// registry cannot resolve is a promise the bot breaks in front of the player.
func TestEveryActionKindVerbIsReallyRegistered(t *testing.T) {
	t.Parallel()

	registered := action.SupportedLabels()
	for _, v := range affordance.Catalogue {
		if v.Kind != affordance.Action {
			continue
		}
		if _, ok := registered[v.Label]; !ok {
			t.Errorf("verb %q is marked Action but the registry cannot resolve it", v.Label)
		}
	}
}

// TestActivitiesAreDispatchedByTheBrainNotTheRegistry records the overlap that
// made the Kind distinction worth having in the first place.
//
// "explore", "look" and "attack" exist in BOTH vocabularies: the brain runs an
// activity of that name, and the registry also resolves a label of that name.
// They are marked Activity deliberately — the brain owns the behaviour and
// decides when it fires, which is the opposite of a model picking a one-shot
// label. A test asserting no Activity name appears in the registry would be
// asserting a falsehood about how this codebase is actually built.

// TestBothKindsArePresent keeps the catalogue from collapsing into one
// mechanism. A bot that can only act, or can only idle, is not a bot.
func TestBothKindsArePresent(t *testing.T) {
	t.Parallel()

	var activities, actions int
	for _, v := range affordance.Catalogue {
		if v.Kind == affordance.Activity {
			activities++
		} else {
			actions++
		}
	}
	if activities == 0 || actions == 0 {
		t.Errorf("catalogue has %d activities and %d actions; both kinds are required", activities, actions)
	}
}
