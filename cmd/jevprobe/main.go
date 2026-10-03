// Command jevprobe isolates WHICH idle-only question the live endpoint rejects.
//
// runner.go questions() has two early returns for Busy/Exploring. Above the
// first: 6 reflex nouls + escalate + risk = 8, all accepted in production.
// Below the first return the idle path adds goal, activity, affordance,
// locomotion, gaze and (on a ledge) drop. The live endpoint rejects the whole
// evaluate call with 400 "Invalid decision request. Send only model, state,
// and bounded typed questions...". An earlier probe proved 1..20 synthetic
// noul questions all pass, so the bound is not the count.
//
// This probe therefore sends the real question set, one idle-only question at
// a time on top of the accepted baseline, and prints the verdict for each. It
// then sends the whole idle set to confirm the 400 is reproducible, and — for
// the affordance question, whose criteria map is derived from runtime world
// state and is the one question with no fixed size — bisects the criteria count
// to find the edge.
//
// Every question here is built by the same exported builders the runner calls,
// from the same exported helpers (agi.Curriculum, agi.AvailableGoals,
// affordance.Derive), so nothing is hand-written into a shape production does
// not send.
//
// Exit status is always 0. A probe that cannot reach the network, or that is
// refused at every key, still produced data.
//
// Usage:
//
//	go run ./cmd/jevprobe                       # baseline + each idle key
//	go run ./cmd/jevprobe -mode full            # all idle keys in one call
//	go run ./cmd/jevprobe -mode bisect-afford   # walk the criteria count up
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"bedrock-ai/internal/bot/affordance"
	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/bot/perception"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/jev"
)

// probeState is the state text sent with every call. It is DescribeState of the
// probe snapshot, so the state the model sees is the same shape production
// sends rather than a placeholder string that might be accepted for the wrong
// reason.
func probeState(s agi.Snapshot) string { return agi.DescribeState(s) }

// snapshot is a rich idle world: water, a chest, tools, a block to gather, a
// player with line of sight, crops, animals, crafting available and a ledge
// ahead. Rich on purpose — a question is only as big as the world that produces
// it, and a sparse snapshot would understate the affordance criteria map that
// this probe is looking for an edge in.
func snapshot() agi.Snapshot {
	return agi.Snapshot{
		Now:            time.Now(),
		Coords:         "120, 71, -43",
		HP:             18,
		Hunger:         14,
		HeldItem:       "iron_axe",
		Inventory:      "oak_log x8, cobblestone x12, wheat x3",
		VisibleMob:     "cow (6m N)",
		NearBlocks:     "oak_log, oak_log, chest, chest, water, water, wheat, wheat, sheep",
		NearBlocksText: "oak_log (4m N), chest (3m E), water (7m S), wheat (2m NE), sheep (5m W)",
		VisibleSigns:   []string{"[Storage]", "[Tools]"},
		Nearby: []agi.Person{
			{Name: "Axeliory", Distance: 4.5, HasLineOf: true, LookingAt: true},
		},
		LedgeAhead:   true,
		FreeSlots:    11,
		GoalSummary:  "stock_up (progress 0, 4 min left)",
		PlanSummary:  "gather wood, then store it in the chest",
		Craftable:    4,
		IsRaining:    false,
		EpisodeText:  "",
		Conversation: "Axeliory: hello",
		Features: perception.Features{
			Water:     true,
			RipeCrops: 3,
			Logs:      9,
			Animals:   2,
		},
		Vocabulary: agi.NewVocabulary(),
	}
}

// baseline is the set runner.go builds ABOVE the first early return: the six
// reflex nouls, the escalation noul and the risk choice. These are the eight
// production proves are accepted, so every idle-only question is measured on
// top of them — that isolates the idle-only question rather than re-testing the
// baseline.
func baseline() map[string]json.RawMessage {
	q := jev.BuildReflexQuestions()
	q[agi.EscalateQuestion] = jev.MustNoul(agi.EscalateInstructions)
	for name, raw := range jev.BuildRiskQuestion() {
		q[name] = raw
	}
	return q
}

// affordanceWorld is a stub affordance.World that reports everything the
// derivation can gate on as available, so affordance.Derive returns the
// largest legal set the catalogue can produce. The real adapter in
// internal/bot reads the live bot; this reads a described world, which is the
// same interface and the same derivation, so the criteria map is identical in
// shape to the largest one production can build.
type affordanceWorld struct{}

