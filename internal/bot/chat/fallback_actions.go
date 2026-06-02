package chat

import (
	"fmt"
	"regexp"
	"strings"

	"bedrock-ai/internal/bot/action"
)

var coordinateIntentRegex = regexp.MustCompile(`(?i)(?:koordinat|kordinat|coords?|coordinate|goto|jalan\s+ke|pergi\s+ke|ke)[^-+0-9]*([-+]?\d+(?:\.\d+)?)\s+([-+]?\d+(?:\.\d+)?)\s+([-+]?\d+(?:\.\d+)?)`)

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

	lower := strings.ToLower(msg)
	if strings.Contains(lower, "kesini") ||
		strings.Contains(lower, "ke sini") ||
		strings.Contains(lower, "come here") ||
		strings.Contains(lower, "datang ke sini") {
		return []action.Step{{Label: "come"}}
	}
	return nil
}

// affirmativeWords are markers a chatty LLM uses to commit to an action
// without actually emitting the <action> tag. When we see one of these in the
// reply AND the user message has clear action intent, we synthesize the
// action ourselves.
var affirmativeWords = []string{
	"siap", "oke", "ok", "okay", "iya", "ya ", "yes", "sip ", "sip,", "sip.",
	"bentar", "sebentar", "bntar", "baik", "lanjut", "mau ", "akan ",
	"i'll", "got it",
}

// isAffirmativeReply reports whether the LLM's reply text contains a word
// indicating it committed to performing an action.
func isAffirmativeReply(reply string) bool {
	r := strings.ToLower(reply)
	for _, w := range affirmativeWords {
		if strings.Contains(r, w) {
			return true
		}
	}
	return false
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
// <action> markup.
func inferActionIntent(msg string) []action.Step {
	lower := strings.ToLower(msg)

	var label string
	switch {
	case containsAny(lower, "buatin", "bikin", "buat ", "craft", "bikinin"):
		label = "craft"
	case containsAny(lower, "kasih", "kasi", "berikan", "kasihin", "give"):
		label = "give"
	case containsAny(lower, "drop", "buang", "lempar"):
		label = "drop"
	case containsAny(lower, "cari", "kumpulin", "kumpulkan", "ambilin", "carikan", "gather"):
		label = "gather"
	case containsAny(lower, "tambang", "mining", "mine ", "gali"):
		label = "mine"
	case containsAny(lower, "makan", "eat"):
		label = "eat"
	default:
		return nil
	}

	// Resolve target item from aliases.
	var item string
	for _, alias := range itemAliases {
		if strings.Contains(lower, alias[0]) {
			item = alias[1]
			break
		}
	}
	if item == "" {
		return nil
	}

	count := 1
	if m := countRegex.FindString(lower); m != "" {
		_, _ = fmt.Sscanf(m, "%d", &count)
	}

	param := item
	if label != "drop" && label != "eat" {
		param = fmt.Sprintf("%s,%d", item, count)
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
