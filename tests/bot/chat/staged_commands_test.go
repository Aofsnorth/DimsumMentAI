package chat_test

import (
	"testing"

	"bedrock-ai/internal/bot/chat"
)

// stagedAsserts compares the parsed expansion against (label, param) pairs.
func stagedAsserts(t *testing.T, msg string, want ...[2]string) {
	t.Helper()
	steps, _, ok := chat.ParseStagedCommandForTest(msg)
	if !ok {
		t.Fatalf("parse(%q) was not recognised as a staged routine", msg)
	}
	if len(steps) != len(want) {
		t.Fatalf("parse(%q) produced %d steps, want %d: %+v", msg, len(steps), len(want), steps)
	}
	for i, s := range steps {
		if s.Label != want[i][0] || s.Param != want[i][1] {
			t.Fatalf("parse(%q) step %d = %s:%s, want %s:%s (all: %+v)",
				msg, i, s.Label, s.Param, want[i][0], want[i][1], steps)
		}
	}
}

// TestStagedRoutineThePlayerTyped is the request that drove this feature,
// verbatim: three stages, the third counted once, the whole thing five times.
func TestStagedRoutineThePlayerTyped(t *testing.T) {
	t.Parallel()

	msg := `luna coba lompat, jalan 1 block, muter 1kali sebanyak 5 kali`
	steps, summary, ok := chat.ParseStagedCommandForTest(msg)
	if !ok {
		t.Fatal("the staged routine was not recognised at all")
	}
	if len(steps) != 15 {
		t.Fatalf("expansion produced %d steps, want 15 (3 stages × 5): %+v", len(steps), steps)
	}
	for round := 0; round < 5; round++ {
		base := round * 3
		if steps[base].Label != "emote" || steps[base].Param != "jump" {
			t.Fatalf("round %d step 0 = %+v, want emote:jump", round, steps[base])
		}
		if steps[base+1].Label != "walk" || steps[base+1].Param != "1" {
			t.Fatalf("round %d step 1 = %+v, want walk:1", round, steps[base+1])
		}
		if steps[base+2].Label != "emote" || steps[base+2].Param != "spin" {
			t.Fatalf("round %d step 2 = %+v, want emote:spin", round, steps[base+2])
		}
	}
	if summary == "" {
		t.Fatal("empty summary: the model would have nothing factual to narrate")
	}
}

// TestStagedSpokenSeparators: not every player uses commas.
func TestStagedSpokenSeparators(t *testing.T) {
	t.Parallel()

	stagedAsserts(t, "lompat terus jalan 2 block lalu muter",
		[2]string{"emote", "jump"},
		[2]string{"walk", "2"},
		[2]string{"emote", "spin"},
	)
	stagedAsserts(t, "lompat; jalan 1 block",
		[2]string{"emote", "jump"},
		[2]string{"walk", "1"},
	)
}

// TestStagedCounts applies to the right level: "N kali" repeats its own stage,
// "sebanyak N kali" repeats everything, and the two never blur into each other.
func TestStagedCounts(t *testing.T) {
	t.Parallel()

	// One stage, counted: three hops.
	stagedAsserts(t, "lompat 3 kali",
		[2]string{"emote", "jump"},
		[2]string{"emote", "jump"},
		[2]string{"emote", "jump"},
	)
	// A stage counted inside a repeated routine: "muter 2kali" is two spins
	// per round, "sebanyak 3 kali" is three rounds → six steps.
	stagedAsserts(t, "jongkok sebanyak 2 kali, muter 2kali sebanyak 3 kali",
		[2]string{"emote", "sneak"},
		[2]string{"emote", "sneak"},
		[2]string{"emote", "spin"},
		[2]string{"emote", "spin"},
		[2]string{"emote", "sneak"},
		[2]string{"emote", "sneak"},
		[2]string{"emote", "spin"},
		[2]string{"emote", "spin"},
		[2]string{"emote", "sneak"},
		[2]string{"emote", "sneak"},
		[2]string{"emote", "spin"},
		[2]string{"emote", "spin"},
	)
	// A walk with a distance on its own is a staged single step.
	stagedAsserts(t, "jalan 3 block",
		[2]string{"walk", "3"},
	)
}

// TestStagedIsNotTriggeredByConversation is the false-positive half: messages
// that merely mention the verbs, or ask a question, belong to the model.
func TestStagedIsNotTriggeredByConversation(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{
		"lompat",
		"bisa lompat ngak?",
		"jalan yuk ke base",
		"jalan ke 100 65 200",
		"rumahku 10 block dari sini lho",
		"kamu nggak di whitelist kan",
	} {
		if _, _, ok := chat.ParseStagedCommandForTest(msg); ok {
			t.Errorf("parse(%q) claimed a staged routine; that line belongs to the model", msg)
		}
	}
}

// TestStagedRefusesAPartialParse: a stage the parser does not know must fail
// the whole routine rather than executing a truncated version of what the
// player asked for.
func TestStagedRefusesAPartialParse(t *testing.T) {
	t.Parallel()

	for _, msg := range []string{
		"lompat, bikin stik",
		"jalan 1 block ke rumah",
		"lompat terus serang ayam",
	} {
		if _, _, ok := chat.ParseStagedCommandForTest(msg); ok {
			t.Errorf("parse(%q) executed a truncated routine; an unknown stage must fail the parse", msg)
		}
	}
}

// TestStagedCountsAreBounded: an unbounded count is a player asking the bot to
// hop until the world ends. The caps keep a routine finite.
func TestStagedCountsAreBounded(t *testing.T) {
	t.Parallel()

	steps, _, ok := chat.ParseStagedCommandForTest("lompat 999 kali")
	if !ok {
		t.Fatal("a large count was refused instead of bounded")
	}
	if len(steps) > 20 {
		t.Fatalf("expansion produced %d steps; the per-stage cap did not hold", len(steps))
	}

	steps, _, ok = chat.ParseStagedCommandForTest("lompat 5 kali sebanyak 999 kali")
	if !ok {
		t.Fatal("a large routine repeat was refused instead of bounded")
	}
	if len(steps) > 60 {
		t.Fatalf("expansion produced %d steps; the total cap did not hold", len(steps))
	}
}
