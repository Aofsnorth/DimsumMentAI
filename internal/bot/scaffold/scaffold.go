// Package scaffold places the block a bot puts under its own feet to climb.
//
// Two things went wrong here for a long time, and both looked like "the block
// sometimes does not appear".
//
// The first was that the "jump" was the jump emote. The body never left the
// floor, the placement was aimed at the cell the player was standing in, and the
// server refused it as intersecting an entity. Nothing was ever placed, and
// nothing reported that.
//
// The second was that the cell was never checked. A block cannot be placed into
// a cell that already holds something, and the things that turn up on a hill are
// mostly grass, flowers and tall grass — cheap, instant, and completely blocking.
// The bot stood on a tuft of grass, aimed at it, and placed a block into it.
//
// So: clear the cell, really leave the ground, and confirm the placement from
// the server instead of asserting it.
package scaffold

import (
	"context"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/movement/animation"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot is the slice of the bot that placing a scaffold block needs. It is
// narrow on purpose: this package is reached from two very different places
// (the gatherer's tower and the pathfinder's scaffold step) and neither should
// have to drag the other in.
type Bot interface {
	GetCoords() mgl32.Vec3
	GetBlockName(x, y, z int32) (string, bool)
	// GetBlockNetworkID is the BDS wire ID of the block at the position. A
	// placement names the support block by this ID, and on a connection that
	// negotiates block network ID hashes it is not the local runtime ID.
	GetBlockNetworkID(x, y, z int32) (uint32, bool)
	GetEntityRuntimeID() uint64
	GetHeldItemSlot() uint32
	// HeldItemInstance is the held slot plus the item the server attributes the
	// placement to. The StackNetworkID rides along: on hashed-ID connections a
	// placement without it is silently refused.
	HeldItemInstance() (uint32, protocol.ItemInstance, bool)
	WritePacket(pk packet.Packet) error
	LookAt(pos mgl32.Vec3)
	RequestJump()
	JumpRequested() bool
	Grounded() bool
}

// replaceableBlocks are the things that stand in a scaffold cell without being
// worth keeping: grass, flowers, mushrooms, saplings, the small sea plants, and
// the handful of blocks that are a decoration layer rather than a structure.
//
// The list matters because these break instantly while a stone block takes a
// second and a half. A bot that treated a flower like a boulder would spend most
// of its time watching a poppy.
//
// The distinction is also the one a player makes: you can place a block into
// grass, you cannot place a block into a rock. Getting it backwards either
// refuses every placement on a grassy hill or shatters a wall the bot was
// supposed to climb over.
var replaceableBlocks = map[string]bool{
	"short_grass": true, "tall_grass": true, "grass": true, "fern": true,
	"large_fern": true, "dead_bush": true, "seagrass": true, "tall_seagrass": true,
	"kelp": true, "kelp_plant": true, "snow_layer": true, "carpet": true,
	"torch": true, "red_torch": true, "soul_torch": true, "soul_fire": true,
	"fire": true, "lily_pad": true, "chorus_plant": true,
	"pink_petals": true, "pitcher_plant": true, "chorus_flower": true,
	"sapling": true, "oak_sapling": true, "birch_sapling": true, "spruce_sapling": true,
	"jungle_sapling": true, "acacia_sapling": true, "cherry_sapling": true,
	"dark_oak_sapling": true, "mangrove_propagule": true, "azalea": true,
	"flowering_azalea": true, "bamboo_sapling": true,
	"poppy": true, "dandelion": true, "blue_orchid": true, "allium": true,
	"azure_bluet": true, "red_tulip": true, "orange_tulip": true, "white_tulip": true,
	"pink_tulip": true, "oxeye_daisy": true, "cornflower": true,
	"lily_of_the_valley": true, "wither_rose": true, "torchflower": true,
	"pitcher_pod": true, "open_eyeblossom": true, "closed_eyeblossom": true,
	"brown_mushroom": true, "red_mushroom": true, "crimson_fungus": true,
	"warped_fungus": true, "crimson_roots": true, "warped_roots": true,
	"nether_sprouts": true, "hanging_roots": true, "twisting_vines": true,
	"weeping_vines": true, "vine": true, "glow_lichen": true, "sculk_vein": true,
	"snow": true, "end_rod": true, "chain": true, "lightning_rod": true,
}

// airBlocks are the names that mean "nothing is here". A cell holding one of
// these is free.
var airBlocks = map[string]bool{
	"air": true, "cave_air": true, "void_air": true, "structure_void": true,
}

// normalise strips the namespace and lowercases a block name, so the tables here
// are keyed on the bare name the rest of the bot uses.
func normalise(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.TrimPrefix(n, "minecraft:")
}

// IsReplaceable reports whether a block is something a scaffold placement may
// clear out of the way.
//
// It answers "may this be broken to make room", not "is this trivial". A flower
// and a chest are both breakable, and only one of them is a thing a bot should
// destroy in order to climb a block.
func IsReplaceable(blockName string) bool {
	n := normalise(blockName)
	if n == "" || airBlocks[n] {
		return true
	}
	if replaceableBlocks[n] {
		return true
	}
	// Bedrock has a long tail of flowers and plants and not all of them are in
	// the table. A suffix is the honest way to catch the rest: anything ending
	// in _sapling, _bush, _roots or _vines is a plant, breaks instantly, and is
	// not something a bot should refuse to climb a block over.
	for _, suffix := range []string{"_sapling", "_bush", "_roots", "_vines", "_fungus", "_flower"} {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return false
}

// notWorthBreakingBlocks are the blocks a bot must not try to quarry its way
// through, because no amount of time makes them a shortcut.
//
// These are the walls: bedrock, the portal blocks, the command blocks. They are
// not on this list because they are hard — they are on it because no tool ever
// takes them out, so a bot that starts is a bot that is stuck.
//
// Obsidian and ancient debris used to be here, and that was the single worst
// entry in the table. Obsidian is a wall for a bot with a wooden pickaxe and a
// nine-second job for a bot with a diamond one, and a table with no tools in it
// can only say one of those two things. It said the first, so a bot holding a
// diamond pickaxe refused to clear an obsidian floor and stood there instead.
// They are tool-gated in tool_tier.go now, which can say both.
var notWorthBreakingBlocks = map[string]bool{
	"bedrock":       true,
	"nether_portal": true, "end_portal": true, "end_portal_frame": true,
	"end_gateway": true, "command_block": true, "chain_command_block": true,
	"repeating_command_block": true, "structure_block": true, "barrier": true,
	"light": true, "moving_block": true, "piston": true,
}

// SupportBelowReady reports whether there is a block to aim at when placing a
// support into the cell above click.
//
// A placement is a click on a face, so there has to be a face. When the cell
// below the support is empty the bot has nothing to click, the transaction goes
// out aimed at nothing, and the server refuses it — which is one of the ways a
// climb "sometimes" placed its block and sometimes did not.
func SupportBelowReady(bot Bot, click protocol.BlockPos) (bool, string) {
	name, occupied := Occupied(bot, click)
	if !occupied {
		return false, "there is no block underneath to place onto"
	}
	if IsReplaceable(name) {
		return false, name + " cannot hold a block placed on top of it"
	}
	return true, ""
}

// WorthBreaking reports whether a bot should spend its time breaking this block
// to get through.
//
// It answers a different question from BreakDuration, which is "how long does
// the break take once it is worth starting". Here the answer can be no, and
// saying so is what lets the caller route around instead of standing in front
// of an obsidian pillar swinging at it.
func WorthBreaking(blockName string) bool {
	return WorthBreakingWith(blockName, TierNetherite)
}

// BreakDuration is how long a block needs before it is gone.
//
// Replaceables are instant, which is what makes clearing a grassy cell cost
// nothing at all. Everything else gets a time the bot can afford, and stone is
// deliberately slow rather than optimistic: a bot that reports a cell clear
// before the server has broken the block places into a cell that is still full
// and watches the placement be refused.
func BreakDuration(blockName string) time.Duration {
	if IsReplaceable(blockName) {
		return 0
	}
	n := normalise(blockName)
	switch {
	case strings.Contains(n, "obsidian"), n == "bedrock", strings.Contains(n, "ancient_debris"):
		return 9 * time.Second
	case strings.Contains(n, "stone"), strings.Contains(n, "ore"),
		strings.Contains(n, "iron"), strings.Contains(n, "gold"),
		strings.Contains(n, "diamond"), strings.Contains(n, "netherite"),
		strings.Contains(n, "deepslate"), strings.Contains(n, "blackstone"):
		return 1500 * time.Millisecond
	default:
		return 800 * time.Millisecond
	}
}

// PlaceCell is the cell a scaffold block will occupy: the one above the block
// being stood on.
func PlaceCell(ref protocol.BlockPos) protocol.BlockPos {
	return protocol.BlockPos{ref.X(), ref.Y() + 1, ref.Z()}
}

// AimPoint is where the crosshair has to be for the server to accept a placement
// on the top face of ref.
//
// The top face of the block at ref is the plane at ref.Y+1, so the aim sits just
// above that plane and horizontally centred in the block's own column. The old
// code aimed half a block under the feet, which is inside the block being stood
// on: the eased look pulled toward it, never converged, and the placement was
// refused for pointing at a face the bot was not on.
func AimPoint(ref protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(ref.X()) + 0.5,
		float32(ref.Y()) + 1.55,
		float32(ref.Z()) + 0.5,
	}
}

// Occupied reports whether something is in the cell a block would go into, and
// what it is.
//
// This is the check the old placement never made. A block cannot be placed into
// an occupied cell, and on any hill the thing occupying it is grass.
//
// An unloaded cell is reported as free. Treating "I cannot see it" as "something
// is in the way" would send the bot breaking blocks it never saw, which is the
// opposite failure and just as bad.
func Occupied(bot Bot, cell protocol.BlockPos) (string, bool) {
	name, ok := bot.GetBlockName(cell.X(), cell.Y(), cell.Z())
	if !ok {
		return "", false
	}
	n := normalise(name)
	if airBlocks[n] {
		return "", false
	}
	return n, true
}

// notStandableBlocks are solid-looking blocks a bot still cannot stand on. A
// ladder or a fence in the cell is not the support the path asked for, so the
// step still has to run.
var notStandableBlocks = map[string]bool{
	"ladder": true, "scaffolding": true, "cobweb": true,
	"fire": true, "soul_fire": true,
	"water": true, "flowing_water": true, "lava": true, "flowing_lava": true,
	"nether_portal": true, "end_portal": true, "end_gateway": true,
	"fence": true, "fence_gate": true, "iron_bars": true, "wall": true,
	"trapdoor": true, "iron_trapdoor": true, "slab": true, "stone_slab": true,
	"stairs": true, "rail": true, "activator_rail": true, "detector_rail": true,
	"pressure_plate": true, "tripwire": true, "torch": true, "redstone_torch": true,
	"lever": true, "button": true, "sign": true, "banner": true, "wall_banner": true,
	"door": true, "bed": true, "flower_pot": true, "carpet": true,
	"anvil": true, "chest": true, "trapped_chest": true, "ender_chest": true,
	"hopper": true, "furnace": true, "lit_furnace": true, "brewing_stand": true,
	"dropper": true, "dispenser": true, "crafting_table": true, "enchanting_table": true,
}

// notStandableSuffixes catch the same families across wood and stone variants.
// Bedrock names these by variant — oak_fence, spruce_stairs, smooth_stone_slab —
// and a table of bare names silently misses every one of them, which is how
// "occupied means satisfied" turned into "an oak fence counts as a floor".
var notStandableSuffixes = []string{
	"_fence", "_fence_gate", "_wall", "_slab", "_stairs", "_trapdoor",
	"_door", "_sign", "_banner", "_pressure_plate", "_button", "_rail",
	"_coral_fan", "_candle",
}

// isNotStandable reports whether a block occupies the cell without giving the
// bot something to stand on.
func isNotStandable(blockName string) bool {
	n := normalise(blockName)
	if notStandableBlocks[n] {
		return true
	}
	for _, suffix := range notStandableSuffixes {
		if strings.HasSuffix(n, suffix) {
			return true
		}
	}
	return false
}

// Step is the geometry a path node's "place" action means, resolved once so the
// planner and the executor cannot disagree about it.
//
// A node (X, Y, Z) is a place the bot wants to STAND: its feet end up at Y. So
// the block it needs is the support at (X, Y-1, Z), and the block it clicks to
// put that support there is the one below it at (X, Y-2, Z).
//
// This has to be derived from the node and never from where the bot happens to
// be. Deriving the height from the body produces a block one level off wherever
// the body lags the path, and off in the wrong direction the block lands beside
// the bot instead of under it — which reads as a bot that keeps placing blocks
// in front of itself while jumping.
type Step struct {
	// Support is the cell the bot needs solid in order to stand on the node. It
	// is also the cell the new block goes into.
	Support protocol.BlockPos
	// Click is the block whose top face the placement is aimed at. It must
	// already be solid, or there is nothing to click and the block would float.
	Click protocol.BlockPos
	// Feet is the node itself: where the bot's feet end up.
	Feet protocol.BlockPos
	// Headroom is the cell the bot's head occupies once it is standing on the
	// node: directly above the feet.
	//
	// It was not part of the geometry at all, and that is the whole reason a
	// bot could place a block, jump, and then be sealed inside its own column.
	// A support cell that is clear says nothing about the cell above it, and a
	// climb into a one-block pocket is a bot that is stuck holding a block it
	// has no room to stand in.
	Headroom protocol.BlockPos
}

// NewStep resolves the scaffold geometry for a node the bot wants to stand on.
func NewStep(node protocol.BlockPos) Step {
	return Step{
		Support:  protocol.BlockPos{node.X(), node.Y() - 1, node.Z()},
		Click:    protocol.BlockPos{node.X(), node.Y() - 2, node.Z()},
		Feet:     node,
		Headroom: protocol.BlockPos{node.X(), node.Y() + 1, node.Z()},
	}
}

// BodyBlocksCell reports whether a body standing at feet position pos overlaps
// the block cell, which is the case that makes a placement into that cell
// impossible until the body leaves it.
//
// A bot standing in the node's own column has its legs inside the support cell.
// A real player jumps here; the placement lands on the way up. A bot standing a
// block away can place sideways and never needs to jump, and jumping anyway is
// what made the arm swing with no block appearing.
//
// The column margin is the cell's half width plus the body's real half width:
// a body 0.7 blocks off the column centre still clips the cell, and the old 0.6
// tolerance let that body skip its jump and swing at a placement the server
// would never honour.
func BodyBlocksCell(pos mgl32.Vec3, cell protocol.BlockPos) bool {
	// The cell spans y in [cell.Y, cell.Y+1). The body occupies it when its feet
	// are below the top of that cell while its head is still above its floor.
	if pos.Y() >= float32(cell.Y())+1.0 || pos.Y()+1.8 <= float32(cell.Y()) {
		return false
	}
	const halfWidth = 0.3
	if math.Abs(float64(pos.X()-(float32(cell.X())+0.5))) >= 0.5+halfWidth {
		return false
	}
	if math.Abs(float64(pos.Z()-(float32(cell.Z())+0.5))) >= 0.5+halfWidth {
		return false
	}
	return true
}

// CellSatisfied reports whether the cell a scaffold block would go into already
// holds a real block the bot can stand on, which means the placement has nothing
// left to do.
//
// This is the difference between "the step failed" and "the step is already done",
// and getting it wrong is how a bot ends up stuck on one block forever. A tower
// places the block it is standing on; the next tick the very same path node is
// executed again, and this time the cell holds the dirt the bot itself put there
// a moment ago. A placement that only ever checks "is the cell empty" refuses
// that forever, and because the refusal did not move the path along, the node
// came back every tick and spoke to the player about it each time.
//
// A replaceable block is deliberately NOT a satisfied cell. Grass is something
// the bot has to clear before it can stand on solid ground, which is the same
// distinction a player makes: you can place a block into a tuft of grass, you
// cannot place a block into a rock.
func CellSatisfied(bot Bot, cell protocol.BlockPos) (string, bool) {
	name, occupied := Occupied(bot, cell)
	if !occupied || IsReplaceable(name) {
		return "", false
	}
	// A ladder is not a floor, and neither is a fence. Without this the bot
	// would call a ladder-filled cell "already supported" and walk on past the
	// step that would have let it climb.
	if isNotStandable(name) {
		return "", false
	}
	return name, true
}

// ClearCell removes whatever occupies the cell and reports whether it is free
// afterwards.
//
// A replaceable block is taken straight out. A real block is broken only when
// breakThrough is set, because the two callers want different things: a tower
// wants the cell empty at any cost, while a pathfinder stepping past a wall
// would rather route around it than quarry a hole in it.
//
// Even breakThrough does not override the tier check. A bot that will spend
// four minutes on obsidian instead of walking four steps around it has not been
// told to break through; it has been told the wrong thing.
//
// have is the mining tier of the best pickaxe the bot carries. The tower, which
// builds upward in a column it has already committed to, passes TierHand: it
// has no way to detour, and a column with obsidian in it is a column the bot
// needs out of the way. The pathfinder passes the bot's real tier, because
// there it does have somewhere else to go.
func ClearCell(ctx context.Context, bot Bot, cell protocol.BlockPos, breakThrough bool, have ToolTier) (bool, string) {
	name, occupied := Occupied(bot, cell)
	if !occupied {
		return true, ""
	}
	if !IsReplaceable(name) {
		if !breakThrough {
			return false, "the cell holds " + name + ", which is not something to break for a scaffold"
		}
		if !WorthBreakingWith(name, have) {
			return false, name + " needs a " + RequiredTier(name).String() + " pickaxe, which the bot does not have"
		}
	}

	if duration := BreakDuration(name); duration <= 0 {
		// Nothing to swing at. The packets still go out, because the server owns
		// the removal, but there is no mining rhythm to run.
		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStartBreak,
			BlockPosition:   cell,
			BlockFace:       1,
		})
		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionPredictDestroyBlock,
			BlockPosition:   cell,
			BlockFace:       1,
		})
	} else if !breakCell(ctx, bot, cell, duration) {
		return false, "could not break " + name
	}

	// Confirm from the world rather than assuming the packets landed.
	return WaitClear(ctx, bot, cell, 1500*time.Millisecond)
}

