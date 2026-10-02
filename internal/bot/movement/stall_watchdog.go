package movement

import (
	"context"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"

	"bedrock-ai/internal/bot"
)

// The tick heartbeat, and the goroutine dump that follows its silence.
//
// A session where the bot stops dead produces almost no log: the movement tick
// has nothing to say while it is refusing to move, the stuck counter is Debug,
// and the decision layer keeps ticking cheerfully. That silence is the whole
// failure — and when the bot is NOT dead, only busy, the same silence is
// indistinguishable from a deadlock. There is no way to ask a sync.Mutex who is
// holding it, so the tick is stamped and a separate goroutine, which needs no
// lock of its own to run, reports the stall and prints every goroutine it can
// see. Whoever is parked inside which lock is then in the log rather than a
// guess.
const (
	// tickStallThreshold is how long a 20Hz tick may go un-stamped before the
	// watchdog speaks. A synchronous A* run on a loaded world is the slowest
	// legitimate tick and lands around a few hundred milliseconds.
	tickStallThreshold = 3 * time.Second
	// tickStallCheckInterval is how often the watchdog looks. Slow on purpose:
	// this is a post-mortem aid, not a monitor.
	tickStallCheckInterval = 5 * time.Second
	// tickStallDumpBytes bounds the dump so a huge session cannot flood the log.
	tickStallDumpBytes = 256 << 10
)

// lastTickAt is stamped at the top of every movement tick, before the tick
// takes any lock. A tick that cannot even get past its first lock never
// refreshes it, which is exactly the case the dump exists to name.
var lastTickAt atomic.Int64

// TickStalled reports whether the last stamped tick is old enough that the
// watchdog should dump goroutines. A zero stamp means no tick has ever
// completed, which is itself a stall worth reporting.
func TickStalled(lastTick, now time.Time) bool {
	if lastTick.IsZero() {
		return true
	}
	return now.Sub(lastTick) > tickStallThreshold
}

// startStallWatchdog runs for the life of the connection and needs no bot lock
// to do its job, which is the point: when the tick is wedged on a mutex, the
// only goroutine that can still speak is one that never waits for it.
func startStallWatchdog(b *bot.Bot, ctx context.Context) {
	go func() {
		ticker := time.NewTicker(tickStallCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				last := time.UnixMilli(lastTickAt.Load())
				if !TickStalled(last, now) {
					continue
				}
				b.Logger.Warn("movement tick has not stamped for a while; dumping goroutines",
					slog.Duration("silent_for", now.Sub(last)),
					slog.Time("last_tick", last),
				)
				buf := make([]byte, tickStallDumpBytes)
				n := runtime.Stack(buf, true)
				b.Logger.Warn("goroutine dump:\n" + string(buf[:n]))
			}
		}
	}()
}
