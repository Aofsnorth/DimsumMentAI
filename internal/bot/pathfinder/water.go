// Water-aware pathfinding: the neighbours a body can actually make inside a
// river, a lake, or a flooded tunnel.
//
// Every neighbour rule that existed before this file asks the same question in
// a different costume: "is there something to stand on under the target cell".
// Water answers no for its entire depth, so a river produced a path to the
// bank and then nothing — the bot walked into the shallows and stopped, which
// is the exact failure this file exists to remove.
//
// The rules here are the same kind of thing safety.go builds: a vocabulary that
// answers "can a body be here" and is folded into expansion once, rather than a
// check each movement mode has to remember to make.

package pathfinder

import "math"

// The swim link types. They are separate from LinkWalk because the movement
// layer treats them differently — a walk link is followed by stepping, a swim
// link by pushing against water — and because the path smoother only ever
// straightens LinkWalk, so a swim link survives into the route as the
// checkpoint it is.
const (
	LinkSwim    LinkType = "swim"
	LinkDive    LinkType = "dive"
	LinkSurface LinkType = "surface"
)

// Swim costs, in the same units as the walk rules: roughly one block of travel.
//
// Diving and rising are priced below a block of horizontal swimming because in
// water the body controls its own depth, and a deliberate descent is faster and
// safer than a gravity drop. That pricing is a preference, not a guarantee: the
// ground rules also offer a LinkFall into the riverbed, priced at 1.0 + 0.3 per
// block dropped, and in a wide river that fall can still come out cheaper than
// diving first and swimming along the bottom. Suppressing the fall belongs to
// world_neighbors.go's tryDrop, not here.
const (
	swimEntryCost  = 1.4
	swimStepCost   = 1.0
	swimRiseCost   = 0.7
	swimDiveCost   = 0.5
	swimClimbCost  = 1.2
	swimStepUpCost = 1.8
)

// waterAwareWorld is the narrow view of the world the swim rules read.
//
// It is declared here rather than added to WorldModel for the same reason
// loadAwareWorld is: widening the interface would push the change through every
// implementer in the tree, including the test doubles, for one caller. A model
// that cannot answer is not water, which is the safe direction — the search
// simply finds no swim links.
type waterAwareWorld interface {
	IsWater(x, y, z int32) bool
	IsSolid(x, y, z int32) bool
	IsHazard(x, y, z int32) bool
}

// waterNeighborSource is the expansion side of the same seam, kept apart from
// the predicates so a model that can answer "is this water" is not required to
// also own the neighbour builders.
type waterNeighborSource interface {
	AppendWaterNeighbors(neighbors []Node, node Node) []Node
}

// WaterNeighbors returns the water moves available from a node: the swim, dive
// and surface links, plus the entry and exit links that connect a dry node to
// the water next to it.
//
// GetNeighbors does not call this yet, because world_neighbors.go is not this
// file's to change. Until it does, a caller that wants water-aware search wraps
// the model in NewWaterWorldModel; when the call is added, this and
// AppendWaterNeighbors become the single implementation both paths share.
func (w *LocalWorldModel) WaterNeighbors(node Node) []Node {
	return w.AppendWaterNeighbors(make([]Node, 0, 10), node)
}

// AppendWaterNeighbors adds the water moves onto an existing neighbour slice.
//
// The shape is AppendWaterNeighbors(neighbors, node) []Node on purpose: it is
// the exact seam GetNeighbors uses for every other rule, so adopting the water
// vocabulary there is one line rather than a rewrite.
func (w *LocalWorldModel) AppendWaterNeighbors(neighbors []Node, node Node) []Node {
	if !w.isSwimCell(node.X, node.Y, node.Z) {
		return w.appendWaterEntries(neighbors, node)
	}

	for _, off := range cardinalOffsets {
		tx, tz := node.X+off.dx, node.Z+off.dz
		if w.canSwimTo(tx, node.Y, tz) {
			neighbors = append(neighbors, Node{X: tx, Y: node.Y, Z: tz, G: node.G + swimStepCost, LinkType: LinkSwim})
		}
		neighbors = w.appendWaterExit(neighbors, node, off.dx, off.dz)
	}
	for _, off := range diagonalOffsets {
		tx, tz := node.X+off.dx, node.Z+off.dz
		if w.canSwimTo(tx, node.Y, tz) {
			neighbors = append(neighbors, Node{X: tx, Y: node.Y, Z: tz, G: node.G + 1.414, LinkType: LinkSwim})
		}
	}

	if w.canSwimTo(node.X, node.Y+1, node.Z) {
		neighbors = append(neighbors, Node{X: node.X, Y: node.Y + 1, Z: node.Z, G: node.G + swimRiseCost, LinkType: LinkSurface})
	}
	if w.canSwimTo(node.X, node.Y-1, node.Z) {
		neighbors = append(neighbors, Node{X: node.X, Y: node.Y - 1, Z: node.Z, G: node.G + swimDiveCost, LinkType: LinkDive})
	}
	return neighbors
}

