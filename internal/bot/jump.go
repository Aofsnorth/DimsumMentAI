package bot

import "time"

// A real jump, requested by whatever needs the body off the ground.
//
// The jump emote is not a jump. TriggerEmoteFor sets EmoteState, which the
// client renders as the character springing — and that was the whole of the
// "jump" the scaffolder performed before placing a block under itself. The body
// never left the floor, so the block was placed into the cell the player was
// standing in, the server refused the placement as intersecting an entity, and
// the tower simply did not grow.
//
// The movement loop owns the input flags, and it rebuilds them from the steering
// solution every tick, so setting a flag from outside would be overwritten on
// the next frame. The request is therefore latched here and consumed by the
// loop, which is the only place that can turn it into velocity.
const jumpRequestTTL = 500 * time.Millisecond

// RequestJump asks the movement loop to leave the ground on its next tick.
//
// It is a request, not a command: a bot that is already airborne, or that is not
// connected, simply does not get a jump out of it. The timestamp is what makes
// it a request rather than a latch — a request nobody collects within a few
// ticks is dropped, so a jump asked for and then abandoned does not fire half a
// second later when the bot is doing something else entirely.
func (b *Bot) RequestJump() {
	b.Mu.Lock()
	b.jumpRequestedAt = time.Now()
	b.Mu.Unlock()
}

// ConsumeJumpRequest reports whether a jump was asked for recently, and clears
// it. Only the movement loop calls this, once per tick.
func (b *Bot) ConsumeJumpRequest() bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.jumpRequestedAt.IsZero() {
		return false
	}
	fresh := time.Since(b.jumpRequestedAt) <= jumpRequestTTL
	b.jumpRequestedAt = time.Time{}
	return fresh
}

// JumpRequested reports whether a jump is pending, without consuming it. It
// exists for the scaffolder, which has to know whether its request is still in
// flight before it decides the body never left the ground.
func (b *Bot) JumpRequested() bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.jumpRequestedAt.IsZero() {
		return false
	}
	return time.Since(b.jumpRequestedAt) <= jumpRequestTTL
}

// Grounded reports whether the body is on a floor.
//
// The movement loop owns the flag and writes it on every tick, so this reads it
// under the same lock the loop uses. A scaffold placement has to know this
// before it aims: a body that is already airborne is in the one state where a
// block can be placed into the cell it is standing in.
func (b *Bot) Grounded() bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.IsGrounded
}
