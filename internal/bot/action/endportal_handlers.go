// Filling an End portal, and stepping into it.
//
// The End portal is the one portal this bot cannot light, which is why it needs
// handlers of its own. A stronghold portal is a ring of twelve end_portal_frame
// blocks around a one-by-three hole, and the only thing that opens it is an eye
// of ender used on each frame; there is no flint and steel, no ignition, and no
// state to light. Once the twelfth frame is filled the whole rectangle turns
// into end_portal blocks and the hole is a doorway.
//
// Two facts make this harder than it sounds, and both are geometry rather than
// protocol:
//
//   - THE RING IS NOT IN A KNOWN ORIENTATION. Strongholds generate the portal
//     standing in the XY plane, standing in the ZY plane, or lying flat, and
//     there is nothing in the world that says which. Hardcoding the Overworld
//     layout fills ten of twelve frames and leaves a portal that can never
//     open. Everything here is therefore derived from the loaded blocks
//     themselves: scan a box, take the biggest connected group of them, and
//     read the hole out of the bounding box that leaves.
//   - A FILLED FRAME STOPS LOOKING LIKE A FRAME. Bedrock replaces the
//     end_portal_frame block with an end_portal block when the portal
//     activates, so a half-filled portal is a mixture of the two. A handler
//     that counted frames would try to put a thirteenth eye into a cell that
//     cannot take one.
//
// As everywhere else in this package, success is measured on the world and not
// on the packet: a frame counts as filled only once the cell reads as
// end_portal, and the trip to the End counts only once the terrain says the
// dimension changed.
package action

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/dimension"
	"bedrock-ai/internal/bot/interact"
	"bedrock-ai/internal/bot/perception"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	// endPortalFillItem is the only item that opens an End portal.
	endPortalFillItem = "eye_of_ender"

	// EndPortalScanRadius bounds the box read around the first frame found. The
	// ring is three wide and five tall, so from any one of its cells every
	// other cell is within four; six is margin without turning one frame into
	// a neighbourhood load.
	EndPortalScanRadius = int32(6)
	// endPortalScanBelow and endPortalScanAbove bound the vertical sweep of
	// that box, because the seed handed over by the search can be the top
	// frame as easily as the bottom one.
	endPortalScanBelow = int32(-6)
	endPortalScanAbove = int32(6)

	// endPortalClusterGap is how far apart two cells may sit and still belong
	// to one portal. A standard ring is three across, so its opposite walls are
	// exactly two apart — and that same two is what holds a ruined ring
	// together, since the survivors of a broken ring chain through the holes.
	// Any smaller and a half-destroyed portal would shatter into singletons the
	// bot would treat as unrelated ruins; any larger and two structures in one
	// room would merge into a rectangle that has no interior at all.
	endPortalClusterGap = int32(2)

	// endPortalEyeTimeout bounds the wait for one frame to actually fill. A
	// frame that has not become an end_portal block by now was not clicked,
	// and counting it anyway is how a bot ends up reporting twelve.
	endPortalEyeTimeout = 3 * time.Second
	// endPortalEyePoll is how often a frame is re-read while waiting.
	endPortalEyePoll = 100 * time.Millisecond
	// endPortalClickTimeout bounds the single UseItem click itself.
	endPortalClickTimeout = 3 * time.Second
	// endPortalNavTolerance is how close the bot must arrive before the click,
	// in blocks. Bedrock's UseItem reach is about five, and the ring is five
	// tall, so the top row is only clickable from the floor at the base —
	// which is exactly where a two-and-a-half block tolerance leaves the bot.
	endPortalNavTolerance = 2.5

	// endPortalEnterTolerance is how close the bot must arrive to the hole. The
	// server moves the player on contact, so standing against the filled portal
	// is the transfer; the hole itself is not something a walker can stand in.
	endPortalEnterTolerance = 1.0

	// endPortalTravelTimeout bounds the wait for the world to change after the
	// bot walks in. A dimension change reloads every chunk the bot can see, so
	// this is measured in seconds — the four a block reaction gets is a
	// different question entirely.
	endPortalTravelTimeout = 20 * time.Second
	// endPortalTravelSettle is the pause before the first read. Walking into
	// the portal and the new dimension arriving are not simultaneous, and
	// sampling during the reload only ever reads the world the bot left.
	endPortalTravelSettle = 2 * time.Second
	// endPortalTravelPoll is how often the terrain is re-read while waiting.
	endPortalTravelPoll = 500 * time.Millisecond
	// endPortalDetectRadius is how far the bot looks when asked where it is.
	endPortalDetectRadius = float32(16)
	// endPortalVisibleLimit caps how many visible block names one read costs.
	endPortalVisibleLimit = 96
)

