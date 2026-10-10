package agi_test

import (
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/memory"
)

// A brain that is never shown what the operator dictated re-derives its own
// rules — which is how a bot on a server with rules behaves like a bot without
// any. The chat path already injected long-term memory into its prompts; the
// decision prompt, the one asked every tick in natural mode, did not. These
// tests pin memory into both halves of it.

func newMemoryBot() *bot.Bot {
	store := memory.New("")
	_, _ = store.Add("Rules server ini: jangan grief, jangan ambil isi chest orang.", "Synterix")

	_, _ = store.Add("NPC random teleport ada di kiri setelah sampai survival.", "Synterix")
	return &bot.Bot{
		Name:     "Luna",
		AiClient: ai.NewLLMClient("openai_compatible", "test-model", "http://127.0.0.1:9/v1"),
		Memory:   store,
	}
}

// TestTheBrainReadsTheDictatedRules is the wiring. The operator's rules ride in
// the system prompt (who the bot is) and the decision text (what to do now);
// either half missing them is half a bot that never learned the rules.
func TestTheBrainReadsTheDictatedRules(t *testing.T) {
	t.Parallel()

	system, prompt := agi.NewRunnerForTest(newMemoryBot(), agi.Config{}).BuildDecisionPrompt(agi.Snapshot{
		Now: time.Now(), HP: 20, Hunger: 20, Coords: "X:0 Y:64 Z:0",
	})
	for _, want := range []string{"[LONG-TERM MEMORY]", "jangan grief", "random teleport"} {
		if !strings.Contains(system, want) {
			t.Errorf("the system prompt dropped the dictated rule %q:\n%s", want, system)
		}
		if !strings.Contains(prompt, want) {
			t.Errorf("the decision prompt dropped the dictated rule %q:\n%s", want, prompt)
		}
	}
}

// TestAnEmptyMemoryAddsNothing. Blocks that render nothing when there is
// nothing to render keep the prompt honest about what the bot actually knows.
func TestAnEmptyMemoryAddsNothing(t *testing.T) {
	t.Parallel()

	b := newMemoryBot()
	b.Memory = memory.New("")

	system, prompt := agi.NewRunnerForTest(b, agi.Config{}).BuildDecisionPrompt(agi.Snapshot{
		Now: time.Now(), HP: 20, Hunger: 20, Coords: "X:0 Y:64 Z:0",
	})
	if strings.Contains(system, "[LONG-TERM MEMORY]") {
		t.Errorf("an empty store still produced a memory block in the system prompt:\n%s", system)
	}
	if strings.Contains(prompt, "[LONG-TERM MEMORY]") {
		t.Errorf("an empty store still produced a memory block in the decision text:\n%s", prompt)
	}
}

// TestABareRunnerStillBuildsAPrompt guards the nil path: the runner with no bot
// must keep producing the decision text it always did, memory or no memory.
func TestABareRunnerStillBuildsAPrompt(t *testing.T) {
	t.Parallel()

	_, prompt := agi.NewBareForTest(agi.Config{}).BuildDecisionPrompt(agi.Snapshot{
		Now: time.Now(), HP: 20, Hunger: 20, Coords: "X:0 Y:64 Z:0",
	})
	if prompt == "" {
		t.Fatal("the bare runner lost its decision text")
	}
}
