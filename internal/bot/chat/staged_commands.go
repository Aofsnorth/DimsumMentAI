package chat

import (
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/event"
)

// Staged commands: "luna coba lompat, jalan 1 block, muter 1kali sebanyak 5
// kali". A comma (or "terus"/"lalu"/"abis itu") separates the stages, "N kali"
// repeats a stage, "sebanyak N kali" repeats the whole routine, and "N block"
// gives a walk its distance.
//
// This is parsed in code, not by the model, on purpose. The model narrates the
// routine through replyViaAI — the sentence about it belongs to the model —
// but the sequencing itself is arithmetic: a routine the model has to re-emit
// tag by tag is one misremembered count away from hopping twice and spinning
// four times, and the player watching can tell.

const (
	// stagedMaxClauseRepeat bounds "lompat 999 kali". A staged routine is a
	// demonstration, not a load test, and an unbounded count is a player
	// asking the bot to hop into the void until ClearLag intervenes.
	stagedMaxClauseRepeat = 20

	// stagedMaxSequenceRepeat bounds "sebanyak N kali" the same way.
	stagedMaxSequenceRepeat = 20

	// stagedMaxSteps bounds the expansion as a whole: 20 stages × 20 repeats
	// is not a routine, it is a queue the bot never finishes.
	stagedMaxSteps = 60
)

var (
	// stagedSeparatorRegex splits a message into stages at spoken separators.
	stagedSeparatorRegex = regexp.MustCompile(`(?i)\s+(?:terus|lalu|abis itu|abisitu|then)\s+`)

	// stagedSequenceRegex finds the whole-routine repeat — but only trailing
	// the message. "muter 1kali sebanyak 5 kali" ends with the routine count;
	// "jongkok sebanyak 2 kali, muter 2kali sebanyak 3 kali" has a mid-message
	// "sebanyak" that counts sneaks per round, and only the trailing one is
	// the routine repeat. Stripped before per-stage parsing so its number is
	// never mistaken for a stage's own count.
	stagedSequenceRegex = regexp.MustCompile(`(?i)(?:[,;]?\s*)?(?:diulang|sebanyak|ulangi)\s+(\d+)\s*(?:kali|x|times)?\s*[.!]?\s*$`)

	// stagedStageRepeatRegex finds a stage's own repeat: "1kali", "3 kali",
	// "2x".
	stagedStageRepeatRegex = regexp.MustCompile(`(?i)(\d+)\s*(?:kali|x|times)\b`)

	// stagedDistanceRegex finds a walk's distance: "1 block", "3 blok",
	// "2 langkah", "5 meter".
	stagedDistanceRegex = regexp.MustCompile(`(?i)(\d+)\s*(?:blocks?|blok|langkah|meter)\b`)
)

// stagedVerbs maps the words a stage can start with to a step. The emote set
// is exactly what applyEmote understands; "walk" is the relative move handler.
var stagedVerbs = []struct {
	words []string
	label string
	param func(clause string) string
}{
	{words: []string{"lompat", "loncat", "jump", "salt"}, label: "emote", param: fixedParam("jump")},
	{words: []string{"muter", "putar", "berputar", "pusing", "spin"}, label: "emote", param: fixedParam("spin")},
	{words: []string{"jongkok", "sneak"}, label: "emote", param: fixedParam("sneak")},
	{words: []string{"angguk", "nod"}, label: "emote", param: fixedParam("nod")},
	{words: []string{"goyang", "shake"}, label: "emote", param: fixedParam("shake")},
	{words: []string{"liat kiri kanan", "liat sekeliling", "lookaround", "look around"}, label: "emote", param: fixedParam("lookaround")},
	{words: []string{"wiggle", "goyang pinggul"}, label: "emote", param: fixedParam("wiggle")},
	{words: []string{"jalan", "maju", "gerak", "march", "walk", "lari", "run"}, label: "walk", param: stagedWalkParam},
}

