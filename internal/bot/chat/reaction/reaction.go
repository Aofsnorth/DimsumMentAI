// Package reaction models the delay between a person finishing typing and
// committing to an answer.
//
// Why this exists
// ---------------
// The bot used to answer at exactly the speed its language model happened to
// return a token stream, which meant two things that a person never does. The
// reply was emitted the instant the request completed, and two messages of the
// same shape took the same shape of time because the only thing determining
// the delay was the model's own latency.
//
// A person reads a message, decides it is worth answering, starts composing,
// and only then sends. The gap between the message arriving and the reply
// leaving is not zero, and it is not a constant -- it grows with how much there
// is to think about. Measured human reaction to an unexpected message sits
// around 300ms at the fast end and rises toward a second when the message needs
// a considered answer; deliberate composition before sending adds more.
//
// The fix is not "add latency". Latency that tracks model speed is still model
// speed. The fix is a delay that is chosen from the message, so that a short
// acknowledgement comes back quickly and a question takes longer, with jitter
// on top so the same message twice does not take the same time.
//
// What this deliberately does not do
// ----------------------------------
// It does not decide whether to answer at all. Ignoring some messages is the
// other half of how a person behaves in chat, and it is the half that can break
// a command the player genuinely wanted. See SelectiveReply for that decision
// being made elsewhere; this package only owns the timing.
package reaction

import (
	"strings"
	"time"

	"math/rand"
)

// The bands below are read as "reply after this much has elapsed", measured
// from the moment the message is understood rather than from when it arrives.
//
// The floor is above zero on purpose. A reply that can leave in under 50ms
// reads as a script, not a person, and the model round trip on a short answer
// is already fast enough that the total would otherwise look mechanical.
const (
	// AckMin/AckMax is the band for a message that needs almost no thought:
	// "ok", "hi", "terima kasih". A person answers these almost immediately
	// but not instantly.
	AckMin = 220 * time.Millisecond
	AckMax = 650 * time.Millisecond

	// ShortMin/ShortMax is the band for an ordinary statement or a short
	// request that needs no deliberation.
	ShortMin = 450 * time.Millisecond
	ShortMax = 1_100 * time.Millisecond

	// AskMin/AskMax is the band for something the bot has to actually think
	// about: a question, or a request with a parameter in it. This is where a
	// person visibly pauses before answering.
	AskMin = 800 * time.Millisecond
	AskMax = 2_000 * time.Millisecond

	// LongMax caps the added delay. Past a couple of seconds the wait stops
	// reading as a person composing a reply and starts reading as a bot that
	// hung, and a player who typed a question needs an answer.
	LongMax = 2_400 * time.Millisecond
)

// Delay returns how long to wait before treating a message as answered.
//
// It is a pure function of the message text apart from the jitter, so the band
// a message falls into can be reasoned about and tested directly.
func Delay(msg string) time.Duration {
	min, max := Band(msg)
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min)))
}

// Band classifies a message by how much a person would take to answer it and
// returns the range the delay is drawn from. It is exported because which band
// a message lands in is worth asserting directly, while the sampled delay is
// jittered and so is not.
func Band(msg string) (min, max time.Duration) {
	min, max = band(msg)
	if max > LongMax {
		max = LongMax
	}
	if max < min {
		max = min
	}
	return min, max
}

// band is the raw classification, before the LongMax ceiling is applied.
func band(msg string) (min, max time.Duration) {
	trimmed := strings.TrimSpace(msg)
	if trimmed == "" {
		return AckMin, AckMax
	}

	// A question needs composing, whatever its length.
	if endsWithQuestion(trimmed) {
		return AskMin, AskMax
	}

	// So does anything long enough to need reading twice.
	if len([]rune(trimmed)) > 40 {
		return AskMin, AskMax
	}

	// An acknowledgement is short and does not ask for anything.
	if isAcknowledgement(trimmed) {
		return AckMin, AckMax
	}

	return ShortMin, ShortMax
}

// endsWithQuestion reports whether the message is asking something, in the
// punctuation a player actually types. "?" is the reliable signal; "gimana",
// "boleh", and "bisa" cover the ones typed without it.
func endsWithQuestion(s string) bool {
	if strings.HasSuffix(s, "?") {
		return true
	}
	lower := strings.ToLower(s)
	for _, marker := range []string{"gimana", "boleh", "bisa", "kenapa", "apa "} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// acknowledgements are the short replies a person fires off without thinking.
// They are recognised rather than merely short, because "ya" and "sip" are
// short and need no delay, while "ok, aku ke sana sekarang ya" is also short
// and does.
var acknowledgements = []string{
	"ok", "oke", "okay", "sip", "siap", "ya", "yes", "iya",
	"thanks", "thank you", "makasih", "terima kasih",
	"halo", "hai", "hi", "hello", "hei", "oy",
	"good", "nice", "keren", "mantap", "GG",
}

// isAcknowledgement reports whether a message is a bare acknowledgement.
func isAcknowledgement(s string) bool {
	lower := strings.ToLower(strings.TrimRight(s, ".! "))
	for _, ack := range acknowledgements {
		if lower == ack {
			return true
		}
	}
	return false
}
