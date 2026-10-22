// Package chat handles player chat messages and AI-driven bot responses.
package chat

import (
	"fmt"
	"regexp"
	"strings"

	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/bot/interact"
)

var coordinateIntentRegex = regexp.MustCompile(`(?i)(?:koordinat|kordinat|coords?|coordinate|goto|jalan\s+ke|pergi\s+ke|ke)[^-+0-9]*([-+]?\d+(?:\.\d+)?)\s+([-+]?\d+(?:\.\d+)?)\s+([-+]?\d+(?:\.\d+)?)`)

// comeIntentRegex matches "come to me" in the phrasings a player actually types.
var comeIntentRegex = regexp.MustCompile(
	`(?i)(?:` +
		// Verb, then filler, then the destination.
		`\b(?:datang|mendatangi|memenuhi|menuju|pergi|ikut|come|follow)\b` +
		`[\s\S]{0,24}?` +
		`\b(?:sini|ke\s*sini|tempat\s*ku|tempatku|ke\s+aku|aku|gua|saya|here|me)\b` +
		`(?:\s+(?:dong|ya|please|pls|tolong))?` +
		`|` +
		// A destination that is the whole request.
		`\b(?:kesini|ke\s*sini|ke\s*tempat\s*(?:ku|saya|aku|gua)|come\s*here|come\s*to\s*me|to\s*me|join\s*me|sini)\b` +
		`(?:\s+(?:dong|ya|please|pls|tolong))?` +
		`)`)

// followIntentRegex matches "stay with me" as a standing order rather than a
// one-off trip.
var followIntentRegex = regexp.MustCompile(
	`(?i)\b(?:ikut|follow|ikuti|iring|iringi|temani|bareng|beser)\b`)

// stopFollowingIntentRegex matches a request to stop following, in the
// phrasings a player uses rather than as a command nobody knows about.
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

// stopAttackIntentRegex matches requests to stop attacking / disengage from combat
// in Indonesian and English ("berhenti attack", "stop serang", "jangan serang", etc.).
var stopAttackIntentRegex = regexp.MustCompile(
	`(?i)\b(?:berhenti|stop|batal|jangan|cancel)\b[\s\S]{0,20}?\b(?:attack|serang|nyerang|menyerang|tarung|bertarung|hit|hitting|mukul|mukuk|pvp|fight|fighting)\b` +
		`|\b(?:no\s*attack|disengage)\b`,
)

// isStopAttackIntent reports whether a message is the player asking the bot
// to stop attacking or stop combat.
func isStopAttackIntent(msg string) bool {
	lower := strings.ToLower(strings.TrimSpace(msg))
	if strings.HasPrefix(lower, "!disengage") {
		return true
	}
	return stopAttackIntentRegex.MatchString(lower)
}

func fallbackMovementActions(msg string) []action.Step {
	msg = strings.TrimSpace(msg)
	if msg == "" || isQuestion(msg) || isStopFollowingIntent(msg) {
		return nil
	}
	lower := strings.ToLower(msg)
	if containsAny(lower, "jangan ke sini", "jangan kesini", "gak usah ke sini", "ga usah ke sini", "jangan ikut", "gak usah ikut", "ga usah ikut") {
		return nil
	}
	if match := coordinateIntentRegex.FindStringSubmatch(msg); len(match) == 4 {
		return []action.Step{{
			Label: "goto",
			Param: match[1] + "," + match[2] + "," + match[3],
		}}
	}

	if followIntentRegex.MatchString(lower) {
		return []action.Step{{Label: "follow"}}
	}
	if comeIntentRegex.MatchString(lower) {
		return []action.Step{{Label: "come"}}
	}
	return nil
}

// gatherEverythingCount is what "all of it" resolves to.
const gatherEverythingCount = 4096

var affirmativeWords = []string{
	"siap", "oke", "ok", "okay", "iya", "ya", "yes", "sip", "bentar",
	"sebentar", "bntar", "baik", "lanjut", "mau", "akan", "i'll", "got it",
	"gas", "gaskan", "sabi", "otw", "yoi", "kuy", "ayo", "ayok", "boleh", "sure",
}

// isQuestion reports whether a player message asks whether something is
// possible rather than asking for it to be done.
func isQuestion(msg string) bool {
	trimmed := strings.TrimSpace(msg)
	if strings.HasSuffix(trimmed, "?") {
		return true
	}
	words := strings.Fields(strings.ToLower(trimmed))
	if len(words) == 0 {
		return false
	}
	if questionLeadingWords[words[0]] {
		return true
	}
	return questionTailWords[words[len(words)-1]]
}

