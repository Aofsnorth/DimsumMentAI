// Package movement implements tick-level bot movement, path following, and look
// direction. It is responsible for steering, physics, collision resolution, and
// the PlayerAuthInput heartbeat sent to the server.
package movement

import (
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/pathfinder"

	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/go-gl/mathgl/mgl32"
)

// plansTowardDestination reports whether the bot currently has somewhere it is
// trying to get to.
//
// An idle bot still carries whatever TargetPos was last set, and that value is
// the zero vector until something sets it. Planning toward (0,0,0) is not a
// harmless no-op: the search is a real A* run over a real world, and from a
// world origin's distance it takes the largest iteration budget there is.
func plansTowardDestination(state string) bool {
	return state == "walk_to" || state == "follow"
}

// minChunksForPathfinding is the minimum number of decoded chunks the world
// cache must hold before A* can produce meaningful results. With zero chunks
// the world model sees everything as air, every neighbor is vetoed, and the
// search exhausts its full iteration budget finding nothing.
//
// It is a fallback, not the real test: the real one is "can the world describe
// the tile the bot is standing on", which is what terrainReady asks first. The
// threshold only applies when there is no world model to ask.
const minChunksForPathfinding = 1

// terrainReady reports whether the world can describe the bot's own footing well
// enough to path from it.
//
// The test is deliberately about the bot's own cell rather than about how much
// of the world has arrived. Chunk count is the wrong proxy: on a LAN join the
// first sub-chunk to decode is often nowhere near the player, so a bot standing
// in chunk (-7,12) can see ChunkCount()==1 from a decode in chunk (0,0) while
// every cell under its feet is still air. A* then vetoes all four neighbours for
// having no floor and burns its whole budget on a world that is not there.
//
// Asking the model directly is the honest version: the cell under the start
// node is exactly the one A* needs to consider the start standable, and a bot
// whose own footing is still unknown cannot be routed from anywhere sensible.
func terrainReady(b *bot.Bot, start pathfinder.Node) bool {
	if b.WorldModel != nil {
		return b.WorldModel.CanResolve(start.X, start.Y-1, start.Z)
	}
	if b.WorldCache == nil {
		// No terrain source at all: there is nothing to wait for, and the
		// model-backed path (overrides only) is all the bot will ever have.
		return true
	}
	return b.WorldCache.ChunkCount() >= minChunksForPathfinding
}

