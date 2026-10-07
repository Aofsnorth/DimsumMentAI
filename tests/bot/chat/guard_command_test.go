package chat_test

import (
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/chat"
	"bedrock-ai/internal/bot/combat"
	"bedrock-ai/internal/event"
)

// newGuardBot is the whitelist fixture plus the threat detector the toggle
// flips. The detector gets a nil combat manager because a parse/toggle test
// never scans — it only reads and writes the guard state.
func newGuardBot() *bot.Bot {
	b := newWhitelistBot()
	b.ThreatDet = combat.NewThreatDetector(b, nil, b.Logger)
	return b
}

// TestParseGuardIntentRecognisesTheStandingOrder drives the parser with the
// sentence the feature was asked for, verbatim, plus the shapes around it.
// The enable phrase contains "jangan didiemin" — the parser must read that as
// part of the order, not as a shutdown.
func TestParseGuardIntentRecognisesTheStandingOrder(t *testing.T) {
	t.Parallel()

	enables := []string{
		"Luna kalo ada mob jahat, bunuh aja ya, jangan didiemin",
		"kalo ada creeper, habisin aja",
		"luna guard",
		"guard mode on",
		"monster selalu kamu kill ya",
	}
	for _, m := range enables {
		if enable, ok := chat.ParseGuardIntentForTest(m); !ok || !enable {
			t.Errorf("%q was not read as a guard enable (ok=%v enable=%v)", m, ok, enable)
		}
	}

	disables := []string{
		"matiin guard",
		"guard off",
		"stop guard",
		"berhenti jaga",
		"jangan bunuh mobnya dong",
		"udah gak perlu jaga",
	}
	for _, m := range disables {
		if enable, ok := chat.ParseGuardIntentForTest(m); !ok || enable {
			t.Errorf("%q was not read as a guard disable (ok=%v enable=%v)", m, ok, enable)
		}
	}

	others := []string{
		"coba lompat",
		"halo luna",
		"kamu jangan diemin monster itu, ceritanya seru banget",
		"bantuin aku bunuh zombie ini", // one-off hunt: the AI's job, not a toggle
	}
	for _, m := range others {
		if _, ok := chat.ParseGuardIntentForTest(m); ok {
			t.Errorf("%q was mistaken for a guard toggle", m)
		}
	}
}

// TestGuardCommandIsMainPlayerOnly: a stranger cannot re-arm the bot.
func TestGuardCommandIsMainPlayerOnly(t *testing.T) {
	t.Parallel()

	b := newGuardBot()
	evt := event.ChatEvent{SourceName: "Budi", Message: "luna guard"}

	if !chat.HandleGuardCommandForTest(b, evt, evt.Message) {
		t.Fatal("the guard command was not recognised at all")
	}
	if on, _ := b.ThreatDet.GuardMode(); on {
		t.Fatal("a non-main player turned guard mode on")
	}
}

// TestGuardEnableThenDisableRoundTrip is the whole conversation the feature is
// for: the order arms the detector with the speaker as the protected player,
// and the shutdown disarms it. The nil AiClient makes both replies no-ops, so
// the state change is what the test sees.
func TestGuardEnableThenDisableRoundTrip(t *testing.T) {
	t.Parallel()

	b := newGuardBot()
	enable := event.ChatEvent{
		SourceName: "Synterix",
		Message:    "Luna kalo ada mob jahat, bunuh aja ya, jangan didiemin",
	}
	if !chat.HandleGuardCommandForTest(b, enable, enable.Message) {
		t.Fatal("the enable order was not recognised as a guard command")
	}
	if on, protector := b.ThreatDet.GuardMode(); !on || protector != "Synterix" {
		t.Fatalf("guard state after enable = %v %q, want on with Synterix", on, protector)
	}

	disable := event.ChatEvent{SourceName: "Synterix", Message: "matiin guard"}
	if !chat.HandleGuardCommandForTest(b, disable, disable.Message) {
		t.Fatal("the shutdown was not recognised as a guard command")
	}
	if on, protector := b.ThreatDet.GuardMode(); on || protector != "" {
		t.Fatalf("guard state after disable = %v %q, want off", on, protector)
	}
}

// TestGuardCommandWithoutThreatDetectorDoesNotPanic: a fixture or a config
// that never built the detector must still consume the command safely.
func TestGuardCommandWithoutThreatDetectorDoesNotPanic(t *testing.T) {
	t.Parallel()

	b := newWhitelistBot()
	evt := event.ChatEvent{SourceName: "Synterix", Message: "guard"}

	if !chat.HandleGuardCommandForTest(b, evt, evt.Message) {
		t.Fatal("the guard command was not recognised")
	}
}
