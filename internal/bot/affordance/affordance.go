// Package affordance turns the action registry into something the deciding
// model can actually reason over.
//
// The bot can do 169 things. It could previously choose among about nine. That
// gap is the whole reason it reads as rigid: a model offered "gather or rest"
// cannot express "gather, but put a block between me and the creeper first",
// because that answer has no vocabulary to live in.
//
// So this package does three things, in order:
//
//   - it names every verb, and says whether the verb changes the world
//   - it says whether the verb CONFIRMS against the server, because that is the
//     difference between an action and a wish
//   - it narrows the set to what is legal from where the body actually is
//
// The narrowing is the part that matters. A menu of 169 verbs is a worse menu
// than a menu of nine if every one of them is offered regardless of whether the
// bot is holding the thing, standing next to the thing, or able to reach it.
//
// One rule governs what may be offered, and it is not a matter of taste:
//
//	an unverifiable verb may be offered for a self-changing intent, and never
//	for a world-changing one.
//
// "Look at that" cannot be confirmed and does not need to be — nothing about the
// world moved. "Place a block" can be confirmed (the block appears) and must be,
// because a bot that believes it walled off a creeper it never walled off is a
// bot that will stand in the blast next time.
package affordance

import (
	"sort"
	"strings"
)

// Confirmation is what a verb can prove about the world after running.
type Confirmation int

const (
	// Confirmed means the verb waited for a server-observed change before
	// reporting: the block appeared, the container opened, the item entity
	// left the actor list. This is the only class safe to offer for an action
	// that changes the world.
	Confirmed Confirmation = iota
	// Assumed means the verb reported after writing a packet, without waiting
	// to see the server agree. It works most of the time, which is exactly what
	// makes it dangerous: the failure is invisible until it matters.
	Assumed
	// Unverifiable means there is no observable to wait for. Looking at
	// something, emoting, walking — fine for self-changing intents, never for
	// world-changing ones.
	Unverifiable
)

// String names the class for logs and for the gate's refusal message.
func (c Confirmation) String() string {
	switch c {
	case Confirmed:
		return "confirmed"
	case Assumed:
		return "assumed"
	default:
		return "unverifiable"
	}
}

// Verb is one thing the bot can do, described well enough for a model to choose
// between it and its neighbours.
type Verb struct {
	// Kind says how the verb is carried out, and the distinction is not
	// cosmetic.
	//
	// An Action is a label the action registry resolves. An Activity is something
	// the brain does itself — rest, wander, glance around — and has no registry
	// entry at all. Conflating the two is how the first version of this
	// catalogue came to promise five verbs the registry cannot resolve: the
	// names were right, the mechanism was wrong, and a model offered "wander"
	// would be offered a label that dispatches nowhere.
	Kind Kind
	// Label is what the model names. For an Action it is the registry label; for
	// an Activity it is the brain's own verb.
	Label string
	// Summary is the sentence the model reads. It describes the effect, not the
	// implementation: "put a block in front of you", never "sends an
	// InventoryTransaction".
	Summary string
	// ChangesWorld marks verbs that alter the world or another player's
	// inventory. It is the flag the confirmation gate keys on.
	ChangesWorld bool
	// Confirmation is what the verb can prove.
	Confirmation Confirmation
	// Cost is roughly what one use takes in seconds, so a model can prefer the
	// cheap thing when both are legal. Zero means immediate.
	Cost float32
}

// Kind is how a verb is carried out.
type Kind int

const (
	// Action resolves through the action registry, and it is the zero value on
	// purpose: an entry that forgets its Kind then fails the drift check by name
	// rather than silently offering the model a verb that dispatches nowhere.
	Action Kind = iota
	// Activity is handled by the brain itself and dispatches no registry label.
	Activity
)

// Offerable reports whether this verb may be offered to the model at all.
//
// The judgement is intrinsic to the verb and takes no argument about what the
// caller intends. That is the correction: an earlier version asked whether the
// INTENT changed the world, and therefore refused harmless verbs like "rest"
// during a serious moment while waving through every unconfirmed
// world-changing verb during an idle one — precisely backwards.
//
// A verb that changes the world must be able to prove it changed the world.
// A verb that does not may be offered whatever it can prove, because there is
// nothing for the proof to be about.
func (v Verb) Offerable() bool {
	return !v.ChangesWorld || v.Confirmation == Confirmed
}

