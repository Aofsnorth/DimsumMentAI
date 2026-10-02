package ai

import (
	"fmt"
	"regexp"
	"strings"
)

// Outcome is what the bot is allowed to claim, as reported by the handler that
// actually did the work. It is a copy of the fields this package needs rather
// than an import of the event type, because ai is a leaf package and the
// layering rules forbid it from reaching upward.
//
// The two fields that matter are Success and Count. A handler that mines
// nothing reports Success: false; a handler that gathers items reports the
// measured count, not the requested one. A narration that contradicts either
// is the bot telling a player something the server did not confirm.
type Outcome struct {
	Action  string
	Item    string
	Count   int
	Success bool
	Error   string
}

// failureMarkers are the phrases a model uses when it reports that something
// did not work. They matter for the mirror case: the server confirmed eight
// logs and the model says "I couldn't find any", which misleads the player
// just as effectively as inventing a success.
//
// Kept narrower than the completion list on purpose. A false positive here
// swaps an honest success message for a fallback that says the opposite, so
// "kosong" and "habis" are absent: they appear in plenty of successful
// chatter ("ruang crafting kosong", "sudah habis place-nya") and the cost of
// matching them wrong is higher than the cost of missing them.
var failureMarkers = []string{
	// Indonesian
	"gagal", "tidak bisa", "nggak bisa", "gak bisa", "tidak dapat", "nggak dapat",
	"gak dapat", "kewalahan", "belum nemu", "belum ada", "belum bisa",
	"belum berhasil", "belum sempat", "tidak ada",
	"nggak ada", "gak ada", "gak nemu", "nggak nemu",
	// English
	"failed", "failure", "couldn't", "could not", "can't", "cannot", "unable",
	"no luck", "ran out", "out of", "nothing to", "empty",
}

// ClaimsFailure reports whether text says an action did not work. It is the
// mirror of ClaimsCompletion and is used against a success, where denying a
// confirmed result is the lie.
func ClaimsFailure(text string) bool {
	lowered := strings.ToLower(text)
	for _, m := range failureMarkers {
		if strings.Contains(lowered, m) {
			return true
		}
	}
	return false
}

// StatusFaithful reports whether a narration is compatible with the outcome the
// server actually reported.
//
// It checks one thing in each direction, because both are lies to a player:
// claiming success the server did not grant, and denying success it did. A
// neutral narration passes, which is deliberate — most status replies are
// neutral ("the vein ran out around here") and a filter that rewrote those
// would be noise on top of the problem.
//
// An empty narration is faithful. Silence says nothing false, and treating it
// as a contradiction would make the bot repeat itself every time the model
// chooses not to speak.
func StatusFaithful(reply string, truth Outcome) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return true
	}
	if truth.Success {
		return !ClaimsFailure(trimmed)
	}
	return !ClaimsCompletion(trimmed)
}

// FallbackStatusMessage is the deterministic message to send when the model's
// narration contradicts the outcome. It is the last line of the honesty
// chain: the model's prose is preferred, and this is what a player reads
// instead when that prose cannot be trusted for this particular result.
//
// It states only what Outcome contains. Nothing here is invented, because the
// entire point is that the player is being told something that did not depend
// on the model agreeing.
func FallbackStatusMessage(truth Outcome) string {
	item := friendlyOutcomeItem(truth)

	if !truth.Success {
		reason := truth.Error
		if reason == "" {
			reason = "hasil belum terkonfirmasi"
		}
		return fmt.Sprintf("Gagal: %s (%s)", reason, item)
	}

	if truth.Count > 0 {
		return fmt.Sprintf("Berhasil: %s (%d).", item, truth.Count)
	}
	return fmt.Sprintf("Berhasil: %s.", item)
}

// friendlyOutcomeItem renders the item the way the prompt tells the bot to
// name it — "Oak Planks", not "oak_planks" — because a fallback that violates
// the naming rule the model is held to is a fallback the player notices.
func friendlyOutcomeItem(truth Outcome) string {
	item := strings.TrimSpace(truth.Item)
	if item == "" {
		if truth.Action == "" {
			return "aksi"
		}
		return truth.Action
	}
	return FriendlyItemName(item)
}

// itemNameSplit turns a raw id into its words: "oak_planks" → "Oak Planks".
var itemNameSplit = regexp.MustCompile(`[_\s]+`)

// FriendlyItemName formats a raw item id for human speech.
func FriendlyItemName(item string) string {
	parts := itemNameSplit.Split(item, -1)
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
