package movement_test

import (
	"io"
	"log/slog"
	"testing"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/pathfinder"
	"bedrock-ai/internal/bot/world"

	"github.com/df-mc/dragonfly/server/block/cube"
	"github.com/go-gl/mathgl/mgl32"
)

const slabAirRID = 999999

// slabRIDs finds a bottom stone slab and a full stone block in the embedded
// Geyser palette by name and surface height, so the test does not hard-code
// palette indices.
func slabRIDs(t *testing.T, c *world.WorldCache) (slab, full uint32) {
	t.Helper()
	foundSlab, foundFull := false, false
	for rid := uint32(0); rid < 22091 && !(foundSlab && foundFull); rid++ {
		name, ok := c.BlockName(rid)
		if !ok {
			continue
		}
		switch name {
		case "minecraft:normal_stone_slab":
			if !foundSlab {
				c.SetBlockRID(0, 0, 0, rid)
				if c.TopOffsetAt(0, 0, 0) == 0.5 {
					slab, foundSlab = rid, true
				}
			}
		case "minecraft:stone":
			if !foundFull {
				full, foundFull = rid, true
			}
		}
	}
	c.SetBlockRID(0, 0, 0, slabAirRID)
	if !foundSlab || !foundFull {
		t.Skip("embedded palette has no bottom stone slab / stone")
	}
	return slab, full
}

func newSlabBot(t *testing.T) (*bot.Bot, uint32, uint32) {
	t.Helper()
	cache := world.NewWorldCache(slabAirRID, cube.Range{-64, 319}, nil)
	cache.SetUseBlockNetworkIDHashes(false)
	cache.SetGeyserCompat(true)
	model := pathfinder.NewLocalWorldModel()
	model.SetChunkQuerier(cache)
	b := &bot.Bot{
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		WorldModel: model,
		WorldCache: cache,
	}
	slab, full := slabRIDs(t, cache)
	return b, slab, full
}

// A body resting on a bottom slab must read as one level above the slab's
// cell: that is where A* put it. The raw floor(feet+0.1) read the slab's own
// cell, so every route node looked a level too high and the bot hopped
// forever over the slab.
func TestPathLevelOnSlabMatchesPlanner(t *testing.T) {
	t.Parallel()

	b, slab, full := newSlabBot(t)
	b.WorldCache.SetBlockRID(5, 70, 5, slab)
	b.WorldCache.SetBlockRID(7, 70, 5, full)

	if got := movement.PathLevelYForTest(b, mgl32.Vec3{5.5, 70.5, 5.5}); got != 71 {
		t.Errorf("on a slab at y=70: level = %d, want 71", got)
	}
	if got := movement.PathLevelYForTest(b, mgl32.Vec3{7.5, 71.0, 5.5}); got != 71 {
		t.Errorf("on a full block at y=70: level = %d, want 71", got)
	}
}

// The per-tick body clearance used to mark the slab under the feet as
// non-solid, so the bot was never grounded on it and bobbed forever.
func TestBodyClearanceKeepsSlabUnderFeetSolid(t *testing.T) {
	t.Parallel()

	b, slab, _ := newSlabBot(t)
	b.WorldCache.SetBlockRID(5, 70, 5, slab)
	feet := mgl32.Vec3{5.5, 70.5, 5.5}

	movement.ApplyBodyClearanceForTest(b, feet)

	if !b.WorldModel.IsSolid(5, 70, 5) {
		t.Fatal("slab under the feet reads as non-solid while the body clearance is applied")
	}
	if b.WorldModel.IsSolid(5, 71, 5) || b.WorldModel.IsSolid(5, 72, 5) {
		t.Error("body cells above the slab must stay cleared")
	}
	if got := movement.PathLevelYForTest(b, feet); got != 71 {
		t.Errorf("path level on slab with clearance = %d, want 71", got)
	}
	if !movement.IsGroundedInWorldModelForTest(b, feet) {
		t.Error("bot on a slab is not grounded")
	}

	// Sunk into the slab (below its top): the clearance must still free the body.
	movement.ApplyBodyClearanceForTest(b, mgl32.Vec3{5.5, 70.2, 5.5})
	if b.WorldModel.IsSolid(5, 70, 5) {
		t.Error("feet buried in the slab must still be cleared")
	}
}

