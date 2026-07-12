// Package survival provides automation for managing the bot's survival needs,
// including auto-eat, auto-armor, auto-tool, time tracking, bed sleeping, torch
// placement, death recovery, shelter, and potions.
// This file handles world time tracking and time-of-day queries.
package survival

// SetWorldTime updates the tracked world time (called from packet handler)
func (m *Manager) SetWorldTime(ticks int64) {
	m.mu.Lock()
	m.worldTime = ticks % 24000
	m.isNight = m.worldTime >= 12500 && m.worldTime <= 23500
	m.isDay = !m.isNight
	m.mu.Unlock()
}

// IsNight returns whether it's currently nighttime
func (m *Manager) IsNight() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isNight
}

// GetWorldTime returns current world time in ticks (0-24000)
func (m *Manager) GetWorldTime() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.worldTime
}

// GetTimeOfDay returns a human-readable time description
func (m *Manager) GetTimeOfDay() string {
	m.mu.Lock()
	t := m.worldTime
	m.mu.Unlock()

	switch {
	case t >= 0 && t < 1000:
		return "pagi (fajar)"
	case t >= 1000 && t < 6000:
		return "siang"
	case t >= 6000 && t < 12000:
		return "sore"
	case t >= 12000 && t < 13000:
		return "senja"
	case t >= 13000 && t < 18000:
		return "malam"
	case t >= 18000 && t < 23000:
		return "tengah malam"
	default:
		return "menjelang pagi"
	}
}
