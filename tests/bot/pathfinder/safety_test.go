package pathfinder_test

import (
	"strconv"
	"testing"
	"time"

	"bedrock-ai/internal/bot/pathfinder"
)

// Ground safety is a vocabulary the whole walker inherits through IsHazard.
// These tests pin the answers that vocabulary gives, because every movement
// mode — walk, drop, parkour, step-jump, diagonal, scaffold, ladder — reads
// that one function, and a wrong answer here is a bot that walks into lava
// through whichever rule forgot to check.

// nameQuerier answers block lookups from a name map, so the tests can write
// vanilla block names without inventing runtime IDs.
//
// It implements BlockName because that is the seam the real world resolves
// names through: GetBlockRID hands back a wire ID and blockNameFor asks the
// cache what that ID is called. A fake that returned a name from GetBlockRID
// would be testing a path the production bot never takes.
type nameQuerier struct {
	blocks map[string]uint32
	names  map[uint32]string
	next   uint32
}

func (f *nameQuerier) GetBlockRID(x, y, z int32) (uint32, bool) {
	rid, ok := f.blocks[blockKeyFor(x, y, z)]
	return rid, ok
}

func (f *nameQuerier) BlockName(rid uint32) (string, bool) {
	name, ok := f.names[rid]
	return name, ok
}

func (f *nameQuerier) IsBlockAir(x, y, z int32) (bool, bool) {
	name, ok := f.namesOf(x, y, z)
	return !ok || name == "minecraft:air", ok
}

func (f *nameQuerier) IsBlockSolid(x, y, z int32) (bool, bool) {
	name, ok := f.namesOf(x, y, z)
	return ok && name != "minecraft:air", ok
}

func (f *nameQuerier) namesOf(x, y, z int32) (string, bool) {
	rid, ok := f.blocks[blockKeyFor(x, y, z)]
	if !ok {
		return "", false
	}
	name, ok := f.names[rid]
	return name, ok
}

func blockKeyFor(x, y, z int32) string {
	return strconv.FormatInt(int64(x), 10) + "," +
		strconv.FormatInt(int64(y), 10) + "," +
		strconv.FormatInt(int64(z), 10)
}

// safetyModel builds a world model backed by a map of cell to block name,
// assigning each distinct name its own runtime ID the way a server would.
func safetyModel(cells map[string]string) *pathfinder.LocalWorldModel {
	q := &nameQuerier{
		blocks: make(map[string]uint32, len(cells)),
		names:  make(map[uint32]string, len(cells)),
	}
	byName := make(map[string]uint32, len(cells))
	for cell, name := range cells {
		rid, seen := byName[name]
		if !seen {
			q.next++
			rid = q.next
			byName[name] = rid
			q.names[rid] = name
		}
		q.blocks[cell] = rid
	}
	w := pathfinder.NewLocalWorldModel()
	w.SetChunkQuerier(q)
	return w
}

// TestLethalBlocksAreHazards is the inheritance test. IsHazard is what every
// movement rule already calls, so a lethal block added here is unreachable
// through all of them without touching any of them.
func TestLethalBlocksAreHazards(t *testing.T) {
	t.Parallel()

	// The ones that were already covered, plus the ones a survival bot walks
	// into in ordinary play: a campfire it built yesterday, a berry bush it
	// cannot see past, magma it should have read as a floor.
	lethal := []string{
		"minecraft:lava",
		"minecraft:flowing_lava",
		"minecraft:fire",
		"minecraft:magma_block",
		"minecraft:campfire",
		"minecraft:cactus",
		"minecraft:sweet_berry_bush",
		"minecraft:cobweb",
		"minecraft:end_portal",
	}

	for _, name := range lethal {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := safetyModel(map[string]string{blockKeyFor(4, 64, 0): name})
			if !w.IsHazard(4, 64, 0) {
				t.Errorf("%s is not a hazard", name)
			}
			if !w.IsLethal(4, 64, 0) {
				t.Errorf("%s is not reported lethal", name)
			}
		})
	}
}

// TestOrdinaryTerrainIsNotLethal is the other half, and the one that keeps the
// safety list from swallowing the world. A bot that refuses to walk through
// grass cannot cross a plain, which is worse than the problem being solved.
func TestOrdinaryTerrainIsNotLethal(t *testing.T) {
	t.Parallel()

	passable := []string{
		"minecraft:stone",
		"minecraft:dirt",
		"minecraft:oak_log",
		"minecraft:cobblestone",
		"minecraft:tall_grass",
		"minecraft:sand",
		"minecraft:oak_leaves",
		"minecraft:water",
	}

	for _, name := range passable {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := safetyModel(map[string]string{blockKeyFor(4, 64, 0): name})
			if w.IsHazard(4, 64, 0) {
				t.Errorf("%s is treated as a hazard; the bot would refuse to walk on it", name)
			}
		})
	}
}

// TestTheVoidIsAHazard pins the check that has no block to name. Below the world
// floor there is nothing to fall onto, and the world model answers "not solid"
// for that cell exactly as it does for a cell it has never heard of.
func TestTheVoidIsAHazard(t *testing.T) {
	t.Parallel()

	w := pathfinder.NewLocalWorldModel()

	if !pathfinder.IsVoid(-65) {
		t.Error("-65 is below the world floor and should be void")
	}
	if pathfinder.IsVoid(-64) {
		t.Error("the world floor itself is not void")
	}
	if pathfinder.IsVoid(64) {
		t.Error("sea level is not void")
	}
	if !w.IsHazard(0, -70, 0) {
		t.Error("a cell below the world is not a hazard; the bot will walk off the edge")
	}
	// Unloaded or not, the arithmetic holds: this is not a data question.
	if !w.IsLethal(0, -70, 0) {
		t.Error("the void is not reported as lethal")
	}
}

