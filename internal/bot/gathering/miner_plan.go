package gathering

import (
	"math"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type mineWorld interface {
	IsSolid(x, y, z int32) bool
}

type mineStep struct {
	Position           protocol.BlockPos
	Face               int32
	Aim                mgl32.Vec3
	CountsTowardTarget bool
}

type blockFace struct {
	offset protocol.BlockPos
	face   int32
	aim    mgl32.Vec3
}

var mineFaces = []blockFace{
	{offset: protocol.BlockPos{0, 1, 0}, face: 1, aim: mgl32.Vec3{0.5, 1.0, 0.5}},
	{offset: protocol.BlockPos{0, 0, -1}, face: 2, aim: mgl32.Vec3{0.5, 0.5, 0.0}},
	{offset: protocol.BlockPos{0, 0, 1}, face: 3, aim: mgl32.Vec3{0.5, 0.5, 1.0}},
	{offset: protocol.BlockPos{-1, 0, 0}, face: 4, aim: mgl32.Vec3{0.0, 0.5, 0.5}},
	{offset: protocol.BlockPos{1, 0, 0}, face: 5, aim: mgl32.Vec3{1.0, 0.5, 0.5}},
	{offset: protocol.BlockPos{0, -1, 0}, face: 0, aim: mgl32.Vec3{0.5, 0.0, 0.5}},
}

func planMineStep(world mineWorld, botPos mgl32.Vec3, target protocol.BlockPos) (mineStep, bool) {
	if !world.IsSolid(target.X(), target.Y(), target.Z()) {
		return mineStep{}, false
	}

	if obstruction, ok := firstHorizontalObstruction(world, botPos, target); ok && obstruction != target {
		if step, ok := visibleMineStep(world, obstruction); ok {
			step.CountsTowardTarget = false
			return step, true
		}
		return mineStep{}, false
	}

	step, ok := visibleMineStep(world, target)
	if !ok {
		return mineStep{}, false
	}
	step.CountsTowardTarget = true
	return step, true
}

func visibleMineStep(world mineWorld, pos protocol.BlockPos) (mineStep, bool) {
	for _, f := range mineFaces {
		ax := pos.X() + f.offset.X()
		ay := pos.Y() + f.offset.Y()
		az := pos.Z() + f.offset.Z()
		if !world.IsSolid(ax, ay, az) {
			return mineStep{
				Position: pos,
				Face:     f.face,
				Aim: mgl32.Vec3{
					float32(pos.X()) + f.aim.X(),
					float32(pos.Y()) + f.aim.Y(),
					float32(pos.Z()) + f.aim.Z(),
				},
			}, true
		}
	}
	return mineStep{}, false
}

func firstHorizontalObstruction(world mineWorld, botPos mgl32.Vec3, target protocol.BlockPos) (protocol.BlockPos, bool) {
	startX := int32(math.Floor(float64(botPos.X())))
	startZ := int32(math.Floor(float64(botPos.Z())))
	dx := target.X() - startX
	dz := target.Z() - startZ
	steps := abs32(dx)
	if zSteps := abs32(dz); zSteps > steps {
		steps = zSteps
	}
	if steps <= 1 {
		return protocol.BlockPos{}, false
	}

	for i := int32(1); i < steps; i++ {
		x := startX + int32(math.Round(float64(dx)*float64(i)/float64(steps)))
		z := startZ + int32(math.Round(float64(dz)*float64(i)/float64(steps)))
		if world.IsSolid(x, target.Y(), z) {
			return protocol.BlockPos{x, target.Y(), z}, true
		}
	}
	return protocol.BlockPos{}, false
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
