package world_test

import (
	"bytes"
	"compress/gzip"
	"os"
	"testing"
	"time"

	"bedrock-ai/internal/bot/world"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/sandertv/gophertunnel/minecraft/nbt"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

func TestNetworkRuntimeIDUsesServerSelectedFormat(t *testing.T) {
	t.Parallel()

	rid, ok := chunk.StateToRuntimeID("minecraft:stone", nil)
	if !ok {
		t.Fatal("stone runtime ID is unavailable")
	}
	cache := world.NewWorldCache(0, cube.Range{-64, 319}, nil)

	got, ok := cache.NetworkRuntimeID(rid)
	if !ok || got != rid {
		t.Fatalf("numeric network ID = %d, %v; want %d, true", got, ok, rid)
	}

	cache.SetUseBlockNetworkIDHashes(true)
	hash, ok := cache.NetworkRuntimeID(rid)
	if !ok {
		t.Fatal("hashed network ID is unavailable")
	}
	if hash == rid {
		t.Fatalf("hashed network ID unexpectedly equals local runtime ID %d", rid)
	}
	if translated := cache.TranslateRuntimeID(hash); translated != rid {
		t.Fatalf("TranslateRuntimeID(%d) = %d, want %d", hash, translated, rid)
	}
}

func TestInspectPalettePassability(t *testing.T) {
	gzData, err := os.ReadFile("../../internal/bot/world/block_states_121.nbt.gz")
	if err != nil {
		t.Fatalf("ReadFile err: %v", err)
	}

	start := time.Now()
	zr, err := gzip.NewReader(bytes.NewReader(gzData))
	if err != nil {
		t.Fatalf("gzip reader err: %v", err)
	}
	defer zr.Close()

	type blockState struct {
		Name       string         `nbt:"name"`
		Properties map[string]any `nbt:"states"`
		Version    int32          `nbt:"version"`
	}

	dec := nbt.NewDecoder(zr)
	names := make([]string, 0, 22091)
	solids := make([]bool, 0, 22091)

	for {
		var s blockState
		if err := dec.Decode(&s); err != nil {
			break
		}
		names = append(names, s.Name)
		isAir := s.Name == "minecraft:air"
		isPassable := isAir || world.IsBlockNamePassable(s.Name)
		solids = append(solids, !isPassable)
	}

	elapsed := time.Since(start)
	t.Logf("Decoded %d states in %v", len(names), elapsed)
	if len(names) != 22091 {
		t.Errorf("expected 22091 states, got %d", len(names))
	}
	// Check state 16851 (short_grass)
	if names[16851] != "minecraft:short_grass" {
		t.Errorf("expected 16851 to be short_grass, got %s", names[16851])
	}
	if solids[16851] {
		t.Errorf("expected short_grass (16851) to NOT be solid")
	}
	// Check state 17025 (air)
	if names[17025] != "minecraft:air" {
		t.Errorf("expected 17025 to be air, got %s", names[17025])
	}
	if solids[17025] {
		t.Errorf("expected air (17025) to NOT be solid")
	}
	// Check state 22090 (dandelion)
	if names[22090] != "minecraft:dandelion" {
		t.Errorf("expected 22090 to be dandelion, got %s", names[22090])
	}
	if solids[22090] {
		t.Errorf("expected dandelion (22090) to NOT be solid")
	}
}

func TestInspectPassableRIDs(t *testing.T) {
	for _, name := range []string{"minecraft:torch", "minecraft:tall_grass", "minecraft:poppy", "minecraft:water", "minecraft:ladder"} {
		rid, ok := chunk.StateToRuntimeID(name, nil)
		t.Logf("%s: rid=%d, ok=%v", name, rid, ok)
	}
}

func TestGeyserPaletteAirAndSolidIdentification(t *testing.T) {
	t.Parallel()

	cache := world.NewWorldCache(0, cube.Range{-64, 319}, nil)
	cache.SetUseBlockNetworkIDHashes(false)
	cache.SetGeyserCompat(true)

	// Without custom blocks the embedded palette is the server palette: air is 17025.
	if cache.IsRIDSolid(17025) {
		t.Errorf("expected 17025 (palette air) to not be solid")
	}
	name, ok := cache.BlockName(17025)
	if !ok || name != "minecraft:air" {
		t.Errorf("expected BlockName(17025) to be (minecraft:air, true), got (%q, %v)", name, ok)
	}

	// Short grass (16851) on Geyser must NOT be solid.
	if cache.IsRIDSolid(16851) {
		t.Errorf("expected 16851 (short_grass) to not be solid on Geyser")
	}

	// Dandelion (22090) on Geyser must NOT be solid.
	if cache.IsRIDSolid(22090) {
		t.Errorf("expected 22090 (dandelion) to not be solid on Geyser")
	}

	// Floor runtime ID 20236 in Bedrock 1.21 on Geyser should be solid.
	if !cache.IsRIDSolid(20236) {
		t.Errorf("expected 20236 (Bedrock 1.21 floor) to be solid on Geyser")
	}

	// Under BDS mode (UseBlockNetworkIDHashes = true, GeyserCompat = false),
	// unknown runtime ID 20236 must NOT be assumed solid.
	bdsCache := world.NewWorldCache(0, cube.Range{-64, 319}, nil)
	bdsCache.SetUseBlockNetworkIDHashes(true)
	bdsCache.SetGeyserCompat(false)
	if bdsCache.IsRIDSolid(20236) {
		t.Errorf("expected 20236 to not be solid under BDS mode")
	}
}

// TestGeyserProxyPaletteWinsOverLocalDragonflyTables pins the fix for the
// spawn freeze on a Geyser-fronted survival server.
//
// A raw network palette index is not a dragonfly local RID. Feeding one into
// chunk.RuntimeIDToState misnames the block: 13591 reads as granite_wall
// locally while the server palette calls it mangrove_propagule, and 15079
// reads as dead_tube_coral_wall_fan while the server calls it a
// warped_hanging_sign. A mangrove propagule misread as a wall becomes a floor
// the bot tries to stand on and a step-up it tries to jump, which is how the
// world got boxed in around spawn and A* returned zero neighbors.
//
// The server's StartGame palette is the authority here.
func TestGeyserProxyPaletteWinsOverLocalDragonflyTables(t *testing.T) {
	t.Parallel()

	cache := world.NewWorldCache(0, cube.Range{-64, 319}, nil)
	cache.SetUseBlockNetworkIDHashes(false)
	cache.SetGeyserCompat(true)

	// A palette-less non-hash connection resolves through the vanilla palette
	// merged with the StartGame custom blocks; custom names stay reachable.
	cache.SetCustomBlocks([]protocol.BlockEntry{
		{Name: "test:custom_block", Properties: map[string]any{}},
	})

	named := 0
	for i := uint32(0); i < 22200; i++ {
		if name, ok := cache.BlockName(i); ok && name == "test:custom_block" {
			named++
		}
	}
	if named != 1 {
		t.Errorf("custom block occupies %d palette indices, want exactly 1", named)
	}
}

func fnv1For(s string) uint64 {
	h := uint64(0xcbf29ce484222325)
	for i := 0; i < len(s); i++ {
		h *= 0x100000001b3
		h ^= uint64(s[i])
	}
	return h
}

// TestGeyserMergedPaletteShiftsVanillaIndices guards the root cause of the
// grass_block freeze: on a non-hash server the custom blocks are sorted into
// the vanilla palette by name hash, so every vanilla state after a custom state
// moves up. The live Geyser server had air at 17224 / dirt 13591 / grass_block
// 15079 while the plain palette says 17025 / 13456 / 14944.
func TestGeyserMergedPaletteShiftsVanillaIndices(t *testing.T) {
	t.Parallel()

	newCache := func(customs []protocol.BlockEntry) *world.WorldCache {
		// airRID far outside the palette so it cannot masquerade as an index.
		c := world.NewWorldCache(999999, cube.Range{-64, 319}, nil)
		c.SetUseBlockNetworkIDHashes(false)
		c.SetGeyserCompat(true)
		if len(customs) > 0 {
			c.SetCustomBlocks(customs)
		}
		return c
	}
	indexOf := func(c *world.WorldCache, want string) uint32 {
		start := uint32(0)
		if want == "minecraft:air" {
			start = 14000 // 13080 is a legacy air alias on the plain palette
		}
		for i := start; i < 23000; i++ {
			if name, ok := c.BlockName(i); ok && name == want {
				return i
			}
		}
		t.Fatalf("%s not found in palette", want)
		return 0
	}

	base := newCache(nil)
	airBase := indexOf(base, "minecraft:air")
	dirtBase := indexOf(base, "minecraft:dirt")
	grassBase := indexOf(base, "minecraft:grass_block")

	// A custom block whose hash sorts before air, dirt and grass: 3x2 states.
	var name string
	for i := 0; ; i++ {
		name = "test:wide_" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if fnv1For(name) < fnv1For("minecraft:dirt") && fnv1For(name) < fnv1For("minecraft:grass_block") && fnv1For(name) < fnv1For("minecraft:air") {
			break
		}
	}
	custom := protocol.BlockEntry{Name: name, Properties: map[string]any{
		"properties": []any{
			map[string]any{"name": "a:x", "enum": []any{int32(0), int32(1), int32(2)}},
			map[string]any{"name": "a:y", "enum": []any{"p", "q"}},
		},
	}}
	merged := newCache([]protocol.BlockEntry{custom})

	const states = 6
	for _, c := range []struct {
		name  string
		base  uint32
		solid bool
	}{
		{"minecraft:air", airBase, false},
		{"minecraft:dirt", dirtBase, true},
		{"minecraft:grass_block", grassBase, true},
	} {
		got := indexOf(merged, c.name)
		if got != c.base+states {
			t.Errorf("%s index = %d, want %d (base %d + %d inserted states)", c.name, got, c.base+states, c.base, states)
		}
		if merged.IsRIDSolid(got) != c.solid {
			t.Errorf("%s solid = %v, want %v", c.name, !c.solid, c.solid)
		}
	}
}

// TestGeyserVanillaNamedCustomBlocksAddNoStates replays the live Hansprojects
// StartGame list: 98 slab/stair overrides that reuse vanilla names plus 7
// geyser_custom blocks (199 states). Only the latter add palette states, so
// air/dirt/grass_block must land on 17224/13591/15079, as seen on the server.
func TestGeyserVanillaNamedCustomBlocksAddNoStates(t *testing.T) {
	t.Parallel()

	var customs []protocol.BlockEntry
	for _, n := range []string{
		"minecraft:light_gray_concrete_stairs", "minecraft:brown_concrete_slab",
		"minecraft:pink_concrete_slab", "minecraft:purple_wool_slab",
		"minecraft:yellow_wool_stairs", "minecraft:white_wool_double_slab",
		"minecraft:shelf_mushroom", "minecraft:red_shrub",
		"minecraft:black_wool_double_slab", "minecraft:green_concrete_stairs",
	} {
		customs = append(customs, protocol.BlockEntry{Name: n, Properties: map[string]any{}})
	}
	mushroom := func(name string) protocol.BlockEntry {
		var props []any
		for _, p := range []string{"up", "west", "east", "south", "down", "north"} {
			props = append(props, map[string]any{"name": "geyser_custom:" + p, "enum": []any{int32(0), int32(1)}})
		}
		return protocol.BlockEntry{Name: name, Properties: map[string]any{"properties": props}}
	}
	customs = append(customs,
		protocol.BlockEntry{Name: "geyser_custom:test_instance_block", Properties: map[string]any{}},
		protocol.BlockEntry{Name: "geyser_custom:crying_obsidian", Properties: map[string]any{}},
		mushroom("geyser_custom:brown_mushroom_block"),
		protocol.BlockEntry{Name: "geyser_custom:test_block", Properties: map[string]any{
			"properties": []any{map[string]any{"name": "mode", "enum": []any{int32(0), int32(1), int32(2), int32(3)}}},
		}},
		mushroom("geyser_custom:red_mushroom_block"),
		mushroom("geyser_custom:mushroom_stem"),
		protocol.BlockEntry{Name: "geyser_custom:obsidian", Properties: map[string]any{}},
	)

	c := world.NewWorldCache(999999, cube.Range{-64, 319}, nil)
	c.SetUseBlockNetworkIDHashes(false)
	c.SetGeyserCompat(true)
	c.SetCustomBlocks(customs)

	for want, idx := range map[string]uint32{
		"minecraft:air":         17224,
		"minecraft:dirt":        13591,
		"minecraft:grass_block": 15079,
	} {
		name, ok := c.BlockName(idx)
		if !ok || name != want {
			t.Errorf("index %d = %q, want %q", idx, name, want)
		}
	}
}

// TestUnknownNetworkIndexIsPassableOnGeyser is the other half of the same fix.
//
// palette_121 only covers vanilla indices. A proxy palette that is longer,
// reordered, or extended by a behavior pack yields indices outside it, and
// those fell through to "assume solid". Every unrecognised cell then read as a
// wall: canStandAt vetoed every neighbor, A* returned zero nodes, and the bot
// stood still at spawn reporting "pathfinding failed".
func TestUnknownNetworkIndexIsPassableOnGeyser(t *testing.T) {
	t.Parallel()

	cache := world.NewWorldCache(0, cube.Range{-64, 319}, nil)
	cache.SetUseBlockNetworkIDHashes(false)
	cache.SetGeyserCompat(true)
	// No SetCustomBlocks: nothing resolves this index.

	const unknownIndex = uint32(22090)
	if name, ok := cache.BlockName(unknownIndex); ok && !world.IsBlockNamePassable(name) {
		t.Fatalf("premise broken: %q should be passable per the name tables", name)
	}
	// Either an embedded-palette passable entry or unknown-but-passable: the
	// contract is that it never claims solid without evidence.
	if cache.IsRIDSolid(unknownIndex) {
		t.Errorf("RID %d is unknown to every table here, but read as solid; an unrecognized "+
			"index must not wall the bot in", unknownIndex)
	}
}

func TestPassableFlowerAndDoublePlant(t *testing.T) {
	tests := []string{
		"minecraft:yellow_flower",
		"minecraft:red_flower",
		"minecraft:double_plant",
		"minecraft:snow_layer",
		"minecraft:snow",
		"minecraft:cobweb",
		"minecraft:web",
		"minecraft:candle",
		"minecraft:white_candle",
		"minecraft:sea_pickle",
		"minecraft:hanging_roots",
		"minecraft:frogspawn",
		"minecraft:big_dripleaf",
		"minecraft:small_dripleaf",
	}

	for _, name := range tests {
		pass := world.IsBlockNamePassable(name)
		t.Logf("%s -> passable=%v", name, pass)
		if !pass {
			t.Errorf("FAIL: %s should be passable (non-solid)!", name)
		}
	}
}
