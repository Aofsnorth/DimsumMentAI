// The wall scan's trusted rows, and the fall-link freeze they exist to fix.
//
// The failure: at every downward path link — the logs had (‑36,83,232) →
// (‑36,82,233,fall) and friends — the bot went permanently silent. No hop, no
// slide, no movement packet in either direction. The cause is a collision of two
// individually sane decisions. controlledDescentY sinks the whole body toward the
// landing level the moment the step toward the fall node is computed, and
// checkWallCollision then scans that sunk body for walls. The sunk body still
// straddles the column it is standing on, so the scan reads the very support
// block under the bot's feet as a wall at body level, cancels the horizontal step,
// and the tick loop repeats the exact same computation forever. On drops of two
// or more the support sits one row above the scan bottom, so a bottom-row skip
// alone cannot save it.
//
// The fix trusts the corridor A* already validated: while following a descending
// link, every block row below the level the link descends from is terrain the bot
// walks off, never a wall. Rows at and above that level keep the strict scan, so
// a genuine wall beside the ledge still stops the bot.

package movement_test

import (
	"testing"

	"bedrock-ai/internal/bot/movement"
)

func TestWallScanSkipsRow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                         string
		by, minY, descentFromY       int32
		groundedStepUp, linkDescends bool
		want                         bool
	}{
		{"grounded step-up trusts its bottom row", 82, 82, 0, true, false, true},
		{"grounded step-up still scans head rows", 83, 82, 0, true, false, false},
		{"mid-jump bottom row can be a real wall", 82, 82, 0, false, false, false},
		{"descent trusts rows below the departure level", 82, 82, 83, false, true, true},
		{"descent drop 2 trusts the taller ledge support", 83, 83, 84, false, true, true},
		{"descent still scans rows at the walk-off level", 83, 82, 83, false, true, false},
		{"flat links scan every row", 82, 83, 0, false, false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := movement.WallScanSkipsRow(tc.by, tc.minY, tc.descentFromY, tc.groundedStepUp, tc.linkDescends)
			if got != tc.want {
				t.Fatalf("WallScanSkipsRow(by=%d, minY=%d, descentFromY=%d, groundedStepUp=%v, linkDescends=%v) = %v, want %v",
					tc.by, tc.minY, tc.descentFromY, tc.groundedStepUp, tc.linkDescends, got, tc.want)
			}
		})
	}
}