// Walking from a slab onto a full block half a block higher (or from full
// ground onto a slab) must lift the body instead of burying it in the block.
func TestStepUpAssistLiftsOntoHalfBlockRise(t *testing.T) {
	t.Parallel()

	b, slab, full := newSlabBot(t)
	b.WorldCache.SetBlockRID(5, 70, 5, slab)
	b.WorldCache.SetBlockRID(4, 70, 5, full)

	tc := &movement.TickContext{
		B:          b,
		CurrPos:    mgl32.Vec3{5.1, 70.5, 5.5},
		NextY:      70.5,
		IsGrounded: true,
	}
	got := tc.ApplyStepUpAssistForTest(mgl32.Vec3{4.9, 70.5, 5.5})
	if got.Y() != 71.0 {
		t.Fatalf("walking from a slab onto a full block left the feet at y=%.2f, want 71.00", got.Y())
	}
	if !tc.IsGrounded || tc.NextY != 71.0 {
		t.Errorf("after the step: grounded=%v NextY=%.2f", tc.IsGrounded, tc.NextY)
	}
}

// A ledge taller than step height is not stepped.
func TestStepUpAssistIgnoresFullBlockLedge(t *testing.T) {
	t.Parallel()

	b, _, full := newSlabBot(t)
	b.WorldCache.SetBlockRID(5, 69, 5, full)
	b.WorldCache.SetBlockRID(6, 70, 5, full)

	tc := &movement.TickContext{
		B:          b,
		CurrPos:    mgl32.Vec3{5.9, 70.0, 5.5},
		NextY:      70.0,
		IsGrounded: true,
	}
	if got := tc.ApplyStepUpAssistForTest(mgl32.Vec3{6.1, 70.0, 5.5}); got.Y() != 70.0 {
		t.Fatalf("a full-block ledge was stepped to y=%.2f; that needs a jump", got.Y())
	}
}

func TestStepUpAssistLiftsFallingBodyOntoLedge(t *testing.T) {
	t.Parallel()

	b, slab, full := newSlabBot(t)
	b.WorldCache.SetBlockRID(12, 70, 5, slab)
	b.WorldCache.SetBlockRID(11, 70, 5, full)

	tc := &movement.TickContext{
		B:          b,
		CurrPos:    mgl32.Vec3{12.55, 71.0, 5.5},
		NextY:      70.92,
		VelY:       -0.08,
		IsGrounded: false,
	}
	b.CurrentPath = []pathfinder.Node{{X: 11, Y: 71, Z: 5, LinkType: pathfinder.LinkWalk}}
	b.PathIndex = 0
	got := tc.ApplyStepUpAssistForTest(mgl32.Vec3{12.27, 70.92, 5.5})
	if got.Y() != 71.0 {
		t.Fatalf("falling body sliding onto a full-block ledge was left at y=%.2f, want 71.00", got.Y())
	}
}

func TestStepUpAssistLetsPlannedDropFall(t *testing.T) {
	t.Parallel()

	b, _, full := newSlabBot(t)
	b.WorldCache.SetBlockRID(7, 96, 3, full)
	b.CurrentPath = []pathfinder.Node{{X: 7, Y: 96, Z: 4, LinkType: pathfinder.LinkFall}}
	b.PathIndex = 0

	tc := &movement.TickContext{
		B:          b,
		CurrPos:    mgl32.Vec3{7.5, 97.0, 4.1},
		NextY:      96.92,
		VelY:       -0.08,
		IsGrounded: false,
	}
	got := tc.ApplyStepUpAssistForTest(mgl32.Vec3{7.5, 96.92, 4.2})
	if got.Y() != 96.92 {
		t.Fatalf("a body dropping off a ledge toward a lower node was lifted back to y=%.2f", got.Y())
	}
}
