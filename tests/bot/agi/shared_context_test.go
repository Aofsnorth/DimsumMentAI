package agi_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot/agi"
)

// Fast regression: both reasoning tiers consume the same bounded player history.
func TestSharedPlayerContext(t *testing.T) {
	t.Parallel()
	r := newGoalRunner(t, "", 0)
	b := r.Bot()
	b.LastChatPartner = "Alex"
	b.AiClient = &ai.NvidiaClient{History: ai.NewMessageHistory(20)}
	b.AiClient.History.AddMessage("Other", "user", "private-other-thread")
	b.AiClient.History.AddMessage("Alex", "user", "jangan pergi dulu")
	b.AiClient.History.AddMessage("Alex", "assistant", "Aku tunggu di sini.")
	b.AiClient.History.AddMessage("Alex", "user", "<Alex> ACTION RESULT #42")
	snap := testSnap()
	snap.Conversation = r.ConversationContext()
	snap.Inventory = "iron_ingot x3"
	snap.EpisodeText = "prepare supplies"
	for _, prompt := range []string{agi.DescribeState(snap), r.PlannerMessageFor(snap), agi.BuildEscalationContext(snap, agi.Plan{}).Prompt()} {
		for _, want := range []string{"jangan pergi dulu", "Aku tunggu di sini.", "iron_ingot x3", "prepare supplies"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("context missing %q", want)
			}
		}
		for _, forbidden := range []string{"private-other-thread", "ACTION RESULT #42"} {
			if strings.Contains(prompt, forbidden) {
				t.Errorf("context leaked %q", forbidden)
			}
		}
	}
}

func TestChatIntentTracksCanonicalPlan(t *testing.T) {
	t.Parallel()
	r := newGoalRunner(t, "prepare for the Ender Dragon", 0)
	agi.InstallEpisodeHooks(r.Bot(), r)
	r.SetPlan(agi.Plan{Objective: "gather supplies"})
	first := r.Bot().AutonomyContextFunc()
	if !strings.Contains(first, "prepare for the Ender Dragon") || !strings.Contains(first, "gather supplies") {
		t.Fatalf("missing shared intent: %s", first)
	}
	r.SetPlan(agi.Plan{Objective: "craft tools"})
	if !strings.Contains(r.Bot().AutonomyContextFunc(), "craft tools") {
		t.Fatal("chat intent used stale plan")
	}
}

func TestNaturalDeliberationGate(t *testing.T) {
	t.Parallel()
	r := newGoalRunner(t, "", 0)
	if r.ShouldPlanNaturally(agi.Judgement{}) {
		t.Fatal("idle free play must not call planner")
	}
	if !r.ShouldPlanNaturally(agi.Judgement{Known: true, Escalate: 0.99}) {
		t.Fatal("Jev escalation ignored")
	}
	r.SetPlan(agi.Plan{Objective: "gather supplies"})
	if !r.ShouldPlanNaturally(agi.Judgement{}) {
		t.Fatal("active plan lost control")
	}
	goal := newGoalRunner(t, "prepare for the Ender Dragon", 0)
	if !goal.ShouldPlanNaturally(agi.Judgement{}) {
		t.Fatal("standing goal ignored in natural mode")
	}
}
