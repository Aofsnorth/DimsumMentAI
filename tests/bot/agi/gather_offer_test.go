package agi_test

import (
	"testing"

	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/jev"
)

// The offer that cost a thirty-nine-long loop.
//
// A live run had the bot holding a pile of logs while wood stayed visible in
// the world, and because the reason "gather" was still on the menu was "there
// are logs out there" rather than "the bot needs logs", the model picked it
// again every tick. The gatherer correctly declined each time — thirty-nine
// consecutive skipped gathers in one run — and the body stood still throughout,
// which is what made it look wedged.
//
// The offer is now gated on how much the bot is already carrying.

// TestGatherIsNotOfferedWhileTheBotAlreadyHoldsALogPile is that gate, with the
// boundary the constant defines rather than an arbitrary high number: one log
// under the limit still gets the offer, one at the limit does not. A test that
// only checked "a lot of logs" would pass for a threshold of 5 or of 500.
func TestGatherIsNotOfferedWhileTheBotAlreadyHoldsALogPile(t *testing.T) {
	t.Parallel()

	// Wood in sight and room for more, so the only thing that can turn the
	// offer off is the pile the bot is carrying.
	world := func(logs int) agi.Snapshot {
		return agi.Snapshot{
			NearBlocks: "oak_log",
			FreeSlots:  20,
			HP:         20,
			Hunger:     20,
			LogsHeld:   logs,
		}
	}

	// Prove the offer works at all. Without this the assertions below are
	// satisfied by a curriculum that never offers gather in the first place.
	if !hasActivity(agi.Curriculum(world(0)), jev.ActivityGather) {
		t.Fatal("a bot carrying nothing is not offered gather with wood in sight: " +
			"the fixture is wrong and every assertion below would pass by accident")
	}

	// Still short of the limit: the bot needs the wood, so it is offered.
	if !hasActivity(agi.Curriculum(world(15)), jev.ActivityGather) {
		t.Error("gather was withheld from a bot carrying 15 logs, one short of the " +
			"limit: the offer must survive right up to the boundary")
	}

	// At the limit: the bot is carrying a pile, and picking gather again is the
	// loop. This is the whole bug.
	if hasActivity(agi.Curriculum(world(16)), jev.ActivityGather) {
		t.Error("gather is still offered at 16 logs held: the model will pick it every " +
			"tick, the gatherer will decline every tick, and the body will not move")
	}

	// Well past it, so a stray off-by-one elsewhere cannot quietly reopen the
	// loop.
	if hasActivity(agi.Curriculum(world(64)), jev.ActivityGather) {
		t.Error("gather is still offered to a bot carrying 64 logs")
	}
}

// TestThePileGateDoesNotTakeTheRestOfTheMenuWithIt. Fixing the loop cannot pass
// by starving the bot: the activities that do not need a free hand are still
// offered to a bot carrying everything it can hold.
func TestThePileGateDoesNotTakeTheRestOfTheMenuWithIt(t *testing.T) {
	t.Parallel()

	full := agi.Snapshot{
		NearBlocks: "oak_log",
		FreeSlots:  20,
		HP:         20,
		Hunger:     20,
		LogsHeld:   64,
	}
	menu := agi.Curriculum(full)
	if len(menu) == 0 {
		t.Fatal("a heavily laden bot was left with no activity at all")
	}
	for _, want := range []string{jev.ActivityRest, jev.ActivityWander} {
		if !hasActivity(menu, want) {
			t.Errorf("%q is missing from the menu of a heavily laden bot", want)
		}
	}
}
