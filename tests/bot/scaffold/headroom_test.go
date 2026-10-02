package scaffold_test

import (
	"context"
	"testing"
	"time"

	"bedrock-ai/internal/bot/scaffold"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// The cell above a scaffold step is part of the step.
//
// A bot climbing puts a block under its feet and jumps into the space above.
// Nothing about the support cell says whether that space is free, so a bot
// climbing into a one-block pocket places its block perfectly and then has
// nowhere to stand. The failure is silent: the placement is confirmed, the
// path advances, and the bot sits inside the column it just built.

// TestStepIncludesTheHeadroomCell pins the geometry. The headroom cell is the
// one directly above the feet, and deriving it from the node rather than from
// the body is what keeps it correct when the body lags the path by a block.
func TestStepIncludesTheHeadroomCell(t *testing.T) {
	t.Parallel()

	step := scaffold.NewStep(protocol.BlockPos{4, 70, -9})

	want := protocol.BlockPos{4, 71, -9}
	if step.Headroom != want {
		t.Errorf("headroom = %v, want %v", step.Headroom, want)
	}
	// It must be the head, not the support: conflating the two is how a bot
	// breaks the block it is about to stand on.
	if step.Headroom == step.Support {
		t.Error("headroom collides with the support cell")
	}
}

// TestToolTierOfOnlyCountsPickaxes keeps a diamond sword from answering the
// obsidian question. A sword on obsidian is four minutes of swinging, which is
// the same as nothing, so the tier has to be the hand.
func TestToolTierOfOnlyCountsPickaxes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		item string
		want scaffold.ToolTier
	}{
		{"diamond_pickaxe", scaffold.TierDiamond},
		{"netherite_pickaxe", scaffold.TierNetherite},
		{"iron_pickaxe", scaffold.TierIron},
		{"stone_pickaxe", scaffold.TierStone},
		{"wooden_pickaxe", scaffold.TierWood},
		{"minecraft:diamond_pickaxe", scaffold.TierDiamond},
		{"diamond_sword", scaffold.TierHand},
		{"iron_sword", scaffold.TierHand},
		{"cobblestone", scaffold.TierHand},
		{"", scaffold.TierHand},
	}
	for _, c := range cases {
		if got := scaffold.ToolTierOf(c.item); got != c.want {
			t.Errorf("ToolTierOf(%q) = %v, want %v", c.item, got, c.want)
		}
	}
}

// TestTierOrderingIsTotal is what every comparison in the policy rests on: the
// tiers are ordered, so "is this tool good enough" is a single comparison and
// not a table of pairs.
func TestTierOrderingIsTotal(t *testing.T) {
	t.Parallel()

	ordered := []scaffold.ToolTier{
		scaffold.TierHand,
		scaffold.TierWood,
		scaffold.TierStone,
		scaffold.TierIron,
		scaffold.TierDiamond,
		scaffold.TierNetherite,
	}
	for i := 1; i < len(ordered); i++ {
		if ordered[i-1] >= ordered[i] {
			t.Errorf("tier %v is not below %v", ordered[i-1], ordered[i])
		}
	}
}

// TestObsidianIsWorthBreakingWithADiamondPickaxe is the first half of the user's
// ask. The old table put obsidian on a never-break list with no tools in it, so
// a bot holding a diamond pickaxe refused to clear an obsidian floor while
// standing still. Nine seconds is a cost; forever is a hang.
func TestObsidianIsWorthBreakingWithADiamondPickaxe(t *testing.T) {
	t.Parallel()

	if !scaffold.WorthBreakingWith("obsidian", scaffold.TierDiamond) {
		t.Error("a bot with a diamond pickaxe refused to break obsidian")
	}
	if !scaffold.WorthBreakingWith("minecraft:obsidian", scaffold.TierNetherite) {
		t.Error("a namespaced obsidian was treated differently from a bare one")
	}
}

// TestObsidianIsNotWorthBreakingWithoutADiamondPickaxe is the second half: the
// same block, the same bot, one tier down. Refusing here is what saves a
// fifteen-second detour from becoming a four-minute one.
func TestObsidianIsNotWorthBreakingWithoutADiamondPickaxe(t *testing.T) {
	t.Parallel()

	for _, tier := range []scaffold.ToolTier{
		scaffold.TierHand, scaffold.TierWood, scaffold.TierStone, scaffold.TierIron,
	} {
		if scaffold.WorthBreakingWith("obsidian", tier) {
			t.Errorf("a bot with a %v tool was told to break obsidian", tier)
		}
	}
}

// TestTheWallsAreNeverWorthBreaking at any tier. This is the class the old
// table existed for, and moving obsidian out of it to make it tool-aware must
// not have loosened anything here.
//
// Ancient debris is deliberately absent: it is netherite-gated, so a netherite
// tool takes it and that is correct rather than a leak.
func TestTheWallsAreNeverWorthBreaking(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"bedrock", "nether_portal", "end_portal_frame", "command_block", "structure_block", "barrier"} {
		if scaffold.WorthBreakingWith(name, scaffold.TierNetherite) {
			t.Errorf("WorthBreakingWith(%q, netherite) = true, want false", name)
		}
	}
}

