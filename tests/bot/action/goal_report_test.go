package action_test

import (
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// A gather that skipped the work because the goal was already met is a
// success, not a failure.
//
// The bot announced this as "Gagal: tidak dapat oak_log" while sitting on 39
// oak logs — the gatherer had correctly decided there was nothing to chop, and
// the reporting layer then measured the result as "no inventory change" and
// called it a failure to the player. Measuring against the goal instead of the
// delta is what makes the report match what actually happened.

func newGoalReportBot(have int) *bot.Bot {
	b := newPlanTestBot()
	b.InventoryMap = map[uint32]protocol.ItemStack{1: itemStack(10, uint16(have))}
	b.ItemNames = map[int32]string{10: "minecraft:oak_log"}
	return b
}

// TestAAlreadySatisfiedGatherIsNotAFailure is the reported case, stated as the
// contract. The inventory already holds the goal; nothing moved; it is still a
// success.
func TestAAlreadySatisfiedGatherIsNotAFailure(t *testing.T) {
	t.Parallel()

	b := newGoalReportBot(39)

	installHandler(t, "goalmet", func(b *bot.Bot, param, user string) {
		go func() {
			// before == after: the gatherer did nothing, correctly.
			action.ReportInventoryDelta(b, user, "goalmet", "oak_log", 39, 10)
		}()
	})

	got := action.ExecuteAndWaitWithTimeout(b, "goalmet", "", "player", silentStepTimeout)
	if !got.Success {
		t.Fatalf("ExecuteAndWait() = %+v, want success: the bot already had the goal", got)
	}
}

// TestAPartialGatherReportsTheCount keeps the failure useful. Short of the goal
// is still a failure to the plan, but the player is told how far it got rather
// than being handed a bare "could not".
func TestAPartialGatherReportsTheCount(t *testing.T) {
	t.Parallel()

	b := newGoalReportBot(2)

	installHandler(t, "partial", func(b *bot.Bot, param, user string) {
		go func() {
			b.Mu.Lock()
			b.InventoryMap[1] = itemStack(10, 5)
			b.Mu.Unlock()
			action.ReportInventoryDelta(b, user, "partial", "oak_log", 2, 10)
		}()
	})

	got := action.ExecuteAndWaitWithTimeout(b, "partial", "", "player", silentStepTimeout)
	if got.Success {
		t.Fatal("ExecuteAndWait() = success, want failure: 5 of 10 is not the goal")
	}
	if got.Count != 3 {
		t.Errorf("count = %d, want 3 (the real gain)", got.Count)
	}
	if got.Error == "" {
		t.Error("a short haul must say how far it got, so the planner can act on it")
	}
}

// TestATrueFailureStillReports keeps the policy honest in the other direction.
// Reaching the goal must not become a way to report success for work that never
// happened.
func TestATrueFailureStillReports(t *testing.T) {
	t.Parallel()

	b := newGoalReportBot(0)

	installHandler(t, "truefail", func(b *bot.Bot, param, user string) {
		go func() {
			action.ReportInventoryDelta(b, user, "truefail", "oak_log", 0, 10)
		}()
	})

	got := action.ExecuteAndWaitWithTimeout(b, "truefail", "", "player", silentStepTimeout)
	if got.Success {
		t.Fatal("ExecuteAndWait() = success with an empty inventory and a goal of 10")
	}
	if got.Error == "" {
		t.Error("a real failure must carry a reason")
	}
}
