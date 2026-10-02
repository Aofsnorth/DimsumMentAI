// Tool tier, and what a block costs to remove with what the bot is holding.
//
// A block's hardness is not the only thing that decides whether a bot can take
// it. Obsidian breaks in about nine seconds with a diamond pickaxe and about
// four minutes bare-handed, and "about four minutes" is not a cost, it is a
// hang: the bot stands in front of it, the movement tick stops stamping, and the
// stall watchdog dumps goroutines while every one of them is queued behind the
// mutex the swinging goroutine is holding.
//
// So the question a scaffold step has to ask is not "can this be broken" but
// "can this be broken by the bot as it is equipped, in a time worth spending".
// That is what this file answers, and it answers it as a pure function so the
// policy is testable without a server, a world, or a bot.

package scaffold

import "strings"

// ToolTier ranks the tools that matter for taking a block out of the world.
//
// The ordering is the vanilla one and it is all that is needed: every question
// this package asks is "is the held tool at least this good", never "which of
// two equally good tools is faster", so a single scalar is the honest model and
// five named constants are cheaper than a table of structs nobody reads.
type ToolTier int

const (
	// TierHand is bare hands, or a tool with no mining speed on the block in
	// question. It is the floor, not a failure: dirt, sand and wood all come
	// out with it.
	TierHand ToolTier = iota
	// TierWood is a wooden or golden tool.
	TierWood
	// TierStone is a stone or copper tool.
	TierStone
	// TierIron is an iron tool.
	TierIron
	// TierDiamond is a diamond pickaxe, the tool obsidian demands.
	TierDiamond
	// TierNetherite is a netherite tool, the tool ancient debris demands.
	TierNetherite
)

// String names the tier for the log line that says why a step was skipped.
func (t ToolTier) String() string {
	switch t {
	case TierWood:
		return "wooden"
	case TierStone:
		return "stone"
	case TierIron:
		return "iron"
	case TierDiamond:
		return "diamond"
	case TierNetherite:
		return "netherite"
	default:
		return "hand"
	}
}

// pickaxeTiers maps a tool's tier prefix to the tier it confers.
//
// Keyed on the prefix before the tool kind because that is the part that
// carries the speed: "diamond_pickaxe" and "diamond_sword" share a prefix and
// not a capability, which is why the kind is checked separately below.
var pickaxeTiers = []struct {
	prefix string
	tier   ToolTier
}{
	{"netherite_", TierNetherite},
	{"diamond_", TierDiamond},
	{"iron_", TierIron},
	{"stone_", TierStone},
	{"copper_", TierStone},
	{"wooden_", TierWood},
	{"golden_", TierWood},
}

// ToolTierOf reports the mining tier an item confers, and TierHand for anything
// that is not a pickaxe.
//
// The pickaxe check is not pedantry. A bot holding a diamond sword answers the
// obsidian question with a diamond sword in its hand, and the honest answer is
// TierHand: a sword on obsidian is four minutes of swinging, the same as
// nothing. A bare hand and a sword are the same tier for every block that
// matters here, so the distinction is kept only where it changes the answer.
func ToolTierOf(itemName string) ToolTier {
	n := normalise(itemName)
	if n == "" {
		return TierHand
	}
	if !strings.Contains(n, "pickaxe") {
		return TierHand
	}
	for _, entry := range pickaxeTiers {
		if strings.HasPrefix(n, entry.prefix) {
			return entry.tier
		}
	}
	// A pickaxe with no tier prefix is a modded or unrecognised one. It is
	// assumed to be at least wooden rather than TierHand, because a bot
	// holding a pickaxe and being told it has no pickaxe is the kind of answer
	// that makes it refuse work it can plainly do.
	return TierWood
}

// toolGatedBlocks are the blocks whose harvest needs a specific pickaxe tier.
//
// They are separated from notWorthBreakingBlocks because the answer is
// conditional: obsidian is a wall for a bot with a stone pickaxe and a nine
// second task for a bot with a diamond one, and a table with no tools in it can
// only say one of those two things.
var toolGatedBlocks = []struct {
	match    func(normalised string) bool
	required ToolTier
}{
	{
		// Obsidian is the case that motivates all of this: it is the block a
		// bot most often meets directly above its own head when it tries to
		// climb, because obsidian is what people build floors and nether
		// gateways out of.
		match:    func(n string) bool { return strings.Contains(n, "obsidian") },
		required: TierDiamond,
	},
	{
		match:    func(n string) bool { return strings.Contains(n, "ancient_debris") },
		required: TierNetherite,
	},
}

