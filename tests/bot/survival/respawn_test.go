package survival_test

import (
	"testing"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// The respawn handshake the tests model: a host sends Respawn SearchingForSpawn
// when the player dies, the client answers ReadyToSpawn, the host answers with
// ReadyToSpawn carrying the spawn point, and the client answers
// ClientReadyToSpawn. These tests cover the half the bot drives — noticing the
// death, and asking again when the host goes quiet.

var deathPos = mgl32.Vec3{-95.5, 64, -127.5}

// TestOneZeroHealthReadingIsNotADeath is the debounce. Health arrives on the
// same attribute packet as XP and hunger and is resent constantly, so a single
// zero is a dropped update. Acting on it would respawn a live bot.
func TestOneZeroHealthReadingIsNotADeath(t *testing.T) {
	t.Parallel()

	f := newFakeBot()
	m := newTestManager(f)
	start := time.Unix(1_700_000_000, 0)

	m.ObserveHealthAt(0, deathPos, start)
	m.TickRespawnAt(start.Add(time.Second))

	if m.IsDead() {
		t.Error("one zero-health reading was treated as a death")
	}
	if got := countRespawnRequests(f); got != 0 {
		t.Errorf("sent %d respawn requests, want 0", got)
	}
}

// TestADeathIsNotDeclaredUntilHealthHasBeenZeroAwhile is the same debounce from
// the other side: sustained zero health is a corpse, and it is what lets the bot
// respawn on a host that reports the death through health and nothing else.
func TestADeathIsNotDeclaredUntilHealthHasBeenZeroAwhile(t *testing.T) {
	t.Parallel()

	f := newFakeBot()
	m := newTestManager(f)
	start := time.Unix(1_700_000_000, 0)

	// Well under the confirm delay: still nothing.
	m.ObserveHealthAt(0, deathPos, start)
	m.ObserveHealthAt(0, deathPos, start.Add(100*time.Millisecond))
	m.TickRespawnAt(start.Add(200 * time.Millisecond))
	if m.IsDead() {
		t.Fatal("health zero for a moment was declared a death")
	}

	// Well past it: dead, and the retry asks the host.
	m.ObserveHealthAt(0, deathPos, start.Add(5*time.Second))
	m.TickRespawnAt(start.Add(5*time.Second + 10*time.Millisecond))

	if !m.IsDead() {
		t.Fatal("health zero for seconds was never declared a death")
	}
	if got := countRespawnRequests(f); got != 1 {
		t.Errorf("sent %d respawn requests, want 1", got)
	}
}

// TestARecoveredBotNeverRespawnsItself is the guard on the whole feature: the
// retry is keyed on being dead, so a bot whose health came back must never send
// one.
func TestARecoveredBotNeverRespawnsItself(t *testing.T) {
	t.Parallel()

	f := newFakeBot()
	m := newTestManager(f)
	start := time.Unix(1_700_000_000, 0)

	m.ObserveHealthAt(0, deathPos, start)
	m.ObserveHealthAt(0, deathPos, start.Add(5*time.Second))
	m.ObserveHealthAt(20, deathPos, start.Add(5*time.Second+10*time.Millisecond))
	m.TickRespawnAt(start.Add(10 * time.Second))

	if m.IsDead() {
		t.Error("health back at 20 still read as dead")
	}
	if got := countRespawnRequests(f); got != 0 {
		t.Errorf("a live bot sent %d respawn requests, want 0", got)
	}
}

// TestRespawnRetryWaitsBetweenRequests is the cadence. Flooding the host with a
// packet a second is how a bot gets itself rate-limited on top of being dead.
func TestRespawnRetryWaitsBetweenRequests(t *testing.T) {
	t.Parallel()

	f := newFakeBot()
	m := newTestManager(f)
	start := time.Unix(1_700_000_000, 0)

	m.ObserveHealthAt(0, deathPos, start)
	m.ObserveHealthAt(0, deathPos, start.Add(5*time.Second))

	m.TickRespawnAt(start.Add(5*time.Second + 10*time.Millisecond))
	m.TickRespawnAt(start.Add(5*time.Second + time.Second)) // too soon
	m.TickRespawnAt(start.Add(5*time.Second + 3*time.Second))

	if got := countRespawnRequests(f); got != 2 {
		t.Errorf("sent %d respawn requests, want 2 (one immediate, one after the gap)", got)
	}
}

// TestTheRetryEventuallyGivesUp bounds the loop. A host that has ignored every
// request is not going to answer the next one, and retrying forever against a
// session the server has already dropped is worse than stopping.
func TestTheRetryEventuallyGivesUp(t *testing.T) {
	t.Parallel()

	f := newFakeBot()
	m := newTestManager(f)
	start := time.Unix(1_700_000_000, 0)

	// Death is a debounced reading: one zero-health packet arms the clock, the
	// confirmation only lands on a second zero after deathConfirmDelay. The
	// first ObserveHealthAt arms; this second one is what makes the bot dead.
	m.ObserveHealthAt(0, deathPos, start)
	m.ObserveHealthAt(0, deathPos, start.Add(5*time.Second))
	at := start.Add(5 * time.Second)
	var last int
	for i := 0; i < 400; i++ {
		at = at.Add(time.Second)
		m.TickRespawnAt(at)
		last = countRespawnRequests(f)
	}

	if last == 0 {
		t.Fatal("a dead bot never asked to respawn at all")
	}
	if last > 15 {
		t.Errorf("sent %d respawn requests; the retry has no ceiling", last)
	}
}

// TestRespawningKeepsTheDeathPosition is the trap in clearing the dead flag: the
// death-recovery state is a different thing, and wiping both means a bot that
// respawned forgets where its own items are.
func TestRespawningKeepsTheDeathPosition(t *testing.T) {
	t.Parallel()

	m := newTestManager(newFakeBot())
	start := time.Unix(1_700_000_000, 0)

	m.ObserveHealthAt(0, deathPos, start)
	m.ObserveHealthAt(0, deathPos, start.Add(5*time.Second))
	m.NoteRespawned()

	if m.IsDead() {
		t.Error("still dead after NoteRespawned")
	}
	pos, hasDied := m.GetDeathPos()
	if !hasDied {
		t.Fatal("respawning erased the fact that the bot had died")
	}
	if pos != deathPos {
		t.Errorf("death pos = %v, want %v", pos, deathPos)
	}
}

// TestTheHostDrivenHandshakeBacksTheRetryOff: when the host sends its own
// Respawn, the client answers it and the retry must not talk over that.
func TestTheHostDrivenHandshakeBacksTheRetryOff(t *testing.T) {
	t.Parallel()

	f := newFakeBot()
	m := newTestManager(f)
	start := time.Unix(1_700_000_000, 0)

	m.ObserveHealthAt(0, deathPos, start)
	m.ObserveHealthAt(0, deathPos, start.Add(5*time.Second))

	// The host's packet arrives and is answered by the network layer, which
	// tells the manager it is in flight.
	m.NoteRespawnInFlight()
	m.TickRespawnAt(start.Add(5*time.Second + 10*time.Millisecond))

	if got := countRespawnRequests(f); got != 0 {
		t.Errorf("sent %d respawn requests while the host's own handshake was in flight, want 0", got)
	}
}

// countRespawnRequests counts the Respawn packets the fake bot was handed, which
// is what the retry is judged on.
func countRespawnRequests(f *fakeBot) int {
	n := 0
	for _, pk := range f.written {
		if _, ok := pk.(*packet.Respawn); ok {
			n++
		}
	}
	return n
}

var _ = mgl32.Vec3{}
