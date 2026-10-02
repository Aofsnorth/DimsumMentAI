package ai

import (
	"regexp"
	"strings"
)

// Narration leak detection.
//
// A status reply is asked to be one short chat line about an action the server
// already performed. What arrives is sometimes the model's scratchpad instead:
// the reasoning it wrote while composing that line, ending with the line itself
// somewhere in the middle of a paragraph.
//
// This is worth catching for a reason that has nothing to do with honesty. The
// scratchpad is not a badly worded sentence, it is not a sentence at all, so
// there is nothing to correct and no filter can improve it — the player simply
// reads the model thinking out loud. That happened live, and what reached the
// chat included the prompt's own rules and a quoted draft of the intended
// answer.
//
// The checks below are deliberately structural rather than a blacklist of
// scratchpad vocabulary. A model that reasons in Indonesian, or in a style none
// of the phrases anticipated, still trips the first or second check.

// maxNarrationWords is the longest status line that can plausibly be a chat
// message. The prompt asks for under 25 words; the ceiling is loose so a slightly
// verbose answer is still allowed through, and short enough that a paragraph of
// reasoning cannot.
const maxNarrationWords = 60

// scratchpadMarkers are phrases that only appear in a model talking to itself
// about how to write the answer. They are matched on a lower-cased, whitespace
// collapsed copy so casing and line breaks do not hide them.
var scratchpadMarkers = []string{
	"we need", "we should", "we must", "i need to", "i should", "i must",
	"i'll write", "i will write", "let's write", "let me write",
	"need to answer", "need to reply", "need to respond", "answer should",
	"reply should", "the reply should", "the response should",
	"the prompt", "the system prompt", "as instructed", "per the instructions",
	"under 25 words", "action tags", "no action tags", "answer only the latest",
	"keep it under", "make it short", "tone:", "word count",
	"sistem prompt", "sesuai aturan", "harus jawab", "kita perlu", "saya perlu",
}

// selfCorrectionRegex matches a reply that carries its own abandoned first
// draft.
//
// The log caught the bot shipping "Berhasil! Logs/Cardboard... eh, Log sebanyak
// 10 biji udah masuk tas." — it started to name the item, second-guessed
// itself, corrected, and sent all three beats. The narration checks above do
// not catch it because the sentence is grammatical, the right length, and
// agrees with the server result; only the hesitation gives it away.
//
// The pattern is an ellipsis or dash followed by a hesitation particle, which
// is what a revision looks like in either language. Requiring the punctuation
// is what keeps this from firing on ordinary chat: "eh" appears in Indonesian
// conversation, "... eh," does not.
var selfCorrectionRegex = regexp.MustCompile(`(?i)(?:\.{2,}|…|—|–|-)\s*(?:eh|hmm|mmm|maaf|gitu|oops|i mean|sorry)\b`)

// IsSelfCorrection reports whether a reply narrates its own abandoned first
// attempt rather than just stating the result.
//
// It is separate from IsNarrationLeak because the two look nothing alike to a
// reader — one is the model's notes, the other is a sentence with a stumble in
// it — but they fail the same way, which is by arriving in chat at all.
func IsSelfCorrection(reply string) bool {
	return selfCorrectionRegex.MatchString(reply)
}

// scriptBreachRegex matches a reply carrying text from a script the model was
// not writing in, or an internal identifier where a spoken word belongs.
//
// The log caught "10 batang kayu berhasil_subscription砍到手啦 wkwk" — an
// identifier fragment welded into the sentence, followed by Chinese characters,
// in a reply that was otherwise ordinary Indonesian. Nothing about it reads as
// wrong until you notice the words in it: the length is fine, the claim agrees
// with the server, and the tone is right.
//
// The identifier half is checked because the prompt asks for friendly names
// precisely so "oak_log" never reaches a player, and the one place it does is
// here — an item name pasted in without the formatting applied.
var scriptBreachRegex = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}\p{Cyrillic}\p{Arabic}]|[a-z]+_[a-z_]+`)

// IsScriptCorrupted reports whether a reply contains text from another script,
// or an internal identifier where a spoken word belongs.
func IsScriptCorrupted(reply string) bool {
	if scriptBreachRegex.MatchString(reply) {
		return true
	}
	for _, word := range camelIdentifierRegex.FindAllString(reply, -1) {
		if len(word) >= minCamelIdentifierLen {
			return true
		}
	}
	return false
}

// camelIdentifierRegex matches a Go field name that reached chat intact, such
// as "BlockRuntimeID" or "StackNetworkID".
//
// These carry no underscore, so the identifier check above misses them, and the
// codebase is full of exactly these names — a bot narrating its own plumbing.
// The length floor is what keeps this off ordinary capitalised words: this
// codebase is Minecraft, and "YouTube" is a thing a player types while "Client
// Prediction" is not.
var camelIdentifierRegex = regexp.MustCompile(`\b[A-Z][a-z]{2,}[A-Z][A-Za-z]{4,}\b`)

// minCamelIdentifierLen is the shortest capitalised run treated as a field
// name. Below it, "YouTube" and "McDonald" are ordinary words.
const minCamelIdentifierLen = 12

// IsNarrationLeak reports whether a reply is the model's working-out rather than
// the sentence it was asked for.
//
// It is intentionally mode-independent. The shadow/enforce switch decides what
// happens to a narration that contradicts the server, and a leaked scratchpad
// contradicts nothing — it simply is not addressable to a player. Running it
// through the shadow measurement first would only mean shipping the model's
// private notes into chat in order to collect a statistic about them.
func IsNarrationLeak(reply string) bool {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return false
	}

	// A chat line has no escaped characters. A backslash-quote is what a model
	// produces when it writes a draft answer as a JSON-ish string literal, and
	// it is the highest-precision signal here: no Indonesian sentence a player
	// should read contains one.
	if strings.Contains(trimmed, `\"`) || strings.Contains(trimmed, "\\n") {
		return true
	}

	if len(strings.Fields(trimmed)) > maxNarrationWords {
		return true
	}

	collapsed := strings.ToLower(strings.Join(strings.Fields(trimmed), " "))
	for _, marker := range scratchpadMarkers {
		if strings.Contains(collapsed, marker) {
			return true
		}
	}
	return false
}