// ClearHeadroom removes whatever is in the cell above a scaffold step and
// reports whether the bot may stand there.
//
// It is the check that was missing, and its absence is a specific and very
// ordinary failure: the bot places the block, jumps, and finds a stone slab or
// an obsidian block directly over the place it needs to stand. Nothing about
// the support cell is wrong, so nothing reported anything, and the bot sat
// under its own staircase for as long as it was left running.
//
// have is the tier of the pickaxe the bot is holding, because "can this be
// broken" and "can this be broken in a time worth spending" are different
// questions and the second is the one that matters. lastResort says the bot has
// already dropped this path once to go around and has ended up back here,
// which is the evidence that the world has no other way to offer — at that
// point breaking through beats standing still.
func ClearHeadroom(ctx context.Context, bot Bot, cell protocol.BlockPos, have ToolTier, lastResort bool) (HeadroomAction, string) {
	name, occupied := Occupied(bot, cell)
	action := DecideHeadroom(name, occupied, have, lastResort)

	switch action {
	case HeadroomFree:
		return HeadroomFree, ""
	case HeadroomRouteAround:
		if occupied {
			return action, name + " needs a " + RequiredTier(name).String() + " pickaxe, which the bot does not have"
		}
		return action, "the cell above the step is blocked"
	}

	// The decision above has already answered "is this worth breaking", and the
	// tier is handed to ClearCell so it answers the same question the same way
	// rather than re-deciding it. The one thing that does not survive the trip is
	// the last-resort override: a tunnel decision is a decision to break a block
	// the tier check would otherwise refuse, so it is carried out directly.
	if action != HeadroomTunnel {
		cleared, reason := ClearCell(ctx, bot, cell, true, have)
		if cleared {
			return HeadroomBreak, ""
		}
		return HeadroomRouteAround, reason
	}

	// A tunnel. The block is breakable, it is just not breakable in a time
	// worth spending — and the bot has already been sent around once and come
	// back. The break still runs the real rhythm, because the server owns the
	// removal and a predicted destroy for a nine-second block is a lie.
	broken, reason := BreakAndWait(ctx, bot, cell, BreakDuration(name))
	if !broken {
		return HeadroomRouteAround, "could not tunnel through the " + name + " overhead: " + reason
	}
	return HeadroomTunnel, ""
}

