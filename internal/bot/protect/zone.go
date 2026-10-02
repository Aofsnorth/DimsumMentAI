package protect

import (
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Zone is an axis-aligned box of protected world and what may not be done in
// it.
//
// AABB rather than a radius, because the things a player builds — a plot, a
// walled garden, a storage room in a mine — are boxes, and a radius would
// protect the field next door along with the base. It also has no centre to get
// wrong: the bounds are the whole configuration.
type Zone struct {
	// Name is how the zone is described in a refusal. It has no meaning to the
	// policy — no zone contains another, no zone is special — it exists so that
	// a log line can be matched to a line of config.
	Name string

	// Min and Max are the inclusive corners.
	Min protocol.BlockPos
	Max protocol.BlockPos

	// Enabled turns the zone off without deleting it, so a base being
	// renovated can be unprotected without losing its config.
	Enabled bool

	// Permissions is what the zone refuses. The zero value refuses nothing; see
	// HomeZone for the ordinary case.
	Permissions Permissions
}

// HomeZone is the zone everybody actually wants: enabled, and refusing both
// breaking and building across the given bounds.
//
// It is a constructor rather than a convention because a Zone literal with a
// forgotten Permissions field is an inert zone that looks armed, and the first
// sign of that is a bot tunnelling through the base it was told to protect.
func HomeZone(name string, min, max protocol.BlockPos) Zone {
	return Zone{
		Name:        name,
		Min:         min,
		Max:         max,
		Enabled:     true,
		Permissions: NoBreak | NoBuild,
	}
}

// Contains reports whether pos is inside the zone, bounds included on both
// faces.
//
// Both corners are inside because that is what a player means when they drag a
// selection: the block at the far corner is in the room. Exclusive bounds are
// the classic off-by-one here, and the symptom is always a one-block margin of
// unprotected wall or floor that nobody can explain.
//
// Corners given the wrong way round are treated as a typo and swapped rather
// than producing an empty zone, because "I listed the far corner first" should
// not silently read as "this zone is nowhere".
func (z Zone) Contains(pos protocol.BlockPos) bool {
	min, max := z.bounds()
	return pos.X() >= min.X() && pos.X() <= max.X() &&
		pos.Y() >= min.Y() && pos.Y() <= max.Y() &&
		pos.Z() >= min.Z() && pos.Z() <= max.Z()
}

// Refuses reports whether the zone refuses this action.
//
// An action the zone has never heard of is refused. It is not a value the zone
// can be asked about, and a guard that answers "no" to a question it does not
// understand has a hole the width of whatever nobody thought to add.
func (z Zone) Refuses(action Action) bool {
	switch action {
	case Break:
		return z.Permissions&NoBreak != 0
	case Build:
		return z.Permissions&NoBuild != 0
	default:
		return true
	}
}

// bounds returns the corners in order, whatever order they were written in.
func (z Zone) bounds() (protocol.BlockPos, protocol.BlockPos) {
	min, max := z.Min, z.Max
	if min.X() > max.X() {
		min[0], max[0] = max[0], min[0]
	}
	if min.Y() > max.Y() {
		min[1], max[1] = max[1], min[1]
	}
	if min.Z() > max.Z() {
		min[2], max[2] = max[2], min[2]
	}
	return min, max
}
