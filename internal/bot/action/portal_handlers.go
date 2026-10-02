// Lighting a Nether portal. enterPortal (navigation.go) only walks into a
// portal that is already lit; a freshly built obsidian frame needs flint and
// steel first, and without this handler the bot's honest answer to "go to the
// Nether" at such a frame is "I can't find a portal".
//
// The decision layer lives in dimension.Classify: obsidian frame + air
// interior + no nether_portal block is NeedsLighting. What this file adds is
// the act on top of it — where to aim the click, how to hold the flint, and
// how to report honestly: success only after the nether_portal block is seen
// appearing, never on the strength of having sent the packet.
package action

import (
	"context"
	"fmt"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/dimension"
	"bedrock-ai/internal/bot/interact"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	// portalLightItem is the only item that lights a Nether portal.
	portalLightItem = "flint_and_steel"

	// portalIgnitionTimeout bounds the wait for the nether_portal block to
	// appear after the click. BDS lights the whole frame within a tick, so a
	// wait this long that sees nothing means the click did not land or the
	// frame is not actually valid — either way, failure is the truthful
	// answer.
	portalIgnitionTimeout = 4 * time.Second
	// portalIgnitionPoll is how often the frame is re-read while waiting.
	portalIgnitionPoll = 100 * time.Millisecond
	// portalClickTimeout bounds the single UseItem click sequence itself.
	portalClickTimeout = 3 * time.Second
	// portalNavTolerance is how close the bot must arrive before the click,
	// in blocks. Bedrock's UseItem reach is ~5; standing closer than that
	// keeps the interior cell clickable even for the widest legal frame.
	portalNavTolerance = 2.5
)

func init() {
	// One behaviour, three names: the LLM plans in sentences ("light the
	// portal", "ignite the nether portal") and each phrasing maps here.
	lightPortal := func(b *bot.Bot, param, user string) { handleLightPortal(b, param, user) }
	ActionHandlers["lightportal"] = lightPortal
	ActionHandlers["light_portal"] = lightPortal
	ActionHandlers["igniteportal"] = lightPortal
}

// PortalPlan is what the classification of a found frame means for this
// action. Kept as a pure function so the routing is testable without a bot:
// a lit frame must not trigger an ignite sequence (the flint click on an
// already-lit portal is wasted at best, and reporting it as "lit" would make
// the planner believe it performed the ignition).
type PortalPlan int

const (
	PlanEnterLit  PortalPlan = iota // already lit: just walk in
	PlanLight                       // unlit nether frame: equip flint and ignite
	PlanEndPortal                   // end portal frames: cannot be lit at all
	PlanNotPortal                   // no portal-shaped structure here
)

// RoutePortalState maps a classification onto the plan.
func RoutePortalState(state dimension.PortalState) PortalPlan {
	switch state {
	case dimension.Lit:
		return PlanEnterLit
	case dimension.NeedsLighting:
		return PlanLight
	case dimension.EndPortalFrame, dimension.EndPortalOpen:
		return PlanEndPortal
	default:
		return PlanNotPortal
	}
}

// PortalScan gathers what a region around an obsidian seed looks like: the
// block names dimension.Classify needs, the obsidian cells of the frame, and
// the air cells inside it (candidates to ignite, and to walk into once lit).
//
// lookup resolves a cell to a block name; unloaded cells resolve to not-ok
// and are skipped, which is what keeps a half-loaded frame from being
// classified as if it were solid.
func PortalScan(lookup BlockLookup, seed protocol.BlockPos, dyMin, dyMax int32) (names []string, obsidian, air []protocol.BlockPos) {
	for dx := int32(-3); dx <= 3; dx++ {
		for dy := dyMin; dy <= dyMax; dy++ {
			for dz := int32(-3); dz <= 3; dz++ {
				pos := protocol.BlockPos{seed.X() + dx, seed.Y() + dy, seed.Z() + dz}
				name, ok := lookup(pos.X(), pos.Y(), pos.Z())
				if !ok {
					continue
				}
				names = append(names, name)
				switch clean := NormaliseBlockName(name); clean {
				case "obsidian":
					obsidian = append(obsidian, pos)
				case "air", "cave_air", "void_air":
					air = append(air, pos)
				}
			}
		}
	}
	return names, obsidian, air
}