// RecalculatePath computes the shortest path to targetPos using A* search.
// A* runs without holding b.Mu so SendInputLoop is not blocked for the whole search.
func RecalculatePath(b *bot.Bot) {
	b.Mu.Lock()
	start := pathfinder.Node{
		X: int32(math.Floor(float64(b.Pos.X()))),
		Y: int32(math.Floor(float64(b.Pos.Y() + 0.1))),
		Z: int32(math.Floor(float64(b.Pos.Z()))),
	}
	targetY := b.TargetPos.Y()
	target := pathfinder.Node{
		X: int32(math.Floor(float64(b.TargetPos.X()))),
		Y: int32(math.Floor(float64(targetY))),
		Z: int32(math.Floor(float64(b.TargetPos.Z()))),
	}
	movementState := b.MovementState
	lastTickPos := b.Pos
	b.Mu.Unlock()

	// No destination means no search. Every caller that means to go somewhere
	// sets the state first, so this only ever fires for a re-plan nobody asked
	// for — the damage handler being the one that mattered, because it runs on
	// the packet goroutine and a 30000-iteration search there stalls every
	// packet the bot has not read yet.
	if !plansTowardDestination(movementState) {
		b.Logger.Debug("A* skipped: bot has no destination",
			"movement_state", movementState,
			"target", target,
		)
		return
	}

	// Terrain gate: pathfinding over an unloaded world produces nonsense. The
	// world model sees air everywhere, every neighbor is vetoed, and the search
	// burns its full budget finding nothing. Waiting a few hundred milliseconds
	// for chunks to arrive is cheaper than a 30000-iteration dead end.
	if !terrainReady(b, start) {
		b.Logger.Debug("A* skipped: terrain not loaded yet",
			"chunks", b.WorldCache.ChunkCount(),
			"need", minChunksForPathfinding,
		)
		return
	}

	b.Logger.Info("recalculating path using A*",
		"start_x", start.X, "start_y", start.Y, "start_z", start.Z,
		"target_x", target.X, "target_y", target.Y, "target_z", target.Z,
		"movement_state", movementState,
	)

	b.WorldModel.PurgeFalseSolidOverrides()

	// If the bot's feet are inside a solid block (physics pushed it half a
	// block into the ground, or spawn glitched), snap the start node upward
	// until it's a standable tile. Otherwise every neighbor is vetoed because
	// the floor check at start.Y-1 hits the same solid block the bot is
	// currently inside.
	for i := 0; i < 3; i++ {
		if !b.WorldModel.IsSolid(start.X, start.Y, start.Z) &&
			!b.WorldModel.IsSolid(start.X, start.Y+1, start.Z) {
			break
		}
		start.Y++
	}

	if standTarget, ok := nearestStandableNode(b, start, target, 2); ok {
		target = standTarget
	}

	startRID, _ := b.WorldCache.GetBlockRID(start.X, start.Y, start.Z)
	startHeadRID, _ := b.WorldCache.GetBlockRID(start.X, start.Y+1, start.Z)
	startFloorRID, _ := b.WorldCache.GetBlockRID(start.X, start.Y-1, start.Z)
	targetRID, _ := b.WorldCache.GetBlockRID(target.X, target.Y, target.Z)
	targetHeadRID, _ := b.WorldCache.GetBlockRID(target.X, target.Y+1, target.Z)
	targetFloorRID, _ := b.WorldCache.GetBlockRID(target.X, target.Y-1, target.Z)

	startBlockLeg, _, _ := chunk.RuntimeIDToState(startRID)
	startBlockHead, _, _ := chunk.RuntimeIDToState(startHeadRID)
	startBlockFloor, _, _ := chunk.RuntimeIDToState(startFloorRID)
	targetBlockLeg, _, _ := chunk.RuntimeIDToState(targetRID)
	targetBlockHead, _, _ := chunk.RuntimeIDToState(targetHeadRID)
	targetBlockFloor, _, _ := chunk.RuntimeIDToState(targetFloorRID)

	b.Logger.Debug("A* Path Nodes block debug",
		"start_leg", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockLeg, startRID, b.WorldCache.IsRIDSolid(startRID)),
		"start_head", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockHead, startHeadRID, b.WorldCache.IsRIDSolid(startHeadRID)),
		"start_floor", fmt.Sprintf("%s (rid=%d, solid=%t)", startBlockFloor, startFloorRID, b.WorldCache.IsRIDSolid(startFloorRID)),
		"target_leg", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockLeg, targetRID, b.WorldCache.IsRIDSolid(targetRID)),
		"target_head", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockHead, targetHeadRID, b.WorldCache.IsRIDSolid(targetHeadRID)),
		"target_floor", fmt.Sprintf("%s (rid=%d, solid=%t)", targetBlockFloor, targetFloorRID, b.WorldCache.IsRIDSolid(targetFloorRID)),
	)

	// Pass 1: Normal pathfinding without fallback
	path := pathfinder.FindPath(start, target, b.WorldModel, false)
	if len(path) == 0 {
		// Pass 2: Retry with scaffolding and mining allowed, without fallback
		b.WorldModel.AllowScaffold = true
		b.Logger.Info("Normal pathfinding failed, retrying with scaffolding and mining allowed...")
		path = pathfinder.FindPath(start, target, b.WorldModel, false)
		b.WorldModel.AllowScaffold = false
	}
	if len(path) == 0 {
		// Pass 3: If both failed, retry with fallback enabled so the bot gets as close as possible
		b.Logger.Info("Pathfinding with scaffolding failed, retrying with fallback enabled...")
		path = pathfinder.FindPath(start, target, b.WorldModel, true)
	}

	b.Mu.Lock()
	defer b.Mu.Unlock()
	if len(path) > 0 {
		b.CurrentPath = path
		if len(path) > 1 {
			b.PathIndex = 1
		} else {
			b.PathIndex = 0
		}
		b.TicksStuck = 0
		b.LastTickPos = lastTickPos
		b.LastPathRecalcTime = time.Now()
		b.ConsecutiveStuckCount = 0
		b.LastJumpPathIndex = -1
		b.LastJumpTime = time.Time{}
		// Fresh route from a fresh position: the no-progress window must restart
		// too, or a re-plan triggered by an old stall reports as a new one.
		b.StuckWindowStart = time.Time{}
		b.StuckWindowPos = lastTickPos
		nodeCoords := make([]string, len(path))
		for i, n := range path {
			nodeCoords[i] = fmt.Sprintf("(%d,%d,%d,%s,%s)", n.X, n.Y, n.Z, n.LinkType, n.Action)
		}
		b.Logger.Info("A* pathfinding completed", "nodes", len(path), "path", strings.Join(nodeCoords, " -> "), "movement_state", movementState)
	} else {
		// Reset PathIndex together path so readers in other goroutines
		// (steering.go, follow.go) cannot dereference stale index into nil
		// trip "index out range [N] length 0".
		b.CurrentPath = nil
		b.PathIndex = 0
		b.LastPathRecalcTime = time.Now()
		b.Logger.Warn("A* pathfinding failed resolve walkable path destination",
			"start", start, "target", target, "movement_state", movementState)
		// Diagnostic: dump per-direction walkability around start so we can see
		// which predicate (floor/head/hazard) is vetoing every neighbor.
		type probe struct {
			name         string
			x, y, z      int32
			feet, head   bool
			floor        bool
			feetH, headH bool
			floorH       bool
		}
		probes := []probe{}
		offsets := []struct {
			name       string
			dx, dy, dz int32
		}{
			{"N", 0, 0, -1}, {"S", 0, 0, 1}, {"E", 1, 0, 0}, {"W", -1, 0, 0},
		}
		for _, off := range offsets {
			tx, ty, tz := start.X+off.dx, start.Y+off.dy, start.Z+off.dz
			probes = append(probes, probe{
				name: off.name,
				x:    tx, y: ty, z: tz,
				feet:   b.WorldModel.IsSolid(tx, ty, tz),
				head:   b.WorldModel.IsSolid(tx, ty+1, tz),
				floor:  b.WorldModel.IsSolid(tx, ty-1, tz),
				feetH:  b.WorldModel.IsHazard(tx, ty, tz),
				headH:  b.WorldModel.IsHazard(tx, ty+1, tz),
				floorH: b.WorldModel.IsHazard(tx, ty-1, tz),
			})
		}
		for _, p := range probes {
			b.Logger.Warn("A* neighbor probe",
				"dir", p.name, "x", p.x, "y", p.y, "z", p.z,
				"feet_solid", p.feet, "head_solid", p.head, "floor_solid", p.floor,
				"feet_hazard", p.feetH, "head_hazard", p.headH, "floor_hazard", p.floorH,
			)
			// Also dump the raw RID + translated RID + chunk-load state so we can
			// see whether the wire hash is hitting the local hash table.
			for dy := int32(0); dy <= 2; dy++ {
				if rid, loaded := b.WorldCache.GetBlockRID(p.x, p.y+dy-1, p.z); loaded {
					name, _, nameOK := chunk.RuntimeIDToState(b.WorldCache.TranslateRuntimeID(rid))
					b.Logger.Warn("A* neighbor RID dump",
						"dir", p.name,
						"dy", dy-1,
						"raw_rid", rid,
						"translated", b.WorldCache.TranslateRuntimeID(rid),
						"name_ok", nameOK,
						"name", name,
						"solid", b.WorldModel.IsSolid(p.x, p.y+dy-1, p.z),
					)
				}
			}
		}
	}
}

