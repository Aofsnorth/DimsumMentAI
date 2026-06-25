package ai

import (
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
