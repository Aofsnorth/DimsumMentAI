// Package nilconv_test proves that a path search over a world model with a
// chunk querier survives a binary that never links dragonfly's world package.
//
// It lives in its own directory on purpose. tests/bot/pathfinder links the
// palette with a blank import, so the guard proved by that package is
// unobservable there: chunk.RuntimeIDToState is already non-nil and the call
// can never fault. This package imports nothing from dragonfly but the chunk
// subpackage the pathfinder itself needs, which is exactly the shape of the
// binaries that were crashing — the pathfinder's own consumers that link no
// block palette at all.
package nilconv_test

import (
	"testing"

	"bedrock-ai/internal/bot/pathfinder"

	"github.com/df-mc/dragonfly/server/world/chunk"
)

// loadedQuerier answers "I know every cell" for a world made entirely of
// unrecognised blocks. That is the worst case for the name lookups: they get
// past the loaded check and reach the converter, which is where the nil
// dereference was.
type loadedQuerier struct{}

func (loadedQuerier) GetBlockRID(_, _, _ int32) (uint32, bool) { return 987654, true }

func (loadedQuerier) IsBlockAir(_, _, _ int32) (bool, bool) { return false, true }

func (loadedQuerier) IsBlockSolid(_, _, _ int32) (bool, bool) { return false, true }

// floorModel is a flat floor at y=0 with a standable cell at the origin. Every
// expansion from the origin's neighbour therefore reaches canStandAt, which is
// the one caller of isClimbableSurface and isHalfBlock.
func floorModel() *pathfinder.LocalWorldModel {
	w := pathfinder.NewLocalWorldModel()
	w.SetChunkQuerier(loadedQuerier{})
	for _, off := range [][2]int32{{0, 0}, {1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
		w.SetSolid(off[0], 0, off[1], true)
	}
	return w
}

// TestSearchOverQuerierDoesNotNilDeref is the regression. isHalfBlock and
// isClimbableSurface called chunk.RuntimeIDToState with no nil guard, while
// blockNameFor in world.go has had one since it was written. A binary that
// never links dragonfly's world package leaves that func var nil, so any A*
// over a model with a chunk querier took the process down from inside the
// search — which is the same call stack the movement loop is on.
func TestSearchOverQuerierDoesNotNilDeref(t *testing.T) {
	t.Parallel()

	if chunk.RuntimeIDToState != nil {
		t.Skip("the block palette is linked into this test binary, so the nil " +
			"func var this test is about cannot occur here; guard it in a package " +
			"that does not import dragonfly/server/world")
	}

	w := floorModel()
	start := pathfinder.Node{X: 0, Y: 1, Z: 0}
	target := pathfinder.Node{X: 2, Y: 1, Z: 0}

	// GetNeighbors is where the fault lived; FindPath is what production calls.
	// Neither may panic, and neither may report an empty expansion, which is
	// what a guard that simply answered "nothing is known" would produce.
	neighbors := w.GetNeighbors(start)
	if len(neighbors) == 0 {
		t.Fatal("GetNeighbors returned nothing: the guard swallowed the whole expansion")
	}
	if !neighborsAnyWaterAware(w, start) {
		t.Error("no neighbour was produced at all, so the search cannot move")
	}

	path := pathfinder.FindPath(start, target, w, false)
	if len(path) == 0 {
		t.Fatal("FindPath returned an empty path over a flat, fully decoded floor")
	}
}

// TestIsStandableOnAnUnknownBlockStillExpands pins the failure mode the guard
// must avoid: answering "unknown" is correct, answering "everything is
// unwalkable" is not. A body standing on a block nobody can name is standing on
// a block, and the search has to be able to leave it.
func TestIsStandableOnAnUnknownBlockStillExpands(t *testing.T) {
	t.Parallel()

	if chunk.RuntimeIDToState != nil {
		t.Skip("block palette linked; see TestSearchOverQuerierDoesNotNilDeref")
	}

	w := floorModel()
	// Diagonal expansion is the one that is free of the corner checks the
	// cardinal path takes, so it isolates the stand check from everything else.
	neighbors := w.GetNeighbors(pathfinder.Node{X: 0, Y: 1, Z: 0})

	var walked int
	for _, n := range neighbors {
		if n.Y != 1 {
			continue
		}
		walked++
	}
	if walked == 0 {
		t.Fatalf("no same-level neighbour offered from a floor the model can see: %+v", neighbors)
	}
}

// neighborsAnyWaterAware is a small readability helper: it asks whether the
// expansion produced anything at all, without pinning a particular link type.
func neighborsAnyWaterAware(w *pathfinder.LocalWorldModel, from pathfinder.Node) bool {
	return len(w.GetNeighbors(from)) > 0
}
