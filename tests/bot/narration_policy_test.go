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
