package jev

import "encoding/json"

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
)

// BuildReflexQuestions asks the survival and pacing judgements in one call.
// They travel together because Jev answers a whole set in a single parallel
// pass — splitting them would cost extra round trips for no benefit.
func BuildReflexQuestions() map[string]json.RawMessage {
	return map[string]json.RawMessage{
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
	ActivityRest:     "stay put; nothing nearby is worth the effort",
	ActivityWander:   "walk somewhere new and look around",
	ActivityExplore:  "explore the area a little, as if sightseeing",
	ActivityGather:   "collect a resource it can see or plausibly reach",
	ActivityApproach: "walk over to a nearby player, without necessarily saying anything",
	ActivityChat:     "walk over to a nearby player to talk with them",
	ActivityShelter:  "get under cover before it gets dangerous",
	ActivitySleep:    "sleep until morning",
	ActivityGesture:  "play a short emote, as people do when standing about",
	ActivityMine:     "actually mine or chop the resource in front of it, and collect what drops",
	ActivityLook:     "look around at nearby signs, chests and blocks to see what is here",
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
