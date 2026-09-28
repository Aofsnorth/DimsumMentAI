package bot

import "testing"

// TestOnlyOneLoopOwnsUnpromptedSpeech is the whole point of the ownership rule.
// Two loops each deciding whether to talk, on different rule sets, is how a
// companion turns into a spammer.
func TestOnlyOneLoopOwnsUnpromptedSpeech(t *testing.T) {
	t.Parallel()

	b := &Bot{}
	if got := b.UnpromptedSpeechOwner(); got != SpeechOwnerNone {
		t.Fatalf("fresh bot owner = %q, want %q", got, SpeechOwnerNone)
	}
	if !b.ClaimUnpromptedSpeech(SpeechOwnerProactive) {
		t.Fatal("the first claim was refused; there was no incumbent")
	}
	if got := b.UnpromptedSpeechOwner(); got != SpeechOwnerProactive {
		t.Errorf("owner after proactive claimed = %q, want %q", got, SpeechOwnerProactive)
	}
	// A second claim from the same loop must be refused, so one loop cannot
	// spawn two copies of itself and talk to itself twice.
	if b.ClaimUnpromptedSpeech(SpeechOwnerProactive) {
		t.Error("a duplicate claim from the same owner was granted")
	}
}

// TestAGIOutranksProactiveWhateverTheOrder pins the outcome that matters: AGI
// ends up owning speech no matter which goroutine happens to start first. Both
// loops are launched as goroutines, so a first-come rule would make the result
// depend on scheduling.
func TestAGIOutranksProactiveWhateverTheOrder(t *testing.T) {
	t.Parallel()

	// Proactive first, then AGI arrives and takes over.
	b := &Bot{}
	b.ClaimUnpromptedSpeech(SpeechOwnerProactive)
	if !b.ClaimUnpromptedSpeech(SpeechOwnerAGI) {
		t.Error("AGI could not take speech ownership from proactive")
	}
	if got := b.UnpromptedSpeechOwner(); got != SpeechOwnerAGI {
		t.Errorf("owner = %q, want %q", got, SpeechOwnerAGI)
	}

	// AGI first, proactive arrives second and must be refused.
	b2 := &Bot{}
	if !b2.ClaimUnpromptedSpeech(SpeechOwnerAGI) {
		t.Fatal("AGI could not claim speech ownership")
	}
	if b2.ClaimUnpromptedSpeech(SpeechOwnerProactive) {
		t.Error("proactive took speech ownership from AGI; two brains can now talk")
	}
	if got := b2.UnpromptedSpeechOwner(); got != SpeechOwnerAGI {
		t.Errorf("owner = %q, want %q", got, SpeechOwnerAGI)
	}
}

// TestNobodyStealsFromNobody guards the degenerate claims: an empty owner must
// never be able to take the field, since that would let a misconfigured caller
// quietly take over speech.
func TestNobodyStealsFromNobody(t *testing.T) {
	t.Parallel()

	b := &Bot{}
	if b.ClaimUnpromptedSpeech(SpeechOwnerNone) {
		t.Error("an empty owner string was allowed to claim speech ownership")
	}
	if got := b.UnpromptedSpeechOwner(); got != SpeechOwnerNone {
		t.Errorf("owner = %q after a rejected claim, want %q", got, SpeechOwnerNone)
	}
}

// TestUnknownOwnerCannotDisplaceARealOne is the safety direction: a caller that
// is not a known loop has priority zero and must not be able to take speech
// away from one that is.
func TestUnknownOwnerCannotDisplaceARealOne(t *testing.T) {
	t.Parallel()

	b := &Bot{}
	b.ClaimUnpromptedSpeech(SpeechOwnerAGI)
	if b.ClaimUnpromptedSpeech("something-else") {
		t.Error("an unrecognised owner displaced the AGI brain")
	}
	if got := b.UnpromptedSpeechOwner(); got != SpeechOwnerAGI {
		t.Errorf("owner = %q, want %q", got, SpeechOwnerAGI)
	}
}
