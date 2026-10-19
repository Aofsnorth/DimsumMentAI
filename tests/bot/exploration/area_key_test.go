package exploration_test

import (
	"testing"

	"bedrock-ai/internal/bot/exploration"

	"github.com/go-gl/mathgl/mgl32"
)

// TestAreaKeyKeepsNegativeChunksDistinct is the regression guard for the
// visited-area collision.
//
// areaKey used to build its result from string(rune(cx)). Converting a negative
// int to a rune produces the Unicode replacement character, so every negative
// chunk coordinate became the same string: string(rune(-1)) == string(rune(-2))
// == "\uFFFD". The explorer marks a chunk visited by this key, which meant one
// visit to any chunk in the west or north quadrant marked the whole quadrant as
// done.
func TestAreaKeyKeepsNegativeChunksDistinct(t *testing.T) {
	t.Parallel()

	cases := []mgl32.Vec3{
		{-1, 64, -1},   // chunk -1,-1
		{-17, 64, -17}, // chunk -2,-2
		{15, 64, 15},   // chunk 0,0
		{16, 64, 16},   // chunk 1,1
		{1024, 64, -2048},
	}

	seen := map[string]mgl32.Vec3{}
	for _, pos := range cases {
		key := exploration.AreaKeyForTest(pos)
		if prev, ok := seen[key]; ok {
			t.Fatalf("positions %v and %v share the visited-area key %q; the explorer would skip one",
				prev, pos, key)
		}
		seen[key] = pos
	}
}

// TestAreaKeyGroupsPositionsInsideOneChunk pins the other half: two positions in
// the same 16x16 chunk are the same area.
func TestAreaKeyGroupsPositionsInsideOneChunk(t *testing.T) {
	t.Parallel()

	a := exploration.AreaKeyForTest(mgl32.Vec3{-1, 64, -1})
	b := exploration.AreaKeyForTest(mgl32.Vec3{-15, 70, -15})
	if a != b {
		t.Errorf("two positions in the same negative chunk produced different keys: %q vs %q", a, b)
	}
}