// Catalogue is the full set of named verbs.
//
// It is written by hand rather than generated from the registry, because the
// registry knows a label exists and nothing about what it does. Generate it and
// the model is handed 169 names with no descriptions, which is the flat list it
// already had. The drift risk is handled instead by Check, which fails loudly
// when a named verb is not actually registered.
var Catalogue = []Verb{
	// --- Self-changing. Nothing here can be confirmed and nothing here needs
	// to be: the world does not move, so there is nothing to wait for. ---
	{Kind: Activity, Label: "rest", Summary: "stand still and look around for a while", Confirmation: Unverifiable},
	{Kind: Activity, Label: "wander", Summary: "walk somewhere without a particular reason", Confirmation: Unverifiable},
	{Kind: Activity, Label: "explore", Summary: "walk a wide circuit and see what is there", Confirmation: Unverifiable, Cost: 20},
	{Kind: Activity, Label: "approach", Summary: "walk over to a nearby player", Confirmation: Unverifiable},
	{Kind: Activity, Label: "chat", Summary: "say something to a nearby player", Confirmation: Unverifiable},
	{Kind: Activity, Label: "look", Summary: "read a sign or examine something nearby", Confirmation: Unverifiable},
	{Kind: Activity, Label: "gesture", Summary: "wave, nod, or do a little emote", Confirmation: Unverifiable},
	{Label: "follow", Summary: "walk after a player and keep up with them", Confirmation: Unverifiable},
	{Label: "goto", Summary: "walk to coordinates", Confirmation: Unverifiable},
	{Label: "come", Summary: "walk to where a player is standing now", Confirmation: Unverifiable},
	{Label: "flee", Summary: "run away from a threat", Confirmation: Unverifiable},
	// attack is listed as self-changing on purpose, and that is a decision
	// rather than an oversight — see the note below the catalogue.
	{Kind: Activity, Label: "attack", Summary: "hit a hostile mob until it is gone", Confirmation: Unverifiable, Cost: 3},

	// --- World-changing and CONFIRMED. This is the safe core: everything here
	// waits for the server to show the change. These are the verbs the model may
	// always be offered when the world offers them. ---
	{
		Label: "place", Summary: "put a block from your inventory into the world",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "store", Summary: "put items into a chest you are looking at",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "take", Summary: "take items out of a chest you are looking at",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "readsign", Summary: "read what a sign says",
		Confirmation: Confirmed,
	},
	{
		Label: "plant", Summary: "plant seeds on farmland",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "hoe", Summary: "till a block of dirt into farmland",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "harvest", Summary: "pick ripe crops off a field",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "shear", Summary: "shear a sheep for wool",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "tame", Summary: "feed an animal the right food until it is tamed",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 10,
	},
	{
		Label: "feed", Summary: "breed two animals by feeding them",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "give", Summary: "drop an item for a nearby player to pick up",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "loot", Summary: "pick up items lying on the ground",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "smelt", Summary: "smelt ore in a furnace",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 12,
	},
	{
		Label: "mine", Summary: "dig a resource out of the ground",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 15,
	},
	{
		Label: "gather", Summary: "chop a resource you can see",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 15,
	},
	{
		Label: "craft", Summary: "make an item from what you are carrying",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 2,
	},
	{
		Label: "fish", Summary: "cast a line and reel something in",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 20,
	},
	{
		Label: "build", Summary: "put down a structure you have the materials for",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 30,
	},
	{
		Label: "torch", Summary: "put a torch down so a place stays lit",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "undo", Summary: "take back blocks you placed earlier",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 10,
	},
	{
		Label: "storeall", Summary: "put everything spare into a chest",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 5,
	},
	{
		Label: "retrieve", Summary: "take a named item back out of a chest",
		ChangesWorld: true, Confirmation: Confirmed,
	},
	{
		Label: "shelter", Summary: "build a quick shelter where you are standing",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 20,
	},
	{
		Label: "sleep", Summary: "sleep in a bed until morning — it reports at dispatch rather than waiting for dawn, so it is not offered as a world change",
		Confirmation: Unverifiable, Cost: 10,
	},
	{
		Label: "trade", Summary: "trade with a villager",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 5,
	},
	{
		Label: "lightportal", Summary: "light a nether portal",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 25,
	},
	{
		Label: "fillframe", Summary: "fill an end portal frame with eyes of ender",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 20,
	},
	{
		Label: "activateendportal", Summary: "step into a filled end portal and go through",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 10,
	},
	{
		Kind: Activity, Label: "enterendportal",
		Summary:      "go through an end portal",
		ChangesWorld: true, Confirmation: Confirmed, Cost: 10,
	},
	{Label: "scan", Summary: "look around and read what is here", Confirmation: Unverifiable},
	{Label: "clear", Summary: "pick up everything lying on the ground", Confirmation: Unverifiable},
	{Label: "equip", Summary: "hold something from the inventory", Confirmation: Unverifiable},
	{Label: "eat", Summary: "eat something you are carrying", Confirmation: Unverifiable, Cost: 2},
	{Label: "drop", Summary: "put an item on the ground", Confirmation: Unverifiable},
	{Label: "list_craftable", Summary: "list what could be made right now", Confirmation: Unverifiable},
	{Label: "sign", Summary: "write on a sign — the outcome is not verifiable, so it is never offered as a world change", Confirmation: Unverifiable},
	{Label: "press", Summary: "press a button — the outcome is not verifiable, so it is never offered as a world change", Confirmation: Unverifiable},
	{Label: "npc", Summary: "talk to a villager or NPC", Confirmation: Unverifiable},
	{Label: "button", Summary: "use a button or lever — the outcome is not verifiable, so it is never offered as a world change", Confirmation: Unverifiable},
	{Label: "use", Summary: "use the thing you are looking at — the outcome is not verifiable, so it is never offered as a world change", Confirmation: Unverifiable},
	{Label: "click", Summary: "click on something nearby — the outcome is not verifiable, so it is never offered as a world change", Confirmation: Unverifiable},
	{Label: "interact", Summary: "interact with a block or a mob — the outcome is not verifiable, so it is never offered as a world change", Confirmation: Unverifiable},
}