// appendWaterEntries links a dry node into the water beside it.
//
// The water is entered at the bank's own height or one cell below it. Stepping
// off a bank into a river means walking into the cell in front of you and
// dropping a block as the waterline is lower than your feet; entering from any
// higher would put a body in mid-air over a two-block drop. Nothing further
// down than one block is an entry: below that it is a dive, and the dive rules
// are for a body that is already in the water.
func (w *LocalWorldModel) appendWaterEntries(neighbors []Node, node Node) []Node {
	for _, off := range cardinalOffsets {
		tx, tz := node.X+off.dx, node.Z+off.dz
		for _, drop := range []int32{0, 1} {
			ty := node.Y - drop
			if w.canSwimTo(tx, ty, tz) {
				neighbors = append(neighbors, Node{X: tx, Y: ty, Z: tz, G: node.G + swimEntryCost, LinkType: LinkSwim})
				break
			}
		}
	}
	return neighbors
}

// appendWaterExit links a body in the water back onto solid ground, at its own
// height and one block up (climbing out of a shallow onto a bank).
func (w *LocalWorldModel) appendWaterExit(neighbors []Node, node Node, dx, dz int32) []Node {
	tx, tz := node.X+dx, node.Z+dz
	if w.canStandOnLand(tx, node.Y, tz) {
		neighbors = append(neighbors, Node{X: tx, Y: node.Y, Z: tz, G: node.G + swimClimbCost, LinkType: LinkSurface})
	}
	if w.canStandOnLand(tx, node.Y+1, tz) {
		neighbors = append(neighbors, Node{X: tx, Y: node.Y + 1, Z: tz, G: node.G + swimStepUpCost, LinkType: LinkSurface})
	}
	return neighbors
}

// canStandOnLand is the standable check for an exit out of the water.
//
// It repeats canStandAt instead of calling it, for one concrete reason: that
// helper consults the ladder vocabulary, which reaches for dragonfly's
// RuntimeIDToState converter. A binary that never links the dragonfly world
// package — this package's own test binary, a tool — leaves that var nil, and
// the call takes the process down from inside a search. A ladder beside the
// water is the ladder rules' business, not the water's, so dropping that clause
// changes nothing this file is responsible for and keeps the exit check safe
// everywhere.
func (w *LocalWorldModel) canStandOnLand(x, y, z int32) bool {
	if w.IsSolid(x, y, z) || w.IsSolid(x, y+1, z) {
		return false
	}
	if w.IsHazard(x, y, z) || w.IsHazard(x, y+1, z) {
		return false
	}
	if w.IsHazard(x, y-1, z) {
		return false
	}
	return w.IsSolid(x, y-1, z)
}

// isSwimCell reports whether a body can be swimming at a cell.
//
// Water at the feet or at the head both count. The second case is a body
// standing on a riverbed with the waterline at chest height, which is swimming
// as far as the air bar is concerned, and treating it as a walk node is how a
// bot ends up walking around the bottom of a river.
func (w *LocalWorldModel) isSwimCell(x, y, z int32) bool {
	if w.IsSolid(x, y, z) || w.IsSolid(x, y+1, z) {
		return false
	}
	if w.IsHazard(x, y, z) || w.IsHazard(x, y+1, z) {
		return false
	}
	return w.IsWater(x, y, z) || w.IsWater(x, y+1, z)
}

// canSwimTo is isSwimCell under the name the link builders use: the target
// cell has to hold water and be free of anything a body cannot be inside.
func (w *LocalWorldModel) canSwimTo(x, y, z int32) bool {
	return w.isSwimCell(x, y, z)
}