var questionLeadingWords = map[string]bool{
	"apa": true, "apakah": true, "bisakah": true, "gimana": true,
	"bagaimana": true, "kenapa": true, "kok": true,
}

var questionTailWords = map[string]bool{
	"gak": true, "ga": true, "gk": true, "nggak": true, "ngga": true, "ngk": true,
	"bukan": true, "kah": true, "bisagak": true,
}

// isAffirmativeReply reports whether the LLM's reply text contains a word
// indicating it committed to performing an action.
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

var negators = []string{"belum", "tidak", "nggak", "ndak", "gak", "bukan", "jangan"}

func precededByNegator(lower string, start int) bool {
	prefix := strings.TrimRight(lower[:start], " ")
	for _, neg := range negators {
		if prefix == neg || strings.HasSuffix(prefix, " "+neg) {
			return true
		}
	}
	return false
}

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
	{"diamond_pickaxe", "diamond_pickaxe"},
	{"diamond pickaxe", "diamond_pickaxe"},
	{"diamond_sword", "diamond_sword"},
	{"diamond sword", "diamond_sword"},
	{"wooden_pickaxe", "wooden_pickaxe"},
	{"wooden pickaxe", "wooden_pickaxe"},
	{"wooden_shovel", "wooden_shovel"},
	{"wooden_sword", "wooden_sword"},
	{"wooden sword", "wooden_sword"},
	{"wooden_axe", "wooden_axe"},
	{"stone_pickaxe", "stone_pickaxe"},
	{"stone pickaxe", "stone_pickaxe"},
	{"stone_sword", "stone_sword"},
	{"stone sword", "stone_sword"},
	{"stone_axe", "stone_axe"},
	{"iron_pickaxe", "iron_pickaxe"},
	{"iron pickaxe", "iron_pickaxe"},
	{"iron_sword", "iron_sword"},
	{"iron sword", "iron_sword"},
	{"pedang diamond", "diamond_sword"},
	{"pedang besi", "iron_sword"},
	{"pedang batu", "stone_sword"},
	{"pedang kayu", "wooden_sword"},
	{"pickaxe diamond", "diamond_pickaxe"},
	{"pickaxe besi", "iron_pickaxe"},
	{"pickaxe batu", "stone_pickaxe"},
	{"pickaxe kayu", "wooden_pickaxe"},
	{"kapak batu", "stone_axe"},
	{"kapak kayu", "wooden_axe"},
	{"oak_planks", "oak_planks"},
	{"oak planks", "oak_planks"},
	{"dark_oak_log", "dark_oak_log"},
	{"spruce_log", "spruce_log"},
	{"birch_log", "birch_log"},
	{"oak_log", "oak_log"},
	{"oak log", "oak_log"},
	{"cobblestone", "cobblestone"},
	{"cobble", "cobblestone"},
	{"cooked_beef", "cooked_beef"},
	{"furnace", "furnace"},
	{"kompor", "furnace"},
	{"chest", "chest"},
	{"peti", "chest"},
	{"stick", "stick"},
	{"torch", "torch"},
	{"obor", "torch"},
	{"planks", "oak_planks"},
	{"papan", "oak_planks"},
	{"kayu", "oak_log"},
	{"pohon", "oak_log"},
	{"wood", "oak_log"},
	{"log", "oak_log"},
	{"dirt", "dirt"},
	{"tanah", "dirt"},
	{"stone", "stone"},
	{"batu", "stone"},
	{"sand", "sand"},
	{"pasir", "sand"},
	{"bread", "bread"},
	{"roti", "bread"},
	{"apple", "apple"},
	{"apel", "apple"},
	{"steak", "cooked_beef"},
	{"daging", "cooked_beef"},
	{"pedang", "wooden_sword"},
	{"pickaxe", "wooden_pickaxe"},
	{"kapak", "wooden_axe"},
}

var countRegex = regexp.MustCompile(`\d+`)

