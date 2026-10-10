package pathfinder_test

import (
	"testing"
	"time"

	"bedrock-ai/internal/bot/pathfinder"
)

// The stuck-loop failure this file guards: every damage event — including the
// falls an explore route's own fall/step_jump links produce — permanently
// marked the block under the bot's feet as hazardous. A* then refused routes
// through cells the bot was standing on, every waypoint became unreachable,
// and the bot reported "stuck mulu" while standing on perfectly good grass.
// SetTempHazard is the bounded version the damage path now uses.

func TestTempHazardBlocksWhileLiveAndClearsAfter(t *testing.T) {
	t.Parallel()

	w := pathfinder.NewLocalWorldModel()

	w.SetTempHazard(5, 64, 7, 50*time.Millisecond)
	if !w.IsHazard(5, 64, 7) {
		t.Fatal("a live temp hazard was not honoured by IsHazard")
	}

	time.Sleep(80 * time.Millisecond)
	if w.IsHazard(5, 64, 7) {
		t.Fatal("an expired temp hazard still blocks the cell; the stuck loop would return")
	}
}

func TestTempHazardDoesNotOutliveReset(t *testing.T) {
	t.Parallel()

	w := pathfinder.NewLocalWorldModel()
	w.SetTempHazard(1, 70, 2, time.Hour)

	w.Reset()
	if w.IsHazard(1, 70, 2) {
		t.Fatal("Reset left a temp hazard behind")
	}
}

func TestPermanentHazardStillWorksAlongside(t *testing.T) {
	t.Parallel()

	w := pathfinder.NewLocalWorldModel()

	w.SetHazard(3, 65, 4, true)
	if !w.IsHazard(3, 65, 4) {
		t.Fatal("permanent hazard marking broke")
	}
	w.SetHazard(3, 65, 4, false)
	if w.IsHazard(3, 65, 4) {
		t.Fatal("permanent hazard clearing broke")
	}
}