// BreakAndWait breaks a cell for a known duration and reports whether the
// server actually cleared it.
//
// It is ClearCell for a caller that already knows how long its block takes —
// the descending scaffolder, which has the server-auth break duration, and the
// pathfinder's obstacle miner. The confirmation matters as much here as it does
// on placement: a break that is assumed rather than observed leaves the bot
// standing on a block the server still holds.
func BreakAndWait(ctx context.Context, bot Bot, cell protocol.BlockPos, duration time.Duration) (bool, string) {
	if _, occupied := Occupied(bot, cell); !occupied {
		return true, ""
	}
	if duration <= 0 {
		duration = 400 * time.Millisecond
	}
	if !breakCell(ctx, bot, cell, duration) {
		return false, "the break sequence was cancelled"
	}
	return WaitClear(ctx, bot, cell, 2000*time.Millisecond)
}

// breakCell runs the full break rhythm for a block that needs mining time.
func breakCell(ctx context.Context, bot Bot, cell protocol.BlockPos, duration time.Duration) bool {
	aim := mgl32.Vec3{float32(cell.X()) + 0.5, float32(cell.Y()) + 0.5, float32(cell.Z()) + 0.5}
	bot.LookAt(aim)

	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   cell,
		BlockFace:       1,
	})

	// The shared rhythm, and a real swing on every beat.
	//
	// This used to send a CrackBreak every 200ms and no Animate at all, under a
	// comment claiming it "keeps the arm moving so the break is not visibly
	// frozen". It did not move the arm at all: CrackBreak advances the crack
	// overlay, and only Animate swings the arm. The comment described the intent
	// and the code did the opposite, which is the worst kind of bug — it reads
	// like the case was handled.
	for i, b := range animation.Beats(duration, aim) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(b.Wait):
		}
		if i == 0 {
			// The first beat is the wind-up; the arm is still being raised.
			continue
		}
		_ = bot.WritePacket(animation.MineSwing(bot.GetEntityRuntimeID()))
		bot.LookAt(b.Aim)
		_ = bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionCrackBreak,
			BlockPosition:   cell,
			BlockFace:       1,
		})
	}

	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionCrackBreak,
		BlockPosition:   cell,
		BlockFace:       1,
	})
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionPredictDestroyBlock,
		BlockPosition:   cell,
		BlockFace:       1,
	})
	// StopBreak has to be last, or the server keeps destroy-progress on this
	// cell and the next block placed here comes back already broken.
	_ = bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStopBreak,
		BlockPosition:   cell,
		BlockFace:       1,
	})
	return true
}

