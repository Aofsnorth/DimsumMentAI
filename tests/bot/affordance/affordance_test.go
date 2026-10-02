package affordance_test

import (
	"strings"
	"testing"

	"bedrock-ai/internal/bot/affordance"
)

// The rule the whole layer exists to enforce:
//
//	an unverifiable verb may be offered for a self-changing intent, and never
//	for a world-changing one.
//
// Everything else in this package is convenience. This is the safety property,
// and it is worth stating as a test because the failure it prevents is the one
// the roadmap calls the project's #1 gap: an action reported successful that
// never happened.

// TestNoWorldChangingVerbIsOfferedWithoutConfirmation is the invariant, stated
// as a property over the whole catalogue rather than over a handful of cases.
// A catalogue that grows later must still satisfy it, which is why this walks
// every entry instead of naming a few.
func TestNoWorldChangingVerbIsOfferedWithoutConfirmation(t *testing.T) {
	t.Parallel()

	for _, v := range affordance.Catalogue {
		if !v.ChangesWorld {
			continue
		}
		if v.Confirmation != affordance.Confirmed {
			t.Errorf("verb %q changes the world but is only %v; it must never be offered for a world-changing intent",
				v.Label, v.Confirmation)
		}
	}
}

// TestAWorldChangingVerbIsRefusedWhenUnconfirmed is the gate itself, exercised
// directly so it can be shown to refuse rather than merely described.
func TestAWorldChangingVerbIsRefusedWhenUnconfirmed(t *testing.T) {
	t.Parallel()

	hopeful := affordance.Verb{
		Label: "teleportfake", Summary: "pretend to teleport",
		ChangesWorld: true, Confirmation: affordance.Assumed,
	}
	quiet := affordance.Verb{
		Label: "rest", Summary: "stand still",
		ChangesWorld: false, Confirmation: affordance.Unverifiable,
	}

	kept, refused := affordance.Gate([]affordance.Verb{hopeful, quiet})
	if len(kept) != 1 || kept[0].Label != "rest" {
		t.Errorf("kept = %v, want only the self-changing verb", kept)
	}
	if len(refused) != 1 || refused[0].Label != "teleportfake" {
		t.Errorf("refused = %v, want the world-changing one", refused)
	}
}

// TestTheSameVerbIsAllowedForASelfChangingIntent is the other half, and it
// matters: a gate that refuses everything makes a bot that never acts, which is
// worse than the optimism it replaced.
func TestTheSameVerbIsAllowedForASelfChangingIntent(t *testing.T) {
	t.Parallel()

	hopeful := affordance.Verb{
		Label: "moonwalk", Summary: "slide backwards",
		ChangesWorld: false, Confirmation: affordance.Unverifiable,
	}

	kept, refused := affordance.Gate([]affordance.Verb{hopeful})
	if len(kept) != 1 {
		t.Errorf("a self-changing verb was refused: kept=%v refused=%v", kept, refused)
	}
}

// testWorld is a described world. It exists so the derivation can be tested
// against a situation rather than against a live server, which is the only way
// to know a rule is right instead of merely uncrashed.
type testWorld struct {
	water, container, stackable, tool, chest bool
	freeSlots                                int
	materials                                map[string]bool
	containerItems                           []string
	slotItems                                []string
	craftable                                int
}

func (w testWorld) CanSeeWater() bool            { return w.water }
func (w testWorld) FacingContainer() bool        { return w.container }
func (w testWorld) HoldingStackable() bool       { return w.stackable }
func (w testWorld) HoldingTool() bool            { return w.tool }
func (w testWorld) NearChest() bool              { return w.chest }
func (w testWorld) FreeSlots() int               { return w.freeSlots }
func (w testWorld) HasMaterial(name string) bool { return w.materials[name] }
func (w testWorld) ContainerContents() []string  { return w.containerItems }
func (w testWorld) SlotContents() []string       { return w.slotItems }

func (w testWorld) Snapshot() affordance.Facts {
	return affordance.Facts{FreeSlots: w.freeSlots, Craftable: w.craftable}
}

// world builds a world where most things are legal, so a test can turn off the
// one thing it is about.
func world() testWorld {
	return testWorld{water: true, container: true, stackable: true, tool: true, chest: true, freeSlots: 20}
}

// TestTheIdleOffersAreAvailableAnywhere is the control. A bot with nothing to do
// must still be able to do nothing gracefully.
func TestTheIdleOffersAreAvailableAnywhere(t *testing.T) {
	t.Parallel()

	set := affordance.Derive(world(), false)
	have := map[string]bool{}
	for _, name := range set.Names() {
		have[name] = true
	}
	for _, want := range []string{"rest", "wander", "explore"} {
		if !have[want] {
			t.Errorf("%q is not offered in an ordinary world; a bot with nothing to do must still be able to idle", want)
		}
	}
}

