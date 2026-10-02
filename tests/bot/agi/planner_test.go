package agi_test

import (
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/config"
)

// The planner is the one place an untrusted string becomes a plan the bot will
// act on. These tests are mostly about what it refuses: a model that wraps its
// JSON in prose, invents a step kind nobody implements, or writes a wishlist
// forty steps long must not be able to put the bot somewhere unexecutable.

// TestParsePlanReplyReadsTheDocumentedShape is the contract the system prompt
// asks for. If this breaks, either the prompt or the parser drifted and the
// planner is generating plans nobody reads.
func TestParsePlanReplyReadsTheDocumentedShape(t *testing.T) {
	t.Parallel()

	reply := `{"objective":"get a wooden pickaxe","steps":[
		{"kind":"gather","description":"chop some logs","need":["oak_log"]},
		{"kind":"craft","description":"turn logs into a pickaxe","need":["wooden_pickaxe"]}
	],"notes":"needs a safe spot first"}`

	plan, ok := agi.ParsePlanReply(reply, 12)
	if !ok {
		t.Fatal("a well-formed plan was rejected")
	}
	if plan.Objective != "get a wooden pickaxe" {
		t.Errorf("objective = %q", plan.Objective)
	}
	if plan.Notes != "needs a safe spot first" {
		t.Errorf("notes = %q, want them carried through", plan.Notes)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("got %d steps, want 2", len(plan.Steps))
	}
	if plan.Steps[0].Kind != agi.StepGather || plan.Steps[1].Kind != agi.StepCraft {
		t.Errorf("kinds = %q, %q", plan.Steps[0].Kind, plan.Steps[1].Kind)
	}
	if plan.Steps[0].State != agi.StepPending {
		t.Errorf("a fresh plan starts with step %v, want StepPending", plan.Steps[0].State)
	}
	if plan.Steps[0].Target.Set {
		t.Error("a step with no target was marked as having one")
	}
}

// TestParsePlanReplySurvivesHowModelsActuallyReply is the tolerance half.
// Rejecting a plan because it came inside a code fence would mean a planner
// that fails for formatting rather than for thinking, and formatting is by far
// the most common reason to fail.
func TestParsePlanReplySurvivesHowModelsActuallyReply(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		reply string
	}{
		{"fenced", "```json\n{\"objective\":\"look around\",\"steps\":[{\"kind\":\"observe\",\"description\":\"read the signs\"}]}\n```"},
		{"prose before", "Sure! Here is the plan:\n{\"objective\":\"look around\",\"steps\":[{\"kind\":\"observe\",\"description\":\"read the signs\"}]}"},
		{"prose after", "{\"objective\":\"look around\",\"steps\":[{\"kind\":\"observe\",\"description\":\"read the signs\"}]}\nLet me know if you want changes."},
		{"think block", "<think>the bot has no wood, gather first</think>\n{\"objective\":\"look around\",\"steps\":[{\"kind\":\"observe\",\"description\":\"read the signs\"}]}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			plan, ok := agi.ParsePlanReply(tc.reply, 12)
			if !ok {
				t.Fatalf("reply was rejected:\n%s", tc.reply)
			}
			if len(plan.Steps) != 1 || plan.Steps[0].Kind != agi.StepObserve {
				t.Errorf("steps = %+v", plan.Steps)
			}
		})
	}
}

