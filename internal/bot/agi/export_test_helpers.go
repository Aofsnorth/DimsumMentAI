package agi

import (
	"encoding/json"
	"time"

	"bedrock-ai/internal/bot"

	"github.com/go-gl/mathgl/mgl32"
)

// Test-support accessors.
//
// The black-box suite in tests/bot/agi drives the same methods the loop does, so
// it has to build a Runner and read a few of its fields directly. b, cfg and
// vocabulary are deliberately unexported — they are the wiring the loop owns,
// and a test that could set them directly would be asserting against a shape
// production never uses. These accessors are the way in: the smallest surface
// that lets an external test reach the state it needs without exporting the
// fields themselves.
//
// lastMoved and suspendedBy are genuinely guarded by the runner's mutex, so
// their setters take the lock inside rather than handing out a racy field.

// NewBareForTest returns a Runner with no bot attached, carrying cfg and a
// fresh vocabulary.
//
// The methods exercised this way are the decision ones — the menu, the goals,
// the plan, the watchdog, the one-block read — and all of them are written to
// survive a runner with no bot: log() and note() already fall back to a
// discarding logger when there is nothing to log to.
func NewBareForTest(cfg Config) *Runner {
	return &Runner{cfg: cfg, vocabulary: NewVocabulary()}
}

// Bot returns the bot the runner is driving, or nil when it has none.
func (r *Runner) Bot() *bot.Bot {
	if r == nil {
		return nil
	}
	return r.b
}

// Vocabulary returns what this session has observed. The Vocabulary carries its
// own lock, so the pointer can be handed out and merged/described directly.
func (r *Runner) Vocabulary() *Vocabulary {
	if r == nil {
		return nil
	}
	return r.vocabulary
}

// SetLastMoved records when the bot last changed position.
func (r *Runner) SetLastMoved(at time.Time) {
	r.mu.Lock()
	r.lastMoved = at
	r.mu.Unlock()
}

// SetSuspendedBy records which player's attention is currently holding the bot.
func (r *Runner) SetSuspendedBy(who string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.suspendedBy = who
}

// JudgementForTest parses a locomotion answer the way consult does, without a
// model. The activity is carried through untouched so the helper stays honest
// about what consult returns for a full reply.
func JudgementForTest(activity, locomotion string) Judgement {
	return Judgement{Activity: activity, Locomotion: parseLocomotion(locomotion), Known: true}
}

// NewRunnerForTest returns a Runner driving the given bot. The agi package
// already imports internal/bot, so naming the type here breaks no boundary —
// the boundary is the other way (subpackages must not import internal/bot).
func NewRunnerForTest(b *bot.Bot, cfg Config) *Runner {
	return &Runner{b: b, cfg: cfg, vocabulary: NewVocabulary()}
}

// ApplyLocomotionForTest installs a travel style the way the natural tick
// does after Jev decides.
func (r *Runner) ApplyLocomotionForTest(hint LocomotionHint) {
	r.applyLocomotion(hint)
}

// ApplyGazeForTest installs an attention choice the way the natural tick does
// after Jev decides, so a test can put the two decisions in the same order the
// tick applies them.
func (r *Runner) ApplyGazeForTest(hint GazeHint) {
	r.applyGaze(hint)
}

// ParseGazeForTest exposes the answer parser so a test can assert what an
// unrecognised answer does, which is the property the whole feature rests on.
func ParseGazeForTest(raw string) GazeHint {
	return parseGaze(raw)
}

// GazeAppliesToForTest exposes the activity gate.
func GazeAppliesToForTest(activity string) bool {
	return gazeAppliesTo(activity)
}

// ObserveSubmersionForTest exposes the breath reading the reflex acts on.
//
// The reflex's whole job is one decision — is there time left under water — and
// that decision is made from observeSubmersion. A test that could only see the
// reflex fire would not be able to tell it reading the swim controller's clock
// from it keeping a second clock of its own that happened to agree, so the
// reading is the thing worth reaching.
func (r *Runner) ObserveSubmersionForTest(now time.Time) (underwater bool, seconds int) {
	return r.observeSubmersion(now)
}

// QuestionsForTest renders the batch exactly as a tick would send it, so a test
// can assert on what the model would actually receive.
//
// Without this, a correctly derived affordance set that never reaches the
// request looks identical to one that works: the package compiles, its unit
// tests pass, and the bot behaves exactly as before. That is the failure this
// accessor exists to make visible.
func (r *Runner) QuestionsForTest(s Snapshot) map[string]json.RawMessage {
	return r.questions(s)
}

// ObservePositionForTest drives the watchdog's movement reading from an explicit
// position, so a test can stage a body that drifts inside one block.
//
// The production path reads the real bot position, which a test cannot move on
// its own without a server. Taking the position as an argument keeps the test
// exercising the real judgement rather than a copy of it.
func (r *Runner) ObservePositionForTest(b *bot.Bot, x, y, z float32, now time.Time) Health {
	r.b = b
	b.Pos = mgl32.Vec3{x, y, z}
	// Health comes from the bot for the same reason production does: Diagnose
	// checks death before it checks stuck, so a seam that passed a zero health
	// would report every frozen bot as dead and never exercise the stuck path.
	hp, _, coords := b.GetStatusDetails()
	b.Pos = mgl32.Vec3{x, y, z}
	// WantsToMove comes from the same derivation production uses, rather than
	// being passed in: a test that set its own answer would be testing itself.
	return r.observePosition(now, Snapshot{HP: hp, Coords: coords, WantsToMove: wantsToMove(b)})
}

// RememberAffordanceForTest stages the verb the model chose, and
// TakeAffordanceForTest spends it.
//
// They exist because the bug they guard is a wiring bug: the question was built,
// sent, answered, and then never read, so every test that checked the question
// passed while the bot stood still.
func (r *Runner) RememberAffordanceForTest(picked string) { r.rememberAffordance(picked) }

func (r *Runner) TakeAffordanceForTest() (string, bool) { return r.pendingVerb() }

// AffordanceParamForTest exposes the argument a picked verb carries, so a test
// can prove it survives the trip from the model's answer to the handler.
func (r *Runner) AffordanceParamForTest(verb string) string { return takeAffordanceParam(verb) }

// DispatchedVerb is one dispatch the runner handed to the action registry.
type DispatchedVerb struct {
	Verb  string
	Param string
	Who   string
}

// SetExecuteForTest intercepts the dispatch and records what the handler was
// given, returning a function that restores the real registry.
func (r *Runner) SetExecuteForTest(capture *[]DispatchedVerb) func() {
	previous := execute
	execute = func(b *bot.Bot, verb, param, who string) {
		*capture = append(*capture, DispatchedVerb{Verb: verb, Param: param, Who: who})
	}
	return func() { execute = previous }
}

// DoActivityForTest runs the real activity path, so a test can observe what the
// runner dispatched rather than what its helpers would have produced.
func (r *Runner) DoActivityForTest(activity string) { r.doActivity(activity) }
