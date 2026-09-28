package agi

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"bedrock-ai/internal/jev"
)

// Goal is a multi-tick objective. It is the difference between a bot that reacts
// to whatever happens next and one that is going somewhere.
//
// The activity curriculum answers "what could it do right now". A goal answers
// "what is it trying to do", and then narrows the curriculum to the things that
// actually move it forward. Without that narrowing every tick is an independent
// coin flip, and a bot that does thirty unrelated small things has not been
// busy — it has been lost.
type Goal struct {
	// Name is the stable identifier and the answer key for Jev.
	Name string
	// Description is what the model is shown. It has to read as an intention a
	// person would recognise, because that is the only thing that makes the
	// choice meaningful.
	Description string
	// Advances lists the activities that count as progress on this goal.
	//
	// It is deliberately a set rather than a single activity: a goal like
	// "stock up on supplies" is served by mining, by gathering, and by taking
	// from a chest, and a bot that could only do one of them would abandon the
	// goal whenever the other two happened to be convenient.
	Advances []string
	// Started is when the goal was adopted.
	Started time.Time
	// Deadline is when the goal stops being worth pursuing.
	//
	// Goals expire. A bot pursuing "get wood" forever is not persistent, it is
	// stuck, and the expiry is what lets it notice it has been chasing one thing
	// long past the point where a person would have moved on.
	Deadline time.Time
	// Progress counts activities that advanced the goal. It is exposed so the
	// state text can tell the model it is making headway, which changes the
	// answer: a model told a goal is going nowhere is more likely to abandon it.
	Progress int
}

// Expired reports whether the goal has run out of time.
func (g Goal) Expired(now time.Time) bool {
	return !g.Deadline.IsZero() && now.After(g.Deadline)
}

// advances reports whether an activity makes progress on this goal.
func (g Goal) advances(activity string) bool {
	for _, a := range g.Advances {
		if a == activity {
			return true
		}
	}
	return false
}

// Duration is how long a goal is pursued before it must be re-examined.
func (g Goal) Duration() time.Duration {
	return g.Deadline.Sub(g.Started)
}

// goalCatalogue is the full vocabulary. Only goals that make sense in the
// current world are offered to the model; this is the catalogue, not the menu.
var goalCatalogue = map[string]Goal{
	jev.GoalStockUp: {
		Name:        jev.GoalStockUp,
		Description: "gather the basic supplies it is short of, so it is prepared rather than empty-handed",
		Advances:    []string{jev.ActivityMine, jev.ActivityGather},
	},
	jev.GoalGatherWood: {
		Name:        jev.GoalGatherWood,
		Description: "get hold of some wood, which nearly everything else needs first",
		Advances:    []string{jev.ActivityMine, jev.ActivityGather},
	},
	jev.GoalFindStorage: {
		Name:        jev.GoalFindStorage,
		Description: "find and use the storage in this place, including any labelled rooms",
		Advances:    []string{jev.ActivityLook, jev.ActivityGather},
	},
	jev.GoalExplore: {
		Name:        jev.GoalExplore,
		Description: "see parts of the world it has not been to yet",
		Advances:    []string{jev.ActivityExplore, jev.ActivityWander, jev.ActivityLook},
	},
	jev.GoalBuildShelter: {
		Name:        jev.GoalBuildShelter,
		Description: "put up somewhere safer to be, especially before nightfall",
		Advances:    []string{jev.ActivityShelter, jev.ActivityGather},
	},
	jev.GoalSocialise: {
		Name:        jev.GoalSocialise,
		Description: "be near the people here, which is the whole point of a companion",
		Advances:    []string{jev.ActivityApproach, jev.ActivityChat, jev.ActivityGesture},
	},
	jev.GoalIdle: {
		Name:        jev.GoalIdle,
		Description: "have nothing pressing to do, so simply be here",
		Advances:    []string{jev.ActivityRest, jev.ActivityGesture, jev.ActivityLook},
	},
}

// AvailableGoals returns the goals that make sense right now.
//
// The filter is the important part and follows the same rule as the activity
// curriculum: never offer the model something that cannot succeed. "Get wood"
// is not a goal when there is no wood in sight and the inventory is full;
// offering it anyway produces a confident, pointless plan.
func AvailableGoals(s Snapshot) []Goal {
	out := make([]Goal, 0, len(goalCatalogue))

	// Idle is always available. A goal set that never contains "do nothing" is a
	// goal set that guarantees the bot is always busy, which is precisely the
	// failure this package exists to prevent.
	out = append(out, goalCatalogue[jev.GoalIdle])

	// Anything it could actually gather or mine.
	if s.NearBlocks != "" && s.NearBlocks != "none" && s.InventoryFree() {
		out = append(out, goalCatalogue[jev.GoalStockUp])
		out = append(out, goalCatalogue[jev.GoalGatherWood])
	}

	// Storage is only a goal when there is storage or signage to find.
	if len(s.VisibleSigns) > 0 {
		out = append(out, goalCatalogue[jev.GoalFindStorage])
	} else if strings.Contains(strings.ToLower(s.NearBlocks), "chest") {
		out = append(out, goalCatalogue[jev.GoalFindStorage])
	}

	// Exploring is always worth offering: it has no precondition beyond being
	// able to walk, which makes it the reliable fallback for a goal that wants
	// to see new things.
	out = append(out, goalCatalogue[jev.GoalExplore])

	// Shelter becomes urgent at night, and pointless at midday.
	if s.IsNight {
		out = append(out, goalCatalogue[jev.GoalBuildShelter])
	}

	// Socialising only when there is actually someone to be near.
	if _, ok := nearestVisible(s); ok {
		out = append(out, goalCatalogue[jev.GoalSocialise])
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GoalsFor returns the Jev answer key for the offered goals.
func GoalsFor(goals []Goal) []string {
	names := make([]string, 0, len(goals))
	for _, g := range goals {
		names = append(names, g.Name)
	}
	return names
}

// NarrowToGoal filters a curriculum down to what advances the active goal,
// always keeping "rest" reachable.
//
// Rest stays because a bot locked into a goal with no way to stop is not
// persistent, it is compulsive. Everything else is removed: a goal that
// tolerates every activity has not narrowed anything.
func NarrowToGoal(curriculum []string, goal Goal) []string {
	if goal.Name == "" {
		return curriculum
	}
	narrowed := make([]string, 0, len(curriculum))
	seen := make(map[string]bool, len(goal.Advances))
	for _, activity := range goal.Advances {
		if seen[activity] {
			continue
		}
		seen[activity] = true
		narrowed = append(narrowed, activity)
	}
	// Rest is always reachable, whatever the goal.
	narrowed = append(narrowed, jev.ActivityRest)
	return narrowed
}

// newGoal stamps a catalogue entry with a lifetime.
func newGoal(goal Goal, now time.Time, lifetime time.Duration) Goal {
	goal.Started = now
	goal.Deadline = now.Add(lifetime)
	goal.Progress = 0
	return goal
}

// describeGoal renders the active goal for the state text Jev reads.
func describeGoal(goal Goal) string {
	if goal.Name == "" {
		return "no goal"
	}
	remaining := int(goal.Duration().Seconds() / 60)
	if remaining < 1 {
		remaining = 1
	}
	return fmt.Sprintf("%s (progress %d, %d min left)", goal.Name, goal.Progress, remaining)
}
