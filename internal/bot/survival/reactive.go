// Reactive survival: the things the bot does without being asked.
//
// Every one of these behaviours was already implemented and reachable through
// an action label, which meant the bot only performed them when the LLM
// happened to emit the right action tag. That is not self-preservation, it is
// coincidence: a bot that only eats when someone types "eat" is not looking
// after itself. The config even advertised auto-sleep and auto-shelter, and
// nothing ever read those flags.
//
// The design rule throughout is that urgency decides whether something may
// interrupt. Death recovery interrupts unconditionally, because no player
// ignores the pile of items they just dropped. Everything else defers to
// whatever the bot is already doing, and carries a cooldown, because a
// self-preservation loop that fights the brain's own plan is a bot that appears
// to have a seizure rather than a bot that takes care of itself.
//
// The decision half of every behaviour is a separate function from the acting
// half. That split is not cosmetic: the actions walk, block, and sleep for
// seconds at a time, so a test that exercised them would take a minute per case
// and assert on side effects instead of on the judgement. Deciding is what
// deserves a test; doing is what deserves to be obvious.
package survival

import (
	"context"
	"time"
)

const (
	// deathRecoverDelay is how long after a death the bot goes back for its
	// items. Short enough to be plausible, long enough that the death has
	// actually finished respawning before the bot starts walking anywhere.
	deathRecoverDelay = 3 * time.Second

	// sleepRetryCooldown is the minimum gap between attempts to use a bed.
	//
	// SleepInBed can fail for reasons the bot cannot control — no path, the bed
	// is occupied, the server is busy — and without a cooldown the loop retries
	// every half second forever, which fills the log and, on some servers,
	// looks like a bot hammering an interaction.
	sleepRetryCooldown = 60 * time.Second

	// shelterRetryCooldown is the same for sheltering. Shelters are expensive
	// to build, so retrying aggressively would have the bot walling itself in
	// repeatedly.
	shelterRetryCooldown = 90 * time.Second

	// torchCooldown is the minimum gap between automatic torch placements.
	torchCooldown = 45 * time.Second

	// nightStartTicks is when the bot considers night to have properly fallen.
	//
	// It is later than the isNight flag (which starts at 12500, dusk) on
	// purpose: a player does not run to bed the instant the sky turns, and a bot
	// that does looks twitchy. This is the point at which going to bed is the
	// obvious thing rather than merely a reasonable one.
	nightStartTicks = 13500

	// bedSearchRadius is how far the bot looks for a bed it could use. It walks
	// there, so a small radius is better than a hopeful one: offering to sleep
	// in a bed three chunks away produces a long walk for a bed that may be
	// occupied.
	bedSearchRadius = 12
)

// reactive carries the cooldowns for the unattended behaviours.
type reactiveState struct {
	lastSleepAttempt   time.Time
	lastShelterAttempt time.Time
	lastTorchAttempt   time.Time
}

// NightPlan is the decision about what to do about the dark, before any of it
// is acted on.
type NightPlan int

const (
	// NightDoNothing means the night routine has nothing to do: it is daytime,
	// the bot is busy, or a cooldown is still running.
	NightDoNothing NightPlan = iota
	// NightSleep means a bed is reachable and idle enough to use.
	NightSleep
	// NightShelter means it is properly night, there is no bed to be had, and
	// the bot should put something between itself and the dark.
	NightShelter
)

// ShouldRecoverDeath reports whether the bot should go back for what it dropped
// after dying.
//
// This is the one reactive decision that ignores whether the bot is busy,
// because there is no version of "busy" that outranks your own corpse. It is
// also the most valuable one: a bot that respawns and walks away from its
// dropped inventory has thrown away everything it was carrying.
func (m *Manager) ShouldRecoverDeath() bool {
	m.mu.Lock()
	died, at := m.hasDiedRecently, m.deathTime
	m.mu.Unlock()
	if !died {
		return false
	}
	// Wait out the respawn before starting to walk anywhere.
	return time.Since(at) >= deathRecoverDelay
}