// PortalInteriorCell picks the cell to navigate to on a lit portal: an
// interior air cell if the scan saw one, otherwise the seed block itself —
// the server moves the player on contact, so arriving on the frame is the
// fallback that still works when the interior is unloaded.
func PortalInteriorCell(air []protocol.BlockPos, seed protocol.BlockPos) protocol.BlockPos {
	if len(air) > 0 {
		return air[0]
	}
	return seed
}

// SelectIgnitionTarget picks the cell to apply flint and steel to.
//
// The rule mirrors how a player lights a portal: the click lands on an
// interior air block that touches the frame, and the bottom row is preferred
// because it keeps the click inside reach when the bot stands at ground
// level beside the frame. Every candidate must be a cell the lookup confirms
// as air right now — a guess that clicks a loaded stone cell is the kind of
// silent miss the interact package documents. When the frame's interior is
// not loaded (or there is no air at all), the fallback is the frame's lowest
// obsidian cell: UseItem on a frame block also ignites in vanilla, so it is
// a real second path rather than a shrug.
func SelectIgnitionTarget(obsidian, air []protocol.BlockPos, lookup BlockLookup, botPos mgl32.Vec3) (protocol.BlockPos, bool) {
	if len(obsidian) == 0 {
		return protocol.BlockPos{}, false
	}

	obsidianSet := make(map[[3]int32]struct{}, len(obsidian))
	lowestObsidian := obsidian[0]
	for _, p := range obsidian {
		obsidianSet[[3]int32{p.X(), p.Y(), p.Z()}] = struct{}{}
		if p.Y() < lowestObsidian.Y() {
			lowestObsidian = p
		}
	}

	best := protocol.BlockPos{}
	bestY := int32(0)
	bestDist := float32(0)
	found := false
	for _, cell := range air {
		adjacent := false
		for _, off := range [][3]int32{{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0}, {0, 0, 1}, {0, 0, -1}} {
			if _, ok := obsidianSet[[3]int32{cell.X() + off[0], cell.Y() + off[1], cell.Z() + off[2]}]; ok {
				adjacent = true
				break
			}
		}
		if !adjacent {
			continue
		}
		name, ok := lookup(cell.X(), cell.Y(), cell.Z())
		if !ok || !isAirName(name) {
			continue
		}
		dist := blockDistanceTo(cell, botPos)
		if !found || cell.Y() < bestY || (cell.Y() == bestY && dist < bestDist) {
			best = cell
			bestY = cell.Y()
			bestDist = dist
			found = true
		}
	}
	if found {
		return best, true
	}
	return lowestObsidian, true
}

// findFlintAndSteelSlot locates a flint and steel stack in the inventory.
// The held slot is checked first so an already-held flint does not trigger a
// swap, and lowest slot wins among the rest so repeated calls are
// deterministic instead of following map iteration order.
func findFlintAndSteelSlot(b *bot.Bot) (uint32, bool) {
	slots := b.GetInventorySlots()
	names := b.GetItemNames()
	held := b.GetHeldItemSlot()

	if stack, ok := slots[held]; ok && stack.Count > 0 {
		if portalItemNameMatches(names[stack.NetworkID]) {
			return held, true
		}
	}
	for slot := uint32(0); slot < 36; slot++ {
		stack, ok := slots[slot]
		if !ok || stack.Count == 0 {
			continue
		}
		if portalItemNameMatches(names[stack.NetworkID]) {
			return slot, true
		}
	}
	return 0, false
}

