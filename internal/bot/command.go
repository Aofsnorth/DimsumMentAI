package bot

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Sending a server command is a different channel from chat. A chat Text packet
// whose message happens to start with "/" is just a message: the server shows it
// as text and never runs it. Commands travel in CommandRequest instead, and the
// server answers with CommandOutput. This file owns that channel.
//
// The leading slash matters in both directions. Players type "/register pass
// pass", and the wire format wants the same string, so the normalisation below
// adds a slash when one is missing and strips the rest, rather than making every
// caller remember the convention.

const (
	// maxCommandLineLength is the protocol's own bound on a command string.
	// Not configurable: the encoder rejects anything longer, so a config knob
	// would only be a way to author a packet that cannot be sent.
	maxCommandLineLength = 256

	// defaultCommandOutputWindow is how long a CommandOutput stays
	// interesting. Most servers answer within a tick or two; holding it longer
	// would let an old reply be reported as the result of a much later command.
	// Overridable per bot via Bot.CommandOutputWindow.
	defaultCommandOutputWindow = 5 * time.Second

	// defaultAutoRetryWait is how long the bare command form gets to produce a
	// reply before the slash form is tried. Overridable per bot.
	defaultAutoRetryWait = 1200 * time.Millisecond
)

// commandState holds the last command line and its output. It has its own mutex
// rather than using b.Mu: a CommandOutput arrives on the packet loop, which must
// never block behind whoever is holding the bot lock.
type commandState struct {
	mu       sync.Mutex
	lastLine string
	output   string
	at       time.Time
	// replied records that the server said something after the last command
	// went out, on ANY channel. Most servers answer a command with an ordinary
	// Text packet rather than CommandOutput, so without this the "did it work?"
	// probe would never see a reply and would retry every command forever.
	replied bool
}

// NoteServerReply records that the server answered something. Called by the
// packet handlers for both CommandOutput and Text so the command probe sees a
// reply whichever channel the server chose.
func (b *Bot) NoteServerReply(text string) {
	b.command.mu.Lock()
	if strings.TrimSpace(text) != "" {
		b.command.output = strings.TrimSpace(text)
		b.command.at = time.Now()
	}
	b.command.replied = true
	b.command.mu.Unlock()
}

// IsCommandLine reports whether a configured line should be sent as a server
// command rather than as chat. Anything that does not start with a slash is a
// plain message.
func IsCommandLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "/")
}

// NormalizeCommandLine turns player-typed input into the wire form. A missing
// leading slash is added, surrounding whitespace is dropped, and the result is
// length-checked so an oversized string is rejected before it reaches the
// encoder, which would fail the whole packet.
func NormalizeCommandLine(line string) (string, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return "", errors.New("command kosong")
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	if len(trimmed) > maxCommandLineLength {
		return "", fmt.Errorf("command kepanjangan (%d karakter, maks %d)", len(trimmed), maxCommandLineLength)
	}
	return trimmed, nil
}

// CommandPrefixStyle selects the wire form for server commands. See
// config.ServerConfig.CommandPrefix for why this is not simply always a slash.
const (
	CommandPrefixAuto  = "auto"
	CommandPrefixSlash = "slash"
	CommandPrefixNone  = "none"

	// CommandPrefixNoneFirst and CommandPrefixSlashFirst are the two probe
	// orders used when CommandPrefix is "auto".
	CommandPrefixNoneFirst  = "none_first"
	CommandPrefixSlashFirst = "slash_first"
)

// SendCommand runs a server command by writing a CommandRequest packet.
//
// In "auto" mode it sends the bare form first and, if the server says nothing
// back, retries with the leading slash. Geyser strips a leading "/" before
// translating to Java, so "/register" reaches it as an unknown command, while
// dragonfly ignores anything without the slash. Trying one and failing is how
// the bot discovers which it is talking to, and it costs one round trip only on
// a server that does not answer at all.
func (b *Bot) SendCommand(line string) error {
	normalized, err := NormalizeCommandLine(line)
	if err != nil {
		return err
	}
	if b.Conn == nil {
		return errors.New("bot belum terhubung")
	}

	style := b.commandPrefixStyle()
	if style != CommandPrefixAuto {
		return b.writeCommandLine(commandWireLine(normalized, style))
	}

	// The order of the two attempts is server-dependent, so it is chosen from the
	// detected profile rather than assumed.
	//
	// Measured on play.hansprojects.my.id (Geyser + Floodgate + SimpleLogin):
	// "/register pass pass" is answered "Successfully registered and logged in.",
	// while the bare form is answered with silence — so a Geyser front-end wants
	// slash first. The bare-first order is kept for every other server, because
	// that is what they were verified against and changing it would be a
	// regression on a server that currently works.
	first, second := CommandPrefixNone, CommandPrefixSlash
	if b.CommandPrefixOrder == CommandPrefixSlashFirst {
		first, second = CommandPrefixSlash, CommandPrefixNone
	}

	if err := b.writeCommandLine(commandWireLine(normalized, first)); err != nil {
		return err
	}
	if output, ok := b.awaitServerReply(b.autoRetryWindow()); ok {
		b.Logger.Info("server accepted the command",
			"command", normalized, "style", first, "reply", output)
		return nil
	}

	b.Logger.Warn("server did not answer the first command form, retrying with the other",
		"command", normalized, "tried", first)
	if err := b.writeCommandLine(commandWireLine(normalized, second)); err != nil {
		return err
	}
	return nil
}

