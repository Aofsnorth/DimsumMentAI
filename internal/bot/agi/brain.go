// Package agi gives the bot an agenda of its own.
//
// With AGI on, the bot is not a tool that waits to be told what to do — it is a
// companion that decides what to do next. The loop is deliberately split in
// two, because the two halves have opposite requirements:
//
//   - The reflex layer is local and instant. Danger, hunger and a player
//     walking into view are handled without asking anyone. A bot that pauses to
//     consult a language model before eating is a bot that starves.
//
//   - The decision layer asks the model what to do with itself. It is gated by
//     a probability and a cooldown, because an agent that acts on every thought
//     is a spammer, not a companion.
//
// The most important design constraint is restraint. Everything here is built
// to make the bot *look* like a person who happens to be playing: it acts
// rarely, speaks rarely, and prefers doing nothing to inventing something to
// do. A bot that is always busy is more obviously a bot than one that is
// sometimes still.
package agi

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"bedrock-ai/internal/bot/perception"
)

// Snapshot is everything the brain is allowed to know about the world right
// now. It is a value type on purpose: a decision is made from one immutable
// reading, so the world cannot change underneath the reasoning half way through.
type Snapshot struct {
	Now        time.Time
	Coords     string
	HP         int
	Hunger     int
	HeldItem   string
	Inventory  string
	VisibleMob string
	NearBlocks string
	// VisibleSigns is the text of signage the bot can read right now. It is
	// part of the state Jev reasons over because a labelled storage room is a
	// plan, and a bot that cannot see the labels will search it blindly.
	VisibleSigns []string
	Nearby       []Person
	Busy         bool
	Exploring    bool
	// IsNight drives the day/night behaviour. A bot that wanders off at
	// midnight and gets eaten is doing something no player would do, and the
	// failure is invisible in a log — it just looks like bad luck.
	IsNight bool
	// HasBed records whether the bot could actually sleep it off. Without one,
	// the night reflex has to shelter rather than lie down.
	HasBed bool
	// FreeSlots is how many empty inventory slots the bot has. The curriculum
	// uses it to stop offering work it cannot bank: a bot that mines wood with
	// a full inventory swings at a tree and then has nowhere to put the wood,
	// which looks worse than never offering the activity.
	FreeSlots int
	// GoalSummary describes the active goal in the model's own terms. It goes
	// into the state text so the model can judge whether the goal still makes
	// sense — a bot that is told "currently working towards stock_up (progress
	// 0, 4 min left)" can reason about abandoning it, which a bot that is only
	// shown the present moment cannot.
	GoalSummary string
	// PlanSummary is the active plan, or empty when there is none. It goes into
	// the state text so Jev is deciding with the plan in view rather than
	// picking a bounded action at random.
	PlanSummary string
	// Craftable is how many distinct recipes the bot could make right now. It
	// gates the craft activity, because a bot asked to craft with no ingredients
	// fails every time and the failure is visible.
	Craftable int
	// Features records what is in view that makes specific activities possible.
	// Without these preconditions the curriculum would offer fishing with no
	// water in sight and the bot would cast at a tree, which is worse than never
	// offering fishing at all.
	//
	// It is the perception package's own type rather than a copy, so the
	// preconditions the curriculum reasons about are literally the same scan the
	// block summary used a moment earlier.
	Features perception.Features
	// Underwater and SecondsUnderwater drive the breath reflex. They are derived
	// from the world rather than from an air-supply packet, because the bot
	// cannot see one: knowing that water is where the head is and counting the
	// seconds since is enough to reproduce what a player does, and it does not
	// depend on a protocol field this client has never been shown sending.
	//
	// SecondsUnderwater is zero on the surface. A bot that keeps working at the
	// bottom of a flooded mine and only surfaces when a health bar has already
	// started falling is a bot that surfaces dead.
	Underwater        bool
	SecondsUnderwater int
}

// InventoryFree reports whether the bot has room to collect more. One free
// slot is enough for the decision, because a full inventory is the failure
// this guards against, not a nearly-full one.
func (s Snapshot) InventoryFree() bool {
	return s.FreeSlots > 0
}

// Person is another player the bot is aware of.
type Person struct {
	Name       string
	Distance   float32
	HasLineOf  bool
	LookingAt  bool
	SpeakingTo bool
}

// Urgency is how badly the bot needs to act on this snapshot, in the reflex
// layer. It is an enum rather than a score so the caller can make a decision
// without interpreting a float, and so "critical" can never be out-voted by a
// pile of low-priority wants.
type Urgency int

