package chat_test

import (
	"testing"

	"bedrock-ai/internal/bot/chat"
)

func TestStopAttackIntentPhrasings(t *testing.T) {
	t.Parallel()

	phrasings := []string{
		"berhenti attack",
		"stop attack",
		"berhenti serang",
		"stop serang",
		"jangan serang",
		"jangan serang lagi",
		"jangan nyerang",
		"berhenti nyerang",
		"stop pvp",
		"berhenti pvp",
		"batal attack",
		"batal serang",
		"stop bertarung",
		"berhenti bertarung",
		"stop hitting",
		"stop fighting",
		"disengage",
		"!disengage",
		"BERHENTI ATTACK",
	}

	for _, msg := range phrasings {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()
			if !chat.IsStopAttackIntentForTest(msg) {
				t.Errorf("%q want isStopAttackIntent = true, got false", msg)
			}
		})
	}
}

func TestStopAttackIntent_NonMatchingMessages(t *testing.T) {
	t.Parallel()

	nonMatching := []string{
		"attack zombie",
		"serang creeper",
		"pvp sama aku",
		"ayo serang",
		"halo bot",
	}

	for _, msg := range nonMatching {
		t.Run(msg, func(t *testing.T) {
			t.Parallel()
			if chat.IsStopAttackIntentForTest(msg) {
				t.Errorf("%q want isStopAttackIntent = false, got true", msg)
			}
		})
	}
}
