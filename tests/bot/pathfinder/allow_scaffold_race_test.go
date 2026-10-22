package pathfinder_test

import (
	"sync"
	"testing"

	"bedrock-ai/internal/bot/pathfinder"
)

func TestWorldModelAllowScaffoldConcurrentAccess(t *testing.T) {
	w := pathfinder.NewLocalWorldModel()
	pos := pathfinder.Node{X: 0, Y: 64, Z: 0}
	w.SetSolid(0, 63, 0, true)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				w.SetAllowScaffold((idx+j)%2 == 0)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = w.AllowScaffolding()
				_ = w.GetNeighbors(pos)
			}
		}()
	}
	wg.Wait()
}