// PlanNight decides what to do about the dark.
//
// It is a decision, not an action, so that the judgement can be tested without
// the walking and the blocking sleeps that come with actually doing it.
func (m *Manager) PlanNight() NightPlan {
	if !m.autoNightOn {
		return NightDoNothing
	}
	// Busy means the brain is doing something. Interrupting it to go to bed
	// mid-task is the behaviour that makes a bot look like it cannot hold a
	// thought, so this defers and tries again next tick.
	if m.bot.IsBusy() {
		return NightDoNothing
	}
	if m.GetWorldTime() < nightStartTicks {
		return NightDoNothing
	}

	hasBed := m.hasReachableBed()

	if m.AutoSleepEnabled && hasBed && m.takeCooldown(&m.reactive.lastSleepAttempt, sleepRetryCooldown) {
		return NightSleep
	}
	// Only shelter when there is genuinely nowhere to sleep. Building walls
	// around a bed you already own is the sort of thing that looks like the bot
	// has lost the plot.
	if m.AutoShelterEnabled && !hasBed && m.takeCooldown(&m.reactive.lastShelterAttempt, shelterRetryCooldown) {
		return NightShelter
	}
	return NightDoNothing
}

// ShouldPlaceTorch reports whether the bot should place a torch right now.
//
// The honest limitation, which the constant's comment also records: the bot has
// no light-level data, so this cannot mean "I cannot see" the way it would for
// a player. It means "it is night, I am carrying torches, I am idle, and I have
// not just placed one" — a weaker and more deliberate thing. Wiring it to real
// light data would make it correct; guessing at darkness would make it random.
func (m *Manager) ShouldPlaceTorch() bool {
	if !m.autoTorchOn || !m.AutoTorchEnabled {
		return false
	}
	if !m.IsNight() || m.bot.IsBusy() {
		return false
	}
	// Never announce placing a torch the bot does not have. Checked before the
	// cooldown is consumed, so a bot that has run out of torches does not
	// silently burn a 45-second window.
	if !m.holdsTorch() {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// The cooldown is marked on the manager's own field, under the lock: passing
	// a copy to a helper looks correct and records nothing, which would leave
	// the loop placing a torch every tick.
	field := &m.reactive.lastTorchAttempt
	if !field.IsZero() && time.Since(*field) < torchCooldown {
		return false
	}
	*field = time.Now()
	return true
}

// takeCooldown reports whether a cooldown has run out, and marks it spent when
// it has. Marking here rather than at the call site means two callers cannot
// both decide the same window is free.
//
// The field is passed by pointer on purpose and must be the manager's own field,
// not a copy: handing a local to this helper looks correct and records nothing.
func (m *Manager) takeCooldown(field *time.Time, cooldown time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !field.IsZero() && time.Since(*field) < cooldown {
		return false
	}
	*field = time.Now()
	return true
}

func (m *Manager) hasReachableBed() bool {
	_, ok := m.FindNearbyBed(bedSearchRadius)
	return ok
}

func (m *Manager) holdsTorch() bool {
	names := m.bot.GetItemNames()
	for _, item := range m.bot.GetInventorySlots() {
		if item.Count <= 0 {
			continue
		}
		if names[item.NetworkID] == "torch" {
			return true
		}
	}
	return false
}

// Tick runs the survival automation loop (called every 500ms).
//
// The reactive half is what makes the AutoSleep/AutoShelter/AutoTorch flags
// mean anything. Before this they were advertised in the config and read by
// nothing, so a bot configured for self-preservation still only ever ate and
// equipped armour on its own.
func (m *Manager) Tick() {
	m.tickAutoEat()
	m.tickAutoArmor()
	m.tickDeathRecovery()
	m.tickNightRoutine()
	m.tickTorch()
}

// tickDeathRecovery acts on ShouldRecoverDeath.
func (m *Manager) tickDeathRecovery() {
	if !m.ShouldRecoverDeath() {
		return
	}
	deathPos, _ := m.GetDeathPos()
	m.logger.Info("survival: recovering dropped items", "pos", deathPos)
	m.RecoverFromDeath(context.Background())
}

// tickNightRoutine acts on PlanNight.
func (m *Manager) tickNightRoutine() {
	switch m.PlanNight() {
	case NightSleep:
		m.logger.Info("survival: night, using a bed")
		m.SleepInBed(context.Background())
	case NightShelter:
		m.logger.Info("survival: night with no bed, looking for shelter")
		m.BuildEmergencyShelter(context.Background())
	case NightDoNothing:
	}
}

// tickTorch acts on ShouldPlaceTorch.
func (m *Manager) tickTorch() {
	if !m.ShouldPlaceTorch() {
		return
	}
	m.logger.Info("survival: placing a torch at night")
	m.AutoPlaceTorches(context.Background())
}