// TestGatheringIsNotOfferedWithBareHands is the first real rule. Offering "chop a
// tree" to a bot holding nothing produces a bot that swings at a tree with its
// fist and then reports it as a gather failure.
func TestGatheringIsNotOfferedWithBareHands(t *testing.T) {
	t.Parallel()

	w := world()
	w.tool = false

	set := affordance.Derive(w, false)
	for _, name := range set.Names() {
		if name == "gather" || name == "mine" {
			t.Errorf("%q offered with no axe or pickaxe in hand", name)
		}
	}

	// And the refusal has to say why, because a bot that silently declines to
	// act leaves the operator with nothing.
	var found bool
	for _, withheld := range set.Withheld {
		if withheld.Label == "gather" {
			found = true
			if withheld.Reason == "" {
				t.Error("the withheld gather carries no reason")
			}
		}
	}
	if !found {
		t.Error("gather was neither offered nor recorded as withheld: the set does not account for it")
	}
}

// TestPlacingIsNotOfferedWithEmptyHands is the same rule for the verb that
// matters most for the creeper example. A bot that believes it can place a block
// and cannot is a bot that will stand in the blast next time.
func TestPlacingIsNotOfferedWithEmptyHands(t *testing.T) {
	t.Parallel()

	w := world()
	w.stackable = false

	set := affordance.Derive(w, true)
	for _, name := range set.Names() {
		if name == "place" {
			t.Error("\"place\" offered with nothing to place: the bot would promise a wall it cannot build")
		}
	}
}

// TestFishingIsNotOfferedAwayFromWater covers the rule the curriculum already
// applied at activity level, now at verb level.
func TestFishingIsNotOfferedAwayFromWater(t *testing.T) {
	t.Parallel()

	w := world()
	w.water = false

	set := affordance.Derive(w, false)
	for _, name := range set.Names() {
		if name == "fish" {
			t.Error("\"fish\" offered with no water in sight")
		}
	}
}

// TestAWorldChangingIntentNeverOffersAnUnconfirmedVerb walks the whole
// derivation rather than the gate in isolation, because the two could disagree:
// the gate is right and the caller passes the wrong intent, and only an
// end-to-end check over a real derived set catches that.
func TestAWorldChangingIntentNeverOffersAnUnconfirmedVerb(t *testing.T) {
	t.Parallel()

	for _, name := range affordance.Derive(world(), true).Names() {
		v, ok := affordance.Lookup(name)
		if !ok {
			t.Errorf("derived set offered %q, which is not in the catalogue; the two have drifted", name)
			continue
		}
		if v.ChangesWorld && v.Confirmation != affordance.Confirmed {
			t.Errorf("a world-changing intent was offered %q, which is only %v", name, v.Confirmation)
		}
	}
}

// TestTheOfferedSetAccountsForEveryCatalogueEntry closes the set: everything is
// either offered or withheld with a reason. A verb that vanishes from both is a
// bug that reads as "the bot decided not to" and cannot be told apart from one.
func TestTheOfferedSetAccountsForEveryCatalogueEntry(t *testing.T) {
	t.Parallel()

	set := affordance.Derive(world(), false)
	if len(set.Legal)+len(set.Withheld) != len(affordance.Catalogue) {
		t.Errorf("legal %d + withheld %d = %d, catalogue has %d: entries went missing",
			len(set.Legal), len(set.Withheld), len(set.Legal)+len(set.Withheld), len(affordance.Catalogue))
	}
	for _, withheld := range set.Withheld {
		if withheld.Reason == "" {
			t.Errorf("%q was withheld with no reason", withheld.Label)
		}
	}
}

// TestAnUndescribedWorldStillOffersTheSafeHalf keeps the bot from freezing when
// the world is not available — reconnect, a chunk that has not arrived, a test
// that did not bother to describe anything.
func TestAnUndescribedWorldStillOffersTheSafeHalf(t *testing.T) {
	t.Parallel()

	set := affordance.Derive(nil, false)
	if len(set.Legal) == 0 {
		t.Fatal("an undescribed world offered nothing at all: the bot would stand still forever")
	}
	for _, v := range set.Legal {
		if v.ChangesWorld {
			t.Errorf("%q was offered for a world-changing action with no world described", v.Label)
		}
	}
}

// Stage 3: a verb with no argument is a decision the body has to guess at.
//
// "take" tells the model it may take something; it does not say what, so the
// handler picks. That is the last place the old fixed-menu rigidity survives —
// the model chose, and then chose again inside a handler it cannot see.