func init() {
	// One behaviour, several names: the planner says "fill the end portal
	// frames", "fill the end portal", or "activate the end portal", and every
	// phrasing means the same twelve clicks.
	fillFrames := func(b *bot.Bot, param, user string) { handleFillEndPortal(b, param, user) }
	ActionHandlers["fillframe"] = fillFrames
	ActionHandlers["fillendportal"] = fillFrames
	ActionHandlers["activateendportal"] = fillFrames
	ActionHandlers["fill_frame"] = fillFrames

	ActionHandlers["enterendportal"] = handleEnterEndPortal
	ActionHandlers["enter_endportal"] = handleEnterEndPortal
}

// EndPortalPlan is what a classification of the found structure means for these
// actions. Pure, so the routing can be tested without a bot: the two jobs are
// genuinely different, and running the wrong one wastes the player's eyes of
// ender or walks the bot into a sealed ring.
type EndPortalPlan int

const (
	// PlanEndNotPortal means there is no end portal here at all.
	PlanEndNotPortal EndPortalPlan = iota
	// PlanEndFill means frames are present and need eyes of ender.
	PlanEndFill
	// PlanEndEnter means the portal is already active and only needs walking into.
	PlanEndEnter
)

// RouteEndPortalState maps a classification onto the plan.
func RouteEndPortalState(state dimension.PortalState) EndPortalPlan {
	switch state {
	case dimension.EndPortalFrame:
		return PlanEndFill
	case dimension.EndPortalOpen:
		return PlanEndEnter
	default:
		return PlanEndNotPortal
	}
}

// endPortalCells is what a scan of the altar region found: the loaded cells
// that are part of an end portal structure, keyed by position and carrying the
// name the world cache reported. A map rather than a slice because every
// question asked of it — which cells are taken, what is this one called — is a
// lookup, and because the scan builds it in whatever order the cache hands back.
type endPortalCells map[[3]int32]string

// PosLess orders block positions bottom-first. Bottom-first is not cosmetic: the
// ring is five blocks tall and the bot can only reach its lower half from the
// floor, so when eyes of ender run out the frames that went unfilled should be
// the ones that were never clickable anyway.
func PosLess(a, b protocol.BlockPos) bool {
	if a.Y() != b.Y() {
		return a.Y() < b.Y()
	}
	if a.X() != b.X() {
		return a.X() < b.X()
	}
	return a.Z() < b.Z()
}

// isEndPortalFrameName reports whether a block is an empty portal frame.
func isEndPortalFrameName(name string) bool {
	return NormaliseBlockName(name) == "end_portal_frame"
}

// isEndPortalBlockName reports whether a block is part of an end portal
// structure, whether or not it has been filled. The filled case reads as
// end_portal because Bedrock replaces the frame block itself, which is why both
// names have to be accepted here and separated later.
func isEndPortalBlockName(name string) bool {
	switch NormaliseBlockName(name) {
	case "end_portal_frame", "end_portal":
		return true
	}
	return false
}

