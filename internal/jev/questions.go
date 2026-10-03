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
	// QLocomotion asks how the body should travel while it does this tick's
	// activity: walk, run, or run-and-hop. A travel style, not a destination —
	// the path stays exactly where it is, only the gait changes. Asked as a
	// choice so a model that never heard of it simply does not answer, and
	// the movement rules carry on as before.
	QLocomotion = "locomotion"
	// QGaze asks what the bot should attend to when it is standing still. It is
	// the one question that hands the model a say over perception, and it is
	// scoped as narrowly as it can be: the answer chooses WHAT KIND of thing the
	// head settles on next, never where the body goes and never whether the scan
	// happens at all.
	//
	// The gap this closes is not that the bot could not look — it looks at
	// players, creatures and blocks already — but that the choice was a dice roll
	// with no say from the model. A bot that is curious about the player who just
	// walked past and a bot that is staring at a tree are the same bot as far as
	// the model is concerned, and it had no vocabulary to say otherwise.
	QGaze = "gaze"
	// QDrop asks whether to leap the drop in front of the bot or stop at the
	// edge. It is the narrowest question in the set and the most consequential:
	// the body already refuses cliffs on its own, so answering "no" costs
	// nothing and answering "yes" is the only way a player-like descent ever
	// happens. A model that never heard of it does not answer, and not
	// answering is "stop".
	QDrop = "drop"
)

const (
	RiskCareful  = "careful"
	RiskBold     = "bold"
	RiskReckless = "reckless"

	// QRisk asks how much risk the bot should take on purpose.
	//
	// It is the personality dial, and it exists because a bot whose every
	// decision is a lookup table is not being careful — it is being a machine. A
	// player backs off a creeper one way and jokes with another, and both are
	// the same person.
	QRisk = "risk"

	// QAffordance asks what to do, from a set derived from the world.
	QAffordance = "affordance"
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

// Locomotion options. Kept to three on purpose: a classifier given "jog" versus
// "run" versus "sprint" produces mush, while walk / sprint / sprint-jump are
// three things a viewer can actually tell apart.
const (
	LocomotionWalk       = "walk"
	LocomotionSprint     = "sprint"
	LocomotionSprintJump = "sprint_jump"
)

// Drop options. Two, and the default is the cautious one: a model that does not
// answer this question must leave the bot standing at the edge, because the
// whole point of asking is that the bot already stops there by itself.
const (
	DropLeap = "leap"
	DropStop = "stop"
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

// Gaze options. These name what the head settles on, not where it points, and
// the movement layer turns each into the same thing it would have done on its
// own — including the natural rhythm, the hold durations and the micro-saccades.
const (
	// GazePerson settles on a nearby player.
	GazePerson = "person"
	// GazeMob settles on a nearby creature.
	GazeMob = "mob"
	// GazeBlock settles on a nearby block.
	GazeBlock = "block"
	// GazeAround looks at nothing in particular, which is what a person does when
	// the surroundings are uninteresting.
	GazeAround = "around"
)

// BuildGazeQuestion asks what the bot should look at while it is standing still.
func BuildGazeQuestion() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		QGaze: mustMarshal(ChoiceQuestion{
			Type:         TypeChoice,
			Instructions: "The bot is standing still for a moment. What should its attention settle on? Only choose a player if someone is actually nearby to look at, and only choose a creature if one is nearby. Choose \"around\" if nothing in view is worth watching. This changes only where it looks — never where it goes.",
			Criteria: map[string]string{
				GazePerson: "watch a nearby player",
				GazeMob:    "watch a nearby animal or creature",
				GazeBlock:  "look at a nearby block or feature of the ground",
				GazeAround: "just look around at nothing in particular",
			},
		}),
	}
}

// BuildLocomotionQuestion asks how the body should travel. It rides along in
// the same parallel batch as everything else, so it costs no extra round trip.
//
// Only asked when the bot is about to cover ground: a travel style with
// nowhere to go is a wasted decision that can only ever answer noise. The
// caller decides that from the snapshot; this just builds the question.
func BuildLocomotionQuestion() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		QLocomotion: mustMarshal(ChoiceQuestion{
			Type:         TypeChoice,
			Instructions: "The bot is about to travel somewhere. How should it move? Walk near other players, on ledges, or anywhere running would look wrong. Sprint across open ground when there is somewhere to be. Sprint-jump only on flat open ground it can already see — never near a drop.",
			Criteria: map[string]string{
				LocomotionWalk:       "walk there at a normal pace",
				LocomotionSprint:     "run there",
				LocomotionSprintJump: "run there, jumping as it goes",
			},
		}),
	}
}

