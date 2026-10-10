package chat_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/chat"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/memory"
)

// newAdminBot is the smallest bot the '!' layer works on: one linked player,
// an in-memory store, and a discarding logger. The memory path is empty so a
// test never touches the real file.
func newAdminBot() *bot.Bot {
	b := &bot.Bot{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:                "Luna",
		ConfiguredName:      "Luna",
		RecentBotMessages:   make(map[string]time.Time),
		RecentStatusReports: make(map[string]time.Time),
	}
	b.AiCfg = config.AIConfig{MainPlayers: []string{"Synterix"}}
	b.Memory = memory.New("")
	return b
}

// TestTheAdminLayerBelongsToMainPlayers is the gate. Every verb in the '!' map
// is operator territory — !say speaks as the bot, !remember writes what it
// believes, !stop cancels its work, !cmd runs a command through its connection
// — so a stranger reaching any one of them could puppet it and poison it. The
// gate sits once, at the layer, and refuses the whole surface; the memory store
// is what makes the refusal observable.
func TestTheAdminLayerBelongsToMainPlayers(t *testing.T) {
	t.Parallel()

	b := newAdminBot()

	chat.HandleAdminCommand(b, "!remember jangan rusak rumah orang", "Budi")
	if got := len(b.Memory.Facts()); got != 0 {
		t.Fatalf("a stranger wrote a memory: %d facts stored", got)
	}

	chat.HandleAdminCommand(b, "!remember jangan rusak rumah orang", "Synterix")
	facts := b.Memory.Facts()
	if len(facts) != 1 {
		t.Fatalf("the operator's memory was not stored: %v", facts)
	}
	if facts[0].Text != "jangan rusak rumah orang" {
		t.Errorf("stored %q, want the text that was dictated", facts[0].Text)
	}
}

// TestWhitelistedIsNotOperator keeps the two lists from melting together. The
// whitelist widens who the bot talks to; it must not widen who commands it.
func TestWhitelistedIsNotOperator(t *testing.T) {
	t.Parallel()

	b := newAdminBot()
	b.WhitelistPlayer("Budi")

	chat.HandleAdminCommand(b, "!remember halo", "Budi")
	if len(b.Memory.Facts()) != 0 {
		t.Fatal("a whitelisted player reached the admin layer — the whitelist became an operator grant")
	}
}

// TestNoOperatorsMeansTheLayerIsClosed keeps the gate honest when the config
// names nobody: an empty list must close the surface, not open it to whoever
// happens to be standing there.
func TestNoOperatorsMeansTheLayerIsClosed(t *testing.T) {
	t.Parallel()

	b := newAdminBot()
	b.AiCfg = config.AIConfig{}

	chat.HandleAdminCommand(b, "!remember halo", "SiapaPun")
	if len(b.Memory.Facts()) != 0 {
		t.Fatal("an unconfigured bot still accepted operator commands")
	}
}