// TestAVerbIsOfferedWithWhatItWouldActuallyDo is the point of parameterising.
func TestAVerbIsOfferedWithWhatItWouldActuallyDo(t *testing.T) {
	t.Parallel()

	w := world()
	w.containerItems = []string{"oak_log", "stone"}

	var take string
	for _, name := range affordance.Derive(w, false).Names() {
		if strings.HasPrefix(name, "take:") {
			take = name
		}
	}
	if take == "" {
		t.Fatal("\"take\" offered with no argument while a chest is in reach: " +
			"the model chose an action and the handler guessed its content")
	}
	if !strings.Contains(take, "oak_log") {
		t.Errorf("take option %q does not name what is actually in the chest", take)
	}
}

// TestAVerbIsNotGivenAnArgumentTheWorldCannotSupply is the other direction. An
// argument the bot cannot see is worse than none: the handler reads it as a real
// instruction and the model reads it as a real choice.
func TestAVerbIsNotGivenAnArgumentTheWorldCannotSupply(t *testing.T) {
	t.Parallel()

	w := world()
	w.chest = false
	w.container = false

	for _, name := range affordance.Derive(w, false).Names() {
		if strings.HasPrefix(name, "take:") || strings.HasPrefix(name, "store:") {
			t.Errorf("%q carries an argument with nothing to take it from", name)
		}
	}
}

// TestTheCatalogueStillAnswersToBareVerbs keeps the two vocabularies talking.
//
// A parameterised label must still resolve to its verb, because the gate, the
// catalogue and the drift check all reason about "take" rather than about a
// string with a colon in it.
func TestTheCatalogueStillAnswersToBareVerbs(t *testing.T) {
	t.Parallel()

	v, ok := affordance.Lookup("take:oak_log")
	if !ok {
		t.Fatal("a parameterised label does not resolve to its verb")
	}
	if v.ChangesWorld != true || v.Confirmation != affordance.Confirmed {
		t.Errorf("take:oak_log = %+v, want the same classification as bare \"take\"", v)
	}

	name, param := affordance.SplitVerb("take:oak_log")
	if name != "take" || param != "oak_log" {
		t.Errorf("SplitVerb = (%q,%q), want (take, oak_log)", name, param)
	}
}

// TestAnInventedVerbWithAnArgumentIsStillRefused. Adding a parameter does not
// make the catalogue any more permissive: the gate keys on the verb, so
// "detonate:creeper" is as refused as "detonate".
func TestAnInventedVerbWithAnArgumentIsStillRefused(t *testing.T) {
	t.Parallel()

	if _, ok := affordance.Lookup("detonate:creeper"); ok {
		t.Error("an invented verb with an argument resolved to a real verb")
	}
}

// Stage 2: a verb is offered because the bot's own perception says so.

// TestCraftIsNotOfferedWhenNothingCanBeMade is the hole this stage closed.
//
// The derivation only ever asked whether there was room in the inventory. A bot
// holding nothing, standing in front of no workbench, was offered "craft" every
// tick — and the handler then failed on an action nobody should have been
// offered. The snapshot has counted craftable recipes all along; nobody read it.
func TestCraftIsNotOfferedWhenNothingCanBeMade(t *testing.T) {
	t.Parallel()

	w := world()
	w.craftable = 0
	w.freeSlots = 27

	if got := affordance.Derive(w, false).Names(); contains(got, "craft") {
		t.Errorf("craft offered with nothing craftable and %d free slots: %v", w.freeSlots, got)
	}
}

// TestCraftIsOfferedWhenSomethingCanBeMade is the other direction, so the rule
// cannot pass by refusing everything.
func TestCraftIsOfferedWhenSomethingCanBeMade(t *testing.T) {
	t.Parallel()

	w := world()
	w.craftable = 3

	if got := affordance.Derive(w, false).Names(); !contains(got, "craft") {
		t.Errorf("craft refused with 3 recipes in reach: %v", got)
	}
}

// TestFullInventoryStillRefusesCraft. Room was the only half of the rule that
// used to work, and it has to keep working.
func TestFullInventoryStillRefusesCraft(t *testing.T) {
	t.Parallel()

	w := world()
	w.craftable = 3
	w.freeSlots = 0

	if got := affordance.Derive(w, false).Names(); contains(got, "craft") {
		t.Errorf("craft offered with a full inventory and 3 recipes: %v", got)
	}
}

// TestTheRefusalSaysWhy. A verb that vanishes silently is a bug report with no
// information in it; the reason is what makes it debuggable from a live log.
func TestTheRefusalSaysWhy(t *testing.T) {
	t.Parallel()

	w := world()
	w.craftable = 0
	w.freeSlots = 27

	var reason string
	for _, held := range affordance.Derive(w, false).Withheld {
		if held.Label == "craft" {
			reason = held.Reason
		}
	}
	if reason == "" {
		t.Error("craft was withheld with no stated reason: a verb that vanishes " +
			"silently is a bug report with no information in it")
	}
	if strings.Contains(reason, "unmet") || !strings.Contains(reason, "make") {
		t.Errorf("refusal %q does not name the missing thing", reason)
	}
}

func contains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
