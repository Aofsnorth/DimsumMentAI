package chat_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/chat"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/event"
)

func TestMultipleMainPlayersConfigAndMatching(t *testing.T) {
	t.Parallel()

	cfg := config.AIConfig{
		MainPlayer:  "Synterix, Einji",
		MainPlayers: []string{"AdminPlayer", "Moderator"},
	}

	players := cfg.GetMainPlayers()
	if len(players) != 4 {
		t.Fatalf("expected 4 main players, got %d: %v", len(players), players)
	}

	testCases := []struct {
		name      string
		player    string
		wantMatch bool
	}{
		{"First comma-separated player", "Synterix", true},
		{"Second comma-separated player", "Einji", true},
		{"Slice player 1", "AdminPlayer", true},
		{"Slice player 2", "Moderator", true},
		{"Geyser dot prefix match", ".Synterix", true},
		{"Geyser dot prefix match for slice", ".AdminPlayer", true},
		{"Case insensitive match", "einji", true},
		{"Unknown player", "RandomGamer", false},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := cfg.IsMainPlayer(tc.player)
			if got != tc.wantMatch {
				t.Errorf("IsMainPlayer(%q) = %v, want %v", tc.player, got, tc.wantMatch)
			}
		})
	}
}

func TestShouldProcessChat_MultipleMainPlayers(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:           "Luna",
		ConfiguredName: "Luna",
		AiCfg: config.AIConfig{
			MainPlayers:               []string{"Synterix", "Einji"},
			RespondOnlyToLinkedPlayer: true,
			RespondOnlyWhenTagged:     true,
		},
	}

	// Message from Synterix tagged to Luna -> allowed
	evt1 := event.ChatEvent{SourceName: "Synterix"}
	if !chat.ShouldProcessChatForTest(b, evt1, "@Luna halo", "Luna") {
		t.Errorf("expected Synterix chat to be processed")
	}

	// Message from Einji tagged to Luna -> allowed
	evt2 := event.ChatEvent{SourceName: "Einji"}
	if !chat.ShouldProcessChatForTest(b, evt2, "@Luna halo", "Luna") {
		t.Errorf("expected Einji chat to be processed")
	}

	// Message from unlinked player tagged to Luna -> blocked
	evt3 := event.ChatEvent{SourceName: "Stranger"}
	if chat.ShouldProcessChatForTest(b, evt3, "@Luna halo", "Luna") {
		t.Errorf("expected Stranger chat to be ignored when RespondOnlyToLinkedPlayer=true")
	}
}