// isFilledFrameName reports what a successful fill looks like on the wire: the
// clicked cell is now an end_portal block. Checked on the clicked cell rather
// than across the portal, because the portal only flips on the last frame and a
// partial fill leaves the others still reading as frames.
func isFilledFrameName(name string) bool {
	return NormaliseBlockName(name) == "end_portal"
}

// ScanEndPortal collects the end portal cells in a box around seed.
//
// The lookup is the ordinary world-cache reader, so unloaded cells resolve to
// not-ok and contribute nothing — which is what keeps half a ring from being
// mistaken for a whole one. Nothing here assumes a shape: the orientation, and
// even the size, are read back out of what the world actually has.
func ScanEndPortal(lookup BlockLookup, seed protocol.BlockPos, radius, dyMin, dyMax int32) endPortalCells {
	cells := make(endPortalCells)
	for dx := -radius; dx <= radius; dx++ {
		for dy := dyMin; dy <= dyMax; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				pos := protocol.BlockPos{seed.X() + dx, seed.Y() + dy, seed.Z() + dz}
				name, ok := lookup(pos.X(), pos.Y(), pos.Z())
				if !ok || !isEndPortalBlockName(name) {
					continue
				}
				cells[[3]int32{pos.X(), pos.Y(), pos.Z()}] = name
			}
		}
	}
	return cells
}

// LargestFrameCluster returns the biggest group of cells that belong to the same
// portal, found by chaining through endPortalClusterGap.
//
// Chaining rather than a fixed rule is what makes a ruined portal work: a ring
// with a whole row missing is still one structure, and the survivors link up
// across the hole. The input is sorted first so the grouping does not depend on
// map iteration order — an unseeded scan would otherwise pick a different
// "biggest" group from one tick to the next.
func LargestFrameCluster(cells []protocol.BlockPos) []protocol.BlockPos {
	if len(cells) == 0 {
		return nil
	}
	ordered := append([]protocol.BlockPos(nil), cells...)
	sort.Slice(ordered, func(i, j int) bool { return PosLess(ordered[i], ordered[j]) })

	var groups [][]protocol.BlockPos
	for _, pos := range ordered {
		placed := false
		// Indexed rather than ranged: ranging copies each slice header, so an
		// appended cell would grow a copy and the group would silently stay
		// one cell long.
		for gi := range groups {
			if clusterReaches(groups[gi], pos) {
				groups[gi] = append(groups[gi], pos)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []protocol.BlockPos{pos})
		}
	}

	best := 0
	for i := 1; i < len(groups); i++ {
		if len(groups[i]) > len(groups[best]) {
			best = i
		}
	}
	return groups[best]
}

// clusterReaches reports whether any cell of a group is close enough to pos for
// the two to be part of the same portal.
func clusterReaches(group []protocol.BlockPos, pos protocol.BlockPos) bool {
	for _, member := range group {
		if Abs32(int(member.X()-pos.X())) <= endPortalClusterGap &&
			Abs32(int(member.Y()-pos.Y())) <= endPortalClusterGap &&
			Abs32(int(member.Z()-pos.Z())) <= endPortalClusterGap {
			return true
		}
	}
	return false
}

// PortalParts splits the scan into the three views the rest of the file needs:
// the whole rectangle (where to stand and what to classify), the frames still
// empty (what an eye of ender can go into), and the cells already filled (what
// somebody else, or a previous run, did). All three are position-sorted.
func PortalParts(cells endPortalCells) (rect, frames, open []protocol.BlockPos) {
	all := make([]protocol.BlockPos, 0, len(cells))
	for key, name := range cells {
		if !isEndPortalBlockName(name) {
			continue
		}
		all = append(all, protocol.BlockPos{key[0], key[1], key[2]})
	}

	cluster := LargestFrameCluster(all)
	rect = make([]protocol.BlockPos, 0, len(cluster))
	frames = make([]protocol.BlockPos, 0, len(cluster))
	open = make([]protocol.BlockPos, 0, len(cluster))
	for _, pos := range cluster {
		key := [3]int32{pos.X(), pos.Y(), pos.Z()}
		rect = append(rect, pos)
		if isEndPortalFrameName(cells[key]) {
			frames = append(frames, pos)
		} else {
			open = append(open, pos)
		}
	}
	return rect, frames, open
}

