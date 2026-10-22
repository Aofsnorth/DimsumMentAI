package action_test

import (
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/config"
)

func TestParseNavCoordsRejectsNaNInfAndOutOfBounds(t *testing.T) {
	invalid := []string{
		"NaN, 64, 10",
		"10, +Inf, 10",
		"10, 64, -Inf",
		"1e20, 64, 10",
		"-30000001, 64, 10",
	}
	for _, raw := range invalid {
		if _, ok := action.ParseNavCoords(raw); ok {
			t.Fatalf("expected ParseNavCoords(%q) to fail", raw)
		}
	}

	pos, ok := action.ParseNavCoords("120.5, 64, -300")
	if !ok || pos[0] != 120 || pos[1] != 64 || pos[2] != -300 {
		t.Fatalf("expected valid coords (120,64,-300), got (%v, %v)", pos, ok)
	}
}

func TestServerCommandAndJoinBlockedForNonOperator(t *testing.T) {
	b := bot.NewBot(nil, &config.ServerConfig{Host: "127.0.0.1", Port: 19132}, &config.AIConfig{
		MainPlayers: []string{"AdminHans"},
	}, pathfinder.NewLocalWorldModel())

	action.Execute(b, "join", "192.168.1.99:19132", "Griefer")
	if got := b.TakePendingJoin(); got != "" {
		t.Fatalf("expected non-operator join action to be blocked, got pending join %q", got)
	}
}
