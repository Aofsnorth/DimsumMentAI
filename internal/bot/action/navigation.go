package action

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/perception"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Navigation is the family of "go there" actions. They all end in the same two
// movements — WalkTo for a point, NavigateToBlock for a cell — but the player
// asks for them in very different words, and each asks for a different arrival
// spot:
//
//	goto:X,Y,Z        walk to a point and stop there
//	gotoblock:X,Y,Z   walk up to a block and stop next to it
//	standon:X,Y,Z     stand on TOP of a block, not beside it
//	enterportal       find the nearest portal and step into it
//
// The last two are the ones that used to be missing and that a player actually
// needs: "go stand on that block" and "go through that portal" are not the same
// destination as "go to those coordinates", and picking the wrong one leaves the
// bot standing next to the thing it was told to stand on.
//
// Every action also accepts a block NAME where coordinates are expected
// (gotoblock:oak_log walks to the nearest oak log), because that is how people
// actually give directions. A target that parses as "X,Y,Z" is coordinates;
// anything else is a block name.

// SearchRadii bounds how far the navigation actions look for a named block and
// for a portal, in blocks. They are a struct rather than constants because they
// are taste: a radius that is right in a clearing is wrong in a cave, and a
// wider portal hunt costs a noticeably heavier tick.
type SearchRadii struct {
	Block  int
	Portal int
}

// DefaultSearchRadii is the fallback when a bot has none configured.
var DefaultSearchRadii = SearchRadii{Block: 32, Portal: 48}

// searchRadii returns the radii this bot navigates with.
func searchRadii(b *bot.Bot) SearchRadii {
	if b.SearchRadiusBlocks > 0 && b.SearchRadiusPortal > 0 {
		return SearchRadii{Block: b.SearchRadiusBlocks, Portal: b.SearchRadiusPortal}
	}
	return DefaultSearchRadii
}

// portalBlockNames are the blocks that move the player somewhere else. A Nether
// portal is the common case, but End portals and gateways are reachable the same
// way and cost nothing to include.
var portalBlockNames = []string{
	"portal",
	"nether_portal",
	"end_portal",
	"end_gateway",
	"end_portal_frame",
}

// ParseNavCoords parses an "X,Y,Z" target. The bool reports whether the param
// was coordinates at all — a non-coordinate param is a block name, and the
// caller must not treat a failed parse as a bad request.
func ParseNavCoords(param string) (protocol.BlockPos, bool) {
	parts := strings.Split(strings.TrimSpace(param), ",")
	if len(parts) != 3 {
		return protocol.BlockPos{}, false
	}
	var coords [3]int32
	for i, p := range parts {
		// ParseFloat then truncate: strconv.Atoi rejects "12.5", and players
		// routinely say "100.5, 64, -20" meaning block 100.
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return protocol.BlockPos{}, false
		}
		coords[i] = int32(v)
	}
	return protocol.BlockPos{coords[0], coords[1], coords[2]}, true
}

// NormaliseBlockName lowercases a block name and strips its namespace, so
// "Minecraft:Oak_Log" and "oak_log" are the same target.
func NormaliseBlockName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "minecraft:")
	name = strings.TrimPrefix(name, "custom:")
	return strings.TrimSpace(name)
}

// blockNameMatches reports whether a world block name satisfies a target. A
// target is matched exactly first, then as a suffix, so "log" finds "oak_log"
// and "stone" finds "stone_button" — the loose end is what makes "go to the
// nearest wood" work without the player knowing the species.
func blockNameMatches(world, target string) bool {
	world = NormaliseBlockName(world)
	target = NormaliseBlockName(target)
	if world == "" || target == "" {
		return false
	}
	if world == target {
		return true
	}
	return strings.HasSuffix(world, "_"+target)
}

// BlockLookup resolves a world cell to a block name. Abstracted so the search
// can be exercised without a live world cache.
type BlockLookup func(x, y, z int32) (string, bool)

// findNearestBlock returns the closest matching visible cell in one bounded
// volume pass. Rewalking every cube interior for each shell costs O(radius^4).
func findNearestBlock(origin mgl32.Vec3, radius int, lookup BlockLookup, want func(string) bool) (protocol.BlockPos, bool) {
	ox := int32(math.Floor(float64(origin.X())))
	oy := int32(math.Floor(float64(origin.Y())))
	oz := int32(math.Floor(float64(origin.Z())))

	best := protocol.BlockPos{}
	bestDist := float64(radius*radius + 1)
	found := false

	for dx := -radius; dx <= radius; dx++ {
		for dy := -radius; dy <= radius; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				distSq := float64(dx*dx + dy*dy + dz*dz)
				if distSq > float64(radius*radius) || distSq >= bestDist {
					continue
				}
				name, ok := lookup(ox+int32(dx), oy+int32(dy), oz+int32(dz))
				if !ok || !want(name) {
					continue
				}
				if distSq < bestDist {
					bestDist = distSq
					best = protocol.BlockPos{ox + int32(dx), oy + int32(dy), oz + int32(dz)}
					found = true
				}
			}
		}
	}
	return best, found
}

// Abs32 is abs for the int32 block coordinates the search works in.
func Abs32(v int) int32 {
	if v < 0 {
		return -int32(v)
	}
	return int32(v)
}

