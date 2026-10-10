package gathering_test

import (
	"testing"

	"bedrock-ai/internal/bot/gathering"
)

// The footing rule is what stopped the bot from digging the rim it stands on.
// The live run mined the two surface blocks of the diagonally adjacent column
// (dy -1 and -2, dx +1, dz +1); the drops fell into the shaft, and the chase
// that followed fought the server's position corrections for seventeen seconds
// until the job died with zero logs collected.
func TestUnderminesFooting(t *testing.T) {
	t.Parallel()

	const feetY = 117

	cases := []struct {
		name    string
		dx, dz  int32
		targetY int32
		want    bool
	}{
		// The live failure: diagonal neighbour, one and two below feet level.
		{"diagonal rim block", 1, 1, feetY - 1, true},
		{"diagonal block two below", 1, 1, feetY - 2, true},
		{"directly beside, below", 1, 0, feetY - 1, true},
		{"directly in front, below", 0, 1, feetY - 1, true},
		{"own column below", 0, 0, feetY - 1, true},
		// At or above feet level the block is not part of the footing: wall
		// blocks beside the bot are legitimately minable.
		{"feet level beside", 1, 0, feetY, false},
		{"head level beside", 1, 0, feetY + 1, false},
		{"above feet in own column", 0, 0, feetY + 1, false},
		// Far enough sideways that the bot cannot fall into the hole.
		{"two blocks away, below", 2, 0, feetY - 2, false},
		{"diagonal two away", 2, 2, feetY - 1, false},
	}

	for _, tc := range cases {
		if got := gathering.UnderminesFooting(tc.dx, tc.dz, tc.targetY, feetY); got != tc.want {
			t.Errorf("%s: UnderminesFooting(%d,%d,y=%d) = %v, want %v",
				tc.name, tc.dx, tc.dz, tc.targetY, got, tc.want)
		}
	}
}
