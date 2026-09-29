package action

import (
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/dimension"
	"bedrock-ai/internal/bot/perception"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Stronghold search is the "find the place nobody can point at" problem: the
// portal room has no beacon and sits underground, so there is no single block
// to walk to. The search therefore works in two layers:
//
//   - an expanding square of waypoints around the start point. At each one the
//     bot stands still and scans the loaded world for blocks that only exist in
//     or beside a stronghold (dimension.IsStrongholdHint).
//   - when a hint is seen, the search recentres on it and starts its rings
//     again from there, a bounded number of times, so a stray chiseled brick
//     cannot walk the bot forever.
//
// The waypoint layout and the hint ranking are pure functions — the walking is
// the part that needs a server, and keeping the policy out of it means the
// search can be wrong in a test instead of in a world.

func init() {
	actionHandlers["explorestronghold"] = handleExploreStronghold
	actionHandlers["findstronghold"] = actionHandlers["explorestronghold"]
	actionHandlers["stronghold"] = actionHandlers["explorestronghold"]
}

const (
	// strongholdRingStep is the distance between waypoint rings, in blocks.
	strongholdRingStep = 32
	// strongholdMaxRings bounds one sweep: rings 0..5 reach 160 blocks out.
	strongholdMaxRings = 5
	// strongholdScanRadius is how far each waypoint scans the loaded world
	// cache for hint blocks.
	strongholdScanRadius = 16
	// strongholdMaxConverges caps how many times a hint may recentre the
	// search, so a misleading hint costs a bounded amount of walking.
	strongholdMaxConverges = 3
	// strongholdSettlePause lets chunks arrive after an arrival before the
	// scan runs; scanning mid-stride reads half a world.
	strongholdSettlePause = 800 * time.Millisecond
	// strongholdVisibleLimit caps how many visible block names a scan reads.
	strongholdVisibleLimit = 96
)

// strongholdResult is what one sweep of the search found.
type strongholdResult int

const (
	// strongholdExhausted means every ring was walked and nothing suggested
	// a stronghold. An honest negative: the search looked, and looked away.
	strongholdExhausted strongholdResult = iota
	// strongholdHinted means corridor evidence (chiseled bricks, a gateway)
	// was seen and the search should recenter on it.
	strongholdHinted
	// strongholdFound means the portal room itself was located.
	strongholdFound
)

// strongholdHint is one sighting of a block that suggests a stronghold.
type strongholdHint struct {
	pos protocol.BlockPos
	// strong marks blocks that only exist in the portal room itself
	// (end portal frames, the portal) as opposed to corridor decoration.
	strong bool
}

// strongholdCoreBlocks are the hints that end the search outright: they sit in
// the portal room and nowhere else, so one in view is a destination, not a
// rumour.
var strongholdCoreBlocks = map[string]bool{
	"end_portal_frame": true,
	"end_portal":       true,
}

// isStrongholdCore reports whether a block name is strong enough to end the
// search.
func isStrongholdCore(name string) bool {
	return strongholdCoreBlocks[dimension.Normalise(name)]
}

// handleExploreStronghold starts the stronghold search. It runs on its own
// goroutine because the search walks ring after ring and only reports when it
// has a verdict.
func handleExploreStronghold(b *bot.Bot, _, user string) {
	b.Logger.Info("stronghold search started", "user", user)
	go runStrongholdSearch(b, user)
}

// runStrongholdSearch drives the sweeps: one expanding-square search from the
// start point, then up to strongholdMaxConverges recentred sweeps when a hint
// is seen. Every exit reports a status, so silence is never the answer.
func runStrongholdSearch(b *bot.Bot, user string) {
	start := b.GetCoords()
	centerX := int32(math.Floor(float64(start.X())))
	centerZ := int32(math.Floor(float64(start.Z())))
	walkY := int32(math.Floor(float64(start.Y())))

	var hints []strongholdHint
	for attempt := 0; attempt <= strongholdMaxConverges; attempt++ {
		result, hint := searchStrongholdFrom(b, centerX, centerZ, walkY)
		switch result {
		case strongholdFound:
			arrived := b.NavigateToBlock(hint.pos.X(), hint.pos.Y(), hint.pos.Z(), 1.5)
			reportStatus(b, user, event.ActionStatus{
				Action:  "explorestronghold",
				Item:    fmt.Sprintf("stronghold ketemu di %d,%d,%d", hint.pos.X(), hint.pos.Y(), hint.pos.Z()),
				Success: arrived,
				Error:   navError(arrived, hint.pos),
			})
			return
		case strongholdHinted:
			hints = append(hints, hint)
			best, _ := bestStrongholdHint(start, hints)
			b.Logger.Info("stronghold search: hint ditemukan, pusat pencarian digeser",
				"hint", hint.pos, "converge", attempt+1)
			centerX, centerZ = convergeOnHint(best)
		case strongholdExhausted:
			reportStatus(b, user, event.ActionStatus{
				Action:  "explorestronghold",
				Success: false,
				Error: fmt.Sprintf("nggak nemu petunjuk stronghold dalam radius %d blok",
					strongholdMaxRings*strongholdRingStep),
			})
			return
		}
	}
	reportStatus(b, user, event.ActionStatus{
		Action:  "explorestronghold",
		Success: false,
		Error:   "ada petunjuk stronghold tapi portalnya nggak ketemu; budget pencarian habis",
	})
}

// searchStrongholdFrom walks one expanding-square sweep around a centre and
// returns the first decisive thing seen: a portal-room block ends the sweep
// immediately, a weaker hint ends it too (so the search converges instead of
// finishing a ring it already has news from), and nothing at all is exhausted.
func searchStrongholdFrom(b *bot.Bot, centerX, centerZ, walkY int32) (strongholdResult, strongholdHint) {
	for ring := 0; ring <= strongholdMaxRings; ring++ {
		for _, off := range strongholdWaypoints(ring, strongholdRingStep) {
			wpX := centerX + int32(off[0])
			wpZ := centerZ + int32(off[1])
			// WalkTo tolerance is generous on purpose: the waypoint is a
			// vantage point, not a destination, and rough terrain should not
			// be able to stall the whole search.
			if !b.NavigateToBlock(wpX, walkY, wpZ, 3.0) {
				b.Logger.Info("stronghold search: waypoint nggak bisa dicapai", "x", wpX, "z", wpZ)
				continue
			}
			time.Sleep(strongholdSettlePause)

			if hint, ok := scanStrongholdAt(b); ok {
				if hint.strong {
					return strongholdFound, hint
				}
				return strongholdHinted, hint
			}
		}
	}
	return strongholdExhausted, strongholdHint{}
}

// scanStrongholdAt gathers stronghold evidence from where the bot stands. The
// world cache is asked first because it returns a position the search can
// converge on; the visible-block scan is the fallback for hints exposed in
// caves, where the cache ring may miss what is literally in front of the bot.
func scanStrongholdAt(b *bot.Bot) (strongholdHint, bool) {
	pos, ok := findNearestBlock(b.GetCoords(), strongholdScanRadius, botBlockLookup(b), dimension.IsStrongholdHint)
	if ok {
		name, _ := b.GetBlockName(pos.X(), pos.Y(), pos.Z())
		return strongholdHint{pos: pos, strong: isStrongholdCore(name)}, true
	}
	names := strings.Split(
		perception.VisibleBlockNames(b, float32(strongholdScanRadius), strongholdVisibleLimit), ",")
	for _, name := range names {
		if !dimension.IsStrongholdHint(name) {
			continue
		}
		// A visible name carries no coordinates, so the bot's own cell stands
		// in: recentering the search here is the only move the evidence
		// supports.
		c := b.GetCoords()
		return strongholdHint{
			pos: protocol.BlockPos{
				int32(math.Floor(float64(c.X()))),
				int32(math.Floor(float64(c.Y()))),
				int32(math.Floor(float64(c.Z()))),
			},
			strong: isStrongholdCore(name),
		}, true
	}
	return strongholdHint{}, false
}

// strongholdWaypoints returns the XZ offsets of one search ring, clockwise
// from the north-east corner with the edge midpoints interleaved, which keeps
// consecutive waypoints a short walk apart instead of a diagonal sprint. Ring
// 0 is the single in-place waypoint: scan where you stand before walking.
func strongholdWaypoints(ring, step int) [][2]int {
	if ring <= 0 || step <= 0 {
		return [][2]int{{0, 0}}
	}
	r := ring * step
	return [][2]int{
		{r, -r}, {r, 0}, {r, r}, {0, r},
		{-r, r}, {-r, 0}, {-r, -r}, {0, -r},
	}
}

// bestStrongholdHint picks the hint worth acting on: a strong sighting beats
// any number of weak ones, and within the same strength the nearest one wins.
// Distance is horizontal only — the walk to a portal room is along the ground,
// and its depth is not something the bot can aim at from the surface.
func bestStrongholdHint(from mgl32.Vec3, hints []strongholdHint) (strongholdHint, bool) {
	best := strongholdHint{}
	bestDist := math.MaxFloat64
	found := false
	for _, h := range hints {
		dx := float64(h.pos.X()) - float64(from.X())
		dz := float64(h.pos.Z()) - float64(from.Z())
		dist := math.Sqrt(dx*dx + dz*dz)
		if !found || (h.strong && !best.strong) || (h.strong == best.strong && dist < bestDist) {
			best, bestDist, found = h, dist, true
		}
	}
	return best, found
}

// convergeOnHint returns the XZ centre to search from after a hint is seen:
// the hint's own cell. The walking altitude is deliberately not part of the
// answer — the hint may sit at portal-room depth, and the way down is a
// digging problem the walker cannot solve.
func convergeOnHint(hint strongholdHint) (x, z int32) {
	return hint.pos.X(), hint.pos.Z()
}
