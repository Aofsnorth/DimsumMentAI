package ai_test

import (
	"testing"

	"bedrock-ai/internal/ai"
)

// The mouth is independent of the status channel, and that is the bug.
//
// Everything else in this project works the way it should: handlers report what
// the server actually confirmed, ReportInventoryDelta measures a real before and
// after, and a refused action comes back Success: false. The verdict is real.
//
// But the player never reads the verdict. The model is handed the status in a
// prompt, writes fresh prose from it, and that prose is sent with nothing
// comparing it to what it is describing. The prompt says "failed"; the model can
// write "Berhasil! Aku nemu 12 diamond"; the player sees only the success.
//
// These tests pin the check that closes that gap, in both directions. Claiming a
// success that was refused is a lie. Denying a success that was confirmed is
// almost as bad, because it teaches the player not to believe the bot when it is
// right.

// failedIs what a refused action looks like on the wire.
var failed = ai.Outcome{
	Action:  "mine",
	Item:    "diamond",
	Count:   0,
	Success: false,
	Error:   "tidak ada diamond yang terlihat",
}

// TestASuccessClaimAboutAFailedActionIsNotFaithful is the core case. Everything
// here is a sentence the model could plausibly write while reading a prompt that
// contains the word "failed".
func TestASuccessClaimAboutAFailedActionIsNotFaithful(t *testing.T) {
	t.Parallel()

	claims := []string{
		"Berhasil! Aku nemu 12 diamond.",
		"Sudah aku kumpulkan semua diamond-nya.",
		"Diamond berhasil di-mining.",
		"I mined 12 diamonds.",
		"Gathering complete!",
		"Dikit, tapi aku sudah ambil diamond-nya.",
	}

	for _, reply := range claims {
		if ai.StatusFaithful(reply, failed) {
			t.Errorf("a failed %s was narrated as: %q", failed.Action, reply)
		}
	}
}

// TestAFailureReportAboutAFailedActionIsFaithful is the guard on the guard. The
// filter must not turn every honest failure into a "can't confirm" — that would
// make the bot useless in exactly the case where the player most needs a real
// answer.
func TestAFailureReportAboutAFailedActionIsFaithful(t *testing.T) {
	t.Parallel()

	reports := []string{
		"Wah, gagal nih, tidak ada diamond yang kelihatan.",
		"Aku belum nemu diamond di sini.",
		"Tidak berhasil,gez.",
		"I couldn't find any diamonds nearby.",
	}

	for _, reply := range reports {
		if !ai.StatusFaithful(reply, failed) {
			t.Errorf("an honest failure report was rejected: %q", reply)
		}
	}
}

// TestAFailureClaimAboutASucceededActionIsAlsoUnfaithful is the mirror. A player
// who just watched the bot break eight logs does not want to be told the bot
// found nothing, and a bot that says so on a success is training the player to
// ignore it.
func TestAFailureClaimAboutASucceededActionIsAlsoUnfaithful(t *testing.T) {
	t.Parallel()

	succeeded := ai.Outcome{
		Action:  "chop",
		Item:    "log",
		Count:   8,
		Success: true,
	}

	denials := []string{
		"Gagal, tidak ada log yang bisa ditebang.",
		"Aku belum berhasil menebang apa pun.",
		"Tidak ada log di sini.",
		"I couldn't chop any logs.",
	}

	for _, reply := range denials {
		if ai.StatusFaithful(reply, succeeded) {
			t.Errorf("a confirmed %d-log chop was narrated as: %q", succeeded.Count, reply)
		}
	}

	// And the honest success report passes.
	for _, reply := range []string{
		"Ketemu 8 batangnya!",
		"Berhasil dapat 8 log.",
		"Chopped 8 logs.",
	} {
		if !ai.StatusFaithful(reply, succeeded) {
			t.Errorf("an honest success report was rejected: %q", reply)
		}
	}
}

// TestTheFallbackStatesWhatActuallyHappened is what gets sent instead of the
// model's sentence. It has to carry the reason, because a bare "it failed" is
// not much better than a lie — the player cannot act on it.
func TestTheFallbackStatesWhatActuallyHappened(t *testing.T) {
	t.Parallel()

	got := ai.FallbackStatusMessage(failed)
	if got == "" {
		t.Fatal("the fallback for a failed action is empty; the player would get no message at all")
	}
	if !containsAll(got, "diamond") {
		t.Errorf("the fallback does not name the item the action was about: %q", got)
	}
	if !containsAny(got, "gagal", "belum", "tidak") {
		t.Errorf("the fallback does not read as a failure: %q", got)
	}

	// A failure with no reason given must still say something, not go blank.
	noReason := ai.Outcome{Action: "build", Item: "wall", Success: false}
	if got := ai.FallbackStatusMessage(noReason); got == "" {
		t.Error("a failure with no reason produced an empty fallback")
	}
}

// TestIncompleteIsNotACompletionClaim guards the "complete" addition from
// doing the opposite of its job. "The job is incomplete" contains the letters of
// "complete", and reading that as an announcement of success would invert the
// filter — so the marker is matched with its punctuation and this pins that the
// bare word does not match.
func TestIncompleteIsNotACompletionClaim(t *testing.T) {
	t.Parallel()

	// "Complete" inside a longer word must not read as finished.
	for _, reply := range []string{
		"The job is incomplete.",
		"Saved an incomplete copy.",
		"An incomplete survey, sorry.",
	} {
		if ai.ClaimsCompletion(reply) {
			t.Errorf("%q was read as a completion claim; \"incomplete\" is the opposite of done", reply)
		}
	}
}

// TestAnEmptyReplyIsFaithful. There is nothing to contradict, and the caller
// must not turn "the model said nothing" into a fallback message the player did
// not ask for.
func TestAnEmptyReplyIsFaithful(t *testing.T) {
	t.Parallel()

	if !ai.StatusFaithful("", failed) {
		t.Error("an empty reply was judged unfaithful; the caller would invent a message the model did not write")
	}
	if !ai.StatusFaithful("   ", failed) {
		t.Error("a whitespace-only reply was judged unfaithful")
	}
}

// TestACompletionClaimAgainstAPendingActionIsStripped is the other half of the
// fix, and it lives in the parser rather than here: an <action> tag in a reply
// means the host has not carried it out yet, so a completion claim in the same
// sentence is describing a future event as though it had happened.
func TestACompletionClaimAgainstAPendingActionIsStripped(t *testing.T) {
	t.Parallel()

	parsed := ai.Parse("Berhasil! <action>mine:diamond</action>")
	if len(parsed.Actions) == 0 {
		t.Fatal("the action was not parsed out; the test is not exercising the case it describes")
	}
	if parsed.CleanReply != "" {
		t.Errorf("a completion claim survived alongside a pending action: %q", parsed.CleanReply)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if contains(s, sub) {
			return true
		}
	}
	return false
}
