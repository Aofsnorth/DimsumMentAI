package bot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"bedrock-ai/internal/servercompat"
)

// Switching servers is not a packet — Bedrock has no "go to another server"
// action. It is a new connection, so the run loop has to end the live session
// and dial again. This file owns that hand-off: an action asks for a switch, the
// run loop picks it up between sessions, and nothing else has to know.

const maxServerSwitches = 10

// joinMu guards the switch request and the session cancel below. It is
// deliberately not b.Mu: cancelling a session closes the connection, which makes
// other holders of b.Mu wait on network I/O, and holding b.Mu across that would
// turn a quick switch request into a stall.
type joinState struct {
	mu             sync.Mutex
	pending        string
	applied        string
	hook           func(address string) error
	sessionCancel  context.CancelFunc
	switchFailures int
}

// SetJoinHook installs the function that actually redirects the dialer. It is a
// hook rather than a direct dependency so the bot package does not have to know
// how connections are made, and so tests can observe switches without a network.
func (b *Bot) SetJoinHook(hook func(address string) error) {
	b.join.mu.Lock()
	defer b.join.mu.Unlock()
	b.join.hook = hook
}

// RequestJoin asks the bot to move to another server.
//
// The address is validated here rather than at dial time so the player gets an
// immediate "that is not an address" instead of a silent reconnect loop.
func (b *Bot) RequestJoin(address string) error {
	normalized, err := NormalizeServerAddress(address)
	if err != nil {
		return err
	}

	b.join.mu.Lock()
	hook := b.join.hook
	cancel := b.join.sessionCancel
	b.join.pending = normalized
	b.join.mu.Unlock()

	if hook != nil {
		if err := hook(normalized); err != nil {
			// Take the request back: the dialer still points at the old server, so
			// retrying without applying it would just reconnect to where we were.
			b.join.mu.Lock()
			b.join.pending = ""
			b.join.mu.Unlock()
			return fmt.Errorf("gagal pindah ke %s: %w", normalized, err)
		}
		b.join.mu.Lock()
		b.join.applied = normalized
		b.join.mu.Unlock()
	}

	// End the live session so the run loop can start the new one. Cancelling
	// outside the lock keeps the close (and its network waits) off joinMu.
	if cancel != nil {
		cancel()
	}
	return nil
}

// NormalizeServerAddress validates a host:port target and returns it in a
// canonical form. A bare host is rejected on purpose: Bedrock's default port
// differs between LAN worlds, dedicated servers, and realms, and guessing wrong
// dials a port nothing is listening on.
func NormalizeServerAddress(address string) (string, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return "", errors.New("alamat server kosong")
	}
	if strings.Contains(trimmed, "://") {
		return "", errors.New("alamat harus format host:port, tanpa http:// atau https://")
	}

	host, portText, err := net.SplitHostPort(trimmed)
	if err != nil {
		return "", fmt.Errorf("butuh format host:port, contoh 192.168.1.10:19132")
	}
	host = strings.TrimSpace(host)
	port, err := strconv.Atoi(strings.TrimSpace(portText))
	if err != nil || port <= 0 || port > 65535 {
		return "", fmt.Errorf("port %q tidak valid", portText)
	}
	if host == "" {
		return "", errors.New("alamat tidak punya host")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// takeJoinRequest returns a pending switch request, if any.
func (b *Bot) takeJoinRequest() (string, bool) {
	b.join.mu.Lock()
	defer b.join.mu.Unlock()
	if b.join.pending == "" {
		return "", false
	}
	target := b.join.pending
	b.join.pending = ""
	return target, true
}

// CurrentServer reports the address the bot is connected to.
func (b *Bot) CurrentServer() string {
	b.join.mu.Lock()
	defer b.join.mu.Unlock()
	return b.join.applied
}

// setSessionCancel registers the current session's cancel so a join request can
// end it. Passing nil on session teardown prevents a late join request from
// cancelling a session that has already finished.
func (b *Bot) setSessionCancel(cancel context.CancelFunc) {
	b.join.mu.Lock()
	defer b.join.mu.Unlock()
	b.join.sessionCancel = cancel
}

// sessionResult reports how far a session got, so the run loop can tell "the
// world closed" (retry) from "we never got in" (report it) without parsing the
// error text.
type sessionResult struct {
	// Connected is true once the connection was established and spawned, even if
	// the session later ended badly.
	Connected bool
	Err       error
}

// noteServer records the address in use and re-detects the server profile.
//
// A join switch can move the bot to a different server mid-session, and the
// compat flags (including IdleNudge) are host-derived. Without re-detecting
// here the bot would keep the previous server's behaviour: switch to a Geyser
// host and it would sit still until kicked, or switch away and keep fidgeting
// on a server that never needed it.
// NoteServerForTest exposes noteServer so a test can prove the profile survives
// a session restart. It is the same entry point the run loop uses.
func (b *Bot) NoteServerForTest(address string) { b.noteServer(address) }

func (b *Bot) noteServer(address string) {
	profile := servercompat.Detect(address)
	// An explicit `is_geyser_server` in the config outranks hostname detection.
	// Without this, noteServer would re-detect on every session and silently
	// undo the operator's setting: the config says Geyser, the hostname has no
	// "geyser" in it, and the bot quietly reverts to the non-Geyser behaviour
	// while the config still reads true.
	if b.GeyserOverride != nil {
		profile.Geyser = *b.GeyserOverride
		profile.IdleNudge = *b.GeyserOverride
		profile.NoSubChunks = *b.GeyserOverride
		profile.NoHeldItemEcho = *b.GeyserOverride
		profile.SlashCommandFirst = *b.GeyserOverride
	}
	b.Mu.Lock()
	b.VenityCompat = profile.Venity
	b.NetherGamesCompat = profile.NetherGames
	b.IdleNudge = profile.IdleNudge
	b.GeyserNoSubChunks = profile.NoSubChunks
	b.GeyserNoHeldItemEcho = profile.NoHeldItemEcho
	if profile.SlashCommandFirst {
		b.CommandPrefixOrder = CommandPrefixSlashFirst
	} else {
		b.CommandPrefixOrder = CommandPrefixNoneFirst
	}
	b.Mu.Unlock()

	b.join.mu.Lock()
	defer b.join.mu.Unlock()
	if b.join.applied == "" {
		b.join.applied = address
	}
}

func (b *Bot) logSwitchResult(target string, err error) {
	if err != nil {
		b.Logger.Error("server switch failed", "address", target, "error", err.Error())
		return
	}
	b.Logger.Info("switching server", "address", target)
}
