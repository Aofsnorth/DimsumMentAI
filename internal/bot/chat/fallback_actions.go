// Package chat handles player chat messages and AI-driven bot responses.
package chat

import (
	"fmt"
	"regexp"
	"strings"

	"bedrock-ai/internal/bot/action"
)

var coordinateIntentRegex = regexp.MustCompile(`(?i)(?:koordinat|kordinat|coords?|coordinate|goto|jalan\s+ke|pergi\s+ke|ke)[^-+0-9]*([-+]?\d+(?:\.\d+)?)\s+([-+]?\d+(?:\.\d+)?)\s+([-+]?\d+(?:\.\d+)?)`)

// comeIntentRegex matches "come to me" in the phrasings a player actually
// types, rather than the four contiguous strings the old list enumerated.
//
// There are two shapes and both are needed. A verb carrying a destination —
// "datang ke sini", "pergi ke tempat ku" — has the two parts in that order with
// a little filler between them, because in Indonesian the verb and the
// destination are separable words rather than one fixed phrase. And a
// destination that stands alone — "kesini", "come here" — needs no verb at all,
// because the destination is itself the request.
//
// Demanding a verb AND a destination rejects every standalone phrasing, because
// "kesini" has no verb in front of it; and listing only standalone phrasings
// rejects every one that has a verb, which is what the old four-string list did.
// Both failures are silent and both read to a player as the bot ignoring them.
var comeIntentRegex = regexp.MustCompile(
	`(?i)(?:` +
		// Verb, then filler, then the destination.
		`\b(?:datang|mendatangi|memenuhi|menuju|pergi|ikut|come|follow)\b` +
		`[\s\S]{0,24}?` +
		`\b(?:sini|ke\s*sini|tempat\s*ku|tempatku|ke\s+aku|aku|gua|saya|here|me)\b` +
		`(?:\s+(?:dong|ya|please|pls|tolong))?` +
		`|` +
		// A destination that is the whole request.
		`\b(?:kesini|ke\s*sini|ke\s*tempat\s*ku|ke\s*tempatku|come\s*here|come\s*to\s*me|to\s*me|join\s*me|sini)\b` +
		`(?:\s+(?:dong|ya|please|pls|tolong))?` +
		`)`)

// followIntentRegex matches "stay with me" as a standing order rather than a
// one-off trip.
//
// It is separate from comeIntentRegex because the two are different requests:
// "come" walks to where the player is now and stops, "follow" keeps walking
// after them. A bot that answered "ikut aku" with a one-off walk would arrive,
// stop, and then look broken when the player kept moving.
var followIntentRegex = regexp.MustCompile(
	`(?i)\b(?:ikut|follow|ikuti|iring|iringi|temani|bareng|beser)\b`)

// stopFollowingIntentRegex matches a request to stop following, in the
// phrasings a player uses rather than as a command nobody knows about.
//
// The opt-out has to be as easy to say as the opt-in. "Berhenti ikutin aku" was
// typed at the bot and the bot kept following for four minutes, because the
// only thing it heard was "!nofollow" — a command that exists in the source and
// in no player's head. Both halves of the exchange have to be plain speech.
var stopFollowingIntentRegex = regexp.MustCompile(
	`(?i)\b(?:berhenti|stop)\w*\b[\s\S]{0,20}?\b\w*ikut\w*\b` +
		`|\b(?:berhenti|stop)\w*\b[\s\S]{0,20}?\b(?:following|follow|iring\w*|mengekor)\b` +
		`|\b(?:berhenti|stop)\w*\b[\s\S]{0,20}?\b\w*(?:temani|nemenin|bareng)\w*\b` +
		`|\bjangan\b[\s\S]{0,20}?\b\w*(?:ikut|iring)\w*\b` +
		`|\bno\s*follow\b`)

// isStopFollowingIntent reports whether a message is the player asking the bot
// to stop following them.
func isStopFollowingIntent(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if strings.HasPrefix(lower, "!nofollow") {
		return true
	}
	return stopFollowingIntentRegex.MatchString(lower)
}