// inferActionIntent guesses an action tag from raw user message text. Use
// only as a fallback when the LLM replied affirmatively but forgot the
// <action> markup.
func inferActionIntent(msg, reply string) []action.Step {
	if isQuestion(msg) {
		return nil
	}
	lower := strings.ToLower(msg)
	replyLower := strings.ToLower(reply)

	if steps, ok := inferDirectActionIntent(lower, msg, reply); ok {
		return steps
	}

	label := resolveItemActionLabel(lower)
	if label == "" {
		return nil
	}

	item := resolveItemFromAliases(lower, replyLower)
	if item == "" {
		return nil
	}

	count, additional := resolveActionCount(lower)
	param := item
	switch label {
	case "drop", "eat", "equip", "place":
		// These handlers take a bare item name (no count).
	default:
		param = fmt.Sprintf("%s,%d", item, count)
		if additional {
			param = fmt.Sprintf("%s,+%d", item, count)
		}
	}

	return []action.Step{{Label: label, Param: param}}
}

func inferDirectActionIntent(lower, msg, reply string) ([]action.Step, bool) {
	if containsAny(lower, "klik", "click", "interact", "tekan", "pencet", "sentuh") ||
		(containsAny(lower, "buka") && containsAny(lower, "pintu", "door", "chest", "peti", "trapdoor", "gerbang", "gate", "barrel")) {
		return []action.Step{{Label: "interact", Param: resolveInteractParam(msg, reply)}}, true
	}
	if containsAny(lower, "pakai armor", "pake armor", "pasang armor", "equip armor") {
		return []action.Step{{Label: "armor"}}, true
	}
	if containsAny(lower, "buang yang dipegang", "drop yang dipegang", "drop item di tangan", "drop tangan") {
		return []action.Step{{Label: "drop", Param: ""}}, true
	}
	if containsAny(lower, "tidur", "sleep") {
		return []action.Step{{Label: "sleep"}}, true
	}
	if containsAny(lower, "mancing", "memancing", "fishing") {
		return []action.Step{{Label: "fish", Param: "5"}}, true
	}
	if containsAny(lower, "joget", "dance") {
		return []action.Step{{Label: "dance", Param: "6"}}, true
	}
	if containsAny(lower, "twerk") {
		return []action.Step{{Label: "twerk", Param: "5"}}, true
	}
	if containsAny(lower, "lompat", "loncat") {
		return []action.Step{{Label: "jump"}}, true
	}
	return nil, false
}

func resolveInteractParam(msg, reply string) string {
	req := interact.ParseRequest(msg)
	if req.Block != "" {
		return req.Block
	}
	if req.Entity != "" {
		return req.Entity
	}
	if req.InFront {
		return "depan"
	}
	replyReq := interact.ParseRequest(reply)
	if replyReq.Block != "" {
		return replyReq.Block
	}
	if replyReq.Entity != "" {
		return replyReq.Entity
	}
	return "depan"
}

func resolveItemActionLabel(lower string) string {
	switch {
	case containsAny(lower, "buatin", "bikin", "buat ", "craft", "bikinin"):
		return "craft"
	case containsAny(lower, "taruh", "pasang", "letakkan", "place"):
		return "place"
	case containsAny(lower, "kasih", "kasi", "berikan", "kasihin", "give"):
		return "give"
	case containsAny(lower, "drop", "buang", "lempar"):
		return "drop"
	case containsAny(lower, "pegang", "genggam", "equip", "hold", "pakai", "pake ", "tahan"):
		return "equip"
	case containsAny(lower, "cari", "kumpulin", "kumpulkan", "ambil", "ambilin", "carikan", "gather", "bawa", "tambah", "butuh", "butuhin", "kurang", "tebang", "nebang", "potong"):
		return "gather"
	case containsAny(lower, "tambang", "mining", "mine ", "gali", "hancurin", "pecahin"):
		return "mine"
	case containsAny(lower, "makan", "eat"):
		return "eat"
	default:
		return ""
	}
}

func resolveItemFromAliases(lower, replyLower string) string {
	for _, alias := range itemAliases {
		if strings.Contains(lower, alias[0]) || strings.Contains(replyLower, alias[0]) {
			return alias[1]
		}
	}
	return ""
}

func resolveActionCount(lower string) (int, bool) {
	if containsAny(lower, "semua", "semuanya", "all") {
		return gatherEverythingCount, false
	}
	additional := containsAny(lower, "lagi", "lebih", "tambah", "tambahan", "additional", "more")
	count := 1
	if m := countRegex.FindString(lower); m != "" {
		_, _ = fmt.Sscanf(m, "%d", &count)
	}
	return count, additional
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
