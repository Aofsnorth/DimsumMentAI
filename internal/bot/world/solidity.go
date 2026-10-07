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
	return wc.isAirRID(rid), true
}

// isAirRID reports whether the runtime ID represents air across Bedrock editions.
func (wc *WorldCache) isAirRID(rid uint32) bool {
	wc.mu.RLock()
	serverAirs := wc.serverAirs
	wc.mu.RUnlock()
	if serverAirs != nil {
		// Merged server palette: air is whatever that table says, and the
		// chunk default fill (wc.airRID) was set to it. 13080 is a real block
		// in this space.
		return rid == wc.airRID || (int(rid) < len(serverAirs) && serverAirs[rid])
	}
	if rid == wc.airRID || rid == 13080 {
		return true
	}
	if wc.geyserCompat || !wc.useHashes {
		initPalette121()
		if int(rid) < len(palette121Airs) {
			return palette121Airs[rid]
		}
	}
	return false
}

// IsRIDSolid checks if the given block runtime ID is solid.
//
// The answer is cached per runtime ID. Resolving it the long way builds the
// block's collision boxes (world.BlockByRuntimeID → Model → BBox) and its block
// state (which allocates a name string and a properties map) — for every single
// cell query. Physics, pathfinding and every block scan ask this thousands of
// times a tick, and the palette is finite, so the answer is worth remembering.
func (wc *WorldCache) IsRIDSolid(rid uint32) bool {
	// Geyser / non-hash: rid is a server palette (network) index. It must be
	// resolved in that space — never translated into dragonfly's local one,
	// where the same number names a different block (chunk decodes and
	// UpdateBlock both keep network indices since TranslateRuntimeID is the
	// identity on these connections).
	if wc.geyserCompat || !wc.useHashes {
		wc.paletteMu.RLock()
		solid, cached := wc.ridSolid[rid]
		wc.paletteMu.RUnlock()
		if cached {
			return solid
		}
		solid = wc.resolveGeyserRIDSolid(rid)
		wc.paletteMu.Lock()
		wc.ridSolid[rid] = solid
		wc.paletteMu.Unlock()
		return solid
	}

	if chunk.RuntimeIDToState != nil {
		if _, _, ok := chunk.RuntimeIDToState(rid); !ok {
			rid = wc.TranslateRuntimeID(rid)
		}
	} else {
		rid = wc.TranslateRuntimeID(rid)
	}

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
	if wc.isAirRID(rid) {
		return false
	}

	// Server custom names first: the StartGame palette is the server's own
	// truth, including behavior-pack and proxy remaps that the embedded
	// vanilla palette_121 cannot know.
	if name, ok := wc.serverBlockName(rid); ok {
		return !isBlockNamePassable(name)
	}

	if wc.geyserCompat || !wc.useHashes {
		initPalette121()
		if int(rid) < len(palette121Solids) {
			return palette121Solids[rid]
		}
	}

	if name, ok := wc.BlockName(rid); ok && isBlockNamePassable(name) {
		return false
	}

	wc.mu.RLock()
	if int(rid) < len(wc.customBlockSolid) {
		solid := wc.customBlockSolid[rid]
		wc.mu.RUnlock()
		return solid
	}
	wc.mu.RUnlock()

	b, ok := world.BlockByRuntimeID(rid)
	if !ok {
		if wc.geyserCompat || !wc.useHashes {
			// Unknown RID on Geyser/proxy palettes: the palette_121 embedding
			// only covers vanilla indices, while proxy palettes can remap or
			// extend them (custom entries, behavior packs, stale indices).
			// Treating "unknown" as solid walls off the area: every empty
			// cell reads as a wall, canStandAt vetoes all neighbors, and A*
			// returns zero nodes — the spawn freeze in the stuck report.
			// Unknown is passable until the server proves otherwise.
			return false
		}
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

// resolveGeyserRIDSolid answers solidity for a server palette index on
// Geyser / non-hash connections, in network-index space only:
//  1. air (palette_121 air table + known network air indices);
//  2. the server's own StartGame palette name;
//  3. the embedded canonical Bedrock 1.21 palette;
//  4. an index no table recognizes is PASSABLE, never solid — treating
//     unknown as solid walls the bot in at spawn (the §11 A* deadlock).
func (wc *WorldCache) resolveGeyserRIDSolid(rid uint32) bool {
	if wc.isAirRID(rid) {
		return false
	}
	if name, ok := wc.serverBlockName(rid); ok {
		return !isBlockNamePassable(name)
	}
	initPalette121()
	if int(rid) < len(palette121Solids) {
		return palette121Solids[rid]
	}
	return false
}

// serverBlockName resolves a network palette index through the server's own
// StartGame palette, without falling through to embedded or local tables.
// The caller decides what an absent entry means; this only reports what the
// server said.
func (wc *WorldCache) serverBlockName(rid uint32) (string, bool) {
	wc.mu.RLock()
	defer wc.mu.RUnlock()
	if int(rid) < len(wc.customBlockNames) && wc.customBlockNames[rid] != "" {
		return wc.customBlockNames[rid], true
	}
	return "", false
}

// BlockName returns the block name for a runtime ID, remembering the answer.
//
// Encoding a block state allocates both a name and a properties map, and the
// tree scanner alone asks for well over a hundred thousand of these per gather.
func (wc *WorldCache) BlockName(rid uint32) (string, bool) {
	if wc.isAirRID(rid) {
		return "minecraft:air", true
	}

	// Server palette first: on Geyser / non-hash connections the SubChunk
	// payload already carries palette-121 network indices, and those numbers
	// do NOT match dragonfly's local RIDs. Feeding a raw network index into
	// chunk.RuntimeIDToState misnames the block (e.g. 13591 reads as
	// granite_wall locally while the server palette says mangrove_propagule),
	// and every downstream name check — fence/wall, half-block, hazard —
	// answers for the wrong block.
	if name, ok := wc.serverBlockName(rid); ok {
		return name, true
	}

	if wc.geyserCompat || !wc.useHashes {
		initPalette121()
		if int(rid) < len(palette121Names) {
			return palette121Names[rid], true
		}
		// No local-table fallback on Geyser: a network index fed to
		// chunk.RuntimeIDToState names a different real block, and every
		// downstream check (fence/wall, half-block, hazard) then answers for
		// the wrong one. Unknown stays unknown.
		return "", false
	}

	if chunk.RuntimeIDToState != nil {
		if _, _, ok := chunk.RuntimeIDToState(rid); !ok {
			rid = wc.TranslateRuntimeID(rid)
		}
	} else {
		rid = wc.TranslateRuntimeID(rid)
	}

	wc.paletteMu.RLock()
	name, cached := wc.ridNames[rid]
	wc.paletteMu.RUnlock()
	if cached {
		return name, name != ""
	}

	var ok bool
	if chunk.RuntimeIDToState != nil {
		name, _, ok = chunk.RuntimeIDToState(rid)
	}
	if !ok {
		wc.mu.RLock()
		if int(rid) < len(wc.customBlockNames) && wc.customBlockNames[rid] != "" {
			name = wc.customBlockNames[rid]
			ok = true
		}
		wc.mu.RUnlock()
	}

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
	"minecraft:deadbush": true, "minecraft:bush": true, "minecraft:firefly_bush": true,
	"minecraft:cactus_flower": true, "minecraft:waterlily": true, "minecraft:small_dripleaf_block": true,
	"minecraft:spore_blossom": true, "minecraft:pale_hanging_moss": true,
	"minecraft:redstone_wire":      true,
	"minecraft:unpowered_repeater": true, "minecraft:powered_repeater": true,
	"minecraft:unpowered_comparator": true, "minecraft:powered_comparator": true,
	"minecraft:yellow_flower":      true,
	"minecraft:red_flower":         true,
	"minecraft:double_plant":       true,
	"minecraft:tallgrass":          true,
	"minecraft:snow":               true,
	"minecraft:snow_layer":         true,
	"minecraft:web":                true,
	"minecraft:cobweb":             true,
	"minecraft:sea_pickle":         true,
	"minecraft:frogspawn":          true,
	"minecraft:hanging_roots":      true,
	"minecraft:mangrove_propagule": true,
	"minecraft:small_dripleaf":     true,
	"minecraft:big_dripleaf":       true,
	"minecraft:eyeblossom":         true,
	"minecraft:open_eyeblossom":    true,
	"minecraft:closed_eyeblossom":  true,
	"minecraft:sculk_vein":         true,
	"minecraft:candle":             true,
	"minecraft:white_candle":       true,
	"minecraft:candle_cake":        true,
}

var passableSuffixes = []string{
	"_sign", "_button", "_sapling", "_pressure_plate", "_banner",
}

type passableContains struct {
	sub     string
	exclude []string
}

var passableContainsList = []passableContains{
	{sub: "grass", exclude: []string{"block", "path"}},
	{sub: "fern"},
	{sub: "mushroom"},
	{sub: "fungus"},
	{sub: "roots"},
	{sub: "vines"},
	{sub: "carpet"},
	{sub: "coral", exclude: []string{"block"}},
	{sub: "crop"},
	{sub: "rail"},
	{sub: "torch"},
	{sub: "flower", exclude: []string{"pot", "block"}},
	{sub: "plant", exclude: []string{"pot", "block"}},
	{sub: "candle", exclude: []string{"block"}},
	{sub: "sprout"},
	{sub: "stem", exclude: []string{"stripped", "block"}},
	{sub: "snow", exclude: []string{"block"}},
	{sub: "dripleaf", exclude: []string{"block"}},
	{sub: "pickle"},
	{sub: "lichen"},
	{sub: "vein"},
	{sub: "web"},
	{sub: "bud", exclude: []string{"block"}},
	{sub: "cluster", exclude: []string{"block"}},
}

// solidExactNames are blocks that a generic substring rule would classify as
// passable even though they are full solid floor.
//
// minecraft:grass is the load-bearing one. Modern Bedrock renamed the grass
// block to minecraft:grass_block, but Geyser proxies still publish the legacy
// name minecraft:grass — and the generic "grass" rule (meant for the plant
// minecraft:short_grass / tallgrass) matched it, so every grass block decoded
// as a passable plant. The floor under the bot's feet vanished on grass terrain,
// hasGroundSupportAt went false, and the movement layer cancelled the step:
// the bot walked fine on stone and froze dead on a grass field.
var solidExactNames = map[string]bool{
	"minecraft:grass":      true,
	"minecraft:grass_path": true,
	"minecraft:dirt_path":  true,
}

// IsBlockNamePassable reports whether a block name is passable (non-solid).
func IsBlockNamePassable(name string) bool {
	if solidExactNames[name] {
		return false
	}
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

func isBlockNamePassable(name string) bool {
	return IsBlockNamePassable(name)
}

// TopOffsetFromState returns how far above the bottom of its cell a block's
// walkable surface sits. A full block is 1.0; a bottom slab is 0.5; a snow
// layer is 0.15.
//
// Movement needs this because the cell a body rests on does not tell it the
// height of the surface. Grounding a body on a slab against a full-block top
// face (cell+1.0) puts the feet 0.5 below the real surface, which reads as
// permanently airborne: gravity wins every tick, the wall scan cancels the
// forward step, and the bot cannot cross a slab path no matter how long it
// tries.
func TopOffsetFromState(name string, props map[string]any) float32 {
	switch {
	case strings.Contains(name, "slab"):
		// A double slab is a full block whatever its half state says.
		if strings.Contains(name, "double_slab") {
			return 1.0
		}
		if top, ok := boolState(props, "top_half_bit"); ok && top {
			return 1.0
		}
		// 1.21 palettes name the half with a string, not top_half_bit.
		if half, ok := props["minecraft:vertical_half"].(string); ok && half == "top" {
			return 1.0
		}
		return 0.5
	case strings.Contains(name, "stairs"):
		// A stair's upper step fills the cell, and A* models stair nodes at
		// full-cell level. Grounding at 1.0 matches the route; the lower step
		// is reached by the step-up that precedes it.
		return 1.0
	case name == "minecraft:snow_layer" || name == "minecraft:snow":
		return 0.15
	case strings.Contains(name, "carpet"):
		return 0.0625
	case strings.Contains(name, "trapdoor"):
		return 0.1875
	}
	return 1.0
}

func boolState(props map[string]any, key string) (value bool, ok bool) {
	if props == nil {
		return false, false
	}
	v, present := props[key]
	if !present {
		return false, false
	}
	switch t := v.(type) {
	case bool:
		return t, true
	case uint8:
		return t == 1, true
	case int32:
		return t == 1, true
	case int:
		return t == 1, true
	}
	return false, false
}

// TopOffsetAt returns the walkable surface height of the block at (x, y, z)
// relative to the cell's bottom, defaulting to a full block. Air and unloaded
// cells also report 1.0: neither is a partial floor, and defaulting keeps the
// caller's arithmetic unchanged for everything the bot has not decoded.
func (wc *WorldCache) TopOffsetAt(x, y, z int32) float32 {
	rid, loaded := wc.GetBlockRID(x, y, z)
	if !loaded {
		return 1.0
	}
	return wc.ridTopOffset(rid)
}

func (wc *WorldCache) ridTopOffset(rid uint32) float32 {
	wc.mu.RLock()
	geyser := wc.geyserCompat || !wc.useHashes
	useHashes := wc.useHashes
	customTops := wc.customBlockTops
	wc.mu.RUnlock()

	if geyser {
		if int(rid) < len(customTops) {
			return customTops[rid]
		}
		initPalette121()
		if int(rid) < len(palette121Tops) {
			return palette121Tops[rid]
		}
		return 1.0
	}
	if !useHashes || chunk.RuntimeIDToState == nil {
		return 1.0
	}
	if _, props, ok := chunk.RuntimeIDToState(rid); ok {
		name, _, _ := chunk.RuntimeIDToState(rid)
		return TopOffsetFromState(name, props)
	}
	return 1.0
}
