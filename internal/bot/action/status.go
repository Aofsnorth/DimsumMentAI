// This file is the seam between a handler's real outcome and the plan executor
// that has to report it.
//
// Handlers already tell the bot what happened through
// Bot.ReportActionStatus, but that call only produces a chat message: it is
// fire-and-forget, and the caller learns nothing. Bot lives in the parent
// package, so this seam cannot be bolted onto it from here — instead handlers
// in this package report through reportStatus, which does both jobs: it hands
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

// subscribeStatus registers a channel to receive this bot's action results and
// returns it. The channel is buffered so a handler reporting on its own
// goroutine never blocks on a slow reader; one report is all a step needs, so a
// full buffer simply drops the surplus.
func subscribeStatus(b *bot.Bot) chan event.ActionStatus {
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

// unsubscribeStatus detaches a channel. Steps must unsubscribe when they finish
// so a status produced later cannot be mistaken for the next step's outcome.
func unsubscribeStatus(b *bot.Bot, ch chan event.ActionStatus) {
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

// reportInventoryDelta reports the outcome of a long-running handler that ends
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
func reportInventoryDelta(b *bot.Bot, user, label, item string, before, count int) {
	gained := countInventoryItems(b.GetInventorySlots(), b.GetItemNames(), item) - before
	if gained < 0 {
		// Items can legitimately be consumed mid-run (dropping junk to make
		// room, for example). A negative delta is not negative progress.
		gained = 0
	}
	name := normalizeItemName(item)
	if gained == 0 {
		reportStatus(b, user, event.ActionStatus{
			Action:  label,
			Item:    name,
			Success: false,
			Error:   fmt.Sprintf("tidak dapat %s", name),
		})
		return
	}
	reportStatus(b, user, event.ActionStatus{
		Action:  label,
		Item:    name,
		Count:   gained,
		Success: true,
	})
}

// countInventoryItems totals the stacks in slots whose name matches want.
// Names are normalised first so a caller can ask for "oak_log" and still match
// the "minecraft:oak_log" the server sends.
func countInventoryItems(slots map[uint32]protocol.ItemStack, names map[int32]string, want string) int {
	wanted := normalizeItemName(want)
	total := 0
	for _, stack := range slots {
		name, ok := names[stack.NetworkID]
		if !ok || normalizeItemName(name) != wanted {
			continue
		}
		total += safecast.To[int](stack.Count)
	}
	return total
}

// reportStatus is how a handler in this package reports what happened.
//
// It always forwards to the bot so the player still hears the result, and it
// publishes to any subscriber — which is the plan executor waiting on this
// step. Handlers must route their statuses through here rather than calling
// b.ReportActionStatus directly, otherwise the step that requested the action
// never learns how it went.
func reportStatus(b *bot.Bot, user string, status event.ActionStatus) {
	publishStatus(b, status)
	b.ReportActionStatus(user, status)
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
