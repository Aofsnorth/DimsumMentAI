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

// A server may greet the bot under a name that is not the configured one —
// Floodgate and SimpleLogin hand back something like ".ShigaLucifer" for an
// account configured as "Luna". The PlayerList entry that would carry that name
// is matched by UUID and entity ID, so a server sending neither leaves the bot
// still believing it is called "Luna".
//
// The visible failure is that the bot does not recognise its own chat. The echo
// of a reply arrives attributed to a stranger, and the bot answers itself.
func TestABotRecognisesItsOwnChatUnderTheServerName(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:           "Luna",
		ConfiguredName: "Luna",
	}
	b.NoteServerAssignedName(".ShigaLucifer")

	if !b.IsOwnChatSource(".ShigaLucifer") {
		t.Error("the bot did not recognise its own reply under the name the server uses.\n" +
			"It treats a player's message as its own and answers itself.")
	}
	// Case and a leading dot differ between what the server echoes and what a
	// later message carries; the match cannot depend on them agreeing exactly.
	if !b.IsOwnChatSource("shigalucifer") {
		t.Error("the server-assigned name did not match a differently cased source")
	}
	// The configured name still counts: a server that keeps it must not break.
	if !b.IsOwnChatSource("Luna") {
		t.Error("the configured name stopped counting as the bot's own")
	}
	// Somebody else is still somebody else.
	if b.IsOwnChatSource(".Arthenyxx") {
		t.Error("an unrelated player was treated as the bot")
	}
}

// The whisper banner names its recipient, and that name is the server's name for
// the bot. If it is not recognised, the bot ignores a DM addressed to it — which
// is exactly the "bot ignores my private message" report.
func TestABotRecognisesItselfInAWhisperBannerUnderTheServerName(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Name: "Luna", ConfiguredName: "Luna"}
	if b.IsBotName(".ShigaLucifer") {
		t.Fatal("the test is vacuous: the server name was already recognised")
	}

	b.NoteServerAssignedName(".ShigaLucifer")

	if !b.IsBotName(".ShigaLucifer") {
		t.Error("a whisper addressed to the server's name for the bot was not recognised.\n" +
			"The DM is then attributed to nobody and dropped.")
	}
}

// Learning the name must not overwrite the configured one: the persona and the
// tag matcher still use it, and a bot that answers to neither name stops being
// addressable.
func TestLearningTheServerNameKeepsTheConfiguredName(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Name: "Luna", ConfiguredName: "Luna"}
	b.NoteServerAssignedName(".ShigaLucifer")

	if b.ConfiguredName != "Luna" {
		t.Errorf("ConfiguredName = %q, want Luna: learning the server name must not rewrite it",
			b.ConfiguredName)
	}
	if b.Name != "Luna" {
		t.Errorf("Name = %q, want Luna", b.Name)
	}
}

// An echo arriving before the bot has ever learned a name is the first chance to
// learn one, so the ordering in shouldProcessChat matters: the name is recorded
// and the message is dropped as an echo in the same pass.
func TestAnEchoTeachesTheBotItsNameAndIsDropped(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:           "Luna",
		ConfiguredName: "Luna",
		AiCfg: config.AIConfig{
			MainPlayer:                ".Arthenyxx",
			RespondOnlyToLinkedPlayer: true,
		},
	}
	// The bot has just sent this line; the server stamps its own name on the echo.
	b.TrackBotMessage("Halo Arthenyxx!")

	evt := event.ChatEvent{SourceName: ".ShigaLucifer", Message: "Halo Arthenyxx!"}
	if chat.ShouldProcessChatForTest(b, evt, "Halo Arthenyxx!", "Luna") {
		t.Error("the bot processed its own echoed message")
	}
	if !b.IsOwnChatSource(".ShigaLucifer") {
		t.Error("the echo did not teach the bot the name the server uses for it")
	}
}
