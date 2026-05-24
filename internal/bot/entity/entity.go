package entity

import "github.com/go-gl/mathgl/mgl32"

// Info represents tracked entity details in a shared location to avoid circular dependencies
type Info struct {
	ID       uint64
	Type     string
	Name     string
	Position mgl32.Vec3
	Health   int
}

// WorldModel represents the unified local world map interface used across subsystems
type WorldModel interface {
	IsSolid(x, y, z int32) bool
	SetSolid(x, y, z int32, solid bool)
	IsHazard(x, y, z int32) bool
	SetHazard(x, y, z int32, hazard bool)
}