// TestParsePlanReplyRefusesWishlists is the other half. A plan with no
// objective cannot be reported or replanned against, and a plan of steps the
// brain cannot dispatch is an instruction to guess.
func TestParsePlanReplyRefusesWishlists(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		reply string
	}{
		{"no objective", `{"steps":[{"kind":"gather","description":"wood"}]}`},
		{"blank objective", `{"objective":"   ","steps":[{"kind":"gather","description":"wood"}]}`},
		{"no steps", `{"objective":"do something","steps":[]}`},
		{"only unimplementable steps", `{"objective":"conquer the world","steps":[{"kind":"teleport","description":"to the moon"}]}`},
		{"not json", "I would rather chat about it."},
		{"truncated", `{"objective":"go somewhere","steps":[{"kind":"go_to"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if plan, ok := agi.ParsePlanReply(tc.reply, 12); ok {
				t.Errorf("an unusable reply produced a plan: %+v", plan)
			}
		})
	}
}

// TestParsePlanReplyClampsToMaxSteps checks the budget is real. An unbounded
// plan is a wishlist, and a wishlist cannot be finished.
func TestParsePlanReplyClampsToMaxSteps(t *testing.T) {
	t.Parallel()

	var sb strings.Builder
	sb.WriteString(`{"objective":"build a castle","steps":[`)
	for i := 0; i < 20; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"kind":"mine","description":"step ` + string(rune('a'+i)) + `"}`)
	}
	sb.WriteString(`]}`)

	plan, ok := agi.ParsePlanReply(sb.String(), 5)
	if !ok {
		t.Fatal("a long but valid plan was rejected")
	}
	if len(plan.Steps) != 5 {
		t.Errorf("got %d steps, want the clamp at 5", len(plan.Steps))
	}
	// The leading steps are the reachable ones, so clamping the tail is the
	// right end to cut.
	if plan.Steps[0].Description != "step a" {
		t.Errorf("clamping dropped the leading steps: first is %q", plan.Steps[0].Description)
	}
}

// TestParsePlanReplyNormalisesStepKinds checks the supported alias table.
func TestParsePlanReplyNormalisesStepKinds(t *testing.T) {
	t.Parallel()

	reply := `{"objective":"set up camp","steps":[
		{"kind":"GOTO","description":"to the clearing","target":{"x":10,"y":64,"z":-3}},
		{"kind":"collect","description":"some wood","need":["oak_log"]}
	]}`

	plan, ok := agi.ParsePlanReply(reply, 12)
	if !ok {
		t.Fatal("supported aliases were rejected")
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("got %d steps, want 2 supported aliases: %+v", len(plan.Steps), plan.Steps)
	}
	if plan.Steps[0].Kind != agi.StepGoTo {
		t.Errorf("GOTO normalised to %q, want %q", plan.Steps[0].Kind, agi.StepGoTo)
	}
	if plan.Steps[1].Kind != agi.StepGather {
		t.Errorf("collect normalised to %q, want %q", plan.Steps[1].Kind, agi.StepGather)
	}
	// The world origin is a real place, so a target at 0,0,0 has to stay set.
	if !plan.Steps[0].Target.Set || plan.Steps[0].Target.X != 10 || plan.Steps[0].Target.Z != -3 {
		t.Errorf("target = %+v, want the coordinates kept", plan.Steps[0].Target)
	}
}

// Fast regression: an unsupported prerequisite must never disappear.
func TestParsePlanRejectsUnsupportedPrerequisite(t *testing.T) {
	t.Parallel()
	reply := `{"objective":"reach the End","steps":[{"kind":"throw_eye_and_triangulate"},{"kind":"enter"}]}`
	if _, ok := agi.ParsePlanReply(reply, 12); ok {
		t.Fatal("unsupported prerequisite was silently skipped")
	}
}

// TestStepActionMapsEveryKind is the table between the plan vocabulary and the
// action layer. A kind with no action is a step the brain would have to invent
// behaviour for, which is the thing this layer exists to prevent.
func TestStepActionMapsEveryKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind      string
		need      []string
		target    agi.PlanPoint
		wantLabel string
		wantParam string
	}{
		{agi.StepGather, []string{"oak_log"}, agi.PlanPoint{}, "gather", "oak_log"},
		{agi.StepMine, []string{"coal_ore"}, agi.PlanPoint{}, "automine", "coal_ore"},
		{agi.StepGoTo, nil, agi.PlanPoint{X: 1, Y: 2, Z: 3, Set: true}, "goto", "1,2,3"},
		{agi.StepGoTo, []string{"oak_log"}, agi.PlanPoint{}, "goto", "oak_log"},
		{agi.StepCraft, []string{"wooden_pickaxe"}, agi.PlanPoint{}, "craft", "wooden_pickaxe"},
		{agi.StepEquip, []string{"shield"}, agi.PlanPoint{}, "equip", "shield"},
		{agi.StepCombat, nil, agi.PlanPoint{}, "attack", ""},
		{agi.StepStorage, []string{"cobblestone"}, agi.PlanPoint{}, "store", "cobblestone"},
		{agi.StepStorage, nil, agi.PlanPoint{}, "storeall", ""},
		{agi.StepShelter, nil, agi.PlanPoint{}, "shelter", ""},
		{agi.StepObserve, nil, agi.PlanPoint{}, "readsign", ""},
		{agi.StepEnter, nil, agi.PlanPoint{}, "enterportal", ""},
	}

	for _, tc := range cases {
		t.Run(tc.kind+"_to_"+tc.wantLabel, func(t *testing.T) {
			t.Parallel()
			label, param, ok := agi.StepAction(agi.PlanStep{Kind: tc.kind, Need: tc.need, Target: tc.target})
			if !ok {
				t.Fatalf("kind %q was refused", tc.kind)
			}
			if label != tc.wantLabel || param != tc.wantParam {
				t.Errorf("got %q:%q, want %q:%q", label, param, tc.wantLabel, tc.wantParam)
			}
		})
	}
}

