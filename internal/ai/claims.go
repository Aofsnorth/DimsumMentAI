package ai

import (
	"regexp"
	"strings"
)

// This file is the last thing model output passes through before a player
// reads it.
//
// The bot cannot cheat: it has no flight, no teleport, no x-ray, and its
// inventory is a server mirror. What it can do is say something that did not
// happen. A handler that mines nothing reports a failure through
// action.ReportStatus, and that report is truthful. But the player does not
// read the report — the player reads whatever the model wrote about it, and
// the model is a 49B parameter component that was told to be a "chill and
// helpful companion" with a persona built around sounding natural. Nothing
// between the two compares them, so the mouth is free to upgrade a failure
// into an achievement.
//
// The filter below is deliberately small. It catches the narrow, high-cost
// case — a claim of completion attached to an action that has not run yet, or
// attached to a status the server says failed — and stays out of the way
// everywhere else. A filter that rewrote ordinary conversation would be a
// worse failure than the one it prevents, so the false-positive cases are
// tested as carefully as the true positives.

// completionMarkers are the phrases a model reaches for when it asserts that
// something is finished. Both languages are listed because the bot's own
// configured default is Indonesian (NewLLMClient) and the prompt tells it to
// follow whatever language the player uses, so both have to be covered.
//
// Several are multi-word on purpose ("i got", "got the"). They are matched as
// substrings over the whole reply rather than word by word, because a
// word-by-word scan cannot see them at all.
// "complete!" and "complete." are listed with their punctuation rather than
// bare "complete" on purpose. Bare "complete" is a substring of "incomplete",
// so matching it unqualified would read "the job is incomplete" as an
// announcement of success — the exact inversion this filter exists to stop. The
// punctuated forms are the ones a model actually writes when it means finished.

var completionMarkers = []string{
	// Indonesian
	"berhasil", "sudah", "selesai", "beres", "tuntas", "sukses",
	// English
	"done", "finished", "completed", "complete!", "complete.", "success",
	"got it", "got the", "i got",
	"mined", "crafted", "collected", "killed", "defeated", "cleared", "gathered",
	"already", "accomplished",
}

// hedges are the words that turn an assertion of completion into a statement
// about the future. "Belum berhasil" is precisely what an honest model writes
// when it has not done something yet, and treating it as a claim would strip
// the bot's most trustworthy sentences.
var hedges = []string{
	// Indonesian
	"belum", "nggak", "nenggak", "tidak", "gak", "bukan", "jadi", "akan",
	"bakal", "mau", "ingin", "harus", "bisa", "dapat", "baru", "nanti",
	"kayaknya", "kira", "sepertinya", "kalo", "kalau",
	// English
	"not", "n't", "never", "cannot", "can't", "will", "won't", "going to",
	"gonna", "about to", "haven't", "hasn't", "isn't", "aren't", "doesn't",
	"don't", "didn't", "would", "could", "should", "might", "may", "maybe",
	"probably", "hope", "want to", "wanna", "try", "trying", "let me",
}

// hedgeLookback is how many characters before a marker still count as hedging
// it. It is measured in characters rather than words so a hedge can sit next
// to its marker ("not done") as tightly as it does in a two-sentence reply.
//
// A hedge further back than this is describing something else, and a scan that
// reached further would let "I can't see the cave. Done mining." read as a
// statement about the future when it is not.
const hedgeLookback = 24

// hedgeStops are the boundaries a hedge may not reach across. Without them a
// negation in one sentence would suppress a claim in the next.
const hedgeStops = ".!?\n"

// ClaimsCompletion reports whether text asserts that something is already
// finished. It is the primitive both the parse-time filter and the status
// check are built from.
//
// Hedges suppress the claim, and they do so within a bounded window: "belum
// berhasil" is a statement about the future, while a bare "sudah" is a
// statement about the past.
func ClaimsCompletion(text string) bool {
	lowered := strings.ToLower(text)
	for _, marker := range completionMarkers {
		from := 0
		for {
			idx := strings.Index(lowered[from:], marker)
			if idx < 0 {
				break
			}
			start := from + idx
			if !hedgedBefore(lowered, start) && !insideWord(lowered, start) {
				return true
			}
			from = start + len(marker)
		}
	}
	return false
}

// insideWord reports whether the match at idx begins in the middle of a word.
//
// It exists for "complete" and its punctuated forms. "incomplete" ends with the
// exact bytes of "complete." — in + complete. — so a plain substring scan reads
// "the job is incomplete" as an announcement that the job is finished, which is
// the precise inversion of what this filter is for. Requiring that the character
// before the match is not a letter is what separates the word from its own tail.
//
// Every other marker is unambiguous as a substring ("mined" is not the end of
// another common word in a way that inverts the meaning), and this check is
// applied to all of them because a marker that is never inside a word is
// unaffected by the test.
func insideWord(lowered string, idx int) bool {
	if idx == 0 {
		return false
	}
	c := lowered[idx-1]
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
}

// hedgedBefore reports whether the text immediately preceding a marker at
// idx turns that marker into a statement about the future. The scan stops at a
// sentence boundary so a hedge cannot leak across sentences.
func hedgedBefore(lowered string, idx int) bool {
	start := idx - hedgeLookback
	if start < 0 {
		start = 0
	}
	window := lowered[start:idx]
	if cut := strings.LastIndexAny(window, hedgeStops); cut >= 0 {
		window = window[cut+1:]
	}
	for _, h := range hedges {
		if strings.Contains(window, h) {
			return true
		}
	}
	return false
}

// sentencePattern matches one sentence including its terminator, so a
// filtered reply keeps its punctuation instead of running together into a
// single run-on line.
var sentencePattern = regexp.MustCompile(`[^.!?\n]+[.!?]?`)

// FilterCompletionClaims removes sentences that assert completion while an
// action from the same reply is still pending.
//
// The pending-action flag is the whole mechanism. An action tag is a request
// the host has not carried out yet, so any completion claim in the same reply
// is describing a future event and is false the moment it is sent. With no
// action pending the reply is information, and past-tense language is correct
// there — which is exactly the distinction the system prompt draws at rule 4
// ("unless the user only asked for information") and the reason this filter
// can run on every parsed reply without muting the bot.
//
// Returns the filtered text and whether anything was removed. A reply that is
// nothing but a claim returns an empty string, which every caller already
// treats as "send nothing" — the honest outcome, since there is no honest
// version of that sentence to send instead.
func FilterCompletionClaims(speech string, actionPending bool) (string, bool) {
	if !actionPending || strings.TrimSpace(speech) == "" {
		return speech, false
	}

	sentences := splitSentences(speech)
	kept := make([]string, 0, len(sentences))
	dropped := false

	for _, s := range sentences {
		if ClaimsCompletion(s) {
			dropped = true
			continue
		}
		kept = append(kept, s)
	}

	if !dropped {
		return speech, false
	}
	return strings.TrimSpace(strings.Join(kept, " ")), true
}

// splitSentences breaks a reply into trimmed sentences, each keeping its own
// terminator. It tolerates trailing text with no terminator, because a reply
// cut off mid-sentence happens often enough that dropping it would lose real
// text.
func splitSentences(text string) []string {
	parts := sentencePattern.FindAllString(text, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			return []string{trimmed}
		}
	}
	return out
}
