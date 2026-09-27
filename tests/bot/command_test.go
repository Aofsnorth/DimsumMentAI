package bot_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
)

// TestNormalizeCommandLineMatchesWhatAPlayerTypes keeps the wire format equal
// to the string a human would type. A missing slash is the common failure: the
// command is accepted by the encoder and then silently rejected by the server
// as an unknown command, which looks identical to a permissions problem.
func TestNormalizeCommandLineMatchesWhatAPlayerTypes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "/register pass pass", want: "/register pass pass"},
		{in: "register pass pass", want: "/register pass pass"},
		{in: "  /spawn  ", want: "/spawn"},
		{in: "spawn", want: "/spawn"},
		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "/" + strings.Repeat("a", 512), wantErr: true},
	}
	for _, tc := range cases {
		got, err := bot.NormalizeCommandLine(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeCommandLine(%q) = %q, want an error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeCommandLine(%q) error = %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeCommandLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestIsCommandLineSplitsJoinMessagesOnTheSlash is the rule the whole
// join_messages feature rests on: a slash means "run this", anything else means
// "say this". Getting it backwards would post a password to chat.
func TestIsCommandLineSplitsJoinMessagesOnTheSlash(t *testing.T) {
	t.Parallel()

	if !bot.IsCommandLine("/register pass pass") {
		t.Error(`IsCommandLine("/register pass pass") = false, want true`)
	}
	if !bot.IsCommandLine("   /spawn") {
		t.Error("leading whitespace must not hide the slash")
	}
	if bot.IsCommandLine("halo semua") {
		t.Error(`IsCommandLine("halo semua") = true, want false — plain chat is not a command`)
	}
	if bot.IsCommandLine("") {
		t.Error("an empty line is not a command")
	}
}

// TestSendCommandWithoutConnectionIsReportedNotSilent covers the wiring mistake
// where join messages fire before the connection exists: the player must be told
// the command did not run, not left believing it registered.
func TestSendCommandWithoutConnectionIsReportedNotSilent(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := b.SendCommand("/register pass pass"); err == nil {
		t.Fatal("SendCommand on a disconnected bot succeeded, want an error")
	}
}

// TestSendCommandValidatesBeforeTouchingTheConnection keeps a malformed line
// from being reported as sent when it never left the process.
func TestSendCommandValidatesBeforeTouchingTheConnection(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := b.SendCommand("   "); err == nil {
		t.Fatal("SendCommand(blank) succeeded, want a validation error")
	}
}

// TestLastCommandOutputGoesStale guards against reporting a reply to an old
// command as the result of the current one — the failure that makes a bot claim
// "/register" worked when it actually answered the previous command.
func TestLastCommandOutputGoesStale(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if _, ok := b.LastCommandOutput(); ok {
		t.Error("a bot that never ran a command reported output")
	}

	b.RecordCommandOutput("Welcome to the server")
	if got, ok := b.LastCommandOutput(); !ok || got != "Welcome to the server" {
		t.Errorf("LastCommandOutput() = %q, %v; want the recorded reply", got, ok)
	}

	// Shrink the freshness window to nothing so the reply is stale immediately.
	// Waiting out the real window would make this test sleep on every run.
	b.CommandOutputWindow = -time.Second
	if _, ok := b.LastCommandOutput(); ok {
		t.Error("stale output was still reported as fresh")
	}
}

// TestRunJoinMessagesSkipsWhenNotConfigured keeps the feature free when unused:
// a bot with no join_messages must not sit in a timer before every session.
func TestRunJoinMessagesSkipsWhenNotConfigured(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan struct{})
	go func() {
		b.RunJoinMessages(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("RunJoinMessages blocked with no lines configured")
	}
}

// TestRunJoinMessagesStopsWhenTheSessionEnds covers the reconnect case: a
// session that is ending must stop sending, not keep firing its remaining lines
// into a connection that no longer exists.
func TestRunJoinMessagesStopsWhenTheSessionEnds(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger:              slog.New(slog.NewTextHandler(io.Discard, nil)),
		JoinMessages:        []string{"/one", "/two", "/three"},
		JoinMessageDelay:    time.Hour,
		JoinMessageInterval: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		b.RunJoinMessages(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a cancelled session kept the join messages running")
	}
}
