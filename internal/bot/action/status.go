// This file is the seam between a handler's real outcome and the plan executor
// that has to report it.
//
// Handlers already tell the bot what happened through
// Bot.ReportActionStatus, but that call only produces a chat message: it is
// fire-and-forget, and the caller learns nothing. Bot lives in the parent
// package, so this seam cannot be bolted onto it from here — instead handlers
// in this package report through ReportStatus, which does both jobs: it hands
// the status to the bot exactly as before, and publishes it to whoever is
// waiting on this bot.
//
// The executor in plan.go subscribes before it dispatches an action and takes
// the first report as the verdict. That is the whole point of the change: a
// plan step that failed, hung, or named a label the bot cannot perform has to
// reach the planner as a failure, because the planner re-plans on what it is
// told, not on what the bot hoped happened.
package action

import (
	"fmt"
	"sync"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// statusSubscribers tracks, per bot, the channels waiting for an action result.
//
// Keyed by bot rather than global because several bots can be live in one
// process, and a shared channel would let one bot's action decide another bot's
// plan. A mutex rather than a per-bot lock because the map is written on every
// step and read on every report.
var statusSubscribers = struct {
	mu sync.Mutex
	m  map[*bot.Bot]map[chan event.ActionStatus]struct{}
}{m: make(map[*bot.Bot]map[chan event.ActionStatus]struct{})}

// SubscribeStatus registers a channel to receive this bot's action results and
// returns it. The channel is buffered so a handler reporting on its own
// goroutine never blocks on a slow reader; one report is all a step needs, so a
// full buffer simply drops the surplus.
func SubscribeStatus(b *bot.Bot) chan event.ActionStatus {
	ch := make(chan event.ActionStatus, 1)
	statusSubscribers.mu.Lock()
	defer statusSubscribers.mu.Unlock()
	subs, ok := statusSubscribers.m[b]
	if !ok {
		subs = make(map[chan event.ActionStatus]struct{})
		statusSubscribers.m[b] = subs
	}
	subs[ch] = struct{}{}
	return ch
}

// UnsubscribeStatus detaches a channel. Steps must unsubscribe when they finish
// so a status produced later cannot be mistaken for the next step's outcome.
func UnsubscribeStatus(b *bot.Bot, ch chan event.ActionStatus) {
	statusSubscribers.mu.Lock()
	defer statusSubscribers.mu.Unlock()
	subs, ok := statusSubscribers.m[b]
	if !ok {
		return
	}
	delete(subs, ch)
	if len(subs) == 0 {
		delete(statusSubscribers.m, b)
	}
}

// ReportInventoryDelta reports the outcome of a long-running handler that ends
// by picking items up — mining, looting, harvesting, fishing — by comparing
// what the bot held before and after.
//
// These handlers used to only log their result, which was invisible to the
// plan executor and read as a failure however well the work went. The
// inventory is the same evidence the gatherer itself uses to decide it
// succeeded, so it is a real measurement rather than a claim that the command
// was accepted. Collecting nothing is a failure: the bot finished the motion
// and the world did not change, and saying otherwise is the exact optimistic
// result this reporting path exists to prevent.
//
// But it is measured against the goal that was asked for, not against whether
// anything was gained, and those are not the same thing. A gather for 10 oak
// logs on a bot holding 39 gains nothing, because it correctly skipped the
// work — and reporting that as "tidak dapat oak_log" is the failure the log
// kept showing: the gatherer correctly skipping a satisfied goal while the
// chat layer told the player it had failed.
//
// So the measure is the goal. Enough in the inventory now is success, whatever
// route got there. A delta is still worth reporting when there was one, because
// "you now have 49" is more useful to a player than "you have enough".
func ReportInventoryDelta(b *bot.Bot, user, label, item string, before, wanted int) {
	after := CountInventoryItems(b.GetInventorySlots(), b.GetItemNames(), item)
	gained := after - before
	if gained < 0 {
		// Items can legitimately be consumed mid-run (dropping junk to make
		// room, for example). A negative delta is not negative progress.
		gained = 0
	}

	name := NormalizeItemName(item)
	// No stated goal means there is nothing to be short of, so a real gain is
	// the success condition. Inventing a default target here would report a
	// failure for a handler that never claimed to reach one.
	if wanted <= 0 {
		if gained == 0 {
			ReportStatus(b, user, event.ActionStatus{
				Action:  label,
				Item:    name,
				Success: false,
				Error:   fmt.Sprintf("tidak dapat %s", name),
			})
			return
		}
		ReportStatus(b, user, event.ActionStatus{
			Action:  label,
			Item:    name,
			Count:   gained,
			Success: true,
		})
		return
	}

	switch {
	case after >= wanted:
		ReportStatus(b, user, event.ActionStatus{
			Action:  label,
			Item:    name,
			Count:   gained,
			Success: true,
		})
	case gained > 0:
		// Real progress, short of the goal. Still a failure to the plan, but a
		// partial one, and the count says what was actually achieved.
		ReportStatus(b, user, event.ActionStatus{
			Action:  label,
			Item:    name,
			Count:   gained,
			Success: false,
			Error:   fmt.Sprintf("hanya dapat %d dari %d %s", gained, wanted, name),
		})
	default:
		ReportStatus(b, user, event.ActionStatus{
			Action:  label,
			Item:    name,
			Success: false,
			Error:   fmt.Sprintf("tidak dapat %s", name),
		})
	}
}

// CountInventoryItems totals the stacks in slots whose name matches want.
// Names are normalised first so a caller can ask for "oak_log" and still match
// the "minecraft:oak_log" the server sends.
func CountInventoryItems(slots map[uint32]protocol.ItemStack, names map[int32]string, want string) int {
	wanted := NormalizeItemName(want)
	total := 0
	for _, stack := range slots {
		name, ok := names[stack.NetworkID]
		if !ok || NormalizeItemName(name) != wanted {
			continue
		}
		total += safecast.To[int](stack.Count)
	}
	return total
}

// ReportStatus is how a handler in this package reports what happened.
//
// It always forwards to the bot so the player still hears the result, and it
// publishes to any subscriber — which is the plan executor waiting on this
// step. Handlers must route their statuses through here rather than calling
// b.ReportActionStatus directly, otherwise the step that requested the action
// never learns how it went.
func ReportStatus(b *bot.Bot, user string, status event.ActionStatus) {
	publishStatus(b, status)
	// The chat path publishes again through the hook installed below. That is
	// harmless — the send is non-blocking and a subscriber that already resolved
	// drops it — and it is what makes a report that arrives by either route reach
	// the planner. Duplicating one cheap non-blocking send beats a verdict that
	// silently goes to only one of two places.
	b.ReportActionStatus(user, status)
}

// installPublishHook wires the bot's chat-report path back to the plan
// executor's subscriber list.
//
// It has to be a hook because the action package imports the bot package, so the
// bot cannot call publishStatus directly. Every other cross-package seam in the
// bot uses this same arrangement.
func init() {
	bot.PublishActionStatusFunc = publishStatus
}

// publishStatus fans a status out to this bot's subscribers. Delivery is
// non-blocking: a subscriber that is no longer reading (its step already
// resolved) must not be able to wedge the handler goroutine that is reporting.
func publishStatus(b *bot.Bot, status event.ActionStatus) {
	statusSubscribers.mu.Lock()
	subs, ok := statusSubscribers.m[b]
	if !ok {
		statusSubscribers.mu.Unlock()
		return
	}
	// Copy under the lock so the send happens outside it: a subscriber that
	// unsubscribes on receipt would otherwise deadlock against its own delivery.
	targets := make([]chan event.ActionStatus, 0, len(subs))
	for ch := range subs {
		targets = append(targets, ch)
	}
	statusSubscribers.mu.Unlock()

	for _, ch := range targets {
		select {
		case ch <- status:
		default:
		}
	}
}
