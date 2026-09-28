package jev

import (
	"encoding/json"
	"fmt"
)

// The questions the bot asks Jev.
//
// These are written as decisions rather than commands on purpose. "Should I
// run?" is a judgement the model can weigh against everything in the state at
// once; "set isFleeing = true" is not something a model can answer, only a
// program can. Keeping the wording as a genuine question is what lets Jev do
// the work the hardcoded thresholds currently cannot.

// Question names, referenced when reading answers back.
const (
	QDanger   = "danger"
	QHungry   = "hungry"
	QActivity = "activity"
	QWorthSay = "worth_speaking"
	// QEngagement asks how absorbed the bot should be right now. It is the
	// dial behind every choice question: a bored bot should do something
	// visible, a busy one should stay out of the way, and without this the
	// activity menu can only ever answer "what could it do", never "should it
	// bother".
	QEngagement = "engagement"
	// QSmallTalk asks whether the moment is one where a short remark fits
	// rather than a conversation. It is separate from QWorthSay on purpose:
	// "worth starting a conversation" and "worth saying anything at all" are
	// very different bars, and collapsing them is what produces a bot that
	// either never speaks or talks constantly.
	QSmallTalk = "small_talk"
	// QMining asks whether the bot should go after a resource it can see, which
	// is the decision that keeps a survival loop from being only wandering.
	QMining = "worth_mining"
	// QGoal asks which objective the bot should be working towards across
	// several ticks. It is the one question that is not about right now: a
	// goal outlives the tick that chose it, and answering it is what gives the
	// bot continuity rather than a fresh coin flip every thirty seconds.
	QGoal = "goal"
)

// Activity options offered to the model. Kept short on purpose: Jev is a
// classifier, and a long menu of near-identical options produces mushy
// distributions rather than a clear pick.
const (
	ActivityRest     = "rest"
	ActivityWander   = "wander"
	ActivityGather   = "gather"
	ActivityExplore  = "explore"
	ActivityApproach = "approach"
	ActivityShelter  = "shelter"
	ActivitySleep    = "sleep"
	// ActivityChat walks over to the nearest player. It is distinct from
	// approach on purpose: standing near someone and starting a conversation
	// are separate intents, and a bot that conflates them stands next to a
	// player in silence, which reads as a laggy client rather than a
	// companion.
	ActivityChat = "chat"
	// ActivityGesture plays a short emote. Emoting at nothing in particular is
	// something people do constantly, and it is the cheapest way for a bot to
	// register as idle-but-present instead of frozen.
	ActivityGesture = "gesture"
	// ActivityMine commits to actually extracting a resource it can see. This
	// is the option that was missing: without a work activity, an "autonomous"
	// bot only ever strolls around a field and never changes state, which is
	// the difference between a companion and a screensaver.
	ActivityMine = "mine"
	// ActivityLook reads the signage around the bot. It is the decision that
	// lets a labelled storage room be used rather than blindly searched.
	ActivityLook = "look_around"
	// ActivityFish fishes, and is only ever offered when there is water in
	// sight. A bot that casts a fishing rod at a tree because nobody told it
	// there was no water looks broken in a way that is obvious to a player.
	ActivityFish = "fish"
	// ActivityHarvest collects ripe crops. Offered only when there are ripe
	// ones: harvesting seedlings destroys the field and yields nothing, which
	// is strictly worse than not farming.
	ActivityHarvest = "harvest"
	// ActivityTendAnimals feeds or herds the animals in view.
	ActivityTendAnimals = "tend_animals"
	// ActivityCraft makes something the bot already has the ingredients for.
	// Offered only when it genuinely can, so the bot does not repeatedly choose
	// a recipe it has no materials for.
	ActivityCraft = "craft"
)

// MustNoul builds a noul question as its raw JSON, for a caller assembling a
// batch by hand.
//
// It exists so the AGI layer can ask something of its own without a second
// marshalling path, and without exporting the whole map builder. A model that
// lives in a different package should not have to reimplement the wire shape to
// add one question.
func MustNoul(instructions string) json.RawMessage {
	return mustMarshal(NoulQuestion{Type: TypeNoul, Instructions: instructions})
}

// BuildReflexQuestions asks the survival and pacing judgements in one call.
// They travel together because Jev answers a whole set in a single parallel
// pass — splitting them would cost extra round trips for no benefit.
func BuildReflexQuestions() map[string]json.RawMessage {
	questions := map[string]json.RawMessage{
		QDanger: mustMarshal(NoulQuestion{
			Type:         TypeNoul,
			Instructions: "Is the bot in immediate danger right now — a hostile mob close by, badly hurt, or standing somewhere it could die? Answer yes only if acting now would clearly be safer than doing nothing.",
		}),
		QHungry: mustMarshal(NoulQuestion{
			Type:         TypeNoul,
			Instructions: "Is the bot hungry enough that eating would be the natural next thing a player would do?",
		}),
		QWorthSay: mustMarshal(NoulQuestion{
			Type:         TypeNoul,
			Instructions: "Is this a natural moment to start a conversation nobody asked for? Answer no unless there is a specific reason to speak now — silence is usually the right choice.",
		}),
		QEngagement: mustMarshal(NoulQuestion{
			Type:         TypeNoul,
			Instructions: "How much should the bot be doing something right now? Answer yes if a person in this situation would probably be occupied — mining, building, or working through something. Answer no if they would more likely be standing about, and something worth doing is available.",
		}),
		QSmallTalk: mustMarshal(NoulQuestion{
			Type:         TypeNoul,
			Instructions: "Would a short remark to someone nearby be natural here, as in a greeting or a passing comment? This is a lower bar than starting a real conversation, but it still needs a reason — silence is usually right.",
		}),
		QMining: mustMarshal(NoulQuestion{
			Type:         TypeNoul,
			Instructions: "Is it worth starting to gather a resource it can see right now, judging by whether it looks reachable, useful, and safe to stop for?",
		}),
	}
	return questions
}

