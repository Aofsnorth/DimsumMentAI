package pathfinder

// Test-support accessors for the pathfinder package.

// TempHazardCountForTest reports how many temporary hazard marks are stored.
// Reads no longer delete expired entries (that was a fatal map race), so a test
// needs a way to prove the write-path pruning actually runs.
func (w *LocalWorldModel) TempHazardCountForTest() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.tempHazardBlocks)
}