// stagedFillerWords are the words that make a stage pure noise — the bot's
// name, politeness, enthusiasm. A stage of nothing but these is skipped, so
// "luna coba lompat" is one stage, not two.
var stagedFillerWords = map[string]bool{
	"luna": true, "bot": true, "coba": true, "tolong": true, "yuk": true,
	"ayo": true, "dong": true, "ya": true, "deh": true, "pls": true,
	"please": true, "kamu": true, "sekali": true, "aja": true,
	// Count phrasing residue: the number and its unit were consumed by the
	// repeat regexes, but the connective words they came with ("sebanyak 2
	// kali") stay behind in the clause text and must not fail the strictness
	// check.
	"sebanyak": true, "diulang": true, "ulangi": true, "kali": true,
	"x": true, "times": true,
}

func fixedParam(p string) func(string) string {
	return func(string) string { return p }
}

// stagedWalkParam extracts the distance from a walk stage. "Jalan" with no
// distance is one step — the smallest thing the word can mean — and a number
// without a unit is not a distance ("jalan 3 kali" is a repeat, not a walk of
// three).
func stagedWalkParam(clause string) string {
	if m := stagedDistanceRegex.FindStringSubmatch(clause); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 {
			if n > 32 {
				n = 32
			}
			return strconv.Itoa(n)
		}
	}
	return "1"
}

// stagedStage is one parsed stage before repetition is applied.
type stagedStage struct {
	step   action.Step
	repeat int
	text   string
}

// parseStagedCommand parses a staged routine out of a chat message. It reports
// ok=false for anything that is not unambiguously one — a partial parse that
// silently drops a stage the player asked for is worse than the model handling
// the line its own way.
func parseStagedCommand(msg string) (steps []action.Step, summary string, ok bool) {
	if strings.TrimSpace(msg) == "" {
		return nil, "", false
	}

	sequenceRepeat := 1
	// The routine repeat is extracted from the tail of the message, because
	// "sebanyak N kali" at the end repeats everything, while the same words
	// mid-message are just that stage's own count.
	remaining := msg
	if m := stagedSequenceRegex.FindStringSubmatch(msg); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 {
			if n > stagedMaxSequenceRepeat {
				n = stagedMaxSequenceRepeat
			}
			sequenceRepeat = n
		}
		remaining = strings.TrimRight(strings.TrimSuffix(msg, m[0]), " ,;")
	}

	normalized := stagedSeparatorRegex.ReplaceAllString(remaining, ",")
	var parsed []stagedStage
	for _, part := range strings.FieldsFunc(normalized, func(r rune) bool { return r == ',' || r == ';' }) {
		clause := strings.TrimSpace(part)
		if clause == "" {
			continue
		}
		stage, isFiller, mapped := parseStagedStage(clause)
		if mapped {
			parsed = append(parsed, stage)
		} else if isFiller {
			continue
		} else {
			// A stage that is neither a known action nor filler means the
			// message meant something else: "jalan 1 block ke rumah" is a
			// navigation request, not a routine with a dropped tail.
			return nil, "", false
		}
	}
	if len(parsed) == 0 {
		return nil, "", false
	}

	// Trigger rule: a routine is either several stages, or one stage asked
	// for more than once, or an explicitly repeated routine, or a walk with a
	// stated distance. A bare "lompat" stays with the model, which narrates
	// it like any other line.
	totalWithoutSequence := 0
	for _, s := range parsed {
		totalWithoutSequence += s.repeat
	}
	distanceSeen := stagedDistanceRegex.MatchString(remaining)
	if len(parsed) < 2 && totalWithoutSequence <= 1 && sequenceRepeat <= 1 && !distanceSeen {
		return nil, "", false
	}

	for r := 0; r < sequenceRepeat; r++ {
		for _, s := range parsed {
			for i := 0; i < s.repeat; i++ {
				steps = append(steps, s.step)
				if len(steps) >= stagedMaxSteps {
					return steps, stagedSummary(parsed, sequenceRepeat), true
				}
			}
		}
	}
	return steps, stagedSummary(parsed, sequenceRepeat), true
}

