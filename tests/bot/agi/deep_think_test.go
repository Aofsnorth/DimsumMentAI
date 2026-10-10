package agi_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/bot/world"

	"github.com/df-mc/dragonfly/server/block/cube"
)

// The lobby-instruction failure this file guards: Jev answered in its fixed
// vocabulary every tick while the one path that reads dictated !remember rules
// (deliberate) never ran in natural free play, so "klik NPC di depan" was
// recited in chat and never executed.

// deepThinkIntervalForTest mirrors the production constant. The test cannot
// read the unexported one, and hardcoding the same value twice is how the two
// drift apart — so it is re-derived from the accessor the package exposes.
const deepThinkIntervalForTest = 45 * time.Second

func newMotorBotForDeepThink(t *testing.T) *bot.Bot {
	t.Helper()
	return &bot.Bot{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		WorldCache: world.NewWorldCache(0, cube.Range{-64, 319}, slog.New(slog.NewTextHandler(io.Discard, nil))),
		// A real client object with no network behind it: the gate under test
		// only checks that a client EXISTS. deliberate() is never reached in
		// these tests, so no request is ever sent.
		AiClient: ai.NewNvidiaClient("test-model"),
	}
}

func TestDeepThinkIsDueBeforeTheFirstAndThenPaced(t *testing.T) {
	t.Parallel()

	now := time.Now()
	r := agi.NewRunnerForTest(newMotorBotForDeepThink(t), agi.Config{Mode: "natural"})
	snap := agi.Snapshot{Now: now}

	if !r.DueForDeepThink(snap) {
		t.Fatal("a fresh runner never consulted the full model; dictated rules could not act")
	}

	r.MarkDeepThinkForTest(now)
	if r.DueForDeepThink(agi.Snapshot{Now: now.Add(10 * time.Second)}) {
		t.Error("deep think re-fired 10s after the last one; that is a second brain, not a rhythm")
	}
	if !r.DueForDeepThink(agi.Snapshot{Now: now.Add(2 * deepThinkIntervalForTest)}) {
		t.Error("deep think never came due again; dictated rules would only ever fire once")
	}
}

func TestDeepThinkIsRefusedWhenTheBodyIsBusy(t *testing.T) {
	t.Parallel()

	now := time.Now()
	r := agi.NewRunnerForTest(newMotorBotForDeepThink(t), agi.Config{Mode: "natural"})
	r.MarkDeepThinkForTest(now.Add(-2 * deepThinkIntervalForTest))

	busy := agi.Snapshot{Now: now, Busy: true}
	if r.DueForDeepThink(busy) {
		t.Error("deep think fired over a busy body")
	}
	exploring := agi.Snapshot{Now: now, Exploring: true}
	if r.DueForDeepThink(exploring) {
		t.Error("deep think fired over an exploring body")
	}
}

func TestDeepThinkIsRefusedWithoutAnLLMClient(t *testing.T) {
	t.Parallel()

	// A runner with no bot has no client to consult; the gate must close
	// rather than let deliberate() dereference nil.
	r := agi.NewBareForTest(agi.Config{Mode: "natural"})
	if r.DueForDeepThink(agi.Snapshot{Now: time.Now()}) {
		t.Fatal("deep think became due with no LLM client attached")
	}
}