// CellNames renders the classified rectangle back into block names, so the
// decision about what was found goes through dimension.Classify like every
// other portal in this bot instead of through a private reimplementation of it.
func CellNames(cells endPortalCells, rect []protocol.BlockPos) []string {
	names := make([]string, 0, len(rect))
	for _, pos := range rect {
		if name, ok := cells[[3]int32{pos.X(), pos.Y(), pos.Z()}]; ok {
			names = append(names, name)
		}
	}
	return names
}

// EndPortalInteriorCell picks the cell to stand in to go through the portal: the
// most enclosed cell of the structure, preferring a filled one, and among equals
// the one nearest the bot.
//
// "Most enclosed" rather than "a hole in the box" is deliberate, and it is the
// only rule that works for both states of the portal. An unactivated ring leaves
// a one-by-three hole in the middle; an activated one has already turned that
// hole into end_portal blocks along with the frames, so there is no empty cell
// left to find. In both cases the answer is the same place — the middle of the
// ring — and how many of its six neighbours are also portal cells is what
// identifies it, without ever assuming which way the portal is facing. A stray
// frame elsewhere in the room cannot be mistaken for it: it touches one cell at
// most where the middle of a ring touches four.
//
// Filled cells are preferred over empty ones so that a half-filled portal aims
// at the part that is actually open. A ring with nothing filled has no interior
// to speak of and the ranking lands on a frame cell instead — which is harmless,
// because the enter path refuses to run at all until the portal is active.
func EndPortalInteriorCell(rect, open []protocol.BlockPos, from mgl32.Vec3) (protocol.BlockPos, bool) {
	if len(rect) == 0 {
		return protocol.BlockPos{}, false
	}

	occupied := make(map[[3]int32]struct{}, len(rect))
	for _, pos := range rect {
		occupied[[3]int32{pos.X(), pos.Y(), pos.Z()}] = struct{}{}
	}
	filled := make(map[[3]int32]struct{}, len(open))
	for _, pos := range open {
		filled[[3]int32{pos.X(), pos.Y(), pos.Z()}] = struct{}{}
	}

	best := protocol.BlockPos{}
	bestTouches, bestDist := -1, float32(0)
	bestFilled := false
	found := false
	for _, cell := range rect {
		touches := ringNeighbours(occupied, cell.X(), cell.Y(), cell.Z())
		if touches == 0 {
			continue
		}
		_, isFilled := filled[[3]int32{cell.X(), cell.Y(), cell.Z()}]
		dist := blockDistanceTo(cell, from)
		if !found || betterInterior(touches, isFilled, dist, bestTouches, bestFilled, bestDist) {
			best, bestTouches, bestFilled, bestDist, found = cell, touches, isFilled, dist, true
		}
	}
	return best, found
}

// betterInterior ranks two candidate cells: enclosure first, then whether the
// cell is one the portal has already opened, then how close the bot is. Written
// out rather than folded into a sort so the ordering is one readable sentence.
func betterInterior(touches int, isFilled bool, dist float32, bestTouches int, bestFilled bool, bestDist float32) bool {
	if touches != bestTouches {
		return touches > bestTouches
	}
	if isFilled != bestFilled {
		return isFilled
	}
	return dist < bestDist
}

// ringNeighbours counts how many of the six cells touching a point are part of
// the portal. Zero is the disqualifier: a cell nothing touches is not inside
// anything, it is just somewhere the bounding box happens to reach.
func ringNeighbours(occupied map[[3]int32]struct{}, x, y, z int32) int {
	touches := 0
	for _, off := range [][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
		if _, ok := occupied[[3]int32{x + off[0], y + off[1], z + off[2]}]; ok {
			touches++
		}
	}
	return touches
}

