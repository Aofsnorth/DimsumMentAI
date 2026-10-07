package world

import (
	"fmt"
	"sort"

	"bedrock-ai/internal/safecast"

	"github.com/df-mc/dragonfly/server/world/chunk"
)

// RIDCensus counts the runtime IDs stored in the loaded chunks between minY
// and maxY and renders the topN most common as rid:count:name:solid:top. It
// is a diagnostic: the most common IDs of real terrain are stone, dirt and
// grass, so a wrong name beside a common ID exposes a palette mismatch.
func (wc *WorldCache) RIDCensus(minY, maxY int32, topN int) []string {
	wc.mu.RLock()
	chunks := make([]*chunkEntry, 0, len(wc.chunks))
	for _, c := range wc.chunks {
		chunks = append(chunks, &chunkEntry{c: c})
	}
	wc.mu.RUnlock()

	counts := map[uint32]int{}
	for _, e := range chunks {
		for x := uint8(0); x < 16; x++ {
			for z := uint8(0); z < 16; z++ {
				for y := minY; y <= maxY; y++ {
					if int(y) < wc.r.Min() || int(y) > wc.r.Max() {
						continue
					}
					counts[e.c.Block(x, safecast.To[int16](y), z, 0)]++
				}
			}
		}
	}
	type kv struct {
		rid uint32
		n   int
	}
	all := make([]kv, 0, len(counts))
	for r, n := range counts {
		all = append(all, kv{r, n})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].n > all[j].n })
	if len(all) > topN {
		all = all[:topN]
	}
	out := make([]string, 0, len(all))
	for _, e := range all {
		name, _ := wc.BlockName(e.rid)
		out = append(out, fmt.Sprintf("%d:%d:%s:solid=%v", e.rid, e.n, name, wc.IsRIDSolid(e.rid)))
	}
	return out
}

type chunkEntry struct{ c *chunk.Chunk }
