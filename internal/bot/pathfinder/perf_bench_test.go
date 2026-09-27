package pathfinder

import (
	"fmt"
	"sync"
	"testing"
)

// benchQuerier is a ChunkQuerier with a small palette and no allocations, used
// to measure the world model's own overhead rather than the chunk cache's.
type benchQuerier struct {
	// stone/air alternate so the path has real work to do.
	names map[uint32]string
	solid map[uint32]bool
}

func newBenchQuerier() *benchQuerier {
	return &benchQuerier{
		names: map[uint32]string{0: "minecraft:air", 1: "minecraft:stone", 2: "minecraft:oak_log", 3: "minecraft:oak_door"},
		solid: map[uint32]bool{0: false, 1: true, 2: true, 3: true},
	}
}

func (q *benchQuerier) GetBlockRID(x, y, z int32) (uint32, bool) {
	if y < 0 {
		return 0, true
	}
	if y == 0 {
		return 1, true // floor
	}
	return 0, true
}

func (q *benchQuerier) IsBlockAir(x, y, z int32) (bool, bool) {
	rid, ok := q.GetBlockRID(x, y, z)
	return q.names[rid] == "minecraft:air", ok
}

func (q *benchQuerier) IsBlockSolid(x, y, z int32) (bool, bool) {
	rid, ok := q.GetBlockRID(x, y, z)
	return q.solid[rid], ok
}

func benchWorld() *LocalWorldModel {
	w := NewLocalWorldModel()
	w.SetChunkQuerier(newBenchQuerier())
	w.SetPathBounds(Node{X: 0, Y: 1, Z: 0}, Node{X: 32, Y: 1, Z: 32})
	return w
}

// BenchmarkIsSolid measures the single hottest call in the bot: every physics
// step and every pathfinding neighbour resolves a cell through this.
//
// The point of the benchmark is regression protection. IsSolid used to build
// its map key with fmt.Sprintf, which allocated a string and ran the formatting
// machinery on every call; the key is now a packed int64.
func BenchmarkIsSolid(b *testing.B) {
	w := benchWorld()

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		w.IsSolid(int32(i%64), 1, int32(i%64/64))
	}
}

// BenchmarkIsSolidWithOverrides covers the same call when the model also carries
// mined/placed overrides and stuck-recovery penalties, which is the state the
// world model is usually in while the bot is working.
func BenchmarkIsSolidWithOverrides(b *testing.B) {
	w := benchWorld()
	for i := int32(-4); i <= 4; i++ {
		w.SetTempSolid(i, 1, i, 5)
		w.SetSolid(i, 2, i, true)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		w.IsSolid(int32(i%8), 1, int32(i%8/8))
	}
}

// BenchmarkIsHazardWithQuerier is the other per-neighbour call: pathfinding asks
// it for every candidate tile, so a name lookup that allocates shows up here.
func BenchmarkIsHazardWithQuerier(b *testing.B) {
	w := benchWorld()

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		w.IsHazard(int32(i%32), 1, int32(i%32/32))
	}
}

// BenchmarkBodyClearanceTick models the per-tick bookkeeping the movement loop
// does: clear the previous tick's marks, then mark the bot's own three cells.
func BenchmarkBodyClearanceTick(b *testing.B) {
	w := benchWorld()

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w.ClearBodyClearance()
		w.SetBodyClearance(0, 1, 0)
		w.SetBodyClearance(0, 2, 0)
		w.SetBodyClearance(0, 3, 0)
	}
}

// BenchmarkPackBlockKey pins the key packing against the Sprintf it replaced.
// If packing ever regresses to a string, the allocation count here jumps.
func BenchmarkPackBlockKey(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_ = packBlockKey(int32(i%128)-64, int32(i%16), int32(i%64)-32)
	}
}

// Baseline models the world model exactly as it was before the key packing: a
// map[string]bool keyed by fmt.Sprintf("%d,%d,%d"). It exists so the speedup
// claim is measured in the same binary on the same machine rather than asserted
// from memory — run it next to BenchmarkIsSolid and compare.
type sprintfWorldModel struct {
	mu           sync.RWMutex
	solidBlocks  map[string]bool
	chunkQuerier ChunkQuerier
}

func newSprintfWorldModel() *sprintfWorldModel {
	return &sprintfWorldModel{
		solidBlocks:  make(map[string]bool),
		chunkQuerier: newBenchQuerier(),
	}
}

func (w *sprintfWorldModel) IsSolid(x, y, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	k := fmt.Sprintf("%d,%d,%d", x, y, z)
	if val, ok := w.solidBlocks[k]; ok {
		return val
	}
	if w.chunkQuerier != nil {
		isSolid, loaded := w.chunkQuerier.IsBlockSolid(x, y, z)
		if loaded {
			return isSolid
		}
	}
	return false
}

func BenchmarkIsSolidSprintfBaseline(b *testing.B) {
	w := newSprintfWorldModel()

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		w.IsSolid(int32(i%64), 1, int32(i%64/64))
	}
}

// TestPackBlockKeyRoundTrips is the correctness guard for the key packing: a
// lossy key would silently merge distinct cells, which shows up as blocks that
// are solid where they are not (or the reverse).
func TestPackBlockKeyRoundTrips(t *testing.T) {
	t.Parallel()

	coords := [][3]int32{
		{0, 0, 0},
		{1, 0, 0},
		{-1, 0, 0},
		{1000, 64, -1000},
		{-1000, 64, 1000},
		// Full Bedrock horizontal range and the build-height limits.
		{30000000, 319, 30000000},
		{-30000000, -64, -30000000},
		{33554431, 2047, 33554431},
		{-33554432, -2048, -33554432},
		{0, 2047, 0},
		{0, -2048, 0},
		{12345, -7, -54321},
	}

	seen := make(map[int64][3]int32, len(coords))
	for _, c := range coords {
		key := packBlockKey(c[0], c[1], c[2])
		gotX, gotY, gotZ := unpackBlockKey(key)
		if gotX != c[0] || gotY != c[1] || gotZ != c[2] {
			t.Errorf("round trip of %v = (%d,%d,%d)", c, gotX, gotY, gotZ)
		}
		if prev, dup := seen[key]; dup {
			t.Errorf("key collision: %v and %v share %d", prev, c, key)
		}
		seen[key] = c
	}
}