// portalItemNameMatches reports whether an inventory item name is the
// lighting tool. Substring match on the lowercased name, mirroring how the
// bot's own drop logic names items: servers report "minecraft:flint_and_steel"
// and behaviour packs invent their own prefixes.
func portalItemNameMatches(itemName string) bool {
	return strings.Contains(strings.ToLower(itemName), portalLightItem)
}

// isAirName reports whether a block name is one of the air variants.
func isAirName(name string) bool {
	switch NormaliseBlockName(name) {
	case "air", "cave_air", "void_air":
		return true
	}
	return false
}

// isLitPortalBlock is what ignition success looks like on the wire: the cell
// the frame enclosed has become a Nether portal block.
func isLitPortalBlock(name string) bool {
	switch NormaliseBlockName(name) {
	case "nether_portal", "portal":
		return true
	}
	return false
}

// blockDistanceTo is the straight-line distance from a cell's centre to a
// point, used to break ties between otherwise equal ignition candidates.
func blockDistanceTo(p protocol.BlockPos, from mgl32.Vec3) float32 {
	d := mgl32.Vec3{
		float32(p.X()) + 0.5 - from.X(),
		float32(p.Y()) + 0.5 - from.Y(),
		float32(p.Z()) + 0.5 - from.Z(),
	}
	return d.Len()
}

// portalFailure reports a lightportal attempt that could not complete.
func portalFailure(b *bot.Bot, user string, err error) {
	ReportStatus(b, user, event.ActionStatus{
		Action:  "lightportal",
		Item:    "portal",
		Success: false,
		Error:   err.Error(),
	})
}

// handleLightPortal finds the nearest portal-shaped structure, and either
// lights it (NeedsLighting), walks straight in (Lit), or reports what it
// actually found. It runs on its own goroutine because navigation and the
// ignition wait both block.
func handleLightPortal(b *bot.Bot, param, user string) {
	radii := searchRadii(b)
	obsidianPos, foundObsidian := findNearestBlock(b.GetCoords(), radii.Portal, BotBlockLookup(b), func(name string) bool {
		return NormaliseBlockName(name) == "obsidian"
	})

	// A lit portal contains no obsidian that a name scan cares about once the
	// interior is what matters, so when no frame is found, fall back to the
	// same lit-portal search enterPortal uses. This makes "light the portal"
	// degrade gracefully into "walk into the lit one" instead of failing.
	if !foundObsidian {
		litPos, foundLit := findNearestBlock(b.GetCoords(), radii.Portal, BotBlockLookup(b), func(name string) bool {
			return isLitPortalBlock(name)
		})
		if !foundLit {
			portalFailure(b, user, fmt.Errorf("nggak nemu frame obsidian atau portal dalam radius %d", radii.Portal))
			return
		}
		go func() {
			entered := b.NavigateToBlock(litPos.X(), litPos.Y(), litPos.Z(), 1.0)
			ReportStatus(b, user, event.ActionStatus{
				Action:  "lightportal",
				Item:    fmt.Sprintf("%d,%d,%d", litPos.X(), litPos.Y(), litPos.Z()),
				Success: entered,
				Error:   navError(entered, litPos),
			})
		}()
		return
	}

	names, obsidian, air := PortalScan(BotBlockLookup(b), obsidianPos, -2, 6)
	plan := RoutePortalState(dimension.Classify(names))
	b.Logger.Info("portal scan",
		"seed", fmt.Sprintf("%d,%d,%d", obsidianPos.X(), obsidianPos.Y(), obsidianPos.Z()),
		"state", dimension.Classify(names).String(),
		"obsidian", len(obsidian), "air", len(air))

	switch plan {
	case PlanEnterLit:
		cell := PortalInteriorCell(air, obsidianPos)
		go func() {
			entered := b.NavigateToBlock(cell.X(), cell.Y(), cell.Z(), 1.0)
			ReportStatus(b, user, event.ActionStatus{
				Action:  "lightportal",
				Item:    fmt.Sprintf("%d,%d,%d", cell.X(), cell.Y(), cell.Z()),
				Success: entered,
				Error:   navError(entered, cell),
			})
		}()
	case PlanEndPortal:
		portalFailure(b, user, fmt.Errorf("ini end portal — frame-nya diisi eye of ender, bukan dinyalakan"))
	case PlanNotPortal:
		portalFailure(b, user, fmt.Errorf("nemun obsidian di %d,%d,%d tapi bentuknya bukan frame portal", obsidianPos.X(), obsidianPos.Y(), obsidianPos.Z()))
	case PlanLight:
		go lightPortalFrame(b, user, obsidianPos, obsidian, air)
	}
}