// EmptyFrameCells lists the frames that still need an eye of ender, bottom
// first. A frame that already reads as an end_portal block is somebody's work
// already done — most often the bot's own, on a run that was interrupted — and
// counting it as empty would both spend an item on a cell that cannot take one
// and inflate the number reported at the end.
func EmptyFrameCells(frames, open []protocol.BlockPos) []protocol.BlockPos {
	filled := make(map[[3]int32]struct{}, len(open))
	for _, pos := range open {
		filled[[3]int32{pos.X(), pos.Y(), pos.Z()}] = struct{}{}
	}

	var empty []protocol.BlockPos
	seen := make(map[[3]int32]struct{}, len(frames))
	for _, pos := range frames {
		key := [3]int32{pos.X(), pos.Y(), pos.Z()}
		if _, done := filled[key]; done {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		empty = append(empty, pos)
	}
	sort.Slice(empty, func(i, j int) bool { return PosLess(empty[i], empty[j]) })
	return empty
}

// FramesNeedingEyes caps the fill list at the number of eyes actually held.
// Every queued cell is a UseItem that consumes one, so a twelfth empty-handed
// click is not a harmless retry — it is a frame the bot will report as filled
// and the server will never have touched.
func FramesNeedingEyes(frames, open []protocol.BlockPos, eyes int) []protocol.BlockPos {
	empty := EmptyFrameCells(frames, open)
	if eyes <= 0 || len(empty) == 0 {
		return nil
	}
	if eyes >= len(empty) {
		return empty
	}
	return empty[:eyes]
}

// IsEyeOfEnder reports whether an inventory item name is the frame filler.
// Substring match on the lowercased name, mirroring how the drop and drop-in
// logic name items: servers report "minecraft:eye_of_ender" and behaviour packs
// invent their own prefixes.
func IsEyeOfEnder(itemName string) bool {
	return strings.Contains(strings.ToLower(itemName), endPortalFillItem)
}

// findEyeOfEnderSlot locates an eye of ender stack. The held slot is checked
// first so an already-held filler does not trigger a swap, and the lowest slot
// wins among the rest so repeated calls are deterministic rather than following
// map iteration order.
func findEyeOfEnderSlot(b *bot.Bot) (uint32, bool) {
	slots := b.GetInventorySlots()
	names := b.GetItemNames()
	held := b.GetHeldItemSlot()

	if stack, ok := slots[held]; ok && stack.Count > 0 {
		if name, ok := names[stack.NetworkID]; ok && IsEyeOfEnder(name) {
			return held, true
		}
	}
	for slot := uint32(0); slot < 36; slot++ {
		stack, ok := slots[slot]
		if !ok || stack.Count == 0 {
			continue
		}
		if name, ok := names[stack.NetworkID]; ok && IsEyeOfEnder(name) {
			return slot, true
		}
	}
	return 0, false
}

// visibleDimension reads where the bot is out of the terrain in front of it.
// The bot exposes no server dimension ID, so the blocks are the evidence — and
// they are read through dimension.Detect rather than a local count, so this file
// cannot drift from what the rest of the bot believes a dimension is.
func visibleDimension(b *bot.Bot) dimension.Dimension {
	names := strings.Split(perception.VisibleBlockNames(b, endPortalDetectRadius, endPortalVisibleLimit), ",")
	return dimension.Detect(names)
}

// DimensionChanged decides whether a read of the terrain counts as having
// arrived. Unknown is never an arrival: it is what an empty view returns, and
// the whole world unloads during the transition, so the first readings after
// walking in say nothing at all. Requiring a real, different answer is what
// keeps "I travelled to the End" from being printed by a bot still standing in
// a stronghold.
func DimensionChanged(before, after dimension.Dimension) bool {
	return after != dimension.Unknown && after != before
}

// endPortalSite is one located portal: where it was found and what is in it.
type endPortalSite struct {
	// seed is the cell the search handed over, kept for logging and for the
	// failure messages that have to name somewhere.
	seed protocol.BlockPos
	// rect is the whole rectangle, frames and filled cells together.
	rect []protocol.BlockPos
	// frames are the cells still empty, filled are the ones already done.
	frames []protocol.BlockPos
	filled []protocol.BlockPos
	// state is dimension.Classify over the rectangle.
	state dimension.PortalState
}

// locateEndPortal finds the nearest end portal structure and reads its shape.
//
// The world cache is asked first because it returns a position the rest of the
// action can act on. A visible name carries no coordinates, so perception can
// only be used to explain a miss — which is worth doing, because "there is an end
// portal frame right there and you did not find it" and "there is no end portal
// here" call for completely different next moves.
func locateEndPortal(b *bot.Bot) (endPortalSite, bool) {
	radii := searchRadii(b)
	seed, found := findNearestBlock(b.GetCoords(), radii.Portal, BotBlockLookup(b), isEndPortalBlockName)
	if !found {
		return endPortalSite{}, false
	}

	cells := ScanEndPortal(BotBlockLookup(b), seed, EndPortalScanRadius, endPortalScanBelow, endPortalScanAbove)
	rect, frames, filled := PortalParts(cells)
	return endPortalSite{
		seed:   seed,
		rect:   rect,
		frames: frames,
		filled: filled,
		state:  dimension.Classify(CellNames(cells, rect)),
	}, true
}

// endPortalVisibleHere reports whether an end portal block is in the bot's field
// of view right now. Used only to phrase a failure, never to act: a name with
// no coordinates cannot tell the bot which cell to click.
func endPortalVisibleHere(b *bot.Bot) bool {
	names := strings.Split(perception.VisibleBlockNames(b, endPortalDetectRadius, endPortalVisibleLimit), ",")
	for _, name := range names {
		if isEndPortalBlockName(name) {
			return true
		}
	}
	return false
}

// endPortalFailure reports an end portal action that could not run.
func endPortalFailure(b *bot.Bot, user, actionName string, err error) {
	ReportStatus(b, user, event.ActionStatus{
		Action:  actionName,
		Item:    "end_portal",
		Success: false,
		Error:   err.Error(),
	})
}

// notFoundError phrases a failed search. A frame in plain view gets its own
// message: the difference is that the world cache is empty there, which no amount
// of retrying this action will fix, and saying only "not found" would send the
// planner round the same circle.
func notFoundError(b *bot.Bot, radii SearchRadii) error {
	if endPortalVisibleHere(b) {
		return fmt.Errorf("nggak nemu frame end portal di cache dunia padahal kelihatan di depan (radius %d)", radii.Portal)
	}
	return fmt.Errorf("nggak nemu end portal dalam radius %d blok", radii.Portal)
}

// handleFillEndPortal locates the portal and fills whatever frames are still
// empty. The search and the classification run inline because they are cheap
// and the caller wants an answer either way; the filling itself blocks on
// walking and waiting, so it goes to its own goroutine.
func handleFillEndPortal(b *bot.Bot, _, user string) {
	site, found := locateEndPortal(b)
	if !found {
		endPortalFailure(b, user, "fillframe", notFoundError(b, searchRadii(b)))
		return
	}
	b.Logger.Info("end portal located",
		"at", fmt.Sprintf("%d,%d,%d", site.seed.X(), site.seed.Y(), site.seed.Z()),
		"state", site.state.String(), "frames", len(site.frames), "filled", len(site.filled))

	switch RouteEndPortalState(site.state) {
	case PlanEndEnter:
		// Nothing to do, and saying so plainly is worth more than spending
		// twelve eyes of ender on a portal that is already open.
		ReportStatus(b, user, event.ActionStatus{
			Action:  "fillframe",
			Item:    "end_portal",
			Count:   len(site.filled),
			Success: true,
		})
	case PlanEndFill:
		go runFillEndPortal(b, user, site)
	default:
		endPortalFailure(b, user, "fillframe", fmt.Errorf("yang ketemu bukan end portal (%s)", site.state))
	}
}

// runFillEndPortal does the work: check the supply, equip it, then use one eye
// on one frame at a time, counting only the frames the world confirms.
func runFillEndPortal(b *bot.Bot, user string, site endPortalSite) {
	needed := EmptyFrameCells(site.frames, site.filled)
	if len(needed) == 0 {
		ReportStatus(b, user, event.ActionStatus{
			Action:  "fillframe",
			Item:    "end_portal",
			Success: true,
		})
		return
	}

	eyes := CountInventoryItems(b.GetInventorySlots(), b.GetItemNames(), endPortalFillItem)
	if eyes == 0 {
		endPortalFailure(b, user, "fillframe", fmt.Errorf("nggak punya %s — butuh %d buat buka portal ini", endPortalFillItem, len(needed)))
		return
	}

	// A shortfall is not a reason to stop. Filling the frames it can reach and
	// then saying exactly how many are left is more useful than refusing to
	// start, and the report below is what keeps it from being a false success.
	targets := FramesNeedingEyes(site.frames, site.filled, eyes)
	if len(targets) < len(needed) {
		b.Logger.Info("end portal: eye of ender nggak cukup, ngeisi seperlunya",
			"have", eyes, "need", len(needed))
	}

	slot, ok := findEyeOfEnderSlot(b)
	if !ok {
		endPortalFailure(b, user, "fillframe", fmt.Errorf("nggak nemu slot %s di inventory", endPortalFillItem))
		return
	}
	if err := b.EquipItem(slot); err != nil {
		endPortalFailure(b, user, "fillframe", fmt.Errorf("gagal equip %s: %v", endPortalFillItem, err))
		return
	}

	filled := fillEndPortalFrames(b, targets)
	if filled == len(needed) {
		ReportStatus(b, user, event.ActionStatus{
			Action:  "fillframe",
			Item:    "end_portal",
			Count:   filled,
			Success: true,
		})
		return
	}
	// Partial is a failure. The portal does not open on eleven of twelve
	// frames, so a report that only said "filled some" would leave the
	// planner believing it could go through.
	endPortalFailure(b, user, "fillframe", fmt.Errorf(
		"cuma %d dari %d frame keisi, butuh %d %s lagi", filled, len(needed), len(needed)-filled, endPortalFillItem))
}

// fillEndPortalFrames clicks an eye of ender onto each cell in turn and returns
// how many the world actually accepted.
//
// A frame that cannot be reached is skipped rather than abandoned: the bot may
// be able to get to the bottom of a five-tall ring and not the top, and a
// truthful partial count is worth more than one total failure with no
// information in it. Either way the count returned is the number of cells that
// became end_portal blocks, not the number of packets that went out.
func fillEndPortalFrames(b *bot.Bot, cells []protocol.BlockPos) int {
	interactor := interact.New(b, b.Logger)
	filled := 0
	for _, cell := range cells {
		// A UseItem sent from beyond reach is dropped by the host without a
		// word, and the bot would then wait out the fill timeout for a click
		// that never happened.
		if !b.NavigateToBlock(cell.X(), cell.Y(), cell.Z(), endPortalNavTolerance) {
			b.Logger.Info("end portal: frame nggak bisa didekati", "pos", cell)
			continue
		}

		clickCtx, cancel := context.WithTimeout(context.Background(), endPortalClickTimeout)
		clicked, reason := interactor.ClickBlockAt(clickCtx, cell)
		cancel()
		b.ResetLook()
		if !clicked {
			b.Logger.Info("end portal: klik frame gagal", "pos", cell, "reason", reason)
			continue
		}
		if !waitForFrameFilled(b, cell) {
			b.Logger.Info("end portal: frame nggak berubah jadi end_portal", "pos", cell)
			continue
		}
		filled++
	}
	return filled
}

// waitForFrameFilled polls one cell until it reads as an end_portal block, or
// the timeout passes. The same bounded-poll shape as the Nether ignition wait,
// and for the same reason: the world either changed or it did not, and there is
// no third answer to give.
func waitForFrameFilled(b *bot.Bot, cell protocol.BlockPos) bool {
	deadline := time.Now().Add(endPortalEyeTimeout)
	for {
		if name, ok := b.GetBlockName(cell.X(), cell.Y(), cell.Z()); ok && isFilledFrameName(name) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(endPortalEyePoll)
	}
}

// handleEnterEndPortal walks into an end portal that is already active. It
// refuses anything else: entering an unfilled ring is not a journey, it is
// standing inside a hole, and the server will not move the player.
func handleEnterEndPortal(b *bot.Bot, _, user string) {
	site, found := locateEndPortal(b)
	if !found {
		endPortalFailure(b, user, "enterendportal", notFoundError(b, searchRadii(b)))
		return
	}

	switch RouteEndPortalState(site.state) {
	case PlanEndEnter:
		cell, ok := EndPortalInteriorCell(site.rect, site.filled, b.GetCoords())
		if !ok {
			endPortalFailure(b, user, "enterendportal", fmt.Errorf(
				"end portal di %d,%d,%d nggak punya ruang di dalem buat dilalui",
				site.seed.X(), site.seed.Y(), site.seed.Z()))
			return
		}
		b.Logger.Info("entering end portal",
			"cell", fmt.Sprintf("%d,%d,%d", cell.X(), cell.Y(), cell.Z()))
		go runEnterEndPortal(b, user, cell)
	case PlanEndFill:
		endPortalFailure(b, user, "enterendportal", fmt.Errorf(
			"end portal di %d,%d,%d belum aktif — %d frame masih kosong, isi dulu pakai fillframe",
			site.seed.X(), site.seed.Y(), site.seed.Z(), len(EmptyFrameCells(site.frames, site.filled))))
	default:
		endPortalFailure(b, user, "enterendportal", fmt.Errorf("yang ketemu bukan end portal (%s)", site.state))
	}
}

// runEnterEndPortal walks into the portal and then waits for the world to say
// where the bot now is. The dimension read happens before the walk as well as
// after, because "the terrain changed" is only evidence of arrival if there was
// a before to change from.
func runEnterEndPortal(b *bot.Bot, user string, cell protocol.BlockPos) {
	before := visibleDimension(b)
	if !b.NavigateToBlock(cell.X(), cell.Y(), cell.Z(), endPortalEnterTolerance) {
		endPortalFailure(b, user, "enterendportal", fmt.Errorf(
			"gagal sampai ke end portal di %d,%d,%d", cell.X(), cell.Y(), cell.Z()))
		return
	}
	if waitForDimensionChange(b, before) {
		ReportStatus(b, user, event.ActionStatus{
			Action:  "enterendportal",
			Item:    visibleDimension(b).String(),
			Success: true,
		})
		return
	}
	endPortalFailure(b, user, "enterendportal", fmt.Errorf(
		"sudah masuk tapi dimensinya nggak berubah — masih di %s", before))
}

// waitForDimensionChange polls the terrain until it names a different dimension
// than the one the bot left. The leading settle is not optional: the world
// unloads during the transition, and every reading taken inside that window is
// either the world being left or an empty one, neither of which is an answer.
func waitForDimensionChange(b *bot.Bot, before dimension.Dimension) bool {
	time.Sleep(endPortalTravelSettle)
	deadline := time.Now().Add(endPortalTravelTimeout)
	for {
		if DimensionChanged(before, visibleDimension(b)) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(endPortalTravelPoll)
	}
}