// TestStepActionTreatsRestAsAnActionThatDoesNothing checks the no-op kinds
// resolve successfully with an empty label. Rest and wait are real instructions
// satisfied by standing still; routing them through a dummy action would be a
// lie about what the bot did.
func TestStepActionTreatsRestAsAnActionThatDoesNothing(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{agi.StepRest, agi.StepWait} {
		label, _, ok := agi.StepAction(agi.PlanStep{Kind: kind})
		if !ok {
			t.Errorf("%q was refused, want it resolved as a no-op", kind)
		}
		if label != "" {
			t.Errorf("%q resolved to action %q, want no action at all", kind, label)
		}
	}
}

// TestStepActionRefusesUnrunnableSteps covers the other outcome: a step that
// cannot be executed as written is reported as unrunnable so the planner finds
// out, rather than being quietly skipped and the plan quietly lying.
func TestStepActionRefusesUnrunnableSteps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		step agi.PlanStep
	}{
		{"craft with nothing to make", agi.PlanStep{Kind: agi.StepCraft}},
		{"equip with nothing to equip", agi.PlanStep{Kind: agi.StepEquip}},
		{"go_to with nowhere to go", agi.PlanStep{Kind: agi.StepGoTo}},
		{"kind nobody implemented", agi.PlanStep{Kind: "teleport"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, _, ok := agi.StepAction(tc.step); ok {
				t.Error("an unrunnable step was accepted")
			}
		})
	}
}

// TestPlanLifecycleAdvancesOnlyOnSuccess is the heart of the whole substrate: a
// step is done when it was done. A lifecycle that advanced on failure would let
// a bot report an objective it quietly routed around.
func TestPlanLifecycleAdvancesOnlyOnSuccess(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})
	r.SetPlan(agi.Plan{
		Objective: "get a wooden pickaxe",
		Steps: []agi.PlanStep{
			{Kind: agi.StepGather, Description: "chop logs"},
			{Kind: agi.StepCraft, Description: "make a pickaxe"},
		},
	})

	step, index, ok := r.CurrentStep()
	if !ok || index != 0 || step.Kind != agi.StepGather {
		t.Fatalf("first step = %+v at %d, ok=%v", step, index, ok)
	}
	r.StartStep(index)
	if next, done := r.CompleteStep(index); next != 1 || done {
		t.Errorf("after completing step 0: next=%d done=%v, want next=1 done=false", next, done)
	}

	step, index, _ = r.CurrentStep()
	if step.Kind != agi.StepCraft {
		t.Fatalf("second step = %+v, want the craft", step)
	}

	// A failure does not move the plan on, and it is visible as a failure
	// rather than as progress.
	r.StartStep(index)
	r.FailStep(index, "no logs in the inventory")
	if !agi.PlanHasFailedStep(r.CurrentPlan()) {
		t.Error("a failed step is not visible to the planner")
	}
	// A failed step is not offered back to the executor either. There is
	// nothing left to execute, and handing the next pending step to the brain
	// here is exactly the "skip past the failure" behaviour the design forbids.
	if _, _, ok := r.CurrentStep(); ok {
		t.Error("CurrentStep offered a step to run after the plan had a failure to hand back")
	}

	// The last step finishing ends the plan.
	r.SetPlan(agi.Plan{Objective: "one step", Steps: []agi.PlanStep{{Kind: agi.StepWait, Description: "wait"}}})
	r.StartStep(0)
	if _, done := r.CompleteStep(0); !done {
		t.Error("completing the only step did not finish the plan")
	}
}

