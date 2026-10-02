// Risk appetite: the dial that separates a careful bot from a ridiculous one.
//
// The complaint that started this was that the bot is rigid. A switch on mob
// name is rigid by construction: the same creeper at four blocks produces the
// same panic every time, because the decision is made by a lookup rather than by
// anybody.
//
// What separates a player is not better information. It is that a player has a
// disposition, and the disposition is visible in small decisions: whether they
// walk past a creeper or back off, whether they jump a gap they could walk
// around, whether they let the explosion happen because it would be funny.
//
// So the disposition is a state the model moves, not a constant compiled into a
// switch. Serious and silly are the two ends, and a bot that only ever sits at
// one end is not being careful — it is being a machine, because a machine does
// not change its mind about a creeper at all.
package affordance

import (
	"strings"

	"bedrock-ai/internal/jev"
)

// Appetite is how much risk the bot is willing to take on purpose.
type Appetite int

const (
	// Careful refuses risk it does not have to take. It walks around a cliff,
	// backs off a creeper early, and does not try anything it has not done
	// before. This is the default because it is the one that cannot get the
	// player killed while the model is learning what it likes.
	Careful Appetite = iota
	// Bold takes the obvious line when the alternative is obviously worse, and
	// is willing to eat a little damage for it.
	Bold
	// Reckless will do something that might hurt on purpose, because the point
	// is sometimes the thing that happens rather than the thing that is
	// achieved. It is never offered for anything the bot cannot survive.
	Reckless
)

// String names the level for a log line.
func (a Appetite) String() string {
	switch a {
	case Bold:
		return "bold"
	case Reckless:
		return "reckless"
	default:
		return "careful"
	}
}

// ParseAppetite reads a model answer.
//
// Anything unrecognised is Careful rather than an error, for the same reason the
// locomotion parser defaults to auto: a model answering "" or something invented
// must not change how the bot behaves. The cautious end is the safe default
// because the alternative failure is a bot experimenting with its own death.
func ParseAppetite(raw string) Appetite {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case jev.RiskBold:
		return Bold
	case jev.RiskReckless:
		return Reckless
	default:
		return Careful
	}
}

// DecideSurvival is the question a player answers without thinking: the threat
// is here, so what does the body do about it right now.
//
// It is a pure function on purpose. Everything that decides what to do about a
// creeper lives in combat/tactics.go as hardcoded distances, and that is exactly
// the rigidity being complained about: those distances never move. Giving the
// same numbers to all three temperaments would not help — it would just make
// the complaint more precise.
//
// So the distances move with the disposition, and the disposition moves with the
// model. A careful bot leaves early. A reckless one holds its ground, because a
// creeper is not a real threat and the interesting thing is what happens.
type SurvivalPlan struct {
	// Flee ends the encounter and runs.
	Flee bool
	// SafeDistance is how far the body tries to get.
	SafeDistance float32
	// BlockUp is whether to put a block between the body and the blast. It is
	// the answer to the case this whole file exists for: the composed defence,
	// not the retreat.
	BlockUp bool
	// HoldGround keeps fighting instead of backing off. Reckless only.
	HoldGround bool
}

// CreeperPlan answers "there is a creeper at this distance, what now".
//
// The distances are where the disposition shows. A careful bot treats three
// blocks as too close because the blast reaches three blocks; a reckless one
// treats four as an invitation, because a creeper that has not started hissing
// is not going to.
func CreeperPlan(dist float32, appetite Appetite) SurvivalPlan {
	switch appetite {
	case Reckless:
		// A creeper is a toy. Stand there and let it go off, or blow it up
		// first, depending on which reads better — but either way, do not run.
		return SurvivalPlan{
			SafeDistance: 0,
			HoldGround:   true,
		}
	case Bold:
		// Close enough to fight it, far enough not to be inside the blast.
		if dist < 2.5 {
			return SurvivalPlan{Flee: true, SafeDistance: 6, BlockUp: true}
		}
		return SurvivalPlan{SafeDistance: 2}
	default:
		if dist < 3.0 {
			return SurvivalPlan{Flee: true, SafeDistance: 6, BlockUp: true}
		}
		return SurvivalPlan{SafeDistance: 4}
	}
}
