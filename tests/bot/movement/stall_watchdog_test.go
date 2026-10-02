// The stall watchdog, and the silence it exists to end.
//
// A bot that has stopped dead and a bot that is merely busy produce the same
// log: no movement lines, no stuck counter (that one is Debug), and a decision
// layer that keeps ticking. In the session that prompted this file, the bot went
// silent for eight seconds before it was killed, and nothing in those eight
// seconds said why — four subsystems that only share one lock stopped at the
// same moment, which is the signature of a lock, but not of which lock or which
// goroutine. There is no way to ask a sync.Mutex for its owner, so the tick
// stamps itself and a goroutine that needs no lock to run dumps the stacks.
//
// The threshold is deliberately longer than the slowest legitimate tick: an
// inline A* run over a loaded world is the worst case, and a watchdog that fires
// during normal work trains everyone to ignore it.

package movement_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/movement"
)

func TestTickStalled(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		last time.Time
		want bool
	}{
		{"a tick just stamped is healthy", now.Add(-50 * time.Millisecond), false},
		{"an A* run is still healthy", now.Add(-2 * time.Second), false},
		{"a wedged tick is reported", now.Add(-30 * time.Second), true},
		{"a tick that never ran is reported", time.Time{}, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := movement.TickStalled(tc.last, now); got != tc.want {
				t.Fatalf("TickStalled(last=%s, now=%s) = %v, want %v", tc.last, now, got, tc.want)
			}
		})
	}
}