func TestAncientDebrisNeedsANetheriteTool(t *testing.T) {
	t.Parallel()

	if got := scaffold.RequiredTier("ancient_debris"); got != scaffold.TierNetherite {
		t.Errorf("RequiredTier(ancient_debris) = %v, want netherite", got)
	}
	if scaffold.WorthBreakingWith("ancient_debris", scaffold.TierDiamond) {
		t.Error("a diamond pickaxe was told to break ancient debris")
	}
	if !scaffold.WorthBreakingWith("ancient_debris", scaffold.TierNetherite) {
		t.Error("a netherite pickaxe was refused ancient debris")
	}
}

// TestOrdinaryBlocksCostNothingToTake is what makes the tier check narrow. If
// dirt and stone started requiring a tool, the policy would be refusing work
// the bot can plainly do, which is the failure the detour logic is meant to
// avoid.
func TestOrdinaryBlocksCostNothingToTake(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"dirt", "stone", "cobblestone", "oak_log", "sand", "gravel",
		"deepslate", "iron_ore", "diamond_ore", "netherrack",
	} {
		if got := scaffold.RequiredTier(name); got != scaffold.TierHand {
			t.Errorf("RequiredTier(%q) = %v, want hand", name, got)
		}
		if !scaffold.WorthBreakingWith(name, scaffold.TierHand) {
			t.Errorf("WorthBreakingWith(%q, hand) = false, want true", name)
		}
	}
}

// TestDecideHeadroom is the policy itself, all four branches. Each case is a
// situation a climb actually runs into, and each has one right answer.
func TestDecideHeadroom(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		block      string
		occupied   bool
		have       scaffold.ToolTier
		lastResort bool
		want       scaffold.HeadroomAction
	}{
		{
			name:     "empty cell climbs",
			occupied: false,
			have:     scaffold.TierHand,
			want:     scaffold.HeadroomFree,
		},
		{
			name:     "a plant is not an obstruction",
			block:    "short_grass",
			occupied: true,
			have:     scaffold.TierHand,
			want:     scaffold.HeadroomBreak,
		},
		{
			name:     "a stone slab comes out with bare hands",
			block:    "stone",
			occupied: true,
			have:     scaffold.TierHand,
			want:     scaffold.HeadroomBreak,
		},
		{
			name:     "obsidian with a diamond pickaxe is a nine second task",
			block:    "obsidian",
			occupied: true,
			have:     scaffold.TierDiamond,
			want:     scaffold.HeadroomBreak,
		},
		{
			name:     "obsidian with an iron pickaxe means going around",
			block:    "obsidian",
			occupied: true,
			have:     scaffold.TierIron,
			want:     scaffold.HeadroomRouteAround,
		},
		{
			name:     "obsidian with no pickaxe means going around",
			block:    "obsidian",
			occupied: true,
			have:     scaffold.TierHand,
			want:     scaffold.HeadroomRouteAround,
		},
		{
			name:       "obsidian with no pickaxe and nowhere else to go means tunnelling",
			block:      "obsidian",
			occupied:   true,
			have:       scaffold.TierHand,
			lastResort: true,
			want:       scaffold.HeadroomTunnel,
		},
		{
			name:       "bedrock means going around even on the last try",
			block:      "bedrock",
			occupied:   true,
			have:       scaffold.TierNetherite,
			lastResort: true,
			want:       scaffold.HeadroomRouteAround,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := scaffold.DecideHeadroom(c.block, c.occupied, c.have, c.lastResort)
			if got != c.want {
				t.Errorf("DecideHeadroom(%q, occupied=%t, %v, lastResort=%t) = %v, want %v",
					c.block, c.occupied, c.have, c.lastResort, got, c.want)
			}
		})
	}
}

// TestDecideHeadroomIgnoresToolTierForAnUnoccupiedCell keeps an unloaded or
// air cell from being read as an obstruction. "I cannot see it" must not become
// "something is in the way", which would send the bot breaking blocks it never
// saw.
func TestDecideHeadroomIgnoresToolTierForAnUnoccupiedCell(t *testing.T) {
	t.Parallel()

	if got := scaffold.DecideHeadroom("", false, scaffold.TierHand, false); got != scaffold.HeadroomFree {
		t.Errorf("DecideHeadroom on an empty cell = %v, want free", got)
	}
	if got := scaffold.DecideHeadroom("air", false, scaffold.TierHand, true); got != scaffold.HeadroomFree {
		t.Errorf("DecideHeadroom on air = %v, want free", got)
	}
}

