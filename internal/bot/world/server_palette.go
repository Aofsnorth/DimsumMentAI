package world

import (
	"fmt"
	"reflect"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// fnv1Hash64 is the Bedrock block-name hash (64-bit FNV-1, not FNV-1a). The
// vanilla block palette is ordered by this value, compared as unsigned.
func fnv1Hash64(s string) uint64 {
	h := uint64(0xcbf29ce484222325)
	for i := 0; i < len(s); i++ {
		h *= 0x100000001b3
		h ^= uint64(s[i])
	}
	return h
}

// customBlockStateCount returns how many block states a StartGame custom block
// definition expands to: the product of the value counts of its properties.
func customBlockStateCount(props map[string]any) int {
	list, ok := props["properties"]
	if !ok || list == nil {
		return 1
	}
	v := reflect.ValueOf(list)
	if v.Kind() != reflect.Slice {
		return 1
	}
	count := 1
	for i := 0; i < v.Len(); i++ {
		entry, ok := v.Index(i).Interface().(map[string]any)
		if !ok {
			continue
		}
		enum := reflect.ValueOf(entry["enum"])
		if enum.Kind() != reflect.Slice || enum.Len() == 0 {
			continue
		}
		count *= enum.Len()
	}
	return count
}

// describeCustomBlocks renders each StartGame custom block as
// name[prop:enumLen,...]xStates with a '*' when the name also exists in the
// vanilla palette and '<' when it sorts before air. It is a diagnostic: the
// merged palette is only right if these counts match the server's.
func describeCustomBlocks(customs []protocol.BlockEntry) []string {
	initPalette121()
	vanilla := make(map[string]bool, len(palette121Names))
	for _, n := range palette121Names {
		vanilla[n] = true
	}
	airHash := fnv1Hash64("minecraft:air")
	out := make([]string, 0, len(customs))
	for _, c := range customs {
		s := c.Name
		if vanilla[c.Name] {
			s += "*"
		}
		if fnv1Hash64(c.Name) < airHash {
			s += "<"
		}
		if list, ok := c.Properties["properties"]; ok && list != nil {
			if v := reflect.ValueOf(list); v.Kind() == reflect.Slice {
				s += "["
				for i := 0; i < v.Len(); i++ {
					if e, ok := v.Index(i).Interface().(map[string]any); ok {
						n := 0
						if ev := reflect.ValueOf(e["enum"]); ev.Kind() == reflect.Slice {
							n = ev.Len()
						}
						if i > 0 {
							s += ","
						}
						s += fmt.Sprintf("%v:%d", e["name"], n)
					}
				}
				s += "]"
			}
		}
		s += fmt.Sprintf("x%d", customBlockStateCount(c.Properties))
		out = append(out, s)
	}
	return out
}

// serverPaletteTables is the network-index → block table of a non-hash server.
type serverPaletteTables struct {
	names  []string
	solids []bool
	airs   []bool
	tops   []float32
}

// mergeServerPalette rebuilds the runtime-ID space a non-hash Bedrock server
// really uses. Without block-network-ID hashes the client does not receive a
// palette: it sorts its own vanilla palette together with the StartGame custom
// blocks by block-name hash, so every custom block state shifts all vanilla
// states that sort after it. Indexing the plain vanilla palette (or the 105
// custom entries) by raw runtime ID therefore names the wrong block — on the
// Geyser server this read dirt as granite_wall and grass_block as a hanging
// sign, erasing the ground.
func mergeServerPalette(customs []protocol.BlockEntry) serverPaletteTables {
	initPalette121()

	type group struct {
		name  string
		hash  uint64
		count int
		solid bool
		top   float32
	}
	// Custom entries named like a vanilla block override that block's
	// definition; they add no states to the palette.
	vanilla := make(map[string]bool, len(palette121Names))
	for _, n := range palette121Names {
		vanilla[n] = true
	}
	groups := make([]group, 0, len(customs))
	for _, c := range customs {
		if vanilla[c.Name] {
			continue
		}
		groups = append(groups, group{
			name:  c.Name,
			hash:  fnv1Hash64(c.Name),
			count: customBlockStateCount(c.Properties),
			solid: !isBlockNamePassable(c.Name),
			top:   TopOffsetFromState(c.Name, c.Properties),
		})
	}
	// Stable insertion sort: the custom list is small and ties keep wire order.
	for i := 1; i < len(groups); i++ {
		for j := i; j > 0 && groups[j].hash < groups[j-1].hash; j-- {
			groups[j], groups[j-1] = groups[j-1], groups[j]
		}
	}

	total := len(palette121Names)
	for _, g := range groups {
		total += g.count
	}
	t := serverPaletteTables{
		names:  make([]string, 0, total),
		solids: make([]bool, 0, total),
		airs:   make([]bool, 0, total),
		tops:   make([]float32, 0, total),
	}
	emitGroup := func(g group) {
		for k := 0; k < g.count; k++ {
			t.names = append(t.names, g.name)
			t.solids = append(t.solids, g.solid)
			t.airs = append(t.airs, false)
			t.tops = append(t.tops, g.top)
		}
	}

	gi := 0
	for i, name := range palette121Names {
		h := fnv1Hash64(name)
		for gi < len(groups) && groups[gi].hash < h {
			emitGroup(groups[gi])
			gi++
		}
		t.names = append(t.names, name)
		t.solids = append(t.solids, palette121Solids[i])
		t.airs = append(t.airs, palette121Airs[i])
		t.tops = append(t.tops, palette121Tops[i])
	}
	for ; gi < len(groups); gi++ {
		emitGroup(groups[gi])
	}
	return t
}

// firstIndexOf returns the first palette index naming each of the given blocks.
func (t serverPaletteTables) firstIndexOf(names ...string) map[string]int {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	out := map[string]int{}
	for i, n := range t.names {
		if want[n] {
			if _, seen := out[n]; !seen {
				out[n] = i
			}
		}
	}
	return out
}