// Goal options. These are larger than activities: a goal spans many ticks and
// narrows the activity menu to whatever advances it, which is what turns a
// sequence of reactions into something with intent.
const (
	GoalIdle         = "idle"
	GoalStockUp      = "stock_up"
	GoalGatherWood   = "gather_wood"
	GoalFindStorage  = "find_storage"
	GoalExplore      = "explore"
	GoalBuildShelter = "build_shelter"
	GoalSocialise    = "socialise"
)

// BuildGoalQuestion asks which objective the bot should pursue.
//
// The options are the goals that are actually available in the current world,
// so a model is never asked to pursue something that cannot happen. The
// instructions name the current goal when there is one, because a bot that has
// already committed to something should be nudged to continue it rather than
// flip-flopping every tick.
func BuildGoalQuestion(goals []string, descriptions map[string]string, current string) map[string]json.RawMessage {
	criteria := make(map[string]string, len(goals))
	for _, name := range goals {
		if desc, ok := descriptions[name]; ok {
			criteria[name] = desc
		}
	}
	// Idle is never filtered out. A goal set with no way to stop guarantees the
	// bot is always busy, which is the exact failure this integration avoids.
	if _, ok := criteria[GoalIdle]; !ok {
		criteria[GoalIdle] = "have nothing pressing to do, so simply be here"
	}

	instructions := "What should the bot be working towards over the next several minutes? " +
		"Prefer a goal that is actually available and reachable, not an exciting one that cannot happen. " +
		"Answer the same way twice in a row when the first answer is still a sensible thing to be doing."
	if current != "" {
		instructions += fmt.Sprintf(
			"The bot is currently working towards %q. Prefer to continue it unless it has clearly stopped making sense.",
			current)
	}

	return map[string]json.RawMessage{
		QGoal: mustMarshal(ChoiceQuestion{
			Type:         TypeChoice,
			Instructions: instructions,
			Criteria:     criteria,
		}),
	}
}

// BuildActivityQuestion asks what to do with itself.
//
// The options are curriculum, not a fixed menu — Voyager's first requirement for
// a lifelong agent: propose tasks that suit the world it is actually standing
// in. Offering "mine iron ore" in a desert, or "find a bed" while already
// carrying one, spends a decision on something that cannot succeed. What reaches
// the model is what is plausible right now, and "rest" is always offered so
// silence stays available even when the world looks busy.
func BuildActivityQuestion(curriculum []string) map[string]json.RawMessage {
	if len(curriculum) == 0 {
		curriculum = []string{ActivityRest, ActivityWander}
	}
	criteria := make(map[string]string, len(curriculum)+1)
	for _, name := range curriculum {
		if desc, ok := activityDescriptions[name]; ok {
			criteria[name] = desc
		}
	}
	// Always keep rest reachable. A curriculum that never offers "do nothing"
	// is a curriculum that always does something, which is the exact failure
	// mode this package exists to avoid.
	if _, ok := criteria[ActivityRest]; !ok {
		criteria[ActivityRest] = activityDescriptions[ActivityRest]
	}

	return map[string]json.RawMessage{
		QActivity: mustMarshal(ChoiceQuestion{
			Type:         TypeChoice,
			Instructions: "What should the bot do with itself right now?",
			Criteria:     criteria,
		}),
	}
}

// activityDescriptions is the full vocabulary. Only entries relevant to the
// current snapshot reach the model — this is the catalogue, the curriculum is
// the subset.
var activityDescriptions = map[string]string{
	ActivityRest:        "stay put; nothing nearby is worth the effort",
	ActivityWander:      "walk somewhere new and look around",
	ActivityExplore:     "explore the area a little, as if sightseeing",
	ActivityGather:      "collect a resource it can see or plausibly reach",
	ActivityApproach:    "walk over to a nearby player, without necessarily saying anything",
	ActivityChat:        "walk over to a nearby player to talk with them",
	ActivityShelter:     "get under cover before it gets dangerous",
	ActivitySleep:       "sleep until morning",
	ActivityGesture:     "play a short emote, as people do when standing about",
	ActivityMine:        "actually mine or chop the resource in front of it, and collect what drops",
	ActivityLook:        "look around at nearby signs, chests and blocks to see what is here",
	ActivityFish:        "fish at the water it can see",
	ActivityHarvest:     "harvest the ripe crops in front of it",
	ActivityTendAnimals: "feed or look after the animals nearby",
	ActivityCraft:       "craft something it has the ingredients for right now",
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// Only reachable if a struct above gained an unmarshalable field, which
		// a compile-time-shaped struct of strings and maps cannot do. Panicking
		// here is right: it is a programming error, not a runtime condition.
		panic("jev: question definition is not serializable: " + err.Error())
	}
	return b
}