// CanSwimDirectly reports whether a straight line between two nodes is a legal
// swim, sampling the water the way the walk smoother samples the floor.
//
// The failure it prevents is the same one CanWalkDirectly prevents for ground:
// a string-pulled link is walked without re-checking, so pulling a swim link
// out of the water and across a bank aims the bot at terrain the model never
// looked at.
func CanSwimDirectly(from, to Node, world WorldModel) bool {
	water, ok := world.(waterAwareWorld)
	if !ok {
		return false
	}
	if !water.IsWater(from.X, from.Y, from.Z) && !water.IsWater(from.X, from.Y+1, from.Z) {
		return false
	}

	steps := swimSamples(from, to)
	if steps < 2 {
		return true
	}
	for s := int32(1); s < steps; s++ {
		t := float32(s) / float32(steps)
		bx := int32(math.Floor(float64(from.X) + float64(to.X-from.X)*float64(t)))
		by := int32(math.Floor(float64(from.Y) + float64(to.Y-from.Y)*float64(t)))
		bz := int32(math.Floor(float64(from.Z) + float64(to.Z-from.Z)*float64(t)))
		if water.IsSolid(bx, by, bz) || water.IsSolid(bx, by+1, bz) {
			return false
		}
		if water.IsHazard(bx, by, bz) || water.IsHazard(bx, by+1, bz) {
			return false
		}
		if !water.IsWater(bx, by, bz) && !water.IsWater(bx, by+1, bz) {
			return false
		}
	}
	return true
}

// swimSamples is how finely a straight swim link is checked: twice per block of
// travel, and never fewer than two samples, so even a one-block link is sampled
// in the middle.
func swimSamples(from, to Node) int32 {
	steps := 2 * max3(Abs32(to.X-from.X), Abs32(to.Z-from.Z), Abs32(to.Y-from.Y))
	if steps < 2 {
		steps = 2
	}
	if steps > 64 {
		steps = 64
	}
	return steps
}

func max3(a, b, c int32) int32 {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}

// WaterWorldModel is a WorldModel that answers GetNeighbors with the ground
// rules plus the water rules.
//
// It exists because GetNeighbors is defined on LocalWorldModel in
// world_neighbors.go, and a decorator is the only way to add a rule to a search
// without rewriting the one file every other movement mode also lives in. The
// delegating methods below are the reason it is safe: A* reaches SetPathBounds
// and the smoother reaches IsLoaded through type assertions, and a wrapper that
// promoted only the bare interface would silently downgrade both.
type WaterWorldModel struct {
	WorldModel
}

// NewWaterWorldModel wraps a world model so its neighbours include water.
func NewWaterWorldModel(inner WorldModel) *WaterWorldModel {
	return &WaterWorldModel{WorldModel: inner}
}

// GetNeighbors returns the wrapped model's neighbours plus the water moves.
func (m *WaterWorldModel) GetNeighbors(node Node) []Node {
	neighbors := m.WorldModel.GetNeighbors(node)
	if source, ok := m.WorldModel.(waterNeighborSource); ok {
		return source.AppendWaterNeighbors(neighbors, node)
	}
	return neighbors
}

// WaterNeighbors returns the water moves on their own, for callers that want
// the vocabulary without the ground rules merged in.
func (m *WaterWorldModel) WaterNeighbors(node Node) []Node {
	if source, ok := m.WorldModel.(waterNeighborSource); ok {
		return source.AppendWaterNeighbors(nil, node)
	}
	return nil
}

// IsWater narrows the wrapped model to the water vocabulary, reporting dry for
// a model that has none. A wrapper that lied here would plan every dive on
// guesswork.
func (m *WaterWorldModel) IsWater(x, y, z int32) bool {
	water, ok := m.WorldModel.(waterAwareWorld)
	if !ok {
		return false
	}
	return water.IsWater(x, y, z)
}

// IsLoaded preserves the smoother's "known air vs never decoded" distinction,
// with the same permissive fallback cellsKnown applies when a model cannot tell
// them apart.
func (m *WaterWorldModel) IsLoaded(x, y, z int32) bool {
	law, ok := m.WorldModel.(interface{ IsLoaded(x, y, z int32) bool })
	if !ok {
		return true
	}
	return law.IsLoaded(x, y, z)
}

// SetPathBounds keeps the unknown-cell fallback pointed at the current trip
// rather than the sea-level guess.
func (m *WaterWorldModel) SetPathBounds(start, target Node) {
	if bw, ok := m.WorldModel.(interface{ SetPathBounds(start, target Node) }); ok {
		bw.SetPathBounds(start, target)
	}
}

// DebugNeighborVeto forwards A*'s "start had no neighbours" diagnostic, so
// adding the water rules does not cost the field its best debugging tool.
func (m *WaterWorldModel) DebugNeighborVeto(n Node) string {
	d, ok := m.WorldModel.(interface{ DebugNeighborVeto(n Node) string })
	if !ok {
		return "the wrapped world model has no neighbour diagnostic"
	}
	return d.DebugNeighborVeto(n)
}