// RequiredTier reports the mining tier a block needs before it comes out in a
// time worth spending.
//
// TierHand is the answer for everything that is not gated, which is the large
// majority: dirt, wood, stone, sand, gravel and leaves all come out with bare
// hands, slowly, and "slowly" there is under a second.
func RequiredTier(blockName string) ToolTier {
	n := normalise(blockName)
	for _, gated := range toolGatedBlocks {
		if gated.match(n) {
			return gated.required
		}
	}
	return TierHand
}

// WorthBreakingWith reports whether the bot should spend its time breaking this
// block, given the tier of tool it is actually holding.
//
// This is WorthBreaking with the tool taken into account, and it is the
// function the scaffold executor should be calling. The old WorthBreaking
// cannot express the situation that matters: obsidian was on the never-break
// list unconditionally, so a bot with a diamond pickaxe refused to clear a
// perfectly ordinary obsidian floor while a bot with a wooden one would have
// spent four minutes on it had the table not saved it. Both halves of that were
// wrong.
func WorthBreakingWith(blockName string, have ToolTier) bool {
	n := normalise(blockName)
	if n == "" || airBlocks[n] {
		return false
	}
	if required := RequiredTier(n); required > have {
		return false
	}
	return !notWorthBreakingBlocks[n]
}

// HeadroomAction is what a scaffold step should do about the cell directly
// above the block it is trying to stand on.
type HeadroomAction int

const (
	// HeadroomFree means nothing is in the way. The step proceeds.
	HeadroomFree HeadroomAction = iota
	// HeadroomBreak means the block comes out with the tools in hand.
	HeadroomBreak
	// HeadroomRouteAround means the block cannot be taken in a time worth
	// spending, so the path must be rebuilt to go some other way.
	HeadroomRouteAround
	// HeadroomTunnel means the block cannot be taken cheaply, but the bot has
	// already tried going around and is out of other options, so it breaks
	// through anyway. A bot that stands still forever has not avoided the
	// cost, it has only deferred it while occupying the world.
	HeadroomTunnel
)

// String names the action for the log line.
func (a HeadroomAction) String() string {
	switch a {
	case HeadroomBreak:
		return "break"
	case HeadroomRouteAround:
		return "route_around"
	case HeadroomTunnel:
		return "tunnel"
	default:
		return "free"
	}
}

// DecideHeadroom is the whole policy for the cell above a scaffold step, in one
// pure function.
//
// The four branches are the four situations a climb actually runs into:
//
//   - Nothing there. Climb.
//   - A plant or a block the held tool can take. Break it. This is the branch
//     that was missing entirely, and its absence is why a bot with a perfectly
//     good pickaxe stood under a stone slab it could have removed instead of
//     being stuck on the block it had just placed.
//   - A block the held tool cannot take, such as obsidian with no diamond
//     pickaxe. Route around. Nothing here was asked to tunnel and nothing here
//     should.
//   - The same block, on the last attempt at this node. By then the bot has
//     already dropped the path once to go around, and it is standing here again,
//     which means the world has no other way to offer. Tunnel, because the
//     alternative to breaking through is not a shorter path but no path at all.
func DecideHeadroom(blockName string, occupied bool, have ToolTier, lastResort bool) HeadroomAction {
	if !occupied {
		return HeadroomFree
	}
	if IsReplaceable(blockName) {
		// A tuft of grass is not an obstruction, it is a non-event. Breaking it
		// costs nothing and asking about tools for it is noise.
		return HeadroomBreak
	}
	if WorthBreakingWith(blockName, have) {
		return HeadroomBreak
	}
	if lastResort {
		// Only for a block that is breakable at all. Bedrock is bedrock from
		// any angle, and standing in front of it forever is no better than
		// standing in front of it slowly.
		if neverBreakable(blockName) {
			return HeadroomRouteAround
		}
		return HeadroomTunnel
	}
	return HeadroomRouteAround
}

// neverBreakable reports whether no tool, ever, takes this block out.
func neverBreakable(blockName string) bool {
	return notWorthBreakingBlocks[normalise(blockName)]
}