func fallbackMovementActions(msg string) []action.Step {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return nil
	}
	if match := coordinateIntentRegex.FindStringSubmatch(msg); len(match) == 4 {
		return []action.Step{{
			Label: "goto",
			Param: match[1] + "," + match[2] + "," + match[3],
		}}
	}

	// The "come here" intent, matched as phrases rather than as four exact
	// substrings.
	//
	// The list used to be `kesini`, `ke sini`, `come here`, `datang ke sini`,
	// and that is four ways of saying one thing out of the several dozen a
	// player actually types. "ke tempat ku", "ikut aku", "sini dong" and
	// "follow me" are the same request and none of them matched, so the bot
	// answered them in chat and then stood there — which reads as the bot
	// ignoring an explicit instruction, and is worse than not understanding it
	// because it looked like it had heard.
	//
	// The verbs are matched separately from the particles that carry them,
	// because in Indonesian the two are separable: "ikut" with "sini", "ke"
	// with "tempatku", "datang" with "ke sini". Requiring one contiguous string
	// per phrasing is what made the old list miss the common cases.
	lower := strings.ToLower(msg)

	// Follow is tested first because it is the more specific of the two, and the
	// patterns overlap: "ikut aku" is a follow request and it also matches the
	// come pattern's "ikut … aku" verb-plus-destination shape. Testing come
	// first would answer a standing order with a one-off walk — the bot would
	// arrive, stop, and look broken the moment the player moved again.
	if followIntentRegex.MatchString(lower) {
		return []action.Step{{Label: "follow"}}
	}
	if comeIntentRegex.MatchString(lower) {
		return []action.Step{{Label: "come"}}
	}
	return nil
}

// affirmativeWords are markers a chatty LLM uses to commit to an action
// without actually emitting the <action> tag. When we see one of these in the
// reply AND the user message has clear action intent, we synthesize the
// action ourselves.
//
// The trailing spaces these entries used to carry were an attempt at whole-word
// matching and were not enough: "ya " is a substring of "saya ", so the refusal
// "Saya tak bisa" matched, and the action this synthesises is a mine run — so the
// bot cheerfully started the exact job the model had just declined. A refusal
// and an agreement differ only by a prefix, and substring matching cannot tell
// them apart. See isAffirmativeReply.
// gatherEverythingCount is what "all of it" resolves to. Not a limit: the
// gatherer stops when the world runs out of candidates, so a count past any
// plausible stack means the player gets everything there is.
const gatherEverythingCount = 4096

var affirmativeWords = []string{
	"siap", "oke", "ok", "okay", "iya", "ya", "yes", "sip", "bentar",
	"sebentar", "bntar", "baik", "lanjut", "mau", "akan", "i'll", "got it",
}

// isAffirmativeReply reports whether the LLM's reply text contains a word
// indicating it committed to performing an action.
//
// Matched on word boundaries, not by substring. Punctuation counts as a
// boundary, so "ya!" and "iya," are agreements; "saya", "dayat" and "biaya"
// are not.
func isAffirmativeReply(reply string) bool {
	lower := strings.ToLower(reply)
	for _, w := range affirmativeWords {
		for from := 0; from < len(lower); {
			i := strings.Index(lower[from:], w)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(w)
			from = end
			if start > 0 && !isWordBoundary(lower[start-1]) {
				continue
			}
			if end < len(lower) && !isWordBoundary(lower[end]) {
				continue
			}
			if precededByNegator(lower, start) {
				continue
			}
			return true
		}
	}
	return false
}

// negators are the words that turn a marker into its opposite. "Belum siap" is
// "not ready" and contains "siap" on its own; "tidak akan" contains "akan". A
// refusal that happens to contain a marker word is the case this matcher exists
// to get right, so a marker immediately preceded by one of these does not count.
var negators = []string{"belum", "tidak", "nggak", "ndak", "gak", "bukan", "jangan"}

// precededByNegator reports whether the word at start is negated.
//
// Only the word immediately before is considered. Negation attaches to the
// clause it precedes, and a marker several words into a sentence is not what
// "belum siap" means — "saya belum bisa, tapi oke ya" still commits.
func precededByNegator(lower string, start int) bool {
	// The prefix ends at the marker, so it ends with whatever separated the two
	// words — a space in every natural sentence. Trimmed before comparing, or
	// the suffix test looks for " belum" in "saya belum " and never finds it.
	prefix := strings.TrimRight(lower[:start], " ")
	for _, neg := range negators {
		// The negator must be a whole word: it is the whole prefix, or it ends a
		// space-delimited word. Anything else is a longer word that merely
		// happens to end in the same letters.
		if prefix == neg || strings.HasSuffix(prefix, " "+neg) {
			return true
		}
	}
	return false
}

// isWordBoundary reports whether b can sit next to a matched word without being
// part of it. ASCII letters, digits and underscore continue a word; everything
// else ends it — including every byte of a multi-byte rune, so a leading
// non-ASCII letter cannot fake a boundary.
func isWordBoundary(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', b == '_':
		return false
	default:
		return true
	}
}