func NavigateTo(b *bot.Bot, pos mgl32.Vec3) {
	b.WalkTo(pos)
}

func NavigateToBlock(b *bot.Bot, x, y, z int32, tolerance float32) bool {
	block := mgl32.Vec3{float32(x) + 0.5, float32(y), float32(z) + 0.5}
	target := block
	start := pathfinder.Node{
		X: int32(math.Floor(float64(b.GetCoords().X()))),
		Y: int32(math.Floor(float64(b.GetCoords().Y() + 0.1))),
		Z: int32(math.Floor(float64(b.GetCoords().Z()))),
	}

	// Quick win: if we're already within tolerance, no walking needed.
	if pos := b.GetCoords(); float32(math.Sqrt(float64(
		(pos.X()-block.X())*(pos.X()-block.X())+
			(pos.Y()-block.Y())*(pos.Y()-block.Y())+
			(pos.Z()-block.Z())*(pos.Z()-block.Z())))) <= tolerance {
		return true
	}

	if standTarget, ok := nearestStandableNode(b, start, pathfinder.Node{X: x, Y: y, Z: z}, 3); ok {
		// Reject "snap to current position" — the standable picker found OUR
		// current tile within the search radius, meaning no actual walk is
		// needed but we're still outside the caller's tolerance. Walking to
		// our own tile would just spin the wait loop for 5s.
		if standTarget.X == start.X && standTarget.Y == start.Y && standTarget.Z == start.Z {
			return false
		}
		target = mgl32.Vec3{float32(standTarget.X) + 0.5, float32(standTarget.Y), float32(standTarget.Z) + 0.5}
	}
	b.WalkTo(target)

	// Wait while the bot is actually making progress toward the block. A blind
	// time cap made long walks (e.g. crossing to a distant player) report
	// failure while the bot was still legitimately pathing, which aborted the
	// caller mid-journey. Instead, keep waiting as long as the distance keeps
	// shrinking (progress) or the path is still alive, and only give up when
	// the bot genuinely stalls (no measurable progress for several polls) or
	// the path finishes without reaching tolerance.
	lastDist := float32(math.MaxFloat32)
	stalledPolls := 0
	for i := 0; i < navMaxPolls; i++ {
		time.Sleep(navPollInterval)
		b.Mu.Lock()
		curPos := b.Pos
		mState := b.MovementState
		hasPath := b.CurrentPath != nil
		b.Mu.Unlock()

		dx := curPos.X() - block.X()
		dy := curPos.Y() - block.Y()
		dz := curPos.Z() - block.Z()
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

		decision := evaluateNavProgress(dist, lastDist, tolerance, stalledPolls, mState, hasPath)
		stalledPolls = decision.stalledPolls
		lastDist = dist
		if decision.done {
			return decision.reached
		}
	}
	return false
}

