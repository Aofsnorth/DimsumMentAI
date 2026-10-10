package chat

import (
	"testing"
)

// The bot answered a question by retrying the failed job.
//
// A live run, in shape:
//
//	(player clicks a server NPC whose menu is broken; the click reports failure)
//	klik npc didepannya error, bisa nggak?
//	chat reply sending reply="Oke, aku coba klik lagi ya."
//	chat inferred action from intent (LLM forgot tag)
//	→ <interact:depan> fires again
//
// The player asked whether the NPC could still be clicked, not for another
// click. inferActionIntent keys on the verb in the message, and a question
// carries the same verb as a command — "bisa klik npc didepan gak?" and "klik
// npc didepan" differ by the interrogative alone, which substring matching
// never sees. isAffirmativeReply already guards the model's half of the
// fallback; this is the player's half, and the question is where it was open.

// TestAQuestionNeverBecomesAnAction is the reproduced input. Every phrasing a
// player uses to ask "can you?" must fall out of the fallback, because the
// honest answer to "can you?" is a sentence, not a retry.
func TestAQuestionNeverBecomesAnAction(t *testing.T) {
	t.Parallel()

	// A reply that would pass isAffirmativeReply, as the live run produced.
	willing := "Oke, aku coba lagi ya."

	for _, msg := range []string{
		"klik npc didepan gak?",       // the live input, with the mark
		"klik npc didepan gak",        // chat drops the "?"
		"bisa klik npc didepan nggak", // the particle, no mark
		"apa npcnya bisa diklik",      // leading interrogative
		"bisakah kamu klik npc itu",   // formal leading interrogative
	} {
		if steps := inferActionIntent(msg, willing); len(steps) != 0 {
			t.Errorf("%q produced %d actions: %+v — a question must be answered, not executed", msg, len(steps), steps)
		}
	}
}

// TestACommandKeepsItsAction guards the fix from over-firing. The question
// marker must not swallow the commands: the same verbs without the
// interrogative are exactly the requests the fallback exists to serve, and the
// staged check in buildChatSteps sits before this gate so routines are
// unaffected either way.
func TestACommandKeepsItsAction(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{
		"klik npc didepan",
		"klik npc yang ada didepan itu",
		"tekan tombolnya dong",
		"interact sama npc depan",
		"klik pintu yang bisa dibuka", // "bisa" mid-sentence modifies a noun
	} {
		steps := inferActionIntent(msg, "")
		if len(steps) != 1 {
			t.Fatalf("%q produced %d actions, want 1: %+v", msg, len(steps), steps)
		}
		if steps[0].Label != "interact" {
			t.Errorf("%q produced label %q, want interact", msg, steps[0].Label)
		}
	}
}

// TestIsQuestionMatchesByPosition pins the marker rules: the mark, the
// sentence-opening interrogatives, and the tail particles all read as
// questions; the same words elsewhere in a sentence do not.
func TestIsQuestionMatchesByPosition(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{
		"gak?",
		"kamu kemana tadi ga",
		"kok diem aja",
		"gimana caranya",
	} {
		if !isQuestion(msg) {
			t.Errorf("isQuestion(%q) = false, want true", msg)
		}
	}
	for _, msg := range []string{
		"gak ada apa-apa",
		// "apa kata ..." ("apa kata kita makan") is a suggestion idiom, but the
		// false-positive direction is harmless — a question verdict only
		// withholds the synthesised action, it never invents one — so the
		// leading-"apa" rule is left simple rather than special-cased.
		"jalan aja ga usah lari",
	} {
		if isQuestion(msg) {
			t.Errorf("isQuestion(%q) = true, want false", msg)
		}
	}
}
