package world

import (
	"bedrock-ai/internal/safecast"
	"strings"

	"github.com/df-mc/dragonfly/server/block"
	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/block/model"
	"github.com/df-mc/dragonfly/server/world"
	"github.com/df-mc/dragonfly/server/world/chunk"
)

type mockBlockSource struct{}

func (mockBlockSource) Block(cube.Pos) world.Block {
	return block.Air{}
}

// BlockRIDAt returns the block runtime ID at the given world coordinates.
func (wc *WorldCache) BlockRIDAt(x, y, z int32) (uint32, bool) {
	cPos := chunkPos{X: x >> 4, Z: z >> 4}

	wc.mu.RLock()
	c, ok := wc.chunks[cPos]
	wc.mu.RUnlock()
	if !ok {
		return 0, false
	}

	if int(y) < wc.r.Min() || int(y) > wc.r.Max() {
		return wc.airRID, true
	}

	// c.Block already yields a LOCAL runtime ID: chunk palettes were translated
	// when the sub-chunk was decoded. Translating again feeds a local ID back
	// into the network-hash table, which can remap a perfectly valid block onto
	// an unrelated one whenever that ID happens to be a hash key.
	rid := c.Block(safecast.To[uint8](x&0xf), safecast.To[int16](y), safecast.To[uint8](z&0xf), 0)
	return rid, true
}

// GetBlockRID returns the block runtime ID at the given world coordinates.
func (wc *WorldCache) GetBlockRID(x, y, z int32) (uint32, bool) {
	return wc.BlockRIDAt(x, y, z)
}

// IsBlockAir checks if the block at the given world coordinates is air.
func (wc *WorldCache) IsBlockAir(x, y, z int32) (bool, bool) {
	rid, loaded := wc.GetBlockRID(x, y, z)
	if !loaded {
		return false, false
	}
	return rid == wc.airRID, true
}

// IsRIDSolid checks if the given block runtime ID is solid.
//
// The answer is cached per runtime ID. Resolving it the long way builds the
// block's collision boxes (world.BlockByRuntimeID → Model → BBox) and its block
// state (which allocates a name string and a properties map) — for every single
// cell query. Physics, pathfinding and every block scan ask this thousands of
// times a tick, and the palette is finite, so the answer is worth remembering.
func (wc *WorldCache) IsRIDSolid(rid uint32) bool {
	rid = wc.TranslateRuntimeID(rid)

	wc.paletteMu.RLock()
	solid, cached := wc.ridSolid[rid]
	wc.paletteMu.RUnlock()
	if cached {
		return solid
	}

	solid = wc.resolveRIDSolid(rid)

	wc.paletteMu.Lock()
	wc.ridSolid[rid] = solid
	wc.paletteMu.Unlock()
	return solid
}

// resolveRIDSolid does the actual palette lookup and is only called once per
// distinct runtime ID.
func (wc *WorldCache) resolveRIDSolid(rid uint32) bool {
	if name, ok := wc.BlockName(rid); ok && isBlockNamePassable(name) {
		return false
	}

	b, ok := world.BlockByRuntimeID(rid)
	if !ok {
		return false
	}

	m := b.Model()
	if m == nil {
		return true
	}

	if _, isEmpty := m.(model.Empty); isEmpty {
		return false
	}

	boxes := m.BBox(cube.Pos{}, mockBlockSource{})
	return len(boxes) > 0
}

// BlockName returns the block name for a runtime ID, remembering the answer.
//
// Encoding a block state allocates both a name and a properties map, and the
// tree scanner alone asks for well over a hundred thousand of these per gather.
func (wc *WorldCache) BlockName(rid uint32) (string, bool) {
	rid = wc.TranslateRuntimeID(rid)

	wc.paletteMu.RLock()
	name, cached := wc.ridNames[rid]
	wc.paletteMu.RUnlock()
	if cached {
		return name, name != ""
	}

	name, _, ok := chunk.RuntimeIDToState(rid)

	wc.paletteMu.Lock()
	wc.ridNames[rid] = name
	wc.paletteMu.Unlock()
	return name, ok
}

// IsBlockSolid checks if the block at the given coordinates is solid.
func (wc *WorldCache) IsBlockSolid(x, y, z int32) (bool, bool) {
	rid, loaded := wc.GetBlockRID(x, y, z)
	if !loaded {
		return false, false
	}
	return wc.IsRIDSolid(rid), true
}

var passableExactNames = map[string]bool{
	"minecraft:air": true, "minecraft:water": true, "minecraft:flowing_water": true,
	"minecraft:lava": true, "minecraft:flowing_lava": true, "minecraft:ladder": true,
	"minecraft:tripwire": true, "minecraft:trip_wire": true, "minecraft:tripwire_hook": true,
	"minecraft:lever": true, "minecraft:wheat": true, "minecraft:carrots": true,
	"minecraft:potatoes": true, "minecraft:beetroots": true, "minecraft:nether_wart": true,
	"minecraft:sugar_cane": true, "minecraft:sweet_berry_bush": true, "minecraft:glow_lichen": true,
	"minecraft:vine": true, "minecraft:fire": true, "minecraft:poppy": true,
	"minecraft:dandelion": true, "minecraft:blue_orchid": true, "minecraft:allium": true,
	"minecraft:azure_bluet": true, "minecraft:red_tulip": true, "minecraft:orange_tulip": true,
	"minecraft:white_tulip": true, "minecraft:pink_tulip": true, "minecraft:oxeye_daisy": true,
	"minecraft:cornflower": true, "minecraft:lily_of_the_valley": true, "minecraft:wither_rose": true,
	"minecraft:sunflower": true, "minecraft:lilac": true, "minecraft:rose_bush": true,
	"minecraft:peony": true, "minecraft:pitcher_plant": true, "minecraft:torchflower": true,
	"minecraft:leaf_litter": true, "minecraft:pink_petals": true, "minecraft:wildflowers": true,
}

var passableSuffixes = []string{
	"_sign", "_button", "_sapling", "_pressure_plate",
}

type passableContains struct {
	sub     string
	exclude []string
}

var passableContainsList = []passableContains{
	{sub: "grass", exclude: []string{"block", "path"}},
	{sub: "fern"},
	{sub: "mushroom"},
	{sub: "roots"},
	{sub: "vines"},
	{sub: "carpet"},
	{sub: "coral", exclude: []string{"block"}},
	{sub: "crop"},
	{sub: "rail"},
	{sub: "torch"},
}

func isBlockNamePassable(name string) bool {
	if passableExactNames[name] {
		return true
	}
	for _, suffix := range passableSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	for _, pc := range passableContainsList {
		if strings.Contains(name, pc.sub) {
			for _, ex := range pc.exclude {
				if strings.Contains(name, ex) {
					return false
				}
			}
			return true
		}
	}
	return false
}