const (
	navPollInterval    = 100 * time.Millisecond
	navMaxPolls        = 200 // hard ceiling (~20s) so we never hang forever
	navStallPollsLimit = 15  // ~1.5s with no measurable progress = stuck
	navProgressEpsilon = 0.05
)

// navProgressDecision is the outcome of a single navigation poll.
type navProgressDecision struct {
	done         bool
	reached      bool
	stalledPolls int
}

// evaluateNavProgress decides whether a navigation wait loop should stop, based
// on the current distance to the target, the previous distance, the caller's
// tolerance, the running stall counter, and the movement state. It is pure so
// the progress/stall policy can be unit tested without a live bot.
//
//   - Reaches tolerance -> done, reached.
//   - Path finished or bot idle while still outside tolerance -> done, not reached.
//   - No measurable progress for navStallPollsLimit polls -> done, not reached.
//   - Otherwise -> keep waiting, with an updated stall counter.
func evaluateNavProgress(dist, lastDist, tolerance float32, stalledPolls int, mState string, hasPath bool) navProgressDecision {
	if dist <= tolerance {
		return navProgressDecision{done: true, reached: true, stalledPolls: stalledPolls}
	}

	if lastDist-dist > navProgressEpsilon {
		stalledPolls = 0
	} else {
		stalledPolls++
	}

	// Path finished (arrived at path end) but we're still outside the caller's
	// tolerance, or the bot went idle: nothing more to wait for.
	if mState == "idle" || (mState == "walk_to" && !hasPath) {
		return navProgressDecision{done: true, reached: false, stalledPolls: stalledPolls}
	}
	// Genuinely stuck: making no progress despite an active path.
	if stalledPolls >= navStallPollsLimit {
		return navProgressDecision{done: true, reached: false, stalledPolls: stalledPolls}
	}
	return navProgressDecision{done: false, reached: false, stalledPolls: stalledPolls}
}

func nearestStandableNode(b *bot.Bot, start, target pathfinder.Node, radius int32) (pathfinder.Node, bool) {
	if isStandable(b, target.X, target.Y, target.Z) {
		return target, true
	}

	best := pathfinder.Node{}
	bestScore := float32(math.MaxFloat32)
	for r := int32(1); r <= radius; r++ {
		for dx := -r; dx <= r; dx++ {
			for dz := -r; dz <= r; dz++ {
				if abs32(dx) != r && abs32(dz) != r {
					continue
				}
				for dy := int32(-1); dy <= 2; dy++ {
					candidate := pathfinder.Node{X: target.X + dx, Y: target.Y + dy, Z: target.Z + dz}
					if !isStandable(b, candidate.X, candidate.Y, candidate.Z) {
						continue
					}
					score := pathfinder.Distance(start, candidate) + pathfinder.Distance(candidate, target)*0.25
					if score < bestScore {
						bestScore = score
						best = candidate
					}
				}
			}
		}
		if bestScore < float32(math.MaxFloat32) {
			return best, true
		}
	}
	return pathfinder.Node{}, false
}

func isStandable(b *bot.Bot, x, y, z int32) bool {
	return !b.WorldModel.IsSolid(x, y, z) &&
		!b.WorldModel.IsSolid(x, y+1, z) &&
		!b.WorldModel.IsHazard(x, y, z) &&
		!b.WorldModel.IsHazard(x, y+1, z) &&
		b.WorldModel.IsSolid(x, y-1, z) &&
		!b.WorldModel.IsHazard(x, y-1, z)
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