// Note on "attack".
//
// Hitting a mob does change the world — its health, eventually its existence —
// and handleAttack reports success at dispatch without waiting to see damage
// land. By the rule this package states, it could therefore never be offered
// for a world-changing intent, which would take the bot's ability to fight away
// entirely.
//
// Rather than weaken the rule or pretend the attack confirms, combat keeps
// ownership of attacking. It already runs continuously, already watches for
// death, and is driven by the danger reflex rather than by a one-shot decision.
// A verb the model must choose is the wrong shape for a fight: a fight is not
// finished when the swing is sent. Combat becomes an affordance when it can
// prove a kill, and not before.

// byLabel indexes the catalogue for O(1) lookup during derivation.
var byLabel = func() map[string]Verb {
	m := make(map[string]Verb, len(Catalogue))
	for _, v := range Catalogue {
		m[strings.ToLower(v.Label)] = v
	}
	return m
}()

// Lookup returns the named verb, accepting a parameterised label.
//
// The derivation offers "take:oak_log", which is the verb plus the argument the
// handler will actually receive. Splitting it here is what lets the rest of the
// system keep speaking in plain verb names: the gate, the catalogue and the
// drift check all reason about "take", not about a string with a colon in it.
func Lookup(label string) (Verb, bool) {
	raw := strings.ToLower(strings.TrimSpace(label))
	name, param, _ := strings.Cut(raw, ":")
	v, ok := byLabel[name]
	if !ok {
		return Verb{}, false
	}
	if param != "" {
		v.Label = name + ":" + param
	}
	return v, true
}

// SplitVerb separates a parameterised label into the verb and its argument.
func SplitVerb(label string) (name, param string) {
	name, param, _ = strings.Cut(strings.ToLower(strings.TrimSpace(label)), ":")
	return name, param
}

// Set is the derived, situation-filtered offer.
type Set struct {
	// Legal are the verbs that may be offered right now, in the order a model
	// should prefer reading them: cheap and self-changing first, because those
	// are the answers a bot reaches for when it is idle, and expensive
	// world-changing ones last.
	Legal []Verb
	// Withheld records the verbs that exist and would be legal if something
	// about the world were different. It is what turns "the bot did nothing" from
	// a mystery into a sentence: the log says which door was closed.
	Withheld []WithheldVerb
}

// WithheldVerb is a verb that exists but cannot be offered right now.
type WithheldVerb struct {
	Verb
	// Reason is the sentence a log or a prompt renders. It names the missing
	// thing, not the rule: "no chest in reach" beats "prerequisite unmet".
	Reason string
}

// Names renders the legal set as the label list the model chooses from.
func (s Set) Names() []string {
	out := make([]string, 0, len(s.Legal))
	for _, v := range s.Legal {
		out = append(out, v.Label)
	}
	return out
}

// Criteria renders the legal set as the choice map a choice question carries,
// with the effect described in the model's own terms.
func (s Set) Criteria() map[string]string {
	out := make(map[string]string, len(s.Legal))
	for _, v := range s.Legal {
		out[v.Label] = v.Summary
	}
	return out
}

// Describe renders the set for a human reading the log, so a run can be
// reconstructed without reading the code.
func (s Set) Describe() string {
	if len(s.Legal) == 0 {
		return "nothing legal here"
	}
	var sb strings.Builder
	for i, v := range s.Legal {
		if i > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(v.Label)
		if v.ChangesWorld {
			sb.WriteString("(world)")
		} else {
			sb.WriteString("(self)")
		}
	}
	return sb.String()
}

// Gate drops every world-changing verb that cannot be confirmed, and says why.
//
// It is exported because the refusal has to be testable on its own: a gate that
// is only reachable through the prompt layer cannot be shown to refuse.
func Gate(in []Verb) (kept []Verb, refused []Verb) {
	for _, v := range in {
		if v.Offerable() {
			kept = append(kept, v)
			continue
		}
		refused = append(refused, v)
	}
	return kept, refused
}

// sortForPresentation orders a set: self-changing before world-changing, then
// cheap before expensive, then alphabetical so a run is comparable to the last.
func sortForPresentation(vs []Verb) {
	sort.SliceStable(vs, func(i, j int) bool {
		a, b := vs[i], vs[j]
		if a.ChangesWorld != b.ChangesWorld {
			return !a.ChangesWorld
		}
		if a.Cost != b.Cost {
			return a.Cost < b.Cost
		}
		return a.Label < b.Label
	})
}