// BotBlockLookup resolves semantic targets only when currently visible.
// Movement may still use received terrain for collision, not hidden resources.
func BotBlockLookup(b *bot.Bot) BlockLookup {
	return func(x, y, z int32) (string, bool) {
		name, ok := b.GetBlockName(x, y, z)
		if !ok || NormaliseBlockName(name) == "air" || !perception.SeesBlock(b, protocol.BlockPos{x, y, z}) {
			return "", false
		}
		return name, true
	}
}

// resolveNavTarget turns an action parameter into a block position. It accepts
// either explicit coordinates or a block name to search for, and reports which
// one it used so the failure message can be specific.
func resolveNavTarget(b *bot.Bot, param string, radius int) (protocol.BlockPos, bool, error) {
	param = strings.TrimSpace(param)
	if param == "" {
		return protocol.BlockPos{}, false, fmt.Errorf("butuh koordinat X,Y,Z atau nama block")
	}
	if pos, ok := ParseNavCoords(param); ok {
		return pos, true, nil
	}
	pos, ok := findNearestBlock(b.GetCoords(), radius, BotBlockLookup(b), func(name string) bool {
		return blockNameMatches(name, param)
	})
	if !ok {
		return protocol.BlockPos{}, false, fmt.Errorf("nggak nemu blok '%s' dalam radius %d", param, radius)
	}
	return pos, false, nil
}

// navFailure reports a navigation action that could not start, so the player
// hears why instead of watching the bot stand still.
func navFailure(b *bot.Bot, user, actionName, param string, err error) {
	item := param
	if item == "" {
		item = actionName
	}
	ReportStatus(b, user, event.ActionStatus{
		Action:  actionName,
		Item:    item,
		Success: false,
		Error:   err.Error(),
	})
}

// goToCoords walks to an exact point.
func goToCoords(b *bot.Bot, param, user string) {
	target, isCoords, err := resolveNavTarget(b, param, searchRadii(b).Block)
	if err != nil {
		navFailure(b, user, "goto", param, err)
		return
	}
	if !isCoords {
		b.Logger.Info("goto resolved a block name", "target", param, "at", target)
	}
	go func() {
		arrived := b.NavigateToBlock(target.X(), target.Y(), target.Z(), 1.5)
		ReportStatus(b, user, event.ActionStatus{Action: "goto", Item: param, Success: arrived, Error: navError(arrived, target)})
	}()
}

// goToBlock walks up to a block and stops beside it. NavigateToBlock picks a
// standable neighbour, so this does not try to stand inside the block itself.
func goToBlock(b *bot.Bot, param, user string) {
	runBlockNav(b, "gotoblock", param, user, func(pos protocol.BlockPos) protocol.BlockPos {
		return pos
	})
}

// standOnBlock stands on TOP of a block. The arrival cell is one above the
// target, which is the only difference from goToBlock — and the whole point:
// being next to a block and standing on it are different destinations.
func standOnBlock(b *bot.Bot, param, user string) {
	runBlockNav(b, "standon", param, user, func(pos protocol.BlockPos) protocol.BlockPos {
		return protocol.BlockPos{pos.X(), pos.Y() + 1, pos.Z()}
	})
}

// runBlockNav is the shared body of the block-targeted walks: resolve the
// target, lift it to the arrival cell, and navigate. It runs on its own
// goroutine because NavigateToBlock blocks until the bot arrives.
func runBlockNav(b *bot.Bot, actionName, param, user string, arrival func(protocol.BlockPos) protocol.BlockPos) {
	target, _, err := resolveNavTarget(b, param, searchRadii(b).Block)
	if err != nil {
		navFailure(b, user, actionName, param, err)
		return
	}

	cell := arrival(target)
	go func() {
		ok := b.NavigateToBlock(cell.X(), cell.Y(), cell.Z(), 1.5)
		ReportStatus(b, user, event.ActionStatus{
			Action:  actionName,
			Item:    param,
			Success: ok,
			Error:   navError(ok, target),
		})
	}()
}

func navError(ok bool, target protocol.BlockPos) string {
	if ok {
		return ""
	}
	return fmt.Sprintf("gagal sampai ke %d,%d,%d", target.X(), target.Y(), target.Z())
}

// enterPortal finds the nearest portal and walks into it. The target is the
// portal block itself: the server moves the player on contact, so arriving at
// the cell is what triggers the transfer.
func enterPortal(b *bot.Bot, param, user string) {
	wanted := portalBlockNames
	if name := NormaliseBlockName(param); name != "" {
		wanted = []string{name}
	}

	radii := searchRadii(b)
	pos, ok := findNearestBlock(b.GetCoords(), radii.Portal, BotBlockLookup(b), func(name string) bool {
		clean := NormaliseBlockName(name)
		for _, want := range wanted {
			if clean == want {
				return true
			}
		}
		return false
	})
	if !ok {
		navFailure(b, user, "enterportal", param,
			fmt.Errorf("nggak nemu portal dalam radius %d", radii.Portal))
		return
	}

	b.Logger.Info("entering portal", "at", pos)
	go func() {
		// Aim one cell into the portal so the player actually crosses the
		// threshold; stopping on the frame is not the same as going through.
		entered := b.NavigateToBlock(pos.X(), pos.Y(), pos.Z(), 1.0)
		ReportStatus(b, user, event.ActionStatus{
			Action:  "enterportal",
			Item:    fmt.Sprintf("%d,%d,%d", pos.X(), pos.Y(), pos.Z()),
			Success: entered,
			Error:   navError(entered, pos),
		})
	}()
}
