package bot

import (
	"errors"
	"testing"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/google/uuid"
)

// TestServerDisconnectTellsHostCloseFromKick is the guard for the LAN case. A
// host that closes the world sends no Disconnect packet, so the read fails with
// an empty reason. That is the one situation worth waiting out and rejoining;
// anything the server actually said (kick, ban) is not.
func TestServerDisconnectTellsHostCloseFromKick(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason string
		want   bool
	}{
		{name: "empty reason is a host close", reason: "", want: true},
		{name: "whitespace only is a host close", reason: "   \n", want: true},
		{name: "server explained itself", reason: "You were kicked", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := &ServerDisconnect{Reason: tc.reason}
			if got := d.HostClosedWorld(); got != tc.want {
				t.Fatalf("HostClosedWorld() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestServerDisconnectMessageIsNeverBlank keeps the log line useful: an empty
// reason used to print reason="" and read like a mystery.
func TestServerDisconnectMessageIsNeverBlank(t *testing.T) {
	t.Parallel()

	if msg := (&ServerDisconnect{}).Error(); msg == "" {
		t.Fatal("Error() is blank for an empty reason, want a readable fallback")
	}
	if msg := (&ServerDisconnect{Reason: "banned"}).Error(); msg != "banned" {
		t.Fatalf("Error() = %q, want the server's own reason", msg)
	}
}

// TestReconnectDelayBacksOffAndCaps checks the wait grows so a host that needs
// a few seconds to reopen its world is not hammered, and that it never grows
// without bound.
func TestReconnectDelayBacksOffAndCaps(t *testing.T) {
	t.Parallel()

	if got := reconnectDelay(1); got != reconnectBaseDelay {
		t.Fatalf("reconnectDelay(1) = %v, want %v", got, reconnectBaseDelay)
	}
	if got := reconnectDelay(2); got <= reconnectDelay(1) {
		t.Fatalf("reconnectDelay(2) = %v, want longer than %v", got, reconnectDelay(1))
	}
	// A nonsensical attempt number must still yield a usable delay, not zero
	// (which would turn a retry storm into a busy loop).
	if got := reconnectDelay(0); got != reconnectBaseDelay {
		t.Fatalf("reconnectDelay(0) = %v, want %v", got, reconnectBaseDelay)
	}
	if got := reconnectDelay(-5); got != reconnectBaseDelay {
		t.Fatalf("reconnectDelay(-5) = %v, want %v", got, reconnectBaseDelay)
	}
	for _, attempt := range []int{1, 3, 7, 50, 1000} {
		if got := reconnectDelay(attempt); got > reconnectMaxDelay {
			t.Fatalf("reconnectDelay(%d) = %v, want at most %v", attempt, got, reconnectMaxDelay)
		}
	}
	if got := reconnectDelay(1000); got != reconnectMaxDelay {
		t.Fatalf("reconnectDelay(1000) = %v, want the cap %v", got, reconnectMaxDelay)
	}
}

// TestMaxReconnectAttemptsIsBounded pins the "do not hang forever" side: a
// server that is genuinely gone has to end the process rather than leave the
// user watching a silent retry loop.
func TestMaxReconnectAttemptsIsBounded(t *testing.T) {
	t.Parallel()

	if maxReconnectAttempts < 2 {
		t.Fatalf("maxReconnectAttempts = %d, want room for at least one rejoin", maxReconnectAttempts)
	}
	if maxReconnectAttempts > 20 {
		t.Fatalf("maxReconnectAttempts = %d, want a bound that ends the process", maxReconnectAttempts)
	}
	// The total wait must stay in a range a person will actually sit through.
	total := time.Duration(0)
	for i := 1; i <= maxReconnectAttempts; i++ {
		total += reconnectDelay(i)
	}
	if total > 2*time.Minute {
		t.Fatalf("full retry ladder waits %v, want it bounded under 2 minutes", total)
	}
}

// TestErrTextSurvivesNilError guards the log field helper used on the reconnect
// path, where a session can end without an error.
func TestErrTextSurvivesNilError(t *testing.T) {
	t.Parallel()

	if got := errText(nil); got != "" {
		t.Fatalf("errText(nil) = %q, want empty", got)
	}
	if got := errText(errors.New("boom")); got != "boom" {
		t.Fatalf("errText(err) = %q, want %q", got, "boom")
	}
}

// TestResetSessionStateDropsEntityAndPlayerTracking is the ghost-entity guard.
// A host closing its world sends no RemoveActor and no PlayerList removal, so
// anything still in the tracking maps at rejoin time is a phantom from the old
// world: GetEntities would report mobs that are gone, FindPlayer would hand out
// stale positions, and "interact with what's in front" could click one. The
// reset has to empty all four maps; the new session re-populates them.
func TestResetSessionStateDropsEntityAndPlayerTracking(t *testing.T) {
	t.Parallel()

	b := &Bot{
		Actors: map[uint64]*entity.Info{
			42: {ID: 42, Type: "minecraft:cow", Name: "cow", Position: mgl32.Vec3{1, 64, 2}},
		},
		UniqueIDToRuntimeID: map[int64]uint64{-99: 42},
		PlayerTracker: PlayerTracker{
			PlayerEntityIDs: map[string]uint64{"Arthenyxx": 7},
			PlayerUsernames: map[uint64]string{7: "Arthenyxx"},
			PlayerPositions: map[uint64]mgl32.Vec3{7: {3, 64, 4}},
			PlayerYaws:      map[uint64]float32{7: 90},
			PlayerPitches:   map[uint64]float32{7: 0},
			PlayerUUIDs:     map[uuid.UUID]string{uuid.New(): "Arthenyxx"},
		},
		ItemNames: map[int32]string{213: "minecraft:oak_log"},
	}

	b.resetSessionState()

	if len(b.Actors) != 0 {
		t.Fatalf("Actors still holds %d entries after reset, want 0", len(b.Actors))
	}
	if len(b.UniqueIDToRuntimeID) != 0 {
		t.Fatalf("UniqueIDToRuntimeID still holds %d entries after reset, want 0", len(b.UniqueIDToRuntimeID))
	}
	if len(b.PlayerEntityIDs) != 0 || len(b.PlayerUsernames) != 0 || len(b.PlayerPositions) != 0 ||
		len(b.PlayerYaws) != 0 || len(b.PlayerPitches) != 0 || len(b.PlayerUUIDs) != 0 {
		t.Fatalf("player tracking survived the reset: ids=%d names=%d pos=%d yaws=%d pitches=%d uuids=%d",
			len(b.PlayerEntityIDs), len(b.PlayerUsernames), len(b.PlayerPositions),
			len(b.PlayerYaws), len(b.PlayerPitches), len(b.PlayerUUIDs))
	}
	if len(b.ItemNames) != 0 {
		t.Fatalf("ItemNames still holds %d entries after reset, want 0 (re-seeded per session)", len(b.ItemNames))
	}
}

// TestResetSessionStateKeepsMovementDefaults pins the idle/nil state so the
// reconnect path cannot inherit a walk from the previous world's plan.
func TestResetSessionStateKeepsMovementDefaults(t *testing.T) {
	t.Parallel()

	b := &Bot{
		CurrentPath:           []pathfinder.Node{{X: 1, Y: 64, Z: 2}},
		PathIndex:             1,
		TicksStuck:            40,
		ConsecutiveStuckCount: 2,
		StuckWindowStart:      time.Now(),
		MovementState:         "walk_to",
	}

	b.resetSessionState()

	if b.CurrentPath != nil || b.PathIndex != 0 || b.TicksStuck != 0 ||
		b.ConsecutiveStuckCount != 0 || b.MovementState != "idle" || !b.StuckWindowStart.IsZero() {
		t.Fatalf("movement state survived the reset: path=%v index=%d stuck=%d consecutive=%d state=%q window=%v",
			b.CurrentPath, b.PathIndex, b.TicksStuck, b.ConsecutiveStuckCount, b.MovementState, b.StuckWindowStart)
	}
}
