package bot

// Speech ownership: who decides whether the bot speaks when nobody asked.
//
// Two loops can want to start an unprompted conversation. The AGI brain asks
// Jev first and respects a danger check, a goal, and a social cooldown. The
// proactive chat loop just asks the LLM directly on a timer, with none of
// those guards. When both run, the bot talks to itself on two different rule
// sets: it can say something cheerful twice in a minute, or say something
// cheerful while something is standing behind it. Two brains is not a feature,
// it is two disagreeing opinions about what the bot wants.
//
// The fix is ownership rather than a merge. Exactly one owner may run
// unprompted speech, the claim is atomic because both loops are started as
// goroutines and whichever loses simply stands down, and AGI outranks the
// proactive loop so the outcome does not depend on goroutine scheduling.

// Speech owners.
const (
	// SpeechOwnerNone means no loop has claimed unprompted speech.
	SpeechOwnerNone = ""
	// SpeechOwnerProactive is the timer-driven chat loop.
	SpeechOwnerProactive = "proactive"
	// SpeechOwnerAGI is the autonomy brain.
	SpeechOwnerAGI = "agi"
)

// speechPriority ranks owners. Higher wins a contested claim.
//
// AGI ranks above proactive because it is the better-informed brain: it has the
// danger check, the goal, and the cooldown the other one lacks. If AGI is
// enabled, the proactive loop is redundant at best and incoherent at worst, so
// it steps aside rather than competing.
func speechPriority(owner string) int {
	switch owner {
	case SpeechOwnerAGI:
		return 2
	case SpeechOwnerProactive:
		return 1
	default:
		return 0
	}
}

// ClaimUnpromptedSpeech takes ownership of unprompted speech, reporting whether
// the caller may proceed.
//
// A claim by a higher-priority owner always succeeds, so the outcome is the
// same no matter which goroutine happens to run first. An equal or lower claim
// fails, which is how the loser learns to stand down.
func (b *Bot) ClaimUnpromptedSpeech(owner string) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if speechPriority(owner) <= speechPriority(b.SpeechOwner) {
		return false
	}
	b.SpeechOwner = owner
	return true
}

// UnpromptedSpeechOwner reports who currently owns unprompted speech.
func (b *Bot) UnpromptedSpeechOwner() string {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.SpeechOwner
}