// lightPortalFrame performs the ignition sequence: check the tool exists,
// equip it, stand next to the frame, click the ignition cell, then watch for
// the portal block to appear before claiming success.
func lightPortalFrame(b *bot.Bot, user string, seed protocol.BlockPos, obsidian, air []protocol.BlockPos) {
	slot, ok := findFlintAndSteelSlot(b)
	if !ok {
		portalFailure(b, user, fmt.Errorf("nggak punya %s di inventory", portalLightItem))
		return
	}
	if err := b.EquipItem(slot); err != nil {
		portalFailure(b, user, fmt.Errorf("gagal equip %s: %v", portalLightItem, err))
		return
	}

	target, ok := SelectIgnitionTarget(obsidian, air, BotBlockLookup(b), b.GetCoords())
	if !ok {
		portalFailure(b, user, fmt.Errorf("nggak nemu titik buat nyalain frame di %d,%d,%d", seed.X(), seed.Y(), seed.Z()))
		return
	}

	// Arrive beside the frame first: a UseItem click sent from beyond reach
	// is silently dropped by the host, and the bot would then wait out the
	// ignition timeout for a click that never happened.
	if !b.NavigateToBlock(target.X(), target.Y(), target.Z(), portalNavTolerance) {
		portalFailure(b, user, fmt.Errorf("gagal mendekat ke frame di %d,%d,%d", target.X(), target.Y(), target.Z()))
		return
	}

	interactor := interact.New(b, b.Logger)
	clickCtx, cancel := context.WithTimeout(context.Background(), portalClickTimeout)
	clicked, reason := interactor.ClickBlockAt(clickCtx, target)
	cancel()
	b.ResetLook()
	if !clicked {
		portalFailure(b, user, fmt.Errorf("klik %s di %d,%d,%d gagal: %s", portalLightItem, target.X(), target.Y(), target.Z(), reason))
		return
	}

	if waitForPortalLit(b, seed) {
		ReportStatus(b, user, event.ActionStatus{
			Action:  "lightportal",
			Item:    fmt.Sprintf("%d,%d,%d", target.X(), target.Y(), target.Z()),
			Success: true,
		})
		return
	}
	portalFailure(b, user, fmt.Errorf("portal belum nyala setelah klik di %d,%d,%d", target.X(), target.Y(), target.Z()))
}

// waitForPortalLit polls the cells around the frame until one of them reads
// as a nether_portal block, or the timeout passes. The scan is centred on
// the frame seed rather than the clicked cell because BDS lights the whole
// interior at once, and the clicked cell's own update can lag a tick behind.
func waitForPortalLit(b *bot.Bot, seed protocol.BlockPos) bool {
	deadline := time.Now().Add(portalIgnitionTimeout)
	for {
		for dx := int32(-3); dx <= 3; dx++ {
			for dy := int32(-2); dy <= 6; dy++ {
				for dz := int32(-3); dz <= 3; dz++ {
					name, ok := b.GetBlockName(seed.X()+dx, seed.Y()+dy, seed.Z()+dz)
					if ok && isLitPortalBlock(name) {
						return true
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(portalIgnitionPoll)
	}
}
