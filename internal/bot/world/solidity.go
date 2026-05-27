package world

import (
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

	rid := wc.TranslateRuntimeID(c.Block(uint8(x&0xf), int16(y), uint8(z&0xf), 0))
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
func (wc *WorldCache) IsRIDSolid(rid uint32) bool {
	rid = wc.TranslateRuntimeID(rid)
	name, _, ok := chunk.RuntimeIDToState(rid)
	if ok && isBlockNamePassable(name) {
		return false
	}

	b, ok := world.BlockByRuntimeID(rid)
	if !ok {
		return true
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

// IsBlockSolid checks if the block at the given coordinates is solid.
func (wc *WorldCache) IsBlockSolid(x, y, z int32) (bool, bool) {
	rid, loaded := wc.GetBlockRID(x, y, z)
	if !loaded {
		return false, false
	}
	return wc.IsRIDSolid(rid), true
}

func isBlockNamePassable(name string) bool {
	if strings.HasSuffix(name, "_sign") ||
		strings.HasSuffix(name, "_button") ||
		strings.HasSuffix(name, "_sapling") ||
		strings.HasSuffix(name, "_pressure_plate") ||
		(strings.Contains(name, "grass") && !strings.Contains(name, "block") && !strings.Contains(name, "path")) ||
		strings.Contains(name, "fern") ||
		strings.Contains(name, "mushroom") ||
		strings.Contains(name, "roots") ||
		strings.Contains(name, "vines") ||
		strings.Contains(name, "carpet") ||
		(strings.Contains(name, "coral") && !strings.Contains(name, "block")) ||
		strings.Contains(name, "crop") ||
		strings.Contains(name, "rail") ||
		strings.Contains(name, "torch") {
		return true
	}

	switch name {
	case "minecraft:air", "minecraft:water", "minecraft:flowing_water", "minecraft:lava", "minecraft:flowing_lava",
		"minecraft:ladder", "minecraft:tripwire", "minecraft:trip_wire", "minecraft:tripwire_hook", "minecraft:lever",
		"minecraft:wheat", "minecraft:carrots", "minecraft:potatoes", "minecraft:beetroots", "minecraft:nether_wart",
		"minecraft:sugar_cane", "minecraft:sweet_berry_bush", "minecraft:glow_lichen", "minecraft:vine", "minecraft:fire",
		"minecraft:poppy", "minecraft:dandelion", "minecraft:blue_orchid", "minecraft:allium", "minecraft:azure_bluet",
		"minecraft:red_tulip", "minecraft:orange_tulip", "minecraft:white_tulip", "minecraft:pink_tulip",
		"minecraft:oxeye_daisy", "minecraft:cornflower", "minecraft:lily_of_the_valley", "minecraft:wither_rose",
		"minecraft:sunflower", "minecraft:lilac", "minecraft:rose_bush", "minecraft:peony", "minecraft:pitcher_plant",
		"minecraft:torchflower":
		return true
	}
	return false
}
