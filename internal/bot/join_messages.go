package bot

import (
	"context"
	"log/slog"
	"time"
)

// Some servers are only usable once the client proves it belongs: a password
// prompt, a registration command, a lobby warm-up. Doing that from chat means
// typing it by hand on every reconnect — and the bot reconnects on its own, so
// the prompt would otherwise deadlock it.
//
// The lines are replayed after every join, including a rejoin and a deliberate
// server switch, because every one of those is a fresh session the server has
// never seen.

// RunJoinMessages sends the configured lines once the world has settled.
//
// The first delay is load-bearing: right after spawn the server is still
// finishing the world transfer, and a command sent into that window comes back
// as "unknown command". The gap between lines stops a multi-line burst from
// arriving as a single flood the server rate-limits or kicks for.
func (b *Bot) RunJoinMessages(ctx context.Context) {
	lines := b.JoinMessages
	if len(lines) == 0 {
		return
	}

	delay := b.JoinMessageDelay
	if delay <= 0 {
		delay = 2 * time.Second
	}
	interval := b.JoinMessageInterval
	if interval <= 0 {
		interval = time.Second
	}

	if !sleepCtx(ctx, delay) {
		return
	}
	for i, line := range lines {
		if i > 0 && !sleepCtx(ctx, interval) {
			return
		}
		b.sendJoinLine(ctx, line)
	}
}

// sendJoinLine dispatches one configured line to the channel it belongs to.
func (b *Bot) sendJoinLine(ctx context.Context, line string) {
	if line == "" {
		return
	}
	if IsCommandLine(line) {
		if err := b.SendCommand(line); err != nil {
			b.Logger.Warn("join command failed", slog.String("line", line), slog.String("error", err.Error()))
		}
		return
	}
	// Chat goes out on the session goroutine, so it has to yield to the tick
	// loop rather than block it for the length of the message.
	go func() {
		if ctx.Err() != nil {
			return
		}
		b.SendSafeChat(line)
	}()
}

// sleepCtx waits for d and reports whether the wait completed rather than being
// cut short by the session ending. A cancelled session must not keep sending.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
