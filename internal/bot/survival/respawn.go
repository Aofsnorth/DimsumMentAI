package survival

import (
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Auto-respawn. Getting back into the world after dying is a handshake the
// *client* drives, and the bot implemented only the half of it that a login
// exercises:
//
//  1. host → client  Respawn{SearchingForSpawn}    the player died
//  2. client → host  Respawn{ReadyToSpawn}        "send me a spawn point"
//  3. host → client  Respawn{ReadyToSpawn, pos}   here it is
//  4. client → host  Respawn{ClientReadyToSpawn}  and the player is in the world
//
// Step 4 was the only one written, because that is the exchange a login
// produces — the bot looked perfect on join and nobody noticed. A death starts
// at step 1 instead, and that packet was logged as "self respawn position" and
// then dropped on the floor. The bot went to the death screen and stayed there,
// with no way back and nothing in the log to say why.
//
// This file owns the client half of that exchange for the half of it the bot
// can see: it tracks death from the host's own packet (network/player) and from
// health, and it repeats the request for the host that reports a death and then
// sends nothing at all.
const (
	// deathConfirmDelay is how long health must read zero before the bot treats
	// itself as dead. Health arrives on the same attribute packet as XP and
	// hunger and is resent constantly, so one zero reading is a dropped update,
	// not a corpse. The delay is the entire difference between the two.
	deathConfirmDelay = 750 * time.Millisecond

	// respawnRetryEvery is how often a dead bot repeats its request. It exists
	// for the host that reports the death through health and then never sends
	// step 1: without it the bot waits forever on a handshake that was never
	// coming, and the only symptom is a body lying still on the death screen.
	respawnRetryEvery = 2 * time.Second

	// respawnMaxAttempts bounds the repeating. A host that has ignored this
	// many requests is not going to answer the next one, and an unbounded retry
	// loop against a session the server has already given up on is worse than
	// stopping and letting the session layer redial.
	respawnMaxAttempts = 15
)

// ObserveHealth feeds the health the server last reported into the respawn
// state machine.
//
// feet is passed in rather than read from the bot because the caller is holding
// the bot's own lock: this package's mutex and the bot's are different locks,
// and taking them in the opposite order from elsewhere in the codebase is a
// deadlock waiting for a busy server.
func (m *Manager) ObserveHealth(hp int, feet mgl32.Vec3) {
	m.observeHealthAt(hp, feet, time.Now())
}

func (m *Manager) observeHealthAt(hp int, feet mgl32.Vec3, now time.Time) {
	m.mu.Lock()
	if hp > 0 {
		wasDead := m.dead
		m.dead = false
		m.deadSince = time.Time{}
		m.zeroHealthAt = time.Time{}
		m.respawnTries = 0
		m.lastRespawnAt = time.Time{}
		m.mu.Unlock()
		if wasDead {
			m.logger.Info("survival: alive again", "hp", hp)
		}
		return
	}

	if m.zeroHealthAt.IsZero() {
		m.zeroHealthAt = now
	}
	justDied := !m.dead && now.Sub(m.zeroHealthAt) >= deathConfirmDelay
	if justDied {
		m.dead = true
		m.deadSince = now
		m.deadPos = feet
		m.lastRespawnAt = time.Time{}
		m.respawnTries = 0
	}
	m.mu.Unlock()

	if justDied {
		// Exactly once, on the transition. MarkDeath stamps the timestamp
		// ShouldRecoverDeath waits out before going back for the corpse, and
		// re-stamping it on every subsequent zero-health packet would push that
		// trip out of reach for as long as the bot stayed dead.
		m.MarkDeath(feet)
		m.logger.Info("survival: died, will respawn", "pos", feet)
	}
}

// NoteRespawnInFlight records that the host's own Respawn packet was just
// answered, so the retry backs off for one interval instead of talking over the
// host's half of the handshake.
func (m *Manager) NoteRespawnInFlight() {
	m.mu.Lock()
	m.lastRespawnAt = time.Now()
	m.mu.Unlock()
}

// NoteDied arms the dead state from the death the host itself announced
// through the respawn handshake.
//
// The health path cannot be trusted to notice a death on its own: a body on
// the death screen frequently stops receiving the attribute stream the
// 750ms confirmation waits on, so a host that reports the death and then
// stalls the handshake would leave the retry loop never armed — the bot sat
// on the death screen forever with the machinery one flag away from working.
// The packet is the authoritative signal, so it is the one that arms it.
func (m *Manager) NoteDied(pos mgl32.Vec3) {
	m.mu.Lock()
	m.dead = true
	m.deadSince = time.Now()
	m.deadPos = pos
	m.zeroHealthAt = time.Now()
	m.lastRespawnAt = time.Time{}
	m.respawnTries = 0
	m.mu.Unlock()
	m.logger.Info("survival: death announced by host, respawn loop armed", "pos", pos)
}

// NoteRespawned clears the dead state once the bot is back in the world.
//
// It deliberately leaves hasDiedRecently and deathTime alone. Those drive item
// recovery, and a bot that has just respawned still owes itself a walk back to
// where it died; clearing them here is how "it died and came back" quietly
// became "it died and forgot where".
func (m *Manager) NoteRespawned() {
	m.mu.Lock()
	wasDead := m.dead
	m.dead = false
	m.deadSince = time.Time{}
	m.zeroHealthAt = time.Time{}
	m.respawnTries = 0
	m.lastRespawnAt = time.Time{}
	m.mu.Unlock()
	if wasDead {
		m.logger.Info("survival: respawned")
	}
}

// IsDead reports whether the bot is currently dead.
func (m *Manager) IsDead() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dead
}

// TickRespawn repeats the respawn request while the bot is dead.
//
// It is meant to be driven from the movement tick: the one loop that runs
// whether or not the autonomy brain is switched on, and so the only place a
// death is noticed when the bot is otherwise sitting still doing nothing.
func (m *Manager) TickRespawn() {
	m.tickRespawnAt(time.Now())
}

func (m *Manager) tickRespawnAt(now time.Time) {
	m.mu.Lock()
	dead := m.dead
	tries := m.respawnTries
	gaveUp := dead && tries >= respawnMaxAttempts
	due := dead && !gaveUp &&
		(m.lastRespawnAt.IsZero() || now.Sub(m.lastRespawnAt) >= respawnRetryEvery)
	if due {
		m.lastRespawnAt = now
		m.respawnTries = tries + 1
	}
	pos := m.deadPos
	attempt := tries + 1
	m.mu.Unlock()

	if gaveUp {
		// Fires once: respawnTries stops incrementing, so the equality holds for
		// exactly one tick.
		if tries == respawnMaxAttempts {
			m.logger.Error("survival: host ignored every respawn request, giving up",
				"attempts", tries)
		}
		return
	}
	if !due {
		return
	}

	m.logger.Info("survival: asking the host to respawn", "attempt", attempt, "pos", pos)
	if err := m.bot.WritePacket(&packet.Respawn{
		State:           packet.RespawnStateReadyToSpawn,
		Position:        pos,
		EntityRuntimeID: m.bot.GetEntityRuntimeID(),
	}); err != nil {
		m.logger.Warn("survival: respawn request failed", "attempt", attempt, "error", err)
	}
}