// WaitClear polls until the cell reads as empty, and reports whether it did.
func WaitClear(ctx context.Context, bot Bot, cell protocol.BlockPos, timeout time.Duration) (bool, string) {
	deadline := time.Now().Add(timeout)
	for {
		if _, occupied := Occupied(bot, cell); !occupied {
			return true, ""
		}
		if ctx.Err() != nil {
			return false, "cancelled while clearing the cell"
		}
		if time.Now().After(deadline) {
			return false, "the cell is still occupied"
		}
		select {
		case <-ctx.Done():
			return false, "cancelled while clearing the cell"
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// PlaceVerified puts a block on the top face of ref and reports whether the
// server put it there.
//
// The confirmation is the point of the whole function. The old code wrote the
// transaction and then immediately told the world model the block was there, so
// a refused placement — the common case — was recorded as a success and the
// tower carried on from a block that did not exist. Nothing retried and nothing
// reported.
// PlaceVerified puts a block on the top face of ref, with the packet sequence a
// vanilla client sends: start the use-on, then the click-block transaction,
// then stop the use-on. A bare transaction never worked. The server checks the
// crosshair against the clicked face, attributes the click to a player through
// the trigger type, checks the support block by its wire ID, and the held item
// by its stack network ID — every one of those was missing, and the log filled
// with "the server never placed the block".
func PlaceVerified(ctx context.Context, bot Bot, ref protocol.BlockPos, item protocol.ItemStack) (bool, string) {
	cell := PlaceCell(ref)

	// The cell must be free before the placement is even attempted, and this is
	// not a formality. WaitPlaced confirms by watching the cell become occupied,
	// so a cell that was already occupied would satisfy it instantly — a refused
	// placement, which is the common case, read back as a success. Checking first
	// is what makes the confirmation mean anything.
	if name, occupied := Occupied(bot, cell); occupied {
		return false, "the target cell still holds " + name
	}

	bot.LookAt(AimPoint(ref))
	// The look is eased over roughly a second and the server checks the
	// crosshair against the face, so placing before the head arrives is placing
	// at the wrong angle.
	select {
	case <-ctx.Done():
		return false, "cancelled before placing"
	case <-time.After(180 * time.Millisecond):
	}

	heldSlot, heldItem, ok := bot.HeldItemInstance()
	if !ok {
		return false, "the held slot is empty after equip"
	}
	if heldItem.Stack.Count == 0 || heldItem.Stack.NetworkID != item.NetworkID {
		// The slot changed under the caller (an inventory sync landed between
		// equip and place), so placing would consume the wrong stack.
		return false, "the held slot no longer holds the scaffold item"
	}
	supportNetworkID, ok := bot.GetBlockNetworkID(ref.X(), ref.Y(), ref.Z())
	if !ok {
		return false, "the support block is not loaded"
	}

	// The body check happens here, right before the packets go out: the look
	// settle above is for the crosshair, and the jump window is short — a
	// placement that arrives while the body still occupies the cell is refused
	// as intersecting an entity, and the visible symptom is an arm swinging with
	// no block ever appearing.
	if BodyBlocksCell(bot.GetCoords(), cell) {
		if ok, reason := JumpAndWait(ctx, bot, cell, 800*time.Millisecond); !ok {
			return false, reason
		}
	}

	start := &packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartItemUseOn,
		BlockPosition:   ref,
		ResultPosition:  cell,
		BlockFace:       1,
	}
	tx := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{},
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:       protocol.UseItemActionClickBlock,
			TriggerType:      protocol.TriggerTypePlayerInput,
			BlockPosition:    ref,
			BlockFace:        1,
			HotBarSlot:       safecast.To[int32](heldSlot),
			HeldItem:         heldItem,
			Position:         bot.GetCoords().Add(mgl32.Vec3{0, 1.62, 0}),
			ClickedPosition:  mgl32.Vec3{0.5, 1.0, 0.5},
			BlockRuntimeID:   supportNetworkID,
			ClientPrediction: protocol.ClientPredictionSuccess,
		},
	}
	stop := &packet.PlayerAction{
		EntityRuntimeID: bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStopItemUseOn,
		BlockPosition:   cell,
	}
	if err := bot.WritePacket(start); err != nil {
		return false, "could not start the placement: " + err.Error()
	}
	// The one swing a real client sends on a right-click. Single, before the
	// transaction, never paced: a second swing within the ~300ms cycle restarts
	// the viewer's arm mid-flight and reads as a twitch.
	if err := bot.WritePacket(animation.PlaceSwing(bot.GetEntityRuntimeID())); err != nil {
		return false, "could not swing for the placement: " + err.Error()
	}
	if err := bot.WritePacket(tx); err != nil {
		return false, "could not send the placement: " + err.Error()
	}
	defer func() {
		// The placement is already decided; a failed stop is noise, not a
		// result worth overwriting the confirmation with.
		_ = bot.WritePacket(stop)
	}()

	return WaitPlaced(ctx, bot, cell, 1200*time.Millisecond)
}