// TestUnloadedCellsAreNotHazards keeps the safety net from becoming a cage. A
// cell the bot has no data for is a gap in its knowledge, not evidence of
// death, and refusing to move into undecoded chunks would root it in place.
func TestUnloadedCellsAreNotHazards(t *testing.T) {
	t.Parallel()

	w := safetyModel(map[string]string{})

	if w.IsHazard(500, 64, 500) {
		t.Error("a cell with no data was treated as a hazard")
	}
	if w.IsLethal(500, 64, 500) {
		t.Error("a cell with no data was reported lethal")
	}
}

// TestHeadCellIsOneBlockUp pins the arithmetic the breath reflex rests on. Bedrock
// eye height is 1.62, so the head occupies the cell one above the feet; getting
// this wrong means a bot that surfaces from a puddle or drowns in a metre of
// water.
func TestHeadCellIsOneBlockUp(t *testing.T) {
	t.Parallel()

	hx, hy, hz := pathfinder.HeadCell(10, 64, -3)
	if hx != 10 || hy != 65 || hz != -3 {
		t.Errorf("HeadCell = %d,%d,%d, want 10,65,-3", hx, hy, hz)
	}
}

// TestWaterIsWater covers the breath reflex's only input.
func TestWaterIsWater(t *testing.T) {
	t.Parallel()

	w := safetyModel(map[string]string{
		blockKeyFor(0, 65, 0): "minecraft:water",
		blockKeyFor(0, 64, 0): "minecraft:stone",
	})

	if !w.IsWater(0, 65, 0) {
		t.Error("water was not recognised as water")
	}
	if w.IsWater(0, 64, 0) {
		t.Error("stone under the feet was reported as water")
	}
	// Waist-deep is still breathable: only the head cell decides.
	if w.IsWater(0, 64, 0) != false {
		t.Error("standing in water should not read as submerged")
	}
}

// TestCornerArcNeedsTheWholeWayClear is the corner rule. A body is a block
// wide, so rounding a corner means every cell on the way has to be free at once.
// The version this replaces tested a single midpoint, which let a two-block
// lateral swing clear a one-block gap and a wall at the same time.
func TestCornerArcNeedsTheWholeWayClear(t *testing.T) {
	t.Parallel()

	t.Run("open corner is allowed", func(t *testing.T) {
		t.Parallel()
		w := safetyModel(map[string]string{
			blockKeyFor(1, 63, 0): "minecraft:stone", // floor
			blockKeyFor(1, 64, 0): "minecraft:stone", // the wall being rounded
		})
		if !w.CornerArcClear(0, 64, 0, 1, 1) {
			t.Error("a clear corner was refused")
		}
	})

	t.Run("wall in the middle of the swing is refused", func(t *testing.T) {
		t.Parallel()
		w := safetyModel(map[string]string{
			blockKeyFor(0, 64, 1): "minecraft:stone", // lands between bot and target
		})
		if w.CornerArcClear(0, 64, 0, 0, 2) {
			t.Error("a swing through a wall was allowed; this is the bug the rule exists for")
		}
	})
}

// TestResetForgetsTheOldWorld is the dimension-travel guard. The model's
// overrides are learned facts about a specific place — this block was mined,
// that one placed, this cell is a hazard — and every one of them is about a
// world the bot is no longer standing in.
//
// This is the failure it prevents: arriving in the Nether already certain that
// certain cells are solid, and the pathfinder believing it before a single
// Nether block has arrived.
func TestResetForgetsTheOldWorld(t *testing.T) {
	t.Parallel()

	w := pathfinder.NewLocalWorldModel()
	w.SetSolid(1, 64, 1, true)  // "I mined this, so it is now air"
	w.SetSolid(2, 64, 2, false) // "I placed this, so it is passable"
	w.SetHazard(3, 64, 3, true) // "I learned this cell is dangerous"
	w.SetTempSolid(4, 64, 4, time.Hour)
	w.SetBodyClearance(5, 64, 5)

	w.Reset()

	if w.HasSolidOverride(1, 64, 1) {
		t.Error("a mined-block override survived the reset; the bot still believes it is air")
	}
	if w.HasPassableOverride(2, 64, 2) {
		t.Error("a placed-block override survived the reset")
	}
	if w.HasHazardOverride(3, 64, 3) {
		t.Error("a learned hazard survived the reset")
	}
	if w.HasTempSolidOverride(4, 64, 4) {
		t.Error("a stuck-recovery marker survived the reset")
	}
	if w.HasBodyClearance(5, 64, 5) {
		t.Error("a body-clearance mark survived the reset")
	}
}

// TestResetKeepsTheQuerierAndTheTrip checks what Reset must NOT throw away. The
// chunk querier is where to ask, and the path bounds are what the current trip
// is; neither is a fact about a world, and dropping the querier would leave the
// bot unable to see anything at all.
func TestResetKeepsTheQuerierAndTheTrip(t *testing.T) {
	t.Parallel()

	w := safetyModel(map[string]string{blockKeyFor(0, 64, 0): "minecraft:obsidian"})
	w.SetPathBounds(pathfinder.Node{X: 0, Y: 64, Z: 0}, pathfinder.Node{X: 10, Y: 64, Z: 10})

	w.Reset()

	if !w.HasChunkQuerier() {
		t.Error("Reset dropped the chunk querier; the bot would be blind to its own world")
	}
	if !w.HasPathBounds() {
		t.Error("Reset dropped the path bounds; the current trip was forgotten")
	}
}