// parseStagedStage maps one clause to a step. mapped reports a real action;
// isFiller reports a clause of pure noise that should be skipped.
func parseStagedStage(clause string) (stage stagedStage, isFiller, mapped bool) {
	lower := strings.ToLower(clause)

	// A stage's own repeat: "muter 1kali" is one spin, "lompat 3 kali" three
	// hops. Stripped before verb matching so the number never leaks into the
	// walk distance.
	stage.repeat = 1
	remainder := stagedStageRepeatRegex.ReplaceAllStringFunc(clause, func(match string) string {
		if m := stagedStageRepeatRegex.FindStringSubmatch(match); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n >= 1 {
				if n > stagedMaxClauseRepeat {
					n = stagedMaxClauseRepeat
				}
				stage.repeat = n
			}
		}
		return " "
	})

	for _, verb := range stagedVerbs {
		for _, word := range verb.words {
			if !strings.Contains(lower, word) {
				continue
			}
			stage.step = action.Step{Label: verb.label, Param: verb.param(remainder)}
			stage.text = strings.ToLower(strings.TrimSpace(word))
			// Strictness: what the clause says beyond the verb, its distance and
			// filler words must be nothing. "Jalan 1 block ke rumah" names a
			// destination this parser does not handle — executing the walk and
			// dropping the destination is a truncated routine, so the clause
			// fails the parse and the model takes the line.
			leftover := strings.ReplaceAll(strings.ToLower(remainder), word, " ")
			leftover = stagedDistanceRegex.ReplaceAllString(leftover, " ")
			for _, w := range strings.Fields(leftover) {
				if !stagedFillerWords[w] {
					return stage, false, false
				}
			}
			return stage, false, true
		}
	}

	// No verb. Pure noise ("luna", "coba ya") is skipped; anything else fails
	// the whole parse.
	words := strings.Fields(strings.Map(func(r rune) rune {
		if r == ',' || r == '.' || r == '!' || r == '?' {
			return ' '
		}
		return r
	}, lower))
	if len(words) == 0 {
		return stage, true, false
	}
	for _, w := range words {
		if !stagedFillerWords[w] {
			return stage, false, false
		}
	}
	return stage, true, false
}

// stagedSummary renders the routine for the model to narrate: what the stages
// are, in order, and how many times the whole thing runs. It is the factual
// change description for replyViaAI, not the reply itself.
func stagedSummary(stages []stagedStage, sequenceRepeat int) string {
	var parts []string
	for _, s := range stages {
		switch s.step.Label {
		case "walk":
			parts = append(parts, "walk "+s.step.Param+" block(s)")
		case "emote":
			parts = append(parts, s.step.Param)
		default:
			parts = append(parts, s.step.Label)
		}
		if s.repeat > 1 {
			parts[len(parts)-1] += " ×" + strconv.Itoa(s.repeat)
		}
	}
	out := strings.Join(parts, ", ")
	if sequenceRepeat > 1 {
		out += " — repeated " + strconv.Itoa(sequenceRepeat) + " times"
	}
	return out
}

// handleStagedCommand intercepts a staged routine, starts it, and lets the
// model narrate it. It returns false when the message is not a staged routine,
// so the normal chat flow takes it.
func handleStagedCommand(b *bot.Bot, evt event.ChatEvent, msg string) bool {
	steps, summary, ok := parseStagedCommand(msg)
	if !ok {
		return false
	}
	// A staged routine is machine-driven movement, not conversation. Any player
	// who passed the chat filter used to be able to walk the bot around, which
	// is the same authority the episode and admin commands gate on.
	if !isOperator(b, evt.SourceName) {
		b.Logger.Warn("staged routine ignored: not the configured operator",
			slog.String("from", evt.SourceName))
		return true
	}
	b.Logger.Info("chat: staged routine parsed",
		"user", evt.SourceName, "steps", len(steps), "routine", summary)
	action.ExecutePlan(b, steps, evt.SourceName)
	replyViaAI(b, evt.SourceName, msg, "You are running the staged routine: "+summary+".")
	return true
}