// TestClearHeadroomRemovesAStoneSlabOverhead is the whole fix at the level a
// test can observe: a solid block in the cell above the step is gone afterwards,
// and the step may proceed.
func TestClearHeadroomRemovesAStoneSlabOverhead(t *testing.T) {
	t.Parallel()

	head := protocol.BlockPos{0, 71, 0}
	b := newFakeBot(map[[3]int32]string{
		{0, 71, 0}: "minecraft:stone",
	})

	action, reason := scaffold.ClearHeadroom(context.Background(), b, head, scaffold.TierHand, false)
	if action != scaffold.HeadroomBreak {
		t.Fatalf("action = %v (%s), want break", action, reason)
	}
	if _, still := b.GetBlockName(head.X(), head.Y(), head.Z()); still {
		t.Error("the stone above the step is still there; the bot would be sealed in")
	}
}

// TestClearHeadroomLeavesObsidianAloneWithoutADiamondPickaxe checks the refusal
// is a refusal: no packets, no broken cells, nothing the server has to undo.
func TestClearHeadroomLeavesObsidianAloneWithoutADiamondPickaxe(t *testing.T) {
	t.Parallel()

	head := protocol.BlockPos{0, 71, 0}
	b := newFakeBot(map[[3]int32]string{
		{0, 71, 0}: "minecraft:obsidian",
	})

	action, reason := scaffold.ClearHeadroom(context.Background(), b, head, scaffold.TierIron, false)
	if action != scaffold.HeadroomRouteAround {
		t.Fatalf("action = %v (%s), want route_around", action, reason)
	}
	if len(b.broken) != 0 {
		t.Errorf("the bot swung at obsidian with an iron pickaxe: %v", b.broken)
	}
	if reason == "" {
		t.Error("no reason given for routing around; the log line would be useless")
	}
}

// TestClearHeadroomTakesObsidianWithADiamondPickaxe is the same world with the
// tool that makes it worth taking, and the block must actually go.
func TestClearHeadroomTakesObsidianWithADiamondPickaxe(t *testing.T) {
	t.Parallel()

	head := protocol.BlockPos{0, 71, 0}
	b := newFakeBot(map[[3]int32]string{
		{0, 71, 0}: "minecraft:obsidian",
	})

	action, reason := scaffold.ClearHeadroom(context.Background(), b, head, scaffold.TierDiamond, false)
	if action != scaffold.HeadroomBreak {
		t.Fatalf("action = %v (%s), want break", action, reason)
	}
	if _, still := b.GetBlockName(head.X(), head.Y(), head.Z()); still {
		t.Error("the obsidian is still there even though the bot had the tool for it")
	}
}

// TestClearHeadroomTunnelsOnTheLastResort is the "unless there is genuinely no
// other path" half. Going around is right the first time; standing still
// forever is not, and this is the only branch that turns a refusal into an
// action.
func TestClearHeadroomTunnelsOnTheLastResort(t *testing.T) {
	t.Parallel()

	head := protocol.BlockPos{0, 71, 0}
	b := newFakeBot(map[[3]int32]string{
		{0, 71, 0}: "minecraft:obsidian",
	})

	action, reason := scaffold.ClearHeadroom(context.Background(), b, head, scaffold.TierHand, true)
	if action != scaffold.HeadroomTunnel {
		t.Fatalf("action = %v (%s), want tunnel", action, reason)
	}
	if _, still := b.GetBlockName(head.X(), head.Y(), head.Z()); still {
		t.Error("last resort did not actually break the block; the bot still has nowhere to go")
	}
}

// TestClearHeadroomNeverTunnelsBedrock keeps the last-resort branch honest.
// "Break through if there is no other way" does not extend to "break through
// bedrock", which is not a shortcut under any circumstances.
func TestClearHeadroomNeverTunnelsBedrock(t *testing.T) {
	t.Parallel()

	head := protocol.BlockPos{0, 71, 0}
	b := newFakeBot(map[[3]int32]string{
		{0, 71, 0}: "minecraft:bedrock",
	})

	action, _ := scaffold.ClearHeadroom(context.Background(), b, head, scaffold.TierNetherite, true)
	if action != scaffold.HeadroomRouteAround {
		t.Errorf("action = %v, want route_around: bedrock is bedrock from any angle", action)
	}
	if len(b.broken) != 0 {
		t.Errorf("the bot swung at bedrock: %v", b.broken)
	}
}

// TestClearHeadroomOnAnEmptyCellCostsNothing keeps the common case cheap. A
// climb runs this on every step, and a step that pauses to ask about a cell
// that holds air is a climb that is slower for no reason.
func TestClearHeadroomOnAnEmptyCellCostsNothing(t *testing.T) {
	t.Parallel()

	b := newFakeBot(nil)

	start := time.Now()
	action, reason := scaffold.ClearHeadroom(context.Background(), b, protocol.BlockPos{0, 71, 0}, scaffold.TierHand, false)
	if action != scaffold.HeadroomFree {
		t.Errorf("action = %v (%s), want free", action, reason)
	}
	if len(b.broken) != 0 || len(b.useOnSequence) != 0 {
		t.Errorf("an empty headroom cell produced traffic: %v %v", b.broken, b.useOnSequence)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("asking about an empty cell took %v; this runs on every step of every climb", elapsed)
	}
}
