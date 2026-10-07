package chat_test

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/chat"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/event"
)

// newWhitelistBot is the smallest bot the gate and the grant command agree on:
// one linked player, nobody else, and the runtime maps a reply path expects.
// The whitelist path is left empty so a test never touches the real file.
func newWhitelistBot() *bot.Bot {
	b := &bot.Bot{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:                "Luna",
		ConfiguredName:      "Luna",
		RecentBotMessages:   make(map[string]time.Time),
		RecentStatusReports: make(map[string]time.Time),
	}
	b.AiCfg = config.AIConfig{
		RespondOnlyToLinkedPlayer: true,
		MainPlayers:               []string{"Synterix"},
	}
	return b
}

// TestWhitelistGrantsANonMainPlayerTheChat is the whole point of the command:
// a friend who is not in bot.yaml can be let in from inside the game, and can
// be let out again.
func TestWhitelistGrantsANonMainPlayerTheChat(t *testing.T) {
	t.Parallel()

	b := newWhitelistBot()
	evt := event.ChatEvent{SourceName: "Budi", Message: "Luna halo"}

	if chat.ShouldProcessChatForTest(b, evt, evt.Message, "Luna") {
		t.Fatal("a stranger was processed before being whitelisted")
	}

	if !b.WhitelistPlayer("Budi") {
		t.Fatal("whitelisting a new player reported no change")
	}
	if !chat.ShouldProcessChatForTest(b, evt, evt.Message, "Luna") {
		t.Fatal("a whitelisted player was still dropped by the linked-player gate")
	}

	if !b.UnwhitelistPlayer("Budi") {
		t.Fatal("unwhitelisting a listed player reported no change")
	}
	if chat.ShouldProcessChatForTest(b, evt, evt.Message, "Luna") {
		t.Fatal("a revoked player was still processed after unwhitelisting")
	}
}

// TestWhitelistDoesNotTouchLinkedPlayers: the grant is an extension of access,
// not a second definition of it — the linked players keep their rights without
// appearing in the list, and the list never gates them out.
func TestWhitelistDoesNotTouchLinkedPlayers(t *testing.T) {
	t.Parallel()

	b := newWhitelistBot()
	evt := event.ChatEvent{SourceName: "Synterix", Message: "Luna halo"}

	if !chat.ShouldProcessChatForTest(b, evt, evt.Message, "Luna") {
		t.Fatal("the linked player was dropped — the whitelist gate ate the main gate")
	}

	b.WhitelistPlayer("Budi")
	evtBudi := event.ChatEvent{SourceName: "Budi", Message: "Luna halo"}
	if !chat.ShouldProcessChatForTest(b, evtBudi, evtBudi.Message, "Luna") {
		t.Fatal("the whitelisted player was dropped")
	}
}

// TestWhitelistCommandBelongsToMainPlayers is the boundary that keeps the gate
// a gate: whoever is not linked cannot widen it, even when their chat reaches
// the command (an open-chat config), and even when the target is themselves.
func TestWhitelistCommandBelongsToMainPlayers(t *testing.T) {
	t.Parallel()

	b := newWhitelistBot()
	evt := event.ChatEvent{SourceName: "Budi", Message: `luna, whitelist nama "Budi"`}

	if !chat.HandleWhitelistCommandForTest(b, evt, evt.Message) {
		t.Fatal("the command was not recognised as a whitelist request at all")
	}
	if len(b.WhitelistedPlayers()) != 0 {
		t.Fatalf("a non-main player granted themselves access: %v", b.WhitelistedPlayers())
	}

	evtMain := event.ChatEvent{SourceName: "Synterix", Message: `luna, whitelist nama "Budi"`}
	if !chat.HandleWhitelistCommandForTest(b, evtMain, evtMain.Message) {
		t.Fatal("the main player's grant was not recognised")
	}
	if !b.IsWhitelistedPlayer("Budi") {
		t.Fatal("the main player's grant did not add the player")
	}
}

// TestWhitelistParsesThePhrasesPlayersType covers the accepted forms and the
// false positives: a message that merely contains the word must not become a
// config change.
func TestWhitelistParsesThePhrasesPlayersType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		msg    string
		name   string
		remove bool
		ok     bool
	}{
		{msg: `Luna, whitelist nama "Budi"`, name: "Budi", ok: true},
		{msg: "whitelist nama Budi", name: "Budi", ok: true},
		{msg: "whitelist player Budi", name: "Budi", ok: true},
		{msg: `Luna unwhitelist nama "Budi"`, name: "Budi", remove: true, ok: true},
		{msg: "unwhitelist nama Budi", name: "Budi", remove: true, ok: true},
		{msg: `whitelist nama "Budi Susanto"`, name: "Budi Susanto", ok: true},
		{msg: "kamu nggak di whitelist", ok: false},
		{msg: "whitelist", ok: false},
		{msg: "whitelist dong", ok: false},
	}
	for _, tc := range cases {
		name, remove, ok := chat.ParseWhitelistIntentForTest(tc.msg)
		if ok != tc.ok {
			t.Errorf("parse(%q) ok = %v, want %v", tc.msg, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		if name != tc.name || remove != tc.remove {
			t.Errorf("parse(%q) = (%q, %v), want (%q, %v)", tc.msg, name, remove, tc.name, tc.remove)
		}
	}
}

// TestWhitelistSurvivesARestart is what the sidecar file is for: a restart
// must not silently revoke everybody the operator let in from the game.
func TestWhitelistSurvivesARestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "wl.json")
	first := newWhitelistBot()
	first.SetWhitelistPathForTest(path)
	if !first.WhitelistPlayer("Budi") {
		t.Fatal("grant reported no change")
	}

	second := newWhitelistBot()
	second.SetWhitelistPathForTest(path)
	if !second.IsWhitelistedPlayer("Budi") {
		t.Fatal("a fresh bot did not load the persisted whitelist")
	}
	if !second.UnwhitelistPlayer("Budi") {
		t.Fatal("revocation reported no change on the loaded list")
	}

	third := newWhitelistBot()
	third.SetWhitelistPathForTest(path)
	if third.IsWhitelistedPlayer("Budi") {
		t.Fatal("a revoked player came back from the file")
	}
}