// commandWireLine applies a wire style to an already-normalised command line.
func commandWireLine(normalized, style string) string {
	if style == CommandPrefixSlash {
		return normalized
	}
	return strings.TrimPrefix(normalized, "/")
}

// commandPrefixStyle resolves the configured style, defaulting to auto so a Bot
// built without options (tests, tools) still behaves.
func (b *Bot) commandPrefixStyle() string {
	switch b.CommandPrefix {
	case CommandPrefixSlash, CommandPrefixNone, CommandPrefixAuto:
		return b.CommandPrefix
	default:
		return CommandPrefixAuto
	}
}

// autoRetryWait was a fixed 1200ms; it is now Bot.AutoRetryWait so a slow
// server can be given longer before the slash form is tried.

// autoRetryWindow resolves the retry wait, falling back to the default so a Bot
// built without options (tests, tools) still behaves.
func (b *Bot) autoRetryWindow() time.Duration {
	if b.AutoRetryWait > 0 {
		return b.AutoRetryWait
	}
	return defaultAutoRetryWait
}

// awaitServerReply waits up to d for the server to say anything at all, on any
// channel. It reports the reply text when one carried content.
func (b *Bot) awaitServerReply(d time.Duration) (string, bool) {
	deadline := time.Now().Add(d)
	for {
		b.command.mu.Lock()
		replied := b.command.replied
		output := b.command.output
		b.command.mu.Unlock()
		if replied {
			return output, true
		}
		if !time.Now().Before(deadline) {
			return "", false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// writeCommandLine puts one exact command line on the wire.
func (b *Bot) writeCommandLine(wire string) error {
	b.Mu.Lock()
	playerUUID := b.PlayerUUID
	b.Mu.Unlock()

	pk := &packet.CommandRequest{
		CommandLine: wire,
		CommandOrigin: protocol.CommandOrigin{
			Origin:    protocol.CommandOriginPlayer,
			UUID:      playerUUID,
			RequestID: "",
		},
		Internal: false,
		Version:  protocol.CurrentVersion,
	}
	if err := b.Conn.WritePacket(pk); err != nil {
		return fmt.Errorf("gagal kirim command %s: %w", wire, err)
	}

	b.command.mu.Lock()
	b.command.lastLine = wire
	b.command.output = ""
	b.command.at = time.Time{}
	b.command.replied = false
	b.command.mu.Unlock()

	b.Logger.Info("server command sent", "command", wire)
	return nil
}

// RecordCommandOutput stores the server's reply to the last command so the
// caller can report what actually happened. Called from the packet loop.
func (b *Bot) RecordCommandOutput(line string) {
	b.NoteServerReply(line)
}

// LastCommandOutput returns the reply to the most recent command, and whether
// one arrived inside the freshness window. Stale output is reported as absent
// rather than as the result of whatever command ran last.
func (b *Bot) LastCommandOutput() (string, bool) {
	b.command.mu.Lock()
	defer b.command.mu.Unlock()
	if b.command.output == "" || b.command.at.IsZero() {
		return "", false
	}
	if time.Since(b.command.at) > b.commandOutputWindow() {
		return "", false
	}
	return b.command.output, true
}

// commandOutputWindow resolves the freshness window, falling back to the default
// so a zero Bot (tests, tools) still behaves.
func (b *Bot) commandOutputWindow() time.Duration {
	if b.CommandOutputWindow != 0 {
		return b.CommandOutputWindow
	}
	return defaultCommandOutputWindow
}
