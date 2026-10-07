package bot_test

import (
	"testing"

	"bedrock-ai/internal/bot"
)

func TestPlayerNameMatches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		known    string
		query    string
		expected bool
	}{
		{
			name:     "exact match",
			known:    "ShigaLucifer",
			query:    "ShigaLucifer",
			expected: true,
		},
		{
			name:     "case insensitive match",
			known:    "shigalucifer",
			query:    "SHIGALUCIFER",
			expected: true,
		},
		{
			name:     "floodgate dot prefix on known",
			known:    ".ShigaLucifer",
			query:    "ShigaLucifer",
			expected: true,
		},
		{
			name:     "floodgate dot prefix on query",
			known:    "ShigaLucifer",
			query:    ".ShigaLucifer",
			expected: true,
		},
		{
			name:     "floodgate dot prefix on both",
			known:    ".ShigaLucifer",
			query:    ".shigalucifer",
			expected: true,
		},
		{
			name:     "bracket clan tag stripped",
			known:    "[VIP] .ShigaLucifer",
			query:    "ShigaLucifer",
			expected: true,
		},
		{
			name:     "different player does not match",
			known:    "ShigaLucifer",
			query:    "Arthenyxx",
			expected: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := bot.PlayerNameMatches(tc.known, tc.query)
			if got != tc.expected {
				t.Fatalf("PlayerNameMatches(%q, %q) = %v, want %v", tc.known, tc.query, got, tc.expected)
			}
		})
	}
}
