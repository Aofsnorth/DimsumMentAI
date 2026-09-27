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
	ActivityApproach: "walk over to a nearby player",
	ActivityShelter:  "get under cover before it gets dangerous",
	ActivitySleep:    "sleep until morning",
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