const (
	// UrgencyNone means nothing needs doing right now. This is the common case
	// and it is a success, not a failure: an agent that is never calm is not an
	// agent, it is a loop.
	UrgencyNone Urgency = iota
	// UrgencyLow means the bot is comfortable but idle and would rather be
	// doing something.
	UrgencyLow
	// UrgencyHigh means a survival reflex should fire.
	UrgencyHigh
	// UrgencyCritical means something is about to kill the bot.
	UrgencyCritical
)

// Thresholds are the numbers the reflex layer judges a snapshot against. They
// are a struct rather than constants so they can be configured and tested
// without a live bot.
type Thresholds struct {
	LowHP     int
	LowHunger int
	// LowAirSeconds is how long the bot's head can be under before it stops
	// what it is doing and goes up. It is in seconds because that is the unit
	// the bot can actually observe: it has no air bar to read, only a count of
	// how long it has been down.
	LowAirSeconds int
}

// IsNightTime reports whether a Bedrock world clock reading is night, using
// the configured boundaries. Both sides of the bot have to agree on when it
// is dark, so the boundaries come from config rather than being repeated here.
func IsNightTime(ticks int64, start, end int64) bool {
	if ticks < 0 {
		return false
	}
	t := ticks % 24000
	return t >= start && t <= end
}

// EvaluateUrgency classifies a snapshot for the reflex layer.
//
// The ordering matters and is the whole point: being in danger outranks being
// hungry, which outranks being idle. Reading it the other way round would let a
// full stomach talk the bot out of running from a creeper.
func EvaluateUrgency(s Snapshot, t Thresholds) Urgency {
	switch {
	case s.HP > 0 && s.HP <= t.LowHP/2:
		return UrgencyCritical
	case s.HP > 0 && s.HP <= t.LowHP:
		return UrgencyHigh
	case s.Hunger > 0 && s.Hunger <= t.LowHunger:
		return UrgencyHigh
	case s.IsNight:
		// Night is not an emergency, but it is a reason to stop wandering.
		return UrgencyLow
	case len(s.Nearby) > 0:
		// Someone is here. A companion acknowledges that; it does not need a
		// reason.
		return UrgencyLow
	default:
		return UrgencyNone
	}
}

// Reflex is one thing the bot should do right now without asking the model.
type Reflex struct {
	Kind ReflexKind
	// Person is set for ReflexLook, so the caller does not have to re-find who
	// triggered it.
	Person string
}

// ReflexKind enumerates the local reactions.
type ReflexKind int

const (
	ReflexNone ReflexKind = iota
	// ReflexEat is hunger-driven.
	ReflexEat
	// ReflexFlee is hurt and something is close.
	ReflexFlee
	// ReflexShelter is nightfall with somewhere to go.
	ReflexShelter
	// ReflexSleep is nightfall and a bed to use.
	ReflexSleep
	// ReflexLook turns the head towards a player who came into view.
	ReflexLook
	// ReflexSurface is drowning. It outranks hunger and every social reflex
	// because the other two are recoverable and this one is not.
	ReflexSurface
)

// String names the reflex for a log line and for the evidence record.
//
// It exists because a reflex that is only identifiable by its integer value
// cannot be looked up later. Every question asked of a past session starts with
// "what happened at 14:32" and the answer has to be a word.
func (k ReflexKind) String() string {
	switch k {
	case ReflexEat:
		return "eat"
	case ReflexFlee:
		return "flee"
	case ReflexShelter:
		return "shelter"
	case ReflexSleep:
		return "sleep"
	case ReflexLook:
		return "look"
	case ReflexSurface:
		return "surface"
	default:
		return "none"
	}
}

