package chat

import (
	"strings"
	"testing"
)

// The bot promised six logs and delivered none.
//
// A live run, verbatim:
//
//	Cariin aku 6 Oak Log lagi dung
//	chat reply sending reply="Siap, aku cari 6 Oak Log lagi."
//	Wood gathering skipped, already have enough have=6 target=6
//	Wood gathering finished collected=6 target=6 trees=0
//	status narration contradicted the server result server_success=true
//	  reply="Gak ada yang terkumpul, Oak Log-nya nol. Mau aku coba lagi?"
//
// Three failures stacked in one exchange. The player held six logs and asked for
// six MORE. "lagi" is the ordinary word for that, and the bot read the six as a
// total, decided it was already satisfied, felled nothing, reported six logs
// collected having collected zero, and then told the player there was nothing to
// be had while offering to try again.
//
// The count could not have reached the gatherer any other way. The tag grammar
// had one form, `<action>gather:item,N</action>`, and nothing in any prompt said
// whether N was a total or an increment. The model was not failing at its job; it
// had no way to say what it meant.

// TestAPlayerAskingForMoreGetsAnAdditiveCount is the reproduced input.
func TestAPlayerAskingForMoreGetsAnAdditiveCount(t *testing.T) {
	t.Parallel()

	steps := inferActionIntent("Cariin aku 6 Oak Log lagi dung", "")

	if len(steps) != 1 {
		t.Fatalf("expected one action from the failing message, got %d: %+v", len(steps), steps)
	}
	if got, want := steps[0].Param, "oak_log,+6"; got != want {
		t.Errorf("param = %q, want %q.\n"+
			"\tThe bot already holds 6. A bare 6 is a TOTAL, so it reads as already "+
			"satisfied and the bot does nothing at all.", got, want)
	}
}

// TestAPlayerAskingForATotalKeepsTheBareCount is the guard on the guard. "More"
// is not the only phrasing — "bawa 10 oak log" wants an end state — and making
// every count additive would break the case that gave the total semantic its
// worth: a bot holding 39, asked for 10, correctly staying put.
func TestAPlayerAskingForATotalKeepsTheBareCount(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{
		"Bawa 10 oak log",
		"kumpulin 10 oak log",
		"cari 10 oak log",
	} {
		steps := inferActionIntent(msg, "")
		if len(steps) != 1 {
			t.Fatalf("%q produced %d actions, want 1: %+v", msg, len(steps), steps)
		}
		if got, want := steps[0].Param, "oak_log,10"; got != want {
			t.Errorf("%q → param %q, want %q; a total must stay a total", msg, got, want)
		}
	}
}

// TestTheMoreKeywordsCoverHowPlayersActuallyAsk keeps the fix from being one
// exact string. Indonesian players reach for several ways to say the same thing,
// and only catching "lagi" would leave the bug one synonym away from returning.
func TestTheMoreKeywordsCoverHowPlayersActuallyAsk(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{
		"cariin aku 6 oak log lagi dung",
		"tambah 6 oak log",
		"kumpulin 6 oak log tambahan",
		"aku butuh 6 oak log lebih",
	} {
		steps := inferActionIntent(msg, "")
		if len(steps) != 1 {
			t.Fatalf("%q produced %d actions, want 1: %+v", msg, len(steps), steps)
		}
		if got := steps[0].Param; !strings.HasSuffix(got, ",+6") {
			t.Errorf("%q → param %q, want an additive count ending in ,+6", msg, got)
		}
	}
}
