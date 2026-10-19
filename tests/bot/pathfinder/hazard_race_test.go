package pathfinder_test

import (
	"sync"
	"testing"
	"time"

	"bedrock-ai/internal/bot/pathfinder"
)

// TestConcurrentHazardReadsDoNotCrash is the regression guard for the lazy-delete
// data race.
//
// tempHazardExpiry used to delete expired entries while the caller held only an
// RLock. Two goroutines reading the same expired cell both mutated the map under
// a read lock, which Go's runtime treats as a fatal "concurrent map read and map
// write" crash — not a recoverable panic. Run this under -race to see the
// original failure.
func TestConcurrentHazardReadsDoNotCrash(t *testing.T) {
	t.Parallel()

	model := pathfinder.NewLocalWorldModel()
	const x, y, z = int32(3), int32(64), int32(3)

	// A mark that has already expired, so every reader takes the deletion path.
	model.SetTempHazard(x, y, z, -time.Second)

	const goroutines = 16
	const iterations = 2000
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = model.IsHazard(x, y, z)
				_ = model.IsLoaded(x, y, z)
			}
		}()
	}
	wg.Wait()

	// An expired mark must not be reported as live after all that.
	if model.IsHazard(x, y, z) {
		t.Fatal("an expired temporary hazard is still reported as live")
	}
}

// TestExpiredHazardsArePrunedOnWrite keeps the map from growing forever now that
// reads no longer delete.
func TestExpiredHazardsArePrunedOnWrite(t *testing.T) {
	t.Parallel()

	model := pathfinder.NewLocalWorldModel()
	for i := int32(0); i < 400; i++ {
		model.SetTempHazard(i, 64, 0, -time.Second)
	}
	// The write path prunes once the map grows past its threshold.
	if got := model.TempHazardCountForTest(); got >= 400 {
		t.Fatalf("expired temporary hazards were not pruned: %d still stored", got)
	}
}
