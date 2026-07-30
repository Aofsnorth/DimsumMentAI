package ai

import (
	"strings"
	"testing"
)

func TestProtocolForProvider(t *testing.T) {
	t.Parallel()
	cases := map[string]Protocol{
		"nvidia":               ProtocolOpenAI,
		"openai_compatible":    ProtocolOpenAI,
		"minimax":              ProtocolOpenAI,
		"opengateway":          ProtocolOpenAI,
		"anthropic_compatible": ProtocolAnthropic,
		"google_compatible":    ProtocolGoogle,
	}
	for provider, want := range cases {
		if got := protocolForProvider(provider); got != want {
			t.Errorf("protocolForProvider(%q) = %q, want %q", provider, got, want)
		}
	}
}

func TestBedrockSystemRulesExposePlaceAction(t *testing.T) {
	t.Parallel()

	if !strings.Contains(BedrockSystemRules, "<action>place:item_name</action>") {
		t.Fatal("BedrockSystemRules does not expose the registered place action")
	}
	if !strings.Contains(BedrockSystemRules, "do not use drop") {
		t.Fatal("BedrockSystemRules does not distinguish placing a block from dropping an item")
	}
}

func TestParseReply(t *testing.T) {
	t.Parallel()

	openai := []byte(`{"choices":[{"message":{"content":"hai"}}]}`)
	if got, err := parseOpenAIReply(openai); err != nil || got != "hai" {
		t.Errorf("parseOpenAIReply = %q, %v", got, err)
	}

	// Some OpenAI-compatible servers append a trailing "data: [DONE]" line
	// even when stream=false. The decoder must ignore it.
	trailing := []byte(`{"choices":[{"message":{"content":"hello"}}]}data: [DONE]`)
	if got, err := parseOpenAIReply(trailing); err != nil || got != "hello" {
		t.Errorf("parseOpenAIReply(trailing) = %q, %v", got, err)
	}

	anthropic := []byte(`{"content":[{"type":"text","text":"halo"},{"type":"text","text":"!"}]}`)
	if got, err := parseAnthropicReply(anthropic); err != nil || got != "halo!" {
		t.Errorf("parseAnthropicReply = %q, %v", got, err)
	}

	google := []byte(`{"candidates":[{"content":{"parts":[{"text":"hei"}]}}]}`)
	if got, err := parseGoogleReply(google); err != nil || got != "hei" {
		t.Errorf("parseGoogleReply = %q, %v", got, err)
	}
}
