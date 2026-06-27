package ai

import (
	"strings"
	"testing"
)

func TestParse_NoActions(t *testing.T) {
	t.Parallel()
	r := Parse("Halo, apa kabar?")
	if r.CleanReply != "Halo, apa kabar?" {
		t.Errorf("CleanReply = %q, want %q", r.CleanReply, "Halo, apa kabar?")
	}
	if len(r.Actions) != 0 {
		t.Errorf("Actions = %v, want empty", r.Actions)
	}
}

func TestParse_SingleAction(t *testing.T) {
	t.Parallel()
	r := Parse("Aku datang! <action>come:Steve</action>")
	if r.CleanReply != "Aku datang!" {
		t.Errorf("CleanReply = %q, want %q", r.CleanReply, "Aku datang!")
	}
	if len(r.Actions) != 1 {
		t.Fatalf("Actions len = %d, want 1", len(r.Actions))
	}
	if r.Actions[0].Label != "come" {
		t.Errorf("Label = %q, want %q", r.Actions[0].Label, "come")
	}
	if r.Actions[0].Param != "Steve" {
		t.Errorf("Param = %q, want %q", r.Actions[0].Param, "Steve")
	}
}

func TestParse_MultipleActions(t *testing.T) {
	t.Parallel()
	r := Parse("Oke! <action>gather:wood,5</action> lalu <action>craft:planks</action>")
	if len(r.Actions) != 2 {
		t.Fatalf("Actions len = %d, want 2", len(r.Actions))
	}
	if r.Actions[0].Label != "gather" || r.Actions[0].Param != "wood,5" {
		t.Errorf("Action[0] = %+v", r.Actions[0])
	}
	if r.Actions[1].Label != "craft" || r.Actions[1].Param != "planks" {
		t.Errorf("Action[1] = %+v", r.Actions[1])
	}
}

func TestParse_ActionWithoutParam(t *testing.T) {
	t.Parallel()
	r := Parse("<action>stop</action>")
	if len(r.Actions) != 1 {
		t.Fatalf("Actions len = %d, want 1", len(r.Actions))
	}
	if r.Actions[0].Label != "stop" {
		t.Errorf("Label = %q, want %q", r.Actions[0].Label, "stop")
	}
	if r.Actions[0].Param != "" {
		t.Errorf("Param = %q, want empty", r.Actions[0].Param)
	}
}

func TestParse_EmptyActionTag(t *testing.T) {
	t.Parallel()
	r := Parse("Done <action></action> ok")
	if len(r.Actions) != 0 {
		t.Errorf("Actions = %v, want empty for empty tag", r.Actions)
	}
}

func TestParse_WhitespaceOnlyAction(t *testing.T) {
	t.Parallel()
	r := Parse("Done <action>   </action> ok")
	if len(r.Actions) != 0 {
		t.Errorf("Actions = %v, want empty for whitespace-only tag", r.Actions)
	}
}

func TestParse_UnclosedActionTag(t *testing.T) {
	t.Parallel()
	r := Parse("Doing stuff <action>drop:crafting_table</")
	if len(r.Actions) != 0 {
		t.Errorf("Actions = %v, want empty for unclosed tag", r.Actions)
	}
	if r.CleanReply == "" {
		t.Error("CleanReply should not be empty even with unclosed tag")
	}
}

func TestParse_StripsThinkBlocks(t *testing.T) {
	t.Parallel()
	input := "<think>Let me reason about this...</think>Hello there!"
	r := Parse(input)
	if r.CleanReply != "Hello there!" {
		t.Errorf("CleanReply = %q, want %q (think block not stripped)", r.CleanReply, "Hello there!")
	}
}

func TestParse_StripsUnclosedThinkBlock(t *testing.T) {
	t.Parallel()
	input := "<think>This is my reasoning that got cut off by max tokens"
	r := Parse(input)
	if r.CleanReply != "" {
		t.Errorf("CleanReply = %q, want empty (unclosed think should be fully stripped)", r.CleanReply)
	}
}

func TestParse_StripsLeadingThinkClose(t *testing.T) {
	t.Parallel()
	input := "</think>Here is my answer."
	r := Parse(input)
	if r.CleanReply != "Here is my answer." {
		t.Errorf("CleanReply = %q, want %q", r.CleanReply, "Here is my answer.")
	}
}

func TestParse_CollapsesWhitespace(t *testing.T) {
	t.Parallel()
	r := Parse("Hello\n\n\n   world\t\ttab")
	if r.CleanReply != "Hello world tab" {
		t.Errorf("CleanReply = %q, want %q", r.CleanReply, "Hello world tab")
	}
}

func TestParse_ActionWithExtraSpaces(t *testing.T) {
	t.Parallel()
	r := Parse("<action>  gather  :  wood  </action>")
	if len(r.Actions) != 1 {
		t.Fatalf("Actions len = %d, want 1", len(r.Actions))
	}
	if r.Actions[0].Label != "gather" {
		t.Errorf("Label = %q, want %q", r.Actions[0].Label, "gather")
	}
	if r.Actions[0].Param != "wood" {
		t.Errorf("Param = %q, want %q", r.Actions[0].Param, "wood")
	}
}

