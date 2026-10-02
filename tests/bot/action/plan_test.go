package action_test

import (
	"bedrock-ai/internal/bot/action"
	"io"
	"log/slog"
	"testing"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The plan executor used to hand the AGI planner a hard-coded success for every
// non-craft action after a settle delay, so a step that failed — or a label the
// bot cannot even perform — was reported to the planner as completed. These
// tests pin the truthful contract: a step succeeds only when a handler actually
// reported success, and every other outcome (explicit failure, silence, or an
// unknown label) is reported as a failure with a reason.

// newPlanTestBot returns the minimum bot the plan executor touches. The status
// path is exercised through the in-package hook, not the LLM, so AiClient stays
// nil and no network or model call happens.
func newPlanTestBot() *bot.Bot {
	return &bot.Bot{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Name:         "PlanTestBot",
		InventoryMap: make(map[uint32]protocol.ItemStack),
		ItemNames:    make(map[int32]string),
	}
}

// installHandler registers a handler for the duration of one test. Tests use
// throwaway labels so they never race over the same entry, and restore the
// previous one on cleanup.
func installHandler(t *testing.T, label string, handler action.ActionHandler) {
	t.Helper()
	action.ActionHandlersMu.Lock()
	previous, existed := action.ActionHandlers[label]
	action.ActionHandlers[label] = handler
	action.ActionHandlersMu.Unlock()
	t.Cleanup(func() {
		action.ActionHandlersMu.Lock()
		defer action.ActionHandlersMu.Unlock()
		if existed {
			action.ActionHandlers[label] = previous
			return
		}
		delete(action.ActionHandlers, label)
	})
}

// silentStepTimeout is short enough to keep the expiry-path tests quick while
// still leaving room for the executor's settle wait to elapse first.
const silentStepTimeout = 50 * time.Millisecond

func TestExecuteAndWait_ReturnsReportedSuccess(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()
	installHandler(t, "testok", func(b *bot.Bot, param, user string) {
		action.ReportStatus(b, user, event.ActionStatus{
			Action:  "testok",
			Item:    "oak_log",
			Count:   3,
			Success: true,
		})
	})

	got := action.ExecuteAndWait(b, "testok", "", "player")

	if !got.Success {
		t.Fatalf("ExecuteAndWait() = %+v, want success", got)
	}
	if got.Count != 3 || got.Item != "oak_log" {
		t.Errorf("ExecuteAndWait() = %+v, want the reported item/count", got)
	}
}

// The regression this task exists for: a handler that reports failure must not
// be reported to the planner as success just because the bot sat still
// afterwards.
func TestExecuteAndWait_ReportedFailureIsNotSuccess(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()
	installHandler(t, "testfail", func(b *bot.Bot, param, user string) {
		action.ReportStatus(b, user, event.ActionStatus{
			Action:  "testfail",
			Success: false,
			Error:   "tidak ada bahan",
		})
	})

	got := action.ExecuteAndWait(b, "testfail", "", "player")

	if got.Success {
		t.Fatalf("ExecuteAndWait() = %+v, want failure for a handler that reported failure", got)
	}
	if got.Error == "" {
		t.Error("ExecuteAndWait() dropped the handler's error; the planner cannot explain the failure without it")
	}
}

// An action the bot cannot perform must fail loudly. Silently treating an
// unknown label as done is exactly how a plan "advances" through steps that
// never happened.
func TestExecuteAndWait_UnknownLabelFailsLoudly(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()

	got := action.ExecuteAndWait(b, "definitely_not_a_real_action", "", "player")

	if got.Success {
		t.Fatal("ExecuteAndWait() = success for an unknown label, want failure")
	}
	if got.Error == "" {
		t.Error("ExecuteAndWait() returned an unknown-label failure with no reason")
	}
}

// Silence is not success either. A handler that never reports leaves the
// planner unable to tell "done" from "hung", so the executor fails the step
// instead of guessing.
func TestExecuteAndWait_SilentHandlerFails(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()
	installHandler(t, "testsilent", func(b *bot.Bot, param, user string) {})

	got := action.ExecuteAndWaitWithTimeout(b, "testsilent", "", "player", silentStepTimeout)

	if got.Success {
		t.Fatal("ExecuteAndWait() = success for a handler that never reported, want failure")
	}
	if got.Error == "" {
		t.Error("ExecuteAndWait() returned a timeout failure with no reason")
	}
}

// Rest and wait are satisfied by doing nothing. That is the instruction, not a
// shortcut around it, so the empty label must stay a success and must not wait
// out any timeout.
func TestExecuteAndWait_EmptyLabelIsSatisfiedByDoingNothing(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()

	start := time.Now()
	got := action.ExecuteAndWait(b, "  ", "", "player")

	if !got.Success {
		t.Fatalf("ExecuteAndWait(\"\") = %+v, want success for a rest step", got)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("rest step took %s; it should not wait for a report that will never come", elapsed)
	}
}

// The report arrives on another goroutine for handlers that do real work, so the
// executor has to keep listening rather than sampling once.
func TestExecuteAndWait_AwaitsAsyncReport(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()
	installHandler(t, "testasync", func(b *bot.Bot, param, user string) {
		go func() {
			time.Sleep(30 * time.Millisecond)
			action.ReportStatus(b, user, event.ActionStatus{
				Action:  "testasync",
				Success: true,
				Count:   7,
			})
		}()
	})

	got := action.ExecuteAndWait(b, "testasync", "", "player")

	if !got.Success {
		t.Fatalf("ExecuteAndWait() = %+v, want the async success report", got)
	}
	if got.Count != 7 {
		t.Errorf("ExecuteAndWait() count = %d, want 7", got.Count)
	}
}

// Only the first report decides the step. A handler that reports twice (a
// progress note, then the outcome) must not let a later report overwrite the
// verdict the planner already got.
func TestExecuteAndWait_FirstReportWins(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()
	installHandler(t, "testdouble", func(b *bot.Bot, param, user string) {
		action.ReportStatus(b, user, event.ActionStatus{Action: "testdouble", Success: true, Count: 1})
		action.ReportStatus(b, user, event.ActionStatus{Action: "testdouble", Success: false, Error: "later note"})
	})

	got := action.ExecuteAndWait(b, "testdouble", "", "player")

	if !got.Success {
		t.Fatalf("ExecuteAndWait() = %+v, want the first report to decide the step", got)
	}
}

// A report must not leak from one step into the next. The planner runs steps
// back to back on the same bot, so a status left over from a finished step
// would let a later step claim an outcome it never produced.
func TestExecuteAndWait_ReportsDoNotLeakBetweenSteps(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()
	installHandler(t, "testfirst", func(b *bot.Bot, param, user string) {
		action.ReportStatus(b, user, event.ActionStatus{Action: "testfirst", Success: true})
	})
	installHandler(t, "testsecond", func(b *bot.Bot, param, user string) {})

	if got := action.ExecuteAndWait(b, "testfirst", "", "player"); !got.Success {
		t.Fatalf("first step = %+v, want success", got)
	}

	// The second handler reports nothing. If the first step's status were still
	// queued, this would wrongly read as success.
	got := action.ExecuteAndWaitWithTimeout(b, "testsecond", "", "player", silentStepTimeout)
	if got.Success {
		t.Fatal("second step = success from a leaked report, want failure")
	}
}

// Subscribers are per bot, so one bot's action never satisfies another bot's
// step. Two bots are live in the same process, and a shared channel would let
// one bot's failure decide the other's plan.
func TestStatusSubscribers_AreIsolatedPerBot(t *testing.T) {
	t.Parallel()
	first := newPlanTestBot()
	second := newPlanTestBot()

	firstSub := action.SubscribeStatus(first)
	defer action.UnsubscribeStatus(first, firstSub)
	secondSub := action.SubscribeStatus(second)
	defer action.UnsubscribeStatus(second, secondSub)

	action.ReportStatus(first, "player", event.ActionStatus{Action: "gather", Success: true})

	select {
	case got := <-firstSub:
		if !got.Success {
			t.Errorf("first subscriber got %+v, want the reported success", got)
		}
	default:
		t.Error("the reporting bot's subscriber received nothing")
	}

	select {
	case got := <-secondSub:
		t.Errorf("second bot's subscriber received %+v from another bot's action", got)
	default:
	}
}

// Unsubscribing must actually detach, so a step that finished cannot keep
// delivering into a later step's channel.
func TestUnsubscribeStatus_StopsDelivery(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()

	sub := action.SubscribeStatus(b)
	action.UnsubscribeStatus(b, sub)
	action.ReportStatus(b, "player", event.ActionStatus{Action: "gather", Success: true})

	select {
	case got := <-sub:
		t.Errorf("unsubscribed channel still received %+v", got)
	default:
	}
}

// --- inventory-delta reporting ---
//
// The gathering, looting, farming and fishing handlers all finish their work in
// a goroutine and used to only log the result. Under a plan executor that
// waits for the real outcome, a handler that never reports reads as a failure
// even when it succeeded, so these have to report what they actually did.
// They measure it from the inventory rather than trusting a returned "started
// fine" signal, which is the same evidence the gatherer itself uses.

// itemStack builds an inventory stack. NetworkID sits on the embedded ItemType,
// and a composite literal may not promote it under the language version this
// module targets, so it is set explicitly.
func itemStack(networkID int32, count uint16) protocol.ItemStack {
	stack := protocol.ItemStack{Count: count}
	stack.ItemType.NetworkID = networkID
	return stack
}

func TestCountInventoryItems_CountsMatchingStacksOnly(t *testing.T) {
	t.Parallel()
	slots := map[uint32]protocol.ItemStack{
		1: itemStack(10, 5),
		2: itemStack(11, 3),
		3: itemStack(10, 2),
		4: itemStack(12, 7),
	}
	names := map[int32]string{
		10: "minecraft:oak_log",
		11: "oak_planks",
		12: "cobblestone",
	}

	if got := action.CountInventoryItems(slots, names, "oak_log"); got != 7 {
		t.Errorf("CountInventoryItems(oak_log) = %d, want 7 across both matching stacks", got)
	}
	if got := action.CountInventoryItems(slots, names, "cobblestone"); got != 7 {
		t.Errorf("CountInventoryItems(cobblestone) = %d, want 7", got)
	}
	if got := action.CountInventoryItems(slots, names, "diamond"); got != 0 {
		t.Errorf("CountInventoryItems(diamond) = %d, want 0 for an absent item", got)
	}
}

// A handler that finishes without picking anything up did not do the thing,
// however cleanly it returned. Reporting success there is the same optimistic
// lie this task exists to remove.
func TestReportInventoryDelta_ZeroDeltaIsFailure(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()
	installHandler(t, "testdelta", func(b *bot.Bot, param, user string) {
		go func() {
			action.ReportInventoryDelta(b, user, "testdelta", "oak_log", 3, 3)
		}()
	})

	got := action.ExecuteAndWaitWithTimeout(b, "testdelta", "", "player", silentStepTimeout)

	if got.Success {
		t.Fatalf("ExecuteAndWait() = %+v, want failure when the inventory did not change", got)
	}
	if got.Error == "" {
		t.Error("a zero-delta failure must carry a reason the planner can act on")
	}
}

func TestReportInventoryDelta_RealGainIsSuccess(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()
	// Seed the inventory the way a real session has one, so the delta the
	// handler measures is a genuine change in what the bot holds.
	b.InventoryMap = map[uint32]protocol.ItemStack{1: itemStack(10, 2)}
	b.ItemNames = map[int32]string{10: "minecraft:oak_log"}
	installHandler(t, "testgain", func(b *bot.Bot, param, user string) {
		go func() {
			// The gather is modelled by the inventory actually changing.
			b.Mu.Lock()
			b.InventoryMap[1] = itemStack(10, 5)
			b.Mu.Unlock()
			action.ReportInventoryDelta(b, user, "testgain", "oak_log", 2, 0)
		}()
	})

	got := action.ExecuteAndWaitWithTimeout(b, "testgain", "", "player", silentStepTimeout)

	if !got.Success {
		t.Fatalf("ExecuteAndWait() = %+v, want success when the inventory grew", got)
	}
	if got.Count != 3 {
		t.Errorf("ExecuteAndWait() count = %d, want 3 (the real gain)", got.Count)
	}
	if got.Item != "oak_log" {
		t.Errorf("ExecuteAndWait() item = %q, want oak_log", got.Item)
	}
}

// The gathered item is named in the request, and the reported item must be the
// real one even when the caller left it blank and relied on a default.
func TestReportInventoryDelta_ReportsRequestedItem(t *testing.T) {
	t.Parallel()
	b := newPlanTestBot()
	installHandler(t, "testitem", func(b *bot.Bot, param, user string) {
		go func() {
			action.ReportInventoryDelta(b, user, "testitem", "  Oak_Log  ", 0, 1)
		}()
	})

	got := action.ExecuteAndWaitWithTimeout(b, "testitem", "", "player", silentStepTimeout)

	if got.Item != "oak_log" {
		t.Errorf("ExecuteAndWait() item = %q, want the normalised oak_log", got.Item)
	}
}