// BuildDropQuestion asks whether to leap the drop in front of the bot.
//
// Two options, not a dial. A cliff is binary in practice — a player either goes
// over it or does not — and a three-way "how far" would give a model vocabulary
// for a decision the body cannot execute anyway.
func BuildDropQuestion() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		QDrop: mustMarshal(ChoiceQuestion{
			Type:         TypeChoice,
			Instructions: "There is a drop directly ahead of the bot. Leap off it, or stop at the edge?",
			Criteria: map[string]string{
				DropLeap: "jump off and take the drop — only when the far side is worth reaching and the landing is survivable",
				DropStop: "stop at the edge and go a different way",
			},
		}),
	}
}

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

	// A one-option menu is not a question, and the endpoint refuses it. See
	// minChoiceCriteria — but the case is reachable here specifically, because the
	// curriculum arrives already narrowed by the active goal. A `gather_wood`
	// goal advances only mining and gathering; narrow it in a world offering
	// neither and what is left is the forced "rest" above, alone. That single
	// entry is the whole menu.
	if len(criteria) < minChoiceCriteria {
		return nil
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

// minChoiceCriteria is the smallest number of options a choice question may
// carry. One is not a floor the endpoint enforces out of caution — it rejects it,
// verified against the live endpoint: a two-option choice is accepted, a
// one-option choice returns
//
//	400 Invalid decision request. Send only model, state, and bounded typed
//	questions; chat, streaming, tools, and generation controls are not supported.
//
// which reads like a complaint about the wrong field. Nothing about the body is
// wrong. The count is the problem, and the bound is a floor rather than a
// ceiling: twenty options pass fine.
//
// What makes this expensive is the blast radius. The 400 takes down the entire
// request, not just the offending question, so one menu that narrowed down to a
// single entry silently blinds the bot — it stops being asked danger, hunger,
// whether it is worth speaking, what to do next — while every other question
// rides along in the rejected batch. That is exactly what happened: the bot
// finished a gather, went idle, built a one-option activity menu, and from then
// on fell back to local rules for the rest of the session without recovering,
// because nothing it did afterwards could change the shape of the menu.
//
// Callers range over the returned map, so returning nil skips the question and
// costs nothing.
const minChoiceCriteria = 2

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

// BuildRiskQuestion asks how much risk the bot should take on purpose.
//
// Asked on every tick, like the other state questions: it costs no extra round
// trip, and a disposition that only gets asked when something scary is on screen
// is a disposition that never changes.
func BuildRiskQuestion() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		QRisk: mustMarshal(ChoiceQuestion{
			Type:         TypeChoice,
			Instructions: "How should the bot behave right now? Careful avoids anything that might hurt it. Bold takes the obvious line when the alternative is worse. Reckless does something risky on purpose because it would be interesting.",
			Criteria: map[string]string{
				RiskCareful:  "play it safe: walk around trouble, back off early, try nothing unfamiliar",
				RiskBold:     "take the direct route, fight what is in the way, accept a small risk for a real gain",
				RiskReckless: "do the risky thing on purpose — it is more fun that way, even if it might hurt",
			},
		}),
	}
}

// BuildAffordanceQuestion asks what to do, from a set derived from the world
// rather than from a fixed menu.
//
// This is the question the whole affordance layer exists to improve. A menu is
// a list the programmer wrote; an affordance set is a list of what the bot can
// actually do from where it is standing, holding what it is holding. The model
// reads the difference immediately: it stops choosing tasks that cannot work,
// and it starts composing — "gather, but put a block up first" only has a home
// once the verbs are visible separately from the activities.
//
// The map is the question's criteria, so the set travels with the question
// rather than needing a second rendering step that could disagree with it.
func BuildAffordanceQuestion(available map[string]string) map[string]json.RawMessage {
	if len(available) == 0 {
		// An empty choice is a question no model can answer, and an unanswerable
		// question reads as a bug rather than as "there was nothing to do".
		return nil
	}
	// One possible action is not a decision either. The caller asks whenever
	// anything is available, so a world that offers exactly one thing lands here
	// with a single entry — and a single-entry choice is rejected outright.
	if len(available) < minChoiceCriteria {
		return nil
	}
	return map[string]json.RawMessage{
		QAffordance: mustMarshal(ChoiceQuestion{
			Type:         TypeChoice,
			Instructions: "What should the bot do right now? Only these are possible here — anything else cannot work from where it is standing.",
			Criteria:     available,
		}),
	}
}
