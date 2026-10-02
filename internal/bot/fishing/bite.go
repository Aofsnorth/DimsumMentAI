// Package fishing catches fish, and says so only when a fish actually arrived.
//
// The version this replaces reeled the rod on a deterministic 8-20 second timer
// and then did `caught++`. No bobber was ever watched, no bite was ever seen,
// and the counter it returned had no relationship to anything in the world. The
// bot would report "caught 5 fish" on a server where it had caught none, which
// is the exact failure this whole project exists to remove.
//
// The rule here is the opposite: the rod is reeled only on an observed bite, and
// a bite only becomes a catch when the inventory says so. Two independent
// confirmations, because either one alone is a guess:
//
//   - the bite: the server's fishhook-tease actor event, or — when the hook is
//     tracked as an entity — the bobber jerking down toward the player;
//   - the catch: the total count of a known fishing-loot item going up.
//
// If neither is observable, the trip reports zero. That is a correct answer.
package fishing

import (
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	// BiteMinDrop is how far the bobber must sink between two samples before
	// the motion counts as a bite. A float riding the surface moves by
	// centimetres; a fish taking the hook moves by a fifth of a block.
	BiteMinDrop float32 = 0.12

	// BiteMinSpeed is the average speed the bobber must show, in blocks per
	// second, for the same reason. It keeps a slow, deep, two-second sink from
	// reading the same as the twitch of a strike.
	BiteMinSpeed float32 = 0.25

	// BiteMaxGap bounds how far apart two samples may be and still be compared.
	// Past this the displacement is a chunk resync or a desync correction, and
	// treating that as a fish is how a bot reels in on nothing.
	BiteMaxGap = 2 * time.Second
)

// BobberSample is one observation of the fishing hook.
//
// It is a value, not a pointer to a live entity, so the comparison below is a
// pure function of two numbers and a clock. That is deliberate: a decision that
// depends on live state cannot be tested without a connection, and this decision
// is the one that used to be made on a timer.
type BobberSample struct {
	// ID is the hook's runtime entity ID. Zero means "not observed"; a zero ID
	// is never compared, because a sample at the origin is not evidence.
	ID uint64
	// Position is where the hook was when the sample was taken.
	Position mgl32.Vec3
	// At is when the sample was taken. Two samples with the same timestamp
	// describe no motion at all.
	At time.Time
}

// bobberTypes are the entity type strings Bedrock servers use for the fishing
// hook. Servers disagree, and an empty type is common on proxies that strip it,
// so the set is a set of spellings rather than one canonical name.
var bobberTypes = []string{
	"fishing_hook",
	"fish_hook",
	"fishing_bobber",
	"fishingbobber",
	"bobber",
}

// IsBobberType reports whether an entity type string is the fishing hook.
//
// A substring match on "hook" alone is deliberately not used: "tripwire_hook" is
// a block the bot also tracks, and mistaking it for the line means reeling on
// a stationary piece of redstone.
func IsBobberType(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimPrefix(n, "minecraft:")
	n = strings.ReplaceAll(n, " ", "_")
	if n == "" {
		return false
	}
	for _, want := range bobberTypes {
		if n == want {
			return true
		}
	}
	return false
}

// SankFast reports whether the bobber dropped far enough, quickly enough,
// between two samples.
//
// A sample with no entity ID is not a sample. Neither is a second one from a
// different hook, and neither is one taken so long after the first that the
// movement between them describes a resync rather than a fish.
func SankFast(before, after BobberSample) bool {
	if before.ID == 0 || after.ID == 0 || before.ID != after.ID {
		return false
	}
	gap := after.At.Sub(before.At)
	if gap <= 0 || gap > BiteMaxGap {
		return false
	}
	delta := after.Position.Sub(before.Position)
	if delta.Y() > -BiteMinDrop {
		return false
	}
	speed := float64(delta.Len()) / gap.Seconds()
	return speed >= float64(BiteMinSpeed)
}

// Approached reports whether the bobber got closer to the player between two
// samples.
//
// A hook that sinks but drifts down-current is not a fish taking it; it is
// wind. The anchor is the player's position, which is why this cannot be folded
// into SankFast without lying about one of the two.
func Approached(before, after BobberSample, anchor mgl32.Vec3) bool {
	if before.ID == 0 || after.ID == 0 || before.ID != after.ID {
		return false
	}
	return after.Position.Sub(anchor).Len() < before.Position.Sub(anchor).Len()
}

// ShouldReel is the bite predicate: the hook sank hard enough to be a strike
// and closed on the player while doing it.
//
// This is the trigger the 8-20 second timer used to be. It has to be an
// observation or it is a guess, so anything it cannot vouch for — no hook
// tracked, a stale sample, a different hook — comes back false and the line
// stays in the water.
func ShouldReel(before, after BobberSample, anchor mgl32.Vec3) bool {
	return SankFast(before, after) && Approached(before, after, anchor)
}