// DecideReflex picks at most one reflex to run this tick.
//
// At most one is the important part. Running "eat" and "look" and "flee" in the
// same tick makes the bot do all three at once, which reads as glitchy: a
// character that is eating, turning and running simultaneously is not a person
// multitasking, it is a bot looping over a list.
func DecideReflex(s Snapshot, t Thresholds) Reflex {
	// Drowning is checked before everything else, including the critical health
	// branch. Health only starts falling once the air is nearly gone, so a
	// reflex layer that ranks by health bars will always find out too late. A
	// clock the bot keeps itself is the only warning it actually gets.
	if s.Underwater && s.SecondsUnderwater >= t.LowAirSeconds {
		return Reflex{Kind: ReflexSurface}
	}

	urgency := EvaluateUrgency(s, t)

	// Survival first, and never alongside anything else.
	if urgency == UrgencyCritical {
		return Reflex{Kind: ReflexFlee}
	}
	if s.Hunger > 0 && s.Hunger <= t.LowHunger {
		return Reflex{Kind: ReflexEat}
	}

	// Then people. The nearest not-yet-looked-at player wins, so the bot does
	// not swivel between two of them.
	if person, ok := nearestUnacknowledged(s); ok {
		return Reflex{Kind: ReflexLook, Person: person}
	}

	// Nightfall last among the reflexes, so it never displaces something that
	// saves the bot's life. A bed beats sheltering because sleeping skips the
	// night entirely rather than sitting through it.
	if s.IsNight {
		if s.HasBed {
			return Reflex{Kind: ReflexSleep}
		}
		return Reflex{Kind: ReflexShelter}
	}
	return Reflex{Kind: ReflexNone}
}

// nearestUnacknowledged returns the closest person the bot is not already
// looking at, so a gaze is never re-issued while it is being held.
//
// Only people in actual line of sight qualify. A player standing behind a wall
// is tracked and known about, but turning towards them would be the single most
// obviously wrong thing a "vision" behaviour could do.
func nearestUnacknowledged(s Snapshot) (string, bool) {
	candidates := make([]Person, 0, len(s.Nearby))
	for _, p := range s.Nearby {
		if p.LookingAt || !p.HasLineOf {
			continue
		}
		candidates = append(candidates, p)
	}
	if len(candidates) == 0 {
		return "", false
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Distance == candidates[j].Distance {
			return candidates[i].Name < candidates[j].Name
		}
		return candidates[i].Distance < candidates[j].Distance
	})
	return candidates[0].Name, true
}

// CanSpeak reports whether the bot is allowed to start a conversation right
// now.
//
// Two gates, because each catches a different failure: the cooldown stops
// chatter, and the busy check stops talking over itself.
func CanSpeak(last time.Time, now time.Time, cooldown time.Duration, busy bool) bool {
	if busy {
		return false
	}
	if !last.IsZero() && now.Sub(last) < cooldown {
		return false
	}
	return true
}

// WanderTarget picks a nearby spot to drift toward when the bot has nothing
// better to do.
//
// The heading is derived from a changing seed rather than a fresh random number,
// so consecutive calls keep walking in roughly the same direction. A bot that
// re-rolls its destination every tick jitters on the spot and never gets
// anywhere, which looks far less alive than one that wanders off and settles
// somewhere.
//
// The stride shape is two constants rather than more config: the golden angle is
// a mathematical property of how you walk a circle without clustering, not a
// taste knob, and neither is the sine used to vary the distance.
func WanderTarget(seed int, origin [3]float32, radius float32) (x, y, z float32) {
	const (
		// goldenAngle spreads successive headings evenly around the circle,
		// so a run of targets circles the bot instead of doubling back.
		goldenAngle = 2.399963229728653
		// distanceCycle is how quickly stride length breathes. Small enough
		// that consecutive steps differ, large enough that it reads as a
		// rhythm rather than noise.
		distanceCycle = 0.37
		// minStride and strideSwing bound the distance between a shortest and a
		// longest step, as fractions of the radius.
		minStride   = 0.45
		strideSwing = 0.55
	)

	angle := float64(seed) * goldenAngle
	drift := 0.5 + 0.5*math.Sin(float64(seed)*distanceCycle)
	dist := radius * float32(minStride+strideSwing*drift)

	return origin[0] + float32(math.Cos(angle))*dist,
		origin[1],
		origin[2] + float32(math.Sin(angle))*dist
}

// DescribePeople renders nearby players for a prompt, nearest first, and
// returns "none" rather than an empty string so the model is never left guessing
// whether the list is missing or genuinely empty.
func DescribePeople(people []Person) string {
	if len(people) == 0 {
		return "none"
	}
	sorted := make([]Person, len(people))
	copy(sorted, people)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Distance < sorted[j].Distance })

	parts := make([]string, 0, len(sorted))
	for _, p := range sorted {
		desc := p.Name + " (" + trimFloat(p.Distance) + "m"
		if !p.HasLineOf {
			desc += ", no line of sight"
		}
		if p.SpeakingTo {
			desc += ", talking to you"
		}
		parts = append(parts, desc+")")
	}
	return strings.Join(parts, ", ")
}

func trimFloat(v float32) string {
	s := strconv.FormatFloat(float64(v), 'f', 1, 32)
	return strings.TrimSuffix(s, ".0")
}
