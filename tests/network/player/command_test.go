package player_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/network/player"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// TestRenderCommandOutputKeepsReadableText covers the common case: a server
// sends literal prose, and it has to survive intact.
func TestRenderCommandOutputKeepsReadableText(t *testing.T) {
	t.Parallel()

	got := player.RenderCommandOutput([]protocol.CommandOutputMessage{
		{Success: true, Message: "Welcome to the lobby"},
	})
	if got != "Welcome to the lobby" {
		t.Errorf("RenderCommandOutput() = %q, want the literal message", got)
	}
}

// TestRenderCommandOutputAppendsTranslationParameters is why this function
// exists. Vanilla replies are translation keys with the answer in Parameters
// ("commands.teleport.success" + the destination). Returning only the key would
// leave the bot unable to say where it ended up.
func TestRenderCommandOutputAppendsTranslationParameters(t *testing.T) {
	t.Parallel()

	got := player.RenderCommandOutput([]protocol.CommandOutputMessage{
		{Success: true, Message: "commands.teleport.success", Parameters: []string{"128", "64", "-40"}},
	})
	if !strings.Contains(got, "128") || !strings.Contains(got, "-40") {
		t.Errorf("RenderCommandOutput() = %q, want the parameters included", got)
	}
	if !strings.Contains(got, "commands.teleport.success") {
		t.Errorf("RenderCommandOutput() = %q, want the key kept as a label", got)
	}
}

// TestRenderCommandOutputJoinsAndSkipsEmpty stops a leading blank line from
// making the whole reply look like it started with a separator.
func TestRenderCommandOutputJoinsAndSkipsEmpty(t *testing.T) {
	t.Parallel()

	got := player.RenderCommandOutput([]protocol.CommandOutputMessage{
		{Message: "  "},
		{Message: "first"},
		{Message: "second"},
	})
	if got != "first | second" {
		t.Errorf("RenderCommandOutput() = %q, want %q", got, "first | second")
	}
}

// TestRenderCommandOutputEmptyIsEmpty keeps an unhandled packet from being
// recorded as a successful reply.
func TestRenderCommandOutputEmptyIsEmpty(t *testing.T) {
	t.Parallel()

	if got := player.RenderCommandOutput(nil); got != "" {
		t.Errorf("RenderCommandOutput(nil) = %q, want empty", got)
	}
}