// TestAbandonFailedStepGivesTheStepBack is the retry path. A step that failed
// for a transient reason has to be able to become the current step again,
// otherwise one bad click ends the plan.
func TestAbandonFailedStepGivesTheStepBack(t *testing.T) {
	t.Parallel()

	r := agi.NewBareForTest(agi.Config{})
	r.SetPlan(agi.Plan{
		Objective: "two steps",
		Steps: []agi.PlanStep{
			{Kind: agi.StepGather, Description: "chop logs", State: agi.StepDone},
			{Kind: agi.StepCraft, Description: "make a pickaxe", State: agi.StepFailed, Note: "interrupted"},
		},
	})

	r.AbandonFailedStep(1)
	step, index, ok := r.CurrentStep()
	if !ok || index != 1 {
		t.Fatalf("after abandoning, current step is %d ok=%v, want the failed step at 1", index, ok)
	}
	if step.State != agi.StepActive {
		t.Errorf("state = %v, want StepActive", step.State)
	}
	if step.Note != "" {
		t.Errorf("note = %q, want it cleared on retry", step.Note)
	}
	// The already-done step must stay done. Abandoning one step is not a reason
	// to redo the plan from the start.
	if r.CurrentPlan().Steps[0].State != agi.StepDone {
		t.Error("abandoning a step un-completed an earlier one")
	}
}

// TestReplanScheduleBoundsStaleness checks the plan re-reads itself. A bot
// that pursues a stale objective forever is stuck, not persistent.
func TestReplanScheduleBoundsStaleness(t *testing.T) {
	t.Parallel()

	now := time.Now()
	plan := agi.Plan{
		Objective:   "build a shelter",
		LastPlanned: now,
		ReplanAfter: 10 * time.Minute,
		Expires:     now.Add(time.Hour),
	}

	if plan.PlanDueForReplanning(now.Add(5 * time.Minute)) {
		t.Error("a fresh plan was already due for a replan")
	}
	if !plan.PlanDueForReplanning(now.Add(11 * time.Minute)) {
		t.Error("a plan past its replan interval is not due")
	}
	if plan.PlanExpired(now.Add(30 * time.Minute)) {
		t.Error("a plan expired before its lifetime elapsed")
	}
	if !plan.PlanExpired(now.Add(2 * time.Hour)) {
		t.Error("a plan outlived its lifetime without expiring")
	}
	// A plan with no replan interval is not asked about on a schedule; it is
	// left alone until it finishes or expires.
	quiet := agi.Plan{Objective: "x", LastPlanned: now, Expires: now.Add(time.Hour)}
	if quiet.PlanDueForReplanning(now.Add(30 * time.Minute)) {
		t.Error("a plan with no replan interval is being re-read on a timer")
	}
	// Expiry still applies, whatever the interval says.
	if !quiet.PlanDueForReplanning(now.Add(2 * time.Hour)) {
		t.Error("an expired plan with no replan interval is never re-examined")
	}
}

