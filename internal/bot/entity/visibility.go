package entity

import (
	"math"
	"sort"
	"strings"

	"github.com/go-gl/mathgl/mgl32"
)

const (
	observerEyeHeight = 1.62
	targetEyeHeight   = 1.2
	raySamplesPerCell = 4
)

// SolidityReader reports whether a world cell blocks sight.
type SolidityReader interface {
	IsSolid(x, y, z int32) bool
}

// LoadedBlockReader reports whether a world cell is loaded and known.
type LoadedBlockReader interface {
	GetBlockName(x, y, z int32) (string, bool)
}

// NormalizeName converts a namespaced Minecraft identifier to its canonical name.
func NormalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.TrimPrefix(name, "minecraft:")
	return strings.ReplaceAll(name, " ", "_")
}

// IsItemActor reports whether info represents a dropped item actor.
func IsItemActor(info *Info) bool {
	return info != nil && NormalizeName(info.Type) == "item"
}

// IsMob reports whether info is a living non-player, non-item actor.
func IsMob(info *Info) bool {
	if info == nil || info.Health <= 0 {
		return false
	}
	typ := NormalizeName(info.Type)
	return typ != "" && typ != "player" && typ != "item"
}

// Matches reports whether an entity's canonical name or type matches query.
func Matches(info *Info, query string) bool {
	query = NormalizeName(query)
	if query == "" {
		return true
	}
	name := NormalizeName(info.Name)
	typ := NormalizeName(info.Type)
	return name == query || typ == query || strings.Contains(name, query) || strings.Contains(typ, query)
}

// HasLineOfSight reports whether every traversed world cell is loaded and non-solid.
func HasLineOfSight(solids SolidityReader, blocks LoadedBlockReader, start, end mgl32.Vec3) bool {
	if solids == nil || blocks == nil {
		return false
	}
	delta := end.Sub(start)
	maxAxis := max(abs(delta.X()), abs(delta.Y()), abs(delta.Z()))
	steps := max(1, int(math.Ceil(float64(maxAxis*raySamplesPerCell))))
	last := [3]int32{}
	hasLast := false
	for i := 0; i <= steps; i++ {
		point := start.Add(delta.Mul(float32(i) / float32(steps)))
		cell := [3]int32{
			int32(math.Floor(float64(point.X()))),
			int32(math.Floor(float64(point.Y()))),
			int32(math.Floor(float64(point.Z()))),
		}
		if hasLast && cell == last {
			continue
		}
		last, hasLast = cell, true
		if _, loaded := blocks.GetBlockName(cell[0], cell[1], cell[2]); !loaded {
			return false
		}
		if solids.IsSolid(cell[0], cell[1], cell[2]) {
			return false
		}
	}
	return true
}

// IsVisibleMob reports whether a mob is in range and has conservative LOS.
func IsVisibleMob(solids SolidityReader, blocks LoadedBlockReader, origin mgl32.Vec3, info *Info, maxDistance float32) bool {
	if !IsMob(info) {
		return false
	}
	if maxDistance > 0 && origin.Sub(info.Position).LenSqr() > maxDistance*maxDistance {
		return false
	}
	start := origin.Add(mgl32.Vec3{0, observerEyeHeight, 0})
	end := info.Position.Add(mgl32.Vec3{0, targetEyeHeight, 0})
	return HasLineOfSight(solids, blocks, start, end)
}

// VisibleMobs returns visible mobs ordered nearest first.
func VisibleMobs(solids SolidityReader, blocks LoadedBlockReader, origin mgl32.Vec3, actors map[uint64]*Info, maxDistance float32, excluded map[uint64]struct{}) []*Info {
	visible := make([]*Info, 0, len(actors))
	for id, info := range actors {
		if _, skip := excluded[id]; skip {
			continue
		}
		if IsVisibleMob(solids, blocks, origin, info, maxDistance) {
			visible = append(visible, info)
		}
	}
	sort.Slice(visible, func(i, j int) bool {
		iDist := origin.Sub(visible[i].Position).LenSqr()
		jDist := origin.Sub(visible[j].Position).LenSqr()
		if iDist == jDist {
			return visible[i].ID < visible[j].ID
		}
		return iDist < jDist
	})
	return visible
}

// NearestVisibleMob returns the nearest visible mob matching query.
func NearestVisibleMob(solids SolidityReader, blocks LoadedBlockReader, origin mgl32.Vec3, actors map[uint64]*Info, maxDistance float32, query string, excluded map[uint64]struct{}) (*Info, bool) {
	for _, info := range VisibleMobs(solids, blocks, origin, actors, maxDistance, excluded) {
		if Matches(info, query) {
			return info, true
		}
	}
	return nil, false
}

func abs(value float32) float32 {
	if value < 0 {
		return -value
	}
	return value
}
