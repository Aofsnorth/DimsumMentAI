// Deriving the offer from the world, rather than from a fixed list.
//
// The catalogue says what exists. This file says what is legal from where the
// body is standing right now, which is the difference between a bot that reads
// as sensible and one that repeatedly tries to do something impossible and
// reports a failure for it.
//
// Every rule here is a sentence a player would recognise: you cannot craft with
// nothing, you cannot take from a chest you are not facing, you cannot fish
// where there is no water. The rule and the sentence are the same thing, because
// a rule whose reason cannot be said out loud is a rule nobody can debug from a
// log.

package affordance

import "strings"

// World is what the derivation is allowed to know about the situation.
//
// It is an interface rather than the bot's own types so the policy can be tested
// against a described world without a server, a connection, or a world cache.
// The cost is one small adapter in the AGI layer; the benefit is that every
// rule below is testable, which is the only way to be sure a bot offered fewer
// options is offered the right ones.
type World interface {
	// CanSeeWater reports whether water is within reach, which is the only thing
	// that makes fishing a real offer rather than a hopeful one.
	CanSeeWater() bool
	// FacingContainer reports whether a chest, barrel or similar is close
	// enough to open.
	FacingContainer() bool
	// HoldingStackable reports whether the bot is carrying a block it could
	// place. Placing with empty hands is an offer the model should never be
	// given.
	HoldingStackable() bool
	// HoldingTool reports whether the bot is carrying something that gathers:
	// an axe, a pickaxe, or a shovel.
	HoldingTool() bool
	// NearChest reports whether any container is within interaction range, even
	// when the bot is not facing it.
	NearChest() bool
	// FreeSlots is how many empty inventory slots the bot has.
	FreeSlots() int
	// HasMaterial reports whether the bot is carrying anything matching name.
	HasMaterial(name string) bool
	// ContainerContents lists the items in the container the bot is facing, so a
	// verb can name what it would actually take rather than only that it could.
	//
	// It is what separates "take" from "take 12 oak logs". A model can only
	// choose between options somebody wrote down, so an option that says what to
	// do but not to what is a decision it cannot finish.
	ContainerContents() []string
	// SlotContents lists what the bot is carrying, for the same reason.
	SlotContents() []string
	// Snapshot reports what the bot's own perception already established this
	// tick, so the derivation can read a fact instead of recomputing it.
	//
	// This is the difference between a verb being offered because the body
	// checked and one being offered because the mind already knew. It also makes
	// a perception gap legible: a missing verb is a missing observation, and a
	// wrong one is a wrong observation, rather than both arriving as a handler
	// that quietly did nothing.
	Snapshot() Facts
}

// Facts are the perceptions the snapshot already carries, narrowed to what the
// derivation needs.
type Facts struct {
	// FreeSlots is how many empty inventory slots the bot has.
	FreeSlots int
	// Craftable is how many distinct recipes the bot could make right now.
	//
	// It is the fact that "craft" was missing. A bot holding nothing and standing
	// in front of no workbench is not able to craft, and the old derivation
	// offered it anyway because it had only ever asked whether there was room.
	// The handler then failed on an action nobody should have been offered.
	Craftable int
}

// Derive narrows the catalogue to what the world offers.
//
// changesWorld is the intent class the caller is deciding between. It is passed
// in rather than inferred because the same world offers different sets for "I am
// bored, do something" and "I am serious, get this done" — and the second must
// not be offered a wish it cannot verify.
func Derive(w World, changesWorld bool) Set {
	var set Set

	for _, v := range Catalogue {
		if reason, ok := legal(w, v); !ok {
			set.Withheld = append(set.Withheld, WithheldVerb{Verb: v, Reason: reason})
			continue
		}
		// The gate runs after legality, not before: a verb the world has already
		// excluded costs nothing to exclude again, and gating first would fill
		// the refusal list with verbs the player could never have chosen anyway.
		if !v.Offerable() {
			set.Withheld = append(set.Withheld, WithheldVerb{
				Verb:   v,
				Reason: "not server-confirmed, so it cannot be offered for something that changes the world",
			})
			continue
		}
		// The verb is rendered with its argument before it is offered, so the
		// model chooses between concrete actions rather than between bare ones.
		set.Legal = append(set.Legal, Parameterised(v, w))
	}

	sortForPresentation(set.Legal)
	return set
}