// WaitPlaced polls until the cell reads as holding a block, and reports whether
// it does.
func WaitPlaced(ctx context.Context, bot Bot, cell protocol.BlockPos, timeout time.Duration) (bool, string) {
	deadline := time.Now().Add(timeout)
	for {
		if _, occupied := Occupied(bot, cell); occupied {
			return true, ""
		}
		if ctx.Err() != nil {
			return false, "cancelled while waiting for the block"
		}
		if time.Now().After(deadline) {
			return false, "the server never placed the block"
		}
		select {
		case <-ctx.Done():
			return false, "cancelled while waiting for the block"
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// JumpAndWait asks for a jump and waits for the body to clear cell — the state
// a placement into that cell needs.
//
// The emote is not a jump, and a placement aimed at the cell under the feet while
// the body is still standing in it is a placement the server refuses. So this
// waits for the real thing: the request is latched, the movement loop consumes
// it, and the body goes up.
//
// The result says whether that happened, so a caller reports a scaffold that
// failed instead of retrying forever against a body that never moved.
func JumpAndWait(ctx context.Context, bot Bot, cell protocol.BlockPos, timeout time.Duration) (bool, string) {
	if !BodyBlocksCell(bot.GetCoords(), cell) {
		// The body is already clear of the cell — beside it or risen past its
		// top. That is the state a placement needs, and a jump here is a request
		// that should never have been made.
		return true, ""
	}
	bot.RequestJump()

	deadline := time.Now().Add(timeout)
	for {
		if !BodyBlocksCell(bot.GetCoords(), cell) {
			return true, ""
		}
		if ctx.Err() != nil {
			return false, "cancelled while jumping"
		}
		if time.Now().After(deadline) {
			// Leaving the floor is not enough: a body an inch off the ground
			// still occupies the cell under its feet, and the placement the
			// caller is about to send would be refused as intersecting an entity.
			// Only a body that has risen past the top of the cell counts.
			return false, "the body never cleared the cell"
		}
		select {
		case <-ctx.Done():
			return false, "cancelled while jumping"
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TowerColumn is the reference cell for a tower, given where the feet are: the
// block under the body, whose top face the new block goes on.
func TowerColumn(feet mgl32.Vec3) (ref, cell protocol.BlockPos) {
	x := int32(math.Floor(float64(feet.X())))
	y := int32(math.Floor(float64(feet.Y())))
	z := int32(math.Floor(float64(feet.Z())))
	ref = protocol.BlockPos{x, y - 1, z}
	return ref, PlaceCell(ref)
}