func TestParse_MultilineAction(t *testing.T) {
	t.Parallel()
	r := Parse("Sure\n<action>build:\nhouse\n</action>\nDone")
	if len(r.Actions) != 1 {
		t.Fatalf("Actions len = %d, want 1", len(r.Actions))
	}
	if r.Actions[0].Label != "build" {
		t.Errorf("Label = %q, want %q", r.Actions[0].Label, "build")
	}
}

func TestParse_PlanSteps(t *testing.T) {
	t.Parallel()
	r := Parse("Siap, aku bikin crafting table ya.\n<plan>\n<step>gather:oak_log,4</step>\n<step>craft:oak_planks,16</step>\n<step>craft:crafting_table,1</step>\n</plan>")
	if r.CleanReply != "Siap, aku bikin crafting table ya." {
		t.Errorf("CleanReply = %q, want clean text without plan", r.CleanReply)
	}
	if len(r.PlanSteps) != 3 {
		t.Fatalf("PlanSteps len = %d, want 3", len(r.PlanSteps))
	}
	if r.PlanSteps[0] != "gather:oak_log,4" {
		t.Errorf("PlanSteps[0] = %q, want 'gather:oak_log,4'", r.PlanSteps[0])
	}
	if r.PlanSteps[2] != "craft:crafting_table,1" {
		t.Errorf("PlanSteps[2] = %q, want 'craft:crafting_table,1'", r.PlanSteps[2])
	}
}

func TestParse_PlanStepsEmpty(t *testing.T) {
	t.Parallel()
	r := Parse("Halo, apa kabar?")
	if len(r.PlanSteps) != 0 {
		t.Errorf("PlanSteps = %v, want empty", r.PlanSteps)
	}
}

func TestParse_PlanStrippedFromCleanReply(t *testing.T) {
	t.Parallel()
	r := Parse("Oke! <plan><step>come</step></plan>")
	if r.CleanReply != "Oke!" {
		t.Errorf("CleanReply = %q, want 'Oke!'", r.CleanReply)
	}
}

func TestParsePlanSteps_FromReplan(t *testing.T) {
	t.Parallel()
	reply := "Hmm, kurang kayu. <replan>\n<step>gather:oak_log,2</step>\n<step>craft:oak_planks,8</step>\n</replan>"
	steps := ParsePlanSteps(reply)
	if len(steps) != 2 {
		t.Fatalf("steps len = %d, want 2", len(steps))
	}
	if steps[0] != "gather:oak_log,2" {
		t.Errorf("steps[0] = %q, want 'gather:oak_log,2'", steps[0])
	}
}

func TestParsePlanSteps_FromPlan(t *testing.T) {
	t.Parallel()
	reply := "<plan><step>come</step><step>follow</step></plan>"
	steps := ParsePlanSteps(reply)
	if len(steps) != 2 {
		t.Fatalf("steps len = %d, want 2", len(steps))
	}
	if steps[0] != "come" {
		t.Errorf("steps[0] = %q, want 'come'", steps[0])
	}
}

func TestParsePlanSteps_None(t *testing.T) {
	t.Parallel()
	steps := ParsePlanSteps("Just chatting, no plan here.")
	if steps != nil {
		t.Errorf("steps = %v, want nil", steps)
	}
}

func TestParse_FollowupTag(t *testing.T) {
	t.Parallel()
	r := Parse("Oke aku cek dulu. <action>inventory</action><followup>2</followup>")
	if r.FollowupSec != 2 {
		t.Errorf("FollowupSec = %d, want 2", r.FollowupSec)
	}
	if r.CleanReply != "Oke aku cek dulu." {
		t.Errorf("CleanReply = %q, want 'Oke aku cek dulu.'", r.CleanReply)
	}
	if len(r.Actions) != 1 {
		t.Errorf("Actions len = %d, want 1", len(r.Actions))
	}
}

func TestParse_FollowupTag_StrippedFromReply(t *testing.T) {
	t.Parallel()
	r := Parse("Bentar ya. <followup>5</followup>")
	if strings.Contains(r.CleanReply, "<followup>") {
		t.Errorf("CleanReply should not contain <followup> tag, got %q", r.CleanReply)
	}
}

func TestParse_NoFollowup(t *testing.T) {
	t.Parallel()
	r := Parse("Halo!")
	if r.FollowupSec != 0 {
		t.Errorf("FollowupSec = %d, want 0", r.FollowupSec)
	}
}

func TestParse_FollowupChained(t *testing.T) {
	t.Parallel()
	// A followup reply can itself contain another followup
	r := Parse("Masih ngecek... <followup>3</followup>")
	if r.FollowupSec != 3 {
		t.Errorf("FollowupSec = %d, want 3", r.FollowupSec)
	}
}
