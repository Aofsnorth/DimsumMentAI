package chat_test

import (
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/chat"
)

func TestTagMatchingWithConfiguredNameAndAliases(t *testing.T) {
	t.Parallel()

	// In-game name is .ShigaLucifer (from Xbox Live gamertag),
	// but configured name is Luna, with custom aliases.
	b := &bot.Bot{
		Name:           ".ShigaLucifer",
		ConfiguredName: "Luna",
		TagAliases:     []string{"shiga", "mentai"},
	}

	tests := []struct {
		name      string
		msg       string
		wantMatch bool
	}{
		{
			name:      "Tag with @Luna (configured friendly name)",
			msg:       "@Luna Coba ke kordinat 0, 73, 1",
			wantMatch: true,
		},
		{
			name:      "Bare Luna with punctuation",
			msg:       "Luna, sini dong!",
			wantMatch: true,
		},
		{
			name:      "Bare Luna case-insensitive",
			msg:       "halo luna tolong bantu",
			wantMatch: true,
		},
		{
			name:      "Tag with @.ShigaLucifer (active Xbox gamertag)",
			msg:       "@.ShigaLucifer Coba ke kordinat 0, 73, 1",
			wantMatch: true,
		},
		{
			name:      "Tag with @ShigaLucifer (without dot prefix)",
			msg:       "@ShigaLucifer Coba ke kordinat 0, 73, 1",
			wantMatch: true,
		},
		{
			name:      "Custom alias @shiga",
			msg:       "@shiga sini",
			wantMatch: true,
		},
		{
			name:      "Custom alias @mentai",
			msg:       "Sini @mentai",
			wantMatch: true,
		},
		{
			name:      "Untagged command should not match",
			msg:       "Sini",
			wantMatch: false,
		},
		{
			name:      "Tagging another player should not match",
			msg:       "halo @Synterix apa kabar",
			wantMatch: false,
		},
		{
			name:      "Substring in another word should not match (Lunar)",
			msg:       "Lunar eclipse tonight",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			matched := chat.IsTaggedInBotForTest(tt.msg, b)
			if matched != tt.wantMatch {
				t.Errorf("IsTaggedInBotForTest(%q) = %v, want %v", tt.msg, matched, tt.wantMatch)
			}
		})
	}

	t.Run("OnlyTagAliasesEnforced", func(t *testing.T) {
		bOnly := &bot.Bot{
			Name:           ".ShigaLucifer",
			ConfiguredName: "Luna",
			TagAliases:     []string{"shiga"},
		}
		bOnly.AiCfg.OnlyTagAliases = true

		if chat.IsTaggedInBotForTest("@Luna halo", bOnly) {
			t.Errorf("expected @Luna NOT to match when OnlyTagAliases=true and Luna not in aliases")
		}
		if chat.IsTaggedInBotForTest("@.ShigaLucifer halo", bOnly) {
			t.Errorf("expected @.ShigaLucifer NOT to match when OnlyTagAliases=true and not in aliases")
		}
		if !chat.IsTaggedInBotForTest("@shiga halo", bOnly) {
			t.Errorf("expected @shiga to match when in aliases")
		}
	})
}