func (affordanceWorld) CanSeeWater() bool       { return true }
func (affordanceWorld) FacingContainer() bool   { return true }
func (affordanceWorld) HoldingStackable() bool  { return true }
func (affordanceWorld) HoldingTool() bool       { return true }
func (affordanceWorld) NearChest() bool         { return true }
func (affordanceWorld) FreeSlots() int          { return 11 }
func (affordanceWorld) HasMaterial(string) bool { return true }
func (affordanceWorld) ContainerContents() []string {
	return []string{"oak_log", "cobblestone", "iron_ingot", "diamond"}
}
func (affordanceWorld) SlotContents() []string {
	return []string{"oak_log", "cobblestone", "wheat", "iron_axe"}
}
func (affordanceWorld) Snapshot() affordance.Facts {
	return affordance.Facts{FreeSlots: 11, Craftable: 4}
}

// affordanceCriteria is the criteria map the affordance question would carry,
// derived the way runner.affordances derives it.
func affordanceCriteria(changesWorld bool) map[string]string {
	return affordance.Derive(affordanceWorld{}, changesWorld).Criteria()
}

// idleKeys names every question the idle path adds below the first early
// return, mapped to the builder that produces it.
func idleKeys(s agi.Snapshot) []struct {
	Name string
	Raw  json.RawMessage
} {
	goals := agi.AvailableGoals(s)
	descriptions := make(map[string]string, len(goals))
	for _, g := range goals {
		descriptions[g.Name] = g.Description
	}

	out := []struct {
		Name string
		Raw  json.RawMessage
	}{}

	for name, raw := range jev.BuildGoalQuestion(agi.GoalsFor(goals), descriptions, "") {
		out = append(out, struct {
			Name string
			Raw  json.RawMessage
		}{name, raw})
	}
	for name, raw := range jev.BuildActivityQuestion(agi.Curriculum(s)) {
		out = append(out, struct {
			Name string
			Raw  json.RawMessage
		}{name, raw})
	}
	for name, raw := range jev.BuildAffordanceQuestion(affordanceCriteria(true)) {
		out = append(out, struct {
			Name string
			Raw  json.RawMessage
		}{name, raw})
	}
	for name, raw := range jev.BuildLocomotionQuestion() {
		out = append(out, struct {
			Name string
			Raw  json.RawMessage
		}{name, raw})
	}
	for name, raw := range jev.BuildGazeQuestion() {
		out = append(out, struct {
			Name string
			Raw  json.RawMessage
		}{name, raw})
	}
	for name, raw := range jev.BuildDropQuestion() {
		out = append(out, struct {
			Name string
			Raw  json.RawMessage
		}{name, raw})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func main() {
	configPath := flag.String("config", "configs/bot.yaml", "path to config file")
	mode := flag.String("mode", "each", "each | full | bisect-afford | state")
	maxCriteria := flag.Int("max-criteria", 60, "highest criteria count to try in bisect-afford")
	confirmAfter := flag.Int("confirm", 2, "how many extra counts to try past the first failure")
	startAt := flag.Int("start", 1, "lowest criteria count to try")
	repeat := flag.Int("repeat", 1, "calls per criteria count, to separate a bound from a flake")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Printf("load config: %v\n", err)
		return
	}

	client := jev.FromEnv(cfg.AGI.Jev.BaseURL, cfg.AGI.Jev.Model)
	if !client.Available {
		fmt.Printf("no API key in $%s — cannot probe\n", jev.EnvAPIKey)
		return
	}
	client.SetTimeout(30 * time.Second)

	s := snapshot()
	state := probeState(s)

	fmt.Printf("endpoint: %s\n", client.Endpoint())
	fmt.Printf("model:    %s\n", client.ModelName())
	fmt.Printf("state:    %d bytes\n\n", len(state))

	base := baseline()
	fmt.Printf("baseline: %d questions (%s)\n\n", len(base), keysOf(base))

	switch *mode {
	case "each":
		runEach(client, state, base, idleKeys(s))
	case "full":
		runFull(client, state, base, idleKeys(s))
	case "bisect-afford":
		runBisectAfford(client, state, base, *maxCriteria, *confirmAfter, *startAt, *repeat)
	case "state":
		runState(client, base, idleKeys(s), *maxCriteria)
	case "single":
		runSingle(client, state, base)
	case "narrow":
		runNarrow(client, state, base)
	default:
		fmt.Printf("unknown mode %q\n", *mode)
	}
}

// runEach sends the baseline alone as a control, then the baseline plus each
// single idle-only question. One added question per call means a 400 names the
// question that caused it rather than the set.
func runEach(client *jev.Client, state string, base map[string]json.RawMessage,
	idle []struct {
		Name string
		Raw  json.RawMessage
	}) {
	fmt.Printf("%-14s %-6s %-8s %s\n", "added", "result", "bytes", "detail")

	detail, ok := send(client, state, base)
	report("(baseline)", "", ok, detail)

	for _, q := range idle {
		merged := map[string]json.RawMessage{}
		for k, v := range base {
			merged[k] = v
		}
		merged[q.Name] = q.Raw
		detail, ok := send(client, state, merged)
		report(q.Name, string(q.Raw), ok, detail)
	}

	fmt.Println()
	fmt.Println("RESULT: see the rows above. The first FAIL names the offending question.")
}

// runFull sends baseline plus every idle-only question in one call, which is
// what runner.questions builds on an idle tick with a ledge ahead.
func runFull(client *jev.Client, state string, base map[string]json.RawMessage,
	idle []struct {
		Name string
		Raw  json.RawMessage
	}) {
	merged := map[string]json.RawMessage{}
	for k, v := range base {
		merged[k] = v
	}
	for _, q := range idle {
		merged[q.Name] = q.Raw
	}
	fmt.Printf("full idle set: %d questions\nkeys: %s\n\n", len(merged), keysOf(merged))
	detail, ok := send(client, state, merged)
	if ok {
		fmt.Println("RESULT: full idle set ACCEPTED — the 400 is not reproduced by this set.")
		return
	}
	fmt.Printf("RESULT: full idle set REJECTED: %s\n", detail)
}

// runBisectAfford walks the affordance criteria count upward one entry at a
// time. The affordance question is the only idle-only question whose size is
// decided by runtime world state rather than by a fixed list, so it is the one
// whose bound could be crossed without anybody noticing. The criteria are the
// real derived set, truncated to n entries, so the labels and summaries are
// the ones production sends.
func runBisectAfford(client *jev.Client, state string, base map[string]json.RawMessage, max, confirm, startAt, repeat int) {
	full := affordanceCriteria(true)
	names := make([]string, 0, len(full))
	for k := range full {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Printf("affordance criteria available: %d\n\n", len(names))

	fmt.Printf("%-10s %-6s %s\n", "criteria", "result", "detail")
	firstFail := 0
	for n := startAt; n <= max && n <= len(names); n++ {
		criteria := make(map[string]string, n)
		for _, name := range names[:n] {
			criteria[name] = full[name]
		}
		q := jev.BuildAffordanceQuestion(criteria)
		if len(q) == 0 {
			fmt.Printf("%-10d %-6s %s\n", n, "SKIP", "builder returned nil")
			continue
		}
		merged := map[string]json.RawMessage{}
		for k, v := range base {
			merged[k] = v
		}
		for name, raw := range q {
			merged[name] = raw
		}
		for attempt := 1; attempt <= repeat; attempt++ {
			detail, ok := send(client, state, merged)
			verdict := "OK"
			if !ok {
				verdict = "FAIL"
				if firstFail == 0 {
					firstFail = n
				}
			}
			suffix := ""
			if repeat > 1 {
				suffix = fmt.Sprintf(" (call %d)", attempt)
			}
			fmt.Printf("%-10d %-6s %s%s\n", n, verdict, detail, suffix)
		}
		if firstFail > 0 && n >= firstFail+confirm {
			break
		}
	}

	fmt.Println()
	switch {
	case firstFail == 0:
		fmt.Printf("RESULT: affordance accepted at every criteria count tried (up to %d).\n", max)
	default:
		fmt.Printf("RESULT: affordance fails at %d criteria and above; %d and below accepted.\n", firstFail, firstFail-1)
	}
}

// runState walks the state text upward in size, holding the full idle question
// set fixed at the 14 questions an idle tick sends.
//
// The questions are not the only thing that grows with the world: DescribeState
// renders the block scan, the plan, the episode brief, the inventory and the
// recent conversation into the same string, and none of it is capped. If the
// endpoint's "bounded" applies to the request rather than to any one question,
// the state is where it shows up, so this measures the other axis.
func runState(client *jev.Client, base map[string]json.RawMessage,
	idle []struct {
		Name string
		Raw  json.RawMessage
	}, max int) {
	questions := map[string]json.RawMessage{}
	for k, v := range base {
		questions[k] = v
	}
	for _, q := range idle {
		questions[q.Name] = q.Raw
	}

	fmt.Printf("questions held fixed at %d\n\n", len(questions))
	fmt.Printf("%-10s %-6s %s\n", "state", "result", "detail")
	firstFail := 0
	for n := 1; n <= max; n++ {
		// A block histogram is the shape DescribeState actually produces for a
		// rich scan, so the padding is representative rather than a run of
		// characters that could fail for a different reason.
		state := "bot ticking, idle, alone at home\n" + strings.Repeat(
			"Nearby blocks: oak_log (4m N), cobblestone (6m S), water (7m E), wheat (2m NE), sheep (5m W), chest (3m W).\n", n)
		detail, ok := send(client, state, questions)
		verdict := "OK"
		if !ok {
			// A 502 is the gateway failing, not the endpoint ruling on the
			// request. Counting it as a bound would put a line in this report
			// that says nothing about what the endpoint accepts.
			verdict = "5xx"
			if strings.Contains(detail, "400") {
				verdict = "FAIL"
				if firstFail == 0 {
					firstFail = n
				}
			}
		}
		fmt.Printf("%-10d %-6s %d bytes: %s\n", n, verdict, len(state), truncate(detail, 90))
		if firstFail > 0 && n >= firstFail+2 {
			break
		}
	}

	fmt.Println()
	switch {
	case firstFail == 0:
		fmt.Printf("RESULT: full idle question set accepted at every state size tried (up to %d repeats).\n", max)
	default:
		fmt.Printf("RESULT: full idle question set is rejected once state reaches %d repeats.\n", firstFail)
	}
}

// runSingle separates "one criterion" from "that one criterion".
//
// The bisect fails at exactly one criteria entry and passes at two, which is a
// floor rather than the ceiling the production failure implied. Whether the
// endpoint rejects a single-option choice outright, or rejects that particular
// label, changes what the fix is: one is a builder guard, the other is a
// catalogue fix. So this holds the count at one and varies what is in it.
func runSingle(client *jev.Client, state string, base map[string]json.RawMessage) {
	full := affordanceCriteria(true)

	cases := []struct {
		Name string
		Raw  json.RawMessage
	}{
		{"1real", oneOf(full)},
		{"1plain", oneOf(map[string]string{"do_something": "do the one thing that is possible here"})},
		{"1first", oneOf(map[string]string{"rest": full["rest"]})},
		{"2plain", mustChoice(map[string]string{
			"alpha": "do the first possible thing",
			"beta":  "do the second possible thing",
		})},
	}

	fmt.Printf("%-10s %-6s %s\n", "case", "result", "detail")
	for _, c := range cases {
		merged := map[string]json.RawMessage{}
		for k, v := range base {
			merged[k] = v
		}
		merged[jev.QAffordance] = c.Raw
		detail, ok := send(client, state, merged)
		report(c.Name, string(c.Raw), ok, detail)
	}

	// The same single-criterion question with nothing else in the call, so the
	// floor is a property of the question and not of how it combines with the
	// baseline. A single noul goes alongside it as the control: one question,
	// accepted, so "one question" is not itself the problem.
	fmt.Println()
	fmt.Printf("%-10s %-6s %s\n", "bare", "result", "detail")
	bare := []struct {
		Name string
		Raw  json.RawMessage
	}{
		{"1choice", mustChoice(map[string]string{"only": "the single option"})},
		{"2choice", mustChoice(map[string]string{"one": "first", "two": "second"})},
		{"1noul", jev.MustNoul("Is one question by itself acceptable?")},
	}
	for _, c := range bare {
		detail, ok := send(client, state, map[string]json.RawMessage{"q": c.Raw})
		report(c.Name, string(c.Raw), ok, detail)
	}
}

// oneOf builds a one-entry criteria map deterministically from src.
func oneOf(src map[string]string) json.RawMessage {
	names := make([]string, 0, len(src))
	for k := range src {
		names = append(names, k)
	}
	sort.Strings(names)
	return mustChoice(map[string]string{names[0]: src[names[0]]})
}

// mustChoice marshals a choice question over the given criteria, in the exact
// wire shape jev.ChoiceQuestion produces. It is local because this probe may
// only touch its own file, and the shape is three fields of strings and a map.
func mustChoice(criteria map[string]string) json.RawMessage {
	raw, err := json.Marshal(struct {
		Type         string            `json:"type"`
		Instructions string            `json:"instructions"`
		Criteria     map[string]string `json:"criteria"`
	}{Type: "choice", Instructions: "Only these are possible here.", Criteria: criteria})
	if err != nil {
		panic(err)
	}
	return raw
}

// truncate shortens a detail line so one long error cannot flood the report.
func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "..."
}

// runNarrow drives the activity menu through NarrowToGoal, which is the one
// place the idle path can hand BuildActivityQuestion a curriculum of length
// one.
//
// NarrowToGoal keeps only the activities the active goal advances, then appends
// rest unconditionally. Every goal in the catalogue advances two or more
// activities, but the advances are intersected with the curriculum the world
// actually offered — so a goal advancing mine and gather, adopted in a world
// offering neither, narrows to [rest] alone. A one-entry activity criteria map
// is the shape the endpoint refuses.
func runNarrow(client *jev.Client, state string, base map[string]json.RawMessage) {
	// A world with no gatherable block and no room: the curriculum holds rest,
	// wander and explore, none of which gather_wood advances.
	s := snapshot()
	s.NearBlocks = "none"
	s.NearBlocksText = ""
	s.FreeSlots = 0
	s.Craftable = 0
	s.Nearby = nil
	s.LedgeAhead = false
	s.VisibleSigns = nil

	curriculum := agi.Curriculum(s)
	goal := agi.GoalCatalogue[jev.GoalGatherWood]
	narrowed := agi.NarrowToGoal(curriculum, goal)

	fmt.Printf("curriculum: %v\n", curriculum)
	fmt.Printf("goal %q advances: %v\n", goal.Name, goal.Advances)
	fmt.Printf("narrowed:  %v  (%d entries)\n\n", narrowed, len(narrowed))

	fmt.Printf("%-14s %-6s %s\n", "case", "result", "detail")

	// The real builder over the real narrowed menu.
	merged := map[string]json.RawMessage{}
	for k, v := range base {
		merged[k] = v
	}
	for name, raw := range jev.BuildActivityQuestion(narrowed) {
		merged[name] = raw
	}
	detail, ok := send(client, state, merged)
	report("narrowed", string(jev.BuildActivityQuestion(narrowed)[jev.QActivity]), ok, detail)

	// The same menu unrestrained, as a control: same builder, same snapshot,
	// one activity more.
	merged2 := map[string]json.RawMessage{}
	for k, v := range base {
		merged2[k] = v
	}
	for name, raw := range jev.BuildActivityQuestion(curriculum) {
		merged2[name] = raw
	}
	detail, ok = send(client, state, merged2)
	report("un-narrowed", string(jev.BuildActivityQuestion(curriculum)[jev.QActivity]), ok, detail)
}

// send performs one evaluate and flattens the error into a log line.
func send(client *jev.Client, state string, questions map[string]json.RawMessage) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Evaluate(ctx, state, questions)
	if err != nil {
		return strings.Join(strings.Fields(err.Error()), " "), false
	}
	return fmt.Sprintf("%d answers, model=%s", len(resp.Answers), resp.Model), true
}

func report(name, raw string, ok bool, detail string) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
	}
	fmt.Printf("%-14s %-6s %-8d %s\n", name, verdict, len(raw), detail)
}

// keysOf renders a question set's keys in a stable order, so two runs can be
// diffed by eye.
func keysOf(q map[string]json.RawMessage) string {
	names := make([]string, 0, len(q))
	for k := range q {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