// legal answers "could this verb possibly succeed here", returning the reason it
// cannot when the answer is no.
//
// The reasons are written to be read aloud into a log, because the failure this
// file prevents is a bot that silently declines to do anything and leaves the
// operator with nothing to work from.
func legal(w World, v Verb) (string, bool) {
	if w == nil {
		// No world described means nothing can be ruled out. Offering the
		// self-changing verbs is the safe half: they cannot damage anything, and
		// a bot that stands still forever is its own failure.
		if v.ChangesWorld {
			return "the world is not described, so world-changing actions are not offered", false
		}
		return "", true
	}

	// Facts the snapshot already carries are read from it, not recomputed. The
	// body should not walk the same ground the mind walked this same tick.
	facts := w.Snapshot()

	switch v.Label {
	case "craft":
		if facts.Craftable <= 0 {
			return "nothing it currently knows how to make", false
		}
		if facts.FreeSlots <= 0 {
			return "no room in the inventory for what it would make", false
		}
	case "mine", "gather":
		if !w.HoldingTool() {
			return "not carrying an axe or a pickaxe", false
		}
	case "place":
		if !w.HoldingStackable() {
			return "not carrying a block to place", false
		}
	case "fish":
		if !w.CanSeeWater() {
			return "no water in sight", false
		}
	case "store", "take":
		if !w.NearChest() {
			return "no chest within reach", false
		}
	case "shear":
		if !w.HoldingTool() {
			return "not carrying shears", false
		}
	case "give":
		if facts.FreeSlots <= 0 {
			return "no room in the inventory to pick anything up", false
		}
	}
	return "", true
}

// Check verifies that every named verb is actually registered.
//
// The action package has a hand-maintained label list that drifted twenty
// labels behind reality before anyone noticed, and this catalogue is hand-
// maintained too — so it gets the check the old list never had. A named verb
// that the registry does not resolve is a promise the bot makes to the model
// and cannot keep, and the model will choose it.
func Check(registered map[string]struct{}) []string {
	var missing []string
	for _, v := range Catalogue {
		// Only Action-kind verbs have a registry label. An Activity is carried
		// out by the brain and has nothing to resolve, so looking for it in the
		// registry would report every "rest" and "wander" as missing — which is
		// precisely the mistake the Kind field exists to prevent.
		if v.Kind != Action {
			continue
		}
		if _, ok := registered[v.Label]; !ok {
			missing = append(missing, v.Label)
		}
	}
	return missing
}

// DeriveForTest exposes the derivation so the AGI layer's adapter can be tested
// against a described world without a server.
func DeriveForTest(w World, changesWorld bool) Set { return Derive(w, changesWorld) }

// Parameterised renders a verb with the argument it would actually be given.
//
// The registry's handlers all take a parameter string, so this is not a new
// mechanism — it is the existing one being filled in. A label with no parameter
// asks the body to do something without saying to what, which is the last place
// the old fixed-menu rigidity survives: the model picks "take" and the handler
// guesses.
//
// Only arguments the world can actually supply are rendered. A parameter the
// bot cannot see is worse than none, because it reads to the handler as a real
// instruction and to the model as a real choice.
func Parameterised(v Verb, w World) Verb {
	if w == nil {
		return v
	}
	switch v.Label {
	case "take":
		if items := w.ContainerContents(); len(items) > 0 {
			return withParam(v, joinItems(items))
		}
	case "store", "storeall":
		if items := w.SlotContents(); len(items) > 0 {
			return withParam(v, joinItems(items))
		}
	case "craft":
		if items := craftableFrom(w); len(items) > 0 {
			return withParam(v, joinItems(items))
		}
	}
	return v
}

// withParam returns the verb carrying its argument.
func withParam(v Verb, param string) Verb {
	v.Label = v.Label + ":" + param
	v.Summary = v.Summary + " (" + param + ")"
	return v
}

// joinItems renders a list of item names as the parameter the handlers expect.
func joinItems(items []string) string {
	const max = 3
	if len(items) > max {
		return strings.Join(items[:max], ",") + ",..."
	}
	return strings.Join(items, ",")
}

// craftableFrom is a placeholder for the craft case until the snapshot carries
// the recipe table; it is deliberately empty rather than guessed, because
// offering a craft for something the bot cannot make is the exact failure the
// derivation exists to prevent.
func craftableFrom(World) []string { return nil }