// TestRenderPlanShowsProgress is the log and prompt shape. A planner reasoning
// about a long unstructured blob makes worse plans than one reasoning about
// "step 2 of 5, currently: mine obsidian".
func TestRenderPlanShowsProgress(t *testing.T) {
	t.Parallel()

	plan := agi.Plan{
		Objective: "reach a stronghold",
		Steps: []agi.PlanStep{
			{Kind: agi.StepCraft, Description: "make an iron pickaxe", State: agi.StepDone},
			{Kind: agi.StepGoTo, Description: "find the portal room", Target: agi.PlanPoint{X: 5, Y: 12, Z: -40, Set: true}, State: agi.StepActive, Need: []string{"diamond"}},
			{Kind: agi.StepCombat, Description: "take on the dragon", State: agi.StepFailed, Note: "no weapon"},
		},
	}

	rendered := agi.RenderPlan(plan)
	for _, want := range []string{
		"reach a stronghold",
		"steps 1/3 done",
		"[craft]",
		"[go_to]",
		"5,12,-40",
		"needs diamond",
		"no weapon",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered plan is missing %q:\n%s", want, rendered)
		}
	}
	if agi.RenderPlan(agi.Plan{}) != "no plan" {
		t.Error("an empty plan should render as 'no plan'")
	}
}

// TestPlanningModeIsOffUnlessConfigSaysOtherwise is the M1 claim: planning
// mode is a switch, and the switch defaults to off.
//
// A mode that defaulted to on would change how the bot behaves for anyone who
// upgrades, which is not what "additive" is supposed to mean. This also covers
// the alias handling — a typo must land on default rather than leaving the bot
// half-configured.
func TestPlanningModeIsOffUnlessConfigSaysOtherwise(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want string
	}{
		{"", config.ModeDefault},
		{"default", config.ModeDefault},
		{"planning", config.ModePlanning},
		{"plan", config.ModePlanning},
		{"long_horizon", config.ModePlanning},
		{"PLANNING", config.ModePlanning},
		{"  planning  ", config.ModePlanning},
		{"planing", config.ModeDefault}, // typo
		{"nonsense", config.ModeDefault},
	}

	for _, tc := range cases {
		t.Run("mode_"+tc.raw, func(t *testing.T) {
			t.Parallel()
			got := config.NormalizeMode(tc.raw)
			if got != tc.want {
				t.Errorf("NormalizeMode(%q) = %q, want %q", tc.raw, got, tc.want)
			}
			// The value has to survive the trip into the runner, or the switch
			// is configured but never read.
			runnerCfg := agi.ConfigFrom(config.AGIConfig{Mode: tc.raw})
			if runnerCfg.Mode != tc.want {
				t.Errorf("ConfigFrom(%q).Mode = %q, want %q", tc.raw, runnerCfg.Mode, tc.want)
			}
		})
	}
}

// TestPlanningConfigCarriesThePlanBudget checks the numbers the planner is
// steered by actually reach the runner. A plan budget that lives in config but
// never arrives would leave the planner running on a zero.
func TestPlanningConfigCarriesThePlanBudget(t *testing.T) {
	t.Parallel()

	runnerCfg := agi.ConfigFrom(config.AGIConfig{
		Mode:            "planning",
		PlanLifetimeMin: 45,
		PlanReplanMin:   7,
		PlanMaxSteps:    9,
	})
	if runnerCfg.Mode != config.ModePlanning {
		t.Errorf("Mode = %q", runnerCfg.Mode)
	}
	if runnerCfg.PlanLifetimeMin != 45 || runnerCfg.PlanReplanMin != 7 || runnerCfg.PlanMaxSteps != 9 {
		t.Errorf("plan budget = %d/%d/%d, want 45/7/9",
			runnerCfg.PlanLifetimeMin, runnerCfg.PlanReplanMin, runnerCfg.PlanMaxSteps)
	}

	// The prompt has to be told the same budget the code clamps to, or the
	// planner writes wishlists the code then silently truncates.
	if !strings.Contains(agi.PlannerSystemPrompt(runnerCfg.PlanMaxSteps), "2 to 9 steps") {
		t.Errorf("planner prompt does not state the 9-step budget:\n%s", agi.PlannerSystemPrompt(runnerCfg.PlanMaxSteps))
	}
}
