package survival

import "time"

// Test-support accessors.
//
// The reactive state a black-box test in tests/bot/survival needs to arrange —
// "the last meal was just now", "the last armor swap was just now", "the death
// happened a minute ago" — is deliberately unexported and guarded by the
// manager's mutex. Exporting the fields would put the lock discipline in
// everyone's hands, so these are the way in: the smallest surface that lets an
// external test put the manager into a state, without also exposing the state
// for the manager's own callers to write to.

// HungerLevel reports the currently tracked hunger level.
func (m *Manager) HungerLevel() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hungerLevel
}

// MarkLastEat records when the last automatic meal happened, which starts the
// eat cooldown.
func (m *Manager) MarkLastEat(at time.Time) {
	m.mu.Lock()
	m.lastEatTime = at
	m.mu.Unlock()
}

// MarkArmorAttempt records an automatic armor swap attempt, which starts the
// armor cooldown.
func (m *Manager) MarkArmorAttempt() {
	m.mu.Lock()
	m.reactive.lastArmorAttempt = time.Now()
	m.mu.Unlock()
}

// MarkDeathAt records when the death being recovered from happened, which is how
// a test ages a death past the respawn delay without sleeping through it.
func (m *Manager) MarkDeathAt(at time.Time) {
	m.mu.Lock()
	m.deathTime = at
	m.mu.Unlock()
}
