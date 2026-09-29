package agi

import (
	"bedrock-ai/internal/ai"
	"strings"
	"testing"
)

// Fast regression: both reasoning tiers consume the same bounded player history.
func TestSharedPlayerContext(t *testing.T) {
	t.Parallel()
	r := newGoalRunner(t, "", 0)
	r.b.LastChatPartner = "Alex"
	r.b.AiClient = &ai.NvidiaClient{History: ai.NewMessageHistory(20)}
	r.b.AiClient.History.AddMessage("Other", "user", "private-other-thread")
	r.b.AiClient.History.AddMessage("Alex", "user", "jangan pergi dulu")
	r.b.AiClient.History.AddMessage("Alex", "assistant", "Aku tunggu di sini.")
	r.b.AiClient.History.AddMessage("Alex", "user", "<Alex> ACTION RESULT #42")
	snap := testSnap()
	snap.Conversation = r.conversationContext()
	snap.Inventory = "iron_ingot x3"
	snap.EpisodeText = "prepare supplies"
	for _, prompt := range []string{describeState(snap), r.plannerMessageFor(snap), buildEscalationContext(snap, Plan{}).Prompt()} {
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

func TestNaturalDeliberationGate(t *testing.T) {
	t.Parallel()
	r := newGoalRunner(t, "", 0)
	if r.shouldPlanNaturally(Judgement{}) {
		t.Fatal("idle free play must not call planner")
	}
	if !r.shouldPlanNaturally(Judgement{Known: true, Escalate: 0.99}) {
		t.Fatal("Jev escalation ignored")
	}
	r.plan = Plan{Objective: "gather supplies"}
	if !r.shouldPlanNaturally(Judgement{}) {
		t.Fatal("active plan lost control")
	}
	goal := newGoalRunner(t, "prepare for the Ender Dragon", 0)
	if !goal.shouldPlanNaturally(Judgement{}) {
		t.Fatal("standing goal ignored in natural mode")
	}
}
