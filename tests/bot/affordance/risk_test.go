package affordance_test

import (
	"testing"

	"bedrock-ai/internal/bot/affordance"
	"bedrock-ai/internal/jev"
)

// The complaint the risk dial answers: the bot reacts to a creeper the same way
// every time, because a lookup table does not have a disposition.
//
// These tests pin the three properties that make it a disposition rather than
// another constant: it moves, it moves in the direction the name implies, and it
// defaults to the safe end when the model does not answer.

// TestAppetiteIsParsedFromTheModel is the wiring. The three names have to reach
// the three levels or the dial is decoration.
func TestAppetiteIsParsedFromTheModel(t *testing.T) {
	t.Parallel()

	cases := map[string]affordance.Appetite{
		jev.RiskCareful:  affordance.Careful,
		jev.RiskBold:     affordance.Bold,
		jev.RiskReckless: affordance.Reckless,
		"Careful":        affordance.Careful,
		"  BOLD  ":       affordance.Bold,
	}
	for raw, want := range cases {
		if got := affordance.ParseAppetite(raw); got != want {
			t.Errorf("ParseAppetite(%q) = %v, want %v", raw, got, want)
		}
	}
}

// TestAnUnansweredRiskQuestionIsCareful is the safety property, and it is why
// the default is not "bold".
//
// This is the one question where guessing wrong can kill the bot: a model that
// has never heard of it answers nothing, and "nothing" must never be read as
// permission to be reckless.
func TestAnUnansweredRiskQuestionIsCareful(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "   ", "some nonsense", "yes", "true"} {
		if got := affordance.ParseAppetite(raw); got != affordance.Careful {
			t.Errorf("ParseAppetite(%q) = %v, want careful: silence must never be read as permission", raw, got)
		}
	}
}

// TestTheThreeTemperamentsReactDifferentlyToACreeper is the whole point. If
// these plans were the same, the dial would be a label and not a disposition.
func TestTheThreeTemperamentsReactDifferentlyToACreeper(t *testing.T) {
	t.Parallel()

	careful := affordance.CreeperPlan(2.0, affordance.Careful)
	bold := affordance.CreeperPlan(2.0, affordance.Bold)
	reckless := affordance.CreeperPlan(2.0, affordance.Reckless)

	if !careful.Flee {
		t.Error("a careful bot did not back off a creeper inside its blast radius")
	}
	if !bold.Flee {
		t.Error("a bold bot stood at two blocks from a creeper")
	}
	if reckless.Flee {
		t.Error("a reckless bot ran from a creeper: the whole joke is that it does not")
	}
	if !reckless.HoldGround {
		t.Error("a reckless bot did not hold its ground")
	}
}

// TestTheCarefulBotLeavesBeforeTheBoldOne pins the direction of the dial.
// Getting this backwards — a reckless bot retreating further than a careful one
// — would be worse than having no dial at all, because it would look intentional.
func TestTheCarefulBotLeavesBeforeTheBoldOne(t *testing.T) {
	t.Parallel()

	careful := affordance.CreeperPlan(5.0, affordance.Careful)
	bold := affordance.CreeperPlan(5.0, affordance.Bold)

	if careful.SafeDistance <= bold.SafeDistance {
		t.Errorf("careful keeps %v blocks, bold keeps %v: the careful bot should leave further out",
			careful.SafeDistance, bold.SafeDistance)
	}
}

// TestABlockBetweenTheBodyAndTheBlastIsTheComposedDefence is the answer to the
// case the whole layer was built for: "there is a creeper, put a block so the
// blast does not reach you".
//
// It is also the one branch that needs a block in hand, which is why it is a
// flag rather than unconditional behaviour — a bot with empty hands has nothing
// to put there.
func TestABlockBetweenTheBodyAndTheBlastIsTheComposedDefence(t *testing.T) {
	t.Parallel()

	plan := affordance.CreeperPlan(2.0, affordance.Careful)
	if !plan.BlockUp {
		t.Error("a careful bot inside the blast radius did not reach for a block")
	}
	if !plan.Flee {
		t.Error("blocking up is not a substitute for leaving: both are wanted")
	}
}

// TestFarAwayNobodyReacts is the control. A disposition that changes behaviour
// when nothing is happening is not a disposition, it is noise, and it would make
// the bot twitchy for no reason.
func TestFarAwayNobodyReacts(t *testing.T) {
	t.Parallel()

	for _, appetite := range []affordance.Appetite{affordance.Careful, affordance.Bold, affordance.Reckless} {
		plan := affordance.CreeperPlan(30.0, appetite)
		if plan.Flee || plan.BlockUp {
			t.Errorf("a %v bot reacted to a creeper thirty blocks away", appetite)
		}
	}
}
