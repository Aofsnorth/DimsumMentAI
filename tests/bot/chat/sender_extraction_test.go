package chat_test

import (
	"testing"

	"bedrock-ai/internal/handler"
)

func TestExtractChatSenderAndMessage(t *testing.T) {
	t.Parallel()

	knownPlayers := map[string]bool{
		"Synterix":      true,
		".ShigaLucifer": true,
		"Alice":         true,
	}
	resolver := func(query string) (string, bool) {
		for p := range knownPlayers {
			if p == query || p == "Synterix" && (query == "MEMBER Synterix" || query == "[VIP] Synterix" || query == "Member | Synterix") {
				return p, true
			}
		}
		return "", false
	}

	tests := []struct {
		name        string
		input       string
		withRes     bool
		wantSender  string
		wantMessage string
		wantOK      bool
	}{
		{
			name:        "Server formatted member rank with greater than separator",
			input:       "MEMBER Synterix> @.ShigaLucifer Coba ke kordinat 0, 73, 1",
			withRes:     true,
			wantSender:  "Synterix",
			wantMessage: "@.ShigaLucifer Coba ke kordinat 0, 73, 1",
			wantOK:      true,
		},
		{
			name:        "Server formatted member rank without resolver (fallback heuristic)",
			input:       "MEMBER Synterix> @.ShigaLucifer Coba ke kordinat 0, 73, 1",
			withRes:     false,
			wantSender:  "Synterix",
			wantMessage: "@.ShigaLucifer Coba ke kordinat 0, 73, 1",
			wantOK:      true,
		},
		{
			name:        "VIP rank with bracket and colon",
			input:       "[VIP] Synterix: Sini @Luna",
			withRes:     true,
			wantSender:  "Synterix",
			wantMessage: "Sini @Luna",
			wantOK:      true,
		},
		{
			name:        "Vanilla angle brackets",
			input:       "<Synterix> halo Luna",
			withRes:     true,
			wantSender:  "Synterix",
			wantMessage: "halo Luna",
			wantOK:      true,
		},
		{
			name:        "Plain username with colon",
			input:       "Synterix: halo Luna",
			withRes:     true,
			wantSender:  "Synterix",
			wantMessage: "halo Luna",
			wantOK:      true,
		},
		{
			name:        "Plain username with space and greater than",
			input:       "Synterix > halo Luna",
			withRes:     true,
			wantSender:  "Synterix",
			wantMessage: "halo Luna",
			wantOK:      true,
		},
		{
			name:        "European arrow separator",
			input:       "Synterix » halo Luna",
			withRes:     true,
			wantSender:  "Synterix",
			wantMessage: "halo Luna",
			wantOK:      true,
		},
		{
			name:        "Pipe formatted rank prefix",
			input:       "Member | Synterix: halo Luna",
			withRes:     true,
			wantSender:  "Synterix",
			wantMessage: "halo Luna",
			wantOK:      true,
		},
		{
			name:        "Leading timestamp with rank and separator",
			input:       "[20:08:07] MEMBER Synterix> @Luna sini",
			withRes:     true,
			wantSender:  "Synterix",
			wantMessage: "@Luna sini",
			wantOK:      true,
		},
		{
			name:        "Minecraft color codes stripped",
			input:       "§7MEMBER §fSynterix§8> §r@.ShigaLucifer Coba ke kordinat 0, 73, 1",
			withRes:     true,
			wantSender:  "Synterix",
			wantMessage: "@.ShigaLucifer Coba ke kordinat 0, 73, 1",
			wantOK:      true,
		},
		{
			name:        "System message Daily rewards must be rejected",
			input:       "Daily > Your daily rewards are ready. Use /daily",
			withRes:     true,
			wantSender:  "",
			wantMessage: "",
			wantOK:      false,
		},
		{
			name:        "System message Server restart must be rejected",
			input:       "Server > Server will restart in 5 minutes",
			withRes:     true,
			wantSender:  "",
			wantMessage: "",
			wantOK:      false,
		},
		{
			name:        "Normal sentence with colon not treated as sender",
			input:       "I like playing Minecraft because: it is fun",
			withRes:     false,
			wantSender:  "",
			wantMessage: "",
			wantOK:      false,
		},
		{
			name:        "Join broadcast with no separator",
			input:       ".ShigaLucifer joined the game",
			withRes:     true,
			wantSender:  "",
			wantMessage: "",
			wantOK:      false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var res func(string) (string, bool)
			if tt.withRes {
				res = resolver
			}

			sender, message, ok := handler.ExtractChatSenderAndMessage(tt.input, res)
			if ok != tt.wantOK {
				t.Fatalf("ExtractChatSenderAndMessage(%q) ok = %v, want %v (sender=%q, message=%q)",
					tt.input, ok, tt.wantOK, sender, message)
			}
			if ok {
				if sender != tt.wantSender {
					t.Errorf("ExtractChatSenderAndMessage(%q) sender = %q, want %q",
						tt.input, sender, tt.wantSender)
				}
				if message != tt.wantMessage {
					t.Errorf("ExtractChatSenderAndMessage(%q) message = %q, want %q",
						tt.input, message, tt.wantMessage)
				}
			}
		})
	}
}
