package bot_test

import (
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
)

// A bot that announces every tree is not a character, it is a build log.
//
// The live run produced four actions and four lines of chat: "Kayunya kekumpul
// lagi, stok makin aman buat crafting", "Kayu oak keteme lagi, buat anytime".
// Each one was individually correct — the gathers really did succeed — and
// together they are why the bot reads as a reporting system rather than a
// player. Nobody says "got 10 more logs" out loud after every tree.
//
// The rule this encodes: silence is the default for routine work, and a message
// is something you spend when it means something.

// TestRoutineSuccessesAreQuiet is the reported behaviour, stated as a contract.
func TestRoutineSuccessesAreQuiet(t *testing.T) {
	t.Parallel()

	routine := []event.ActionStatus{
		{Action: "gather", Item: "oak_log", Count: 10, Success: true},
		{Action: "mine", Item: "coal", Count: 6, Success: true},
		{Action: "automine", Item: "iron_ore", Count: 3, Success: true},
		{Action: "harvest", Item: "wheat", Count: 12, Success: true},
		{Action: "fish", Item: "cod", Count: 2, Success: true},
		{Action: "loot", Count: 4, Success: true},
		{Action: "store", Item: "cobblestone", Count: 40, Success: true},
		// Arrival is visible, not news: the player watches the walk end.
		// Stating it reads as a report, and a premature one as a lie.
		{Action: "goto", Item: "60,76,138", Success: true},
		{Action: "gotoblock", Item: "oak_log", Success: true},
		{Action: "standon", Item: "60,76,138", Success: true},
		{Action: "come", Item: "Arthenyxx", Success: true},
	}

	for _, status := range routine {
		if bot.ShouldNarrateStatus(status) {
			t.Errorf("ShouldNarrateStatus(%s) = true, want false: routine work nobody asked about should be silent",
				status.Action)
		}
	}
}

// TestFailuresAreAlwaysNarrated is the asymmetry that makes the silence safe. A
// player needs to know something did not work; a player does not need to know
// another tree fell.
func TestFailuresAreAlwaysNarrated(t *testing.T) {
	t.Parallel()

	failures := []event.ActionStatus{
		{Action: "gather", Item: "oak_log", Success: false, Error: "tidak ada pohon"},
		{Action: "mine", Item: "diamond_ore", Success: false},
		{Action: "craft", Item: "chest", Success: false, Error: "bahan kurang"},
		{Action: "build", Success: false},
	}

	for _, status := range failures {
		if !bot.ShouldNarrateStatus(status) {
			t.Errorf("ShouldNarrateStatus(%s, failed) = false, want true: the player has to be told",
				status.Action)
		}
	}
}

// TestASuccessWithAnErrorStillTalks covers the mixed case. A status carrying
// both a success flag and an error is a contradiction, and the error is the part
// worth surfacing.
func TestASuccessWithAnErrorStillTalks(t *testing.T) {
	t.Parallel()

	status := event.ActionStatus{Action: "gather", Item: "oak_log", Success: true, Error: "terbatas"}

	if !bot.ShouldNarrateStatus(status) {
		t.Error("a status carrying an error was silenced: the contradiction is exactly what the player needs to see")
	}
}

// TestDistinctiveResultsStillTalk keeps the rule from becoming a mute button. A
// crafting table is a thing the player asked for and is waiting on; announcing
// it is what makes the bot useful rather than merely quiet.
func TestDistinctiveResultsStillTalk(t *testing.T) {
	t.Parallel()

	// Note what is absent: emote. A player who nods does not also announce it
	// in chat, and "I nodded" is precisely the kind of line that makes a bot
	// read as a reporting system.
	notable := []event.ActionStatus{
		{Action: "craft", Item: "crafting_table", Count: 1, Success: true},
		{Action: "craft", Item: "wooden_pickaxe", Count: 1, Success: true},
		{Action: "build", Item: "stone_brick", Count: 64, Success: true},
		{Action: "follow", Item: "Arthenyxx", Success: true},
	}

	for _, status := range notable {
		if !bot.ShouldNarrateStatus(status) {
			t.Errorf("ShouldNarrateStatus(%s, %s) = false, want true: a result the player is waiting on should be reported",
				status.Action, status.Item)
		}
	}
}

// A bot that finishes the job and says nothing is the same failure from the
// player's side as a bot that never finished it.
//
// The live run: a player asked for wood, the bot felled nine trees and reported
// "Wood gathering finished collected=10 target=10", and then went quiet. Nothing
// wrong with the work. The silence was the table above doing exactly what it
// said — "chop" and "gather" are both routine, so the closing line was routine
// too, and the one message the player was actually waiting for was suppressed
// along with the per-tree noise.
//
// Terminal is what separates the two. The per-tree reports stay silent; the one
// that closes the job does not.
func TestTerminalReportsTalkEvenWhenTheActionIsRoutine(t *testing.T) {
	t.Parallel()

	terminal := []event.ActionStatus{
		{Action: "gather", Item: "oak_log", Count: 10, Success: true, Terminal: true},
		{Action: "chop", Item: "log", Count: 5, Success: true, Terminal: true},
		{Action: "mine", Item: "cobblestone", Count: 64, Success: true, Terminal: true},
		{Action: "automine", Item: "iron_ore", Count: 12, Success: true, Terminal: true},
		{Action: "fish", Item: "cod", Count: 3, Success: true, Terminal: true},
	}

	for _, status := range terminal {
		if !bot.ShouldNarrateStatus(status) {
			t.Errorf("ShouldNarrateStatus(%s, terminal) = false, want true: this is the line that "+
				"closes the job the player asked for", status.Action)
		}
	}
}

// TestTerminalIsTheOnlyDifference is the guard on the guard. If Terminal made
// everything talk, the per-tree silence above would be undone and the bot goes
// back to announcing every log. Same action, same item, same count — only the
// flag differs, and it has to be the whole difference.
func TestTerminalIsTheOnlyDifference(t *testing.T) {
	t.Parallel()

	progress := event.ActionStatus{Action: "chop", Item: "log", Count: 5, Success: true}
	closing := progress
	closing.Terminal = true

	if bot.ShouldNarrateStatus(progress) {
		t.Error("routine progress talked; the whole point of Terminal is that this stays silent")
	}
	if !bot.ShouldNarrateStatus(closing) {
		t.Error("the closing line was silenced; that is the bug this flag exists to fix")
	}
}

// TestATerminalPartialFailureStillReportsWhatWasShort stops the flag from
// becoming an excuse. "Only got 3 of 10" is a result the player needs, and it
// arrives as a terminal report that failed.
func TestATerminalPartialFailureStillReportsWhatWasShort(t *testing.T) {
	t.Parallel()

	status := event.ActionStatus{
		Action:   "gather",
		Item:     "oak_log",
		Count:    3,
		Success:  false,
		Terminal: true,
		Error:    "hanya dapat 3 dari 10 oak_log",
	}

	if !bot.ShouldNarrateStatus(status) {
		t.Error("a terminal partial failure was silenced; the shortfall is the thing to report")
	}
}
