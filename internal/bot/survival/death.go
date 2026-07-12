// Package survival provides automation for managing the bot's survival needs,
// including auto-eat, auto-armor, auto-tool, time tracking, bed sleeping, torch
// placement, death recovery, shelter, and potions.
// This file tracks the bot's last death position and recovery state.
package survival

import (
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

// MarkDeath records the bot's death position for recovery
func (m *Manager) MarkDeath(pos mgl32.Vec3) {
	m.mu.Lock()
	m.lastDeathPos = pos
	m.hasDiedRecently = true
	m.deathTime = time.Now()
	m.mu.Unlock()
	m.logger.Info("Death recorded", "pos", pos)
}

// GetDeathPos returns the last death position
func (m *Manager) GetDeathPos() (mgl32.Vec3, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastDeathPos, m.hasDiedRecently
}

// ClearDeath clears the death recovery flag
func (m *Manager) ClearDeath() {
	m.mu.Lock()
	m.hasDiedRecently = false
	m.mu.Unlock()
}
