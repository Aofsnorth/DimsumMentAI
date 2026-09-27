package bot

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// TestNormalizeServerAddressRejectsAmbiguousTargets keeps the join action honest
// about what it accepts. Bedrock's port differs between a LAN world, a dedicated
// server and a realm, so a bare host would silently dial a port nothing is
// listening on.
func TestNormalizeServerAddressRejectsAmbiguousTargets(t *testing.T) {
	t.Parallel()

	bad := []string{
		"",
		"   ",
		"example.com",
		"http://example.com:19132",
		"example.com:",
		"example.com:0",
		"example.com:99999",
		"example.com:abc",
		":19132",
	}
	for _, address := range bad {
		if got, err := NormalizeServerAddress(address); err == nil {
			t.Errorf("NormalizeServerAddress(%q) = %q, want an error", address, got)
		}
	}

	got, err := NormalizeServerAddress("  192.168.1.10:19132 ")
	if err != nil {
		t.Fatalf("NormalizeServerAddress(valid) error = %v", err)
	}
	if got != "192.168.1.10:19132" {
		t.Fatalf("NormalizeServerAddress(valid) = %q, want %q", got, "192.168.1.10:19132")
	}
}

// TestRequestJoinAppliesHookAndEndsSession is the behaviour the join action
// depends on: redirect the dialer *and* drop the live connection, because the
// bot is already bound to the old one.
func TestRequestJoinAppliesHookAndEndsSession(t *testing.T) {
	t.Parallel()

	b := &Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	var applied string
	b.SetJoinHook(func(address string) error {
		applied = address
		return nil
	})

	cancelled := false
	b.setSessionCancel(func() { cancelled = true })

	if err := b.RequestJoin("10.0.0.5:25565"); err != nil {
		t.Fatalf("RequestJoin error = %v", err)
	}
	if applied != "10.0.0.5:25565" {
		t.Errorf("hook got %q, want the normalised address", applied)
	}
	if !cancelled {
		t.Error("live session was not cancelled, so the run loop cannot start the new one")
	}

	target, ok := b.takeJoinRequest()
	if !ok || target != "10.0.0.5:25565" {
		t.Fatalf("takeJoinRequest() = %q, %v; want the pending switch", target, ok)
	}
	if _, ok := b.takeJoinRequest(); ok {
		t.Error("the same switch was handed out twice")
	}
}

// TestRequestJoinValidatesBeforeTouchingAnything keeps a typo from half-applying
// a switch: the dialer must keep pointing at the current server.
func TestRequestJoinValidatesBeforeTouchingAnything(t *testing.T) {
	t.Parallel()

	b := &Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	applied := false
	b.SetJoinHook(func(string) error {
		applied = true
		return nil
	})
	cancelled := false
	b.setSessionCancel(func() { cancelled = true })

	if err := b.RequestJoin("not-an-address"); err == nil {
		t.Fatal("RequestJoin(bad address) succeeded, want a validation error")
	}
	if applied {
		t.Error("dialer hook was called for an invalid address")
	}
	if cancelled {
		t.Error("session was cancelled for an invalid address, leaving the bot with no connection")
	}
	if _, ok := b.takeJoinRequest(); ok {
		t.Error("an invalid request was queued as a pending switch")
	}
}

// TestRequestJoinDropsPendingRequestWhenHookFails guards the recovery path: a
// failed redirect must not leave a switch queued that would reconnect to the old
// server on the next pass.
func TestRequestJoinDropsPendingRequestWhenHookFails(t *testing.T) {
	t.Parallel()

	b := &Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	b.SetJoinHook(func(string) error { return context.DeadlineExceeded })

	if err := b.RequestJoin("10.0.0.5:25565"); err == nil {
		t.Fatal("RequestJoin succeeded even though the hook failed")
	}
	if _, ok := b.takeJoinRequest(); ok {
		t.Error("a switch whose redirect failed is still queued")
	}
}

// TestRequestJoinWithoutHookIsReportedNotSilent covers a bot wired without a
// dialer (tests, tools): the player must be told, not left waiting.
func TestRequestJoinWithoutHookIsReportedNotSilent(t *testing.T) {
	t.Parallel()

	b := &Bot{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := b.RequestJoin("10.0.0.5:25565"); err != nil {
		t.Fatalf("RequestJoin without a hook error = %v, want the request to be queued for the run loop", err)
	}
	if _, ok := b.takeJoinRequest(); !ok {
		t.Error("request was dropped instead of queued")
	}
}

// TestMaxServerSwitchesIsBounded keeps a loop of join requests from keeping the
// process alive forever.
func TestMaxServerSwitchesIsBounded(t *testing.T) {
	t.Parallel()

	if maxServerSwitches < 2 {
		t.Fatalf("maxServerSwitches = %d, want room for more than one switch", maxServerSwitches)
	}
	if maxServerSwitches > 50 {
		t.Fatalf("maxServerSwitches = %d, want a bound that ends the process", maxServerSwitches)
	}
}