// itemAliases maps a substring found in user messages to the canonical
// Minecraft item name used by the action handlers. Longer/more specific
// aliases are listed first so they win the substring scan.
var itemAliases = [][2]string{
	{"crafting_table", "crafting_table"},
	{"crafting table", "crafting_table"},
	{"craftingtable", "crafting_table"},
	{"oak_planks", "oak_planks"},
	{"oak planks", "oak_planks"},
	{"oak_log", "oak_log"},
	{"oak log", "oak_log"},
	{"wooden_axe", "wooden_axe"},
	{"wooden_pickaxe", "wooden_pickaxe"},
	{"wooden_shovel", "wooden_shovel"},
	{"stone_axe", "stone_axe"},
	{"stone_pickaxe", "stone_pickaxe"},
	{"cobblestone", "cobblestone"},
	{"cobble", "cobblestone"},
	{"furnace", "furnace"},
	{"chest", "chest"},
	{"stick", "stick"},
	{"torch", "torch"},
	{"planks", "oak_planks"},
	{"papan", "oak_planks"},
	{"kayu", "oak_log"},
	{"wood", "oak_log"},
	{"log", "oak_log"},
	{"dirt", "dirt"},
	{"tanah", "dirt"},
	{"stone", "stone"},
	{"batu", "stone"},
	{"sand", "sand"},
	{"pasir", "sand"},
}

var countRegex = regexp.MustCompile(`\d+`)

// inferActionIntent guesses an action tag from raw user message text. Use
// only as a fallback when the LLM replied affirmatively but forgot the
// <action> markup. The LLM reply is also scanned for the item name, since
// users often omit it ("coba pegang di tangan") while the reply names it
// ("oke aku pegang oak log-nya").
func inferActionIntent(msg, reply string) []action.Step {
	lower := strings.ToLower(msg)
	replyLower := strings.ToLower(reply)

	var label string
	switch {
	case containsAny(lower, "buatin", "bikin", "buat ", "craft", "bikinin"):
		label = "craft"
	case containsAny(lower, "kasih", "kasi", "berikan", "kasihin", "give"):
		label = "give"
	case containsAny(lower, "drop", "buang", "lempar"):
		label = "drop"
	case containsAny(lower, "pegang", "genggam", "equip", "hold", "pakai", "pake ", "tahan"):
		label = "equip"
	case containsAny(lower, "cari", "kumpulin", "kumpulkan", "ambilin", "carikan", "gather", "bawa", "tambah", "butuh", "butuhin", "kurang"):
		label = "gather"
	case containsAny(lower, "tambang", "mining", "mine ", "gali"):
		label = "mine"
	case containsAny(lower, "makan", "eat"):
		label = "eat"
	default:
		return nil
	}

	// Resolve target item from aliases, preferring the user message and
	// falling back to the LLM reply.
	var item string
	for _, alias := range itemAliases {
		if strings.Contains(lower, alias[0]) || strings.Contains(replyLower, alias[0]) {
			item = alias[1]
			break
		}
	}
	if item == "" {
		return nil
	}

	count := 1
	additional := false
	if containsAny(lower, "semua", "semuanya", "all") {
		// "Take all of them" is not a request for zero. Zero was the encoding
		// here, and zero is not neutral downstream: the gatherer reads a count of
		// zero or less as "one block" and proceeds, so "ambil semua batu yang ada"
		// felled exactly one block and reported success.
		//
		// The gatherer loops until the count is reached or the world runs out of
		// candidates, so a count past any plausible stack means what the player
		// asked for: everything there is.
		count = gatherEverythingCount
	} else {
		// "lagi", "tambah", "more" — the player already has some and wants more
		// on top. Without this the count is read as a total, and a bot holding 6
		// asked for "6 lagi" reports that it already has enough and stops.
		if containsAny(lower, "lagi", "lebih", "tambah", "tambahan", "additional", "more") {
			additional = true
		}
		if m := countRegex.FindString(lower); m != "" {
			_, _ = fmt.Sscanf(m, "%d", &count)
		}
	}

	param := item
	switch label {
	case "drop", "eat", "equip":
		// These handlers take a bare item name (no count).
	default:
		param = fmt.Sprintf("%s,%d", item, count)
		if additional {
			param = fmt.Sprintf("%s,+%d", item, count)
		}
	}

	return []action.Step{{Label: label, Param: param}}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
