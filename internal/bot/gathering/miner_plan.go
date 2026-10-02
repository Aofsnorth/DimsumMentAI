package gathering

import (
	"math"

	"bedrock-ai/internal/bot/entity"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	mineEyeHeight            float32 = 1.62
	mineVisibilitySampleStep float32 = 0.2
)

// MineWorld is the perception boundary for mining candidates. Unknown cells
// return false from IsLoaded and must not be mined or counted as exposure.
type MineWorld interface {
	IsSolid(x, y, z int32) bool
	IsLoaded(x, y, z int32) bool
}

type botMineWorld struct {
	bot   Bot
	model entity.WorldModel
}

func (w botMineWorld) IsSolid(x, y, z int32) bool {
	return w.model.IsSolid(x, y, z)
}

func (w botMineWorld) IsLoaded(x, y, z int32) bool {
	_, loaded := w.bot.GetBlockName(x, y, z)
	return loaded
}

// MineStep is one resolved approach to a mining target: which face to click,
// where to aim, and whether the block counts toward the target the miner was
// asked for.
type MineStep struct {
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

func PlanMineStep(world MineWorld, botPos mgl32.Vec3, target protocol.BlockPos) (MineStep, bool) {
	if !world.IsLoaded(target.X(), target.Y(), target.Z()) || !world.IsSolid(target.X(), target.Y(), target.Z()) {
		return MineStep{}, false
	}

	for _, face := range mineFaces {
		adjacent := protocol.BlockPos{
			target.X() + face.offset.X(),
			target.Y() + face.offset.Y(),
			target.Z() + face.offset.Z(),
		}
		if !mineBlockClear(world, adjacent) {
			continue
		}
		aim := mgl32.Vec3{
			float32(target.X()) + face.aim.X(),
			float32(target.Y()) + face.aim.Y(),
			float32(target.Z()) + face.aim.Z(),
		}
		if !mineSightLineClear(world, botPos.Add(mgl32.Vec3{0, mineEyeHeight, 0}), aim, target) {
			continue
		}
		return MineStep{
			Position:           target,
			Face:               face.face,
			Aim:                aim,
			CountsTowardTarget: true,
		}, true
	}
	return MineStep{}, false
}

func mineBlockClear(world MineWorld, pos protocol.BlockPos) bool {
	return world.IsLoaded(pos.X(), pos.Y(), pos.Z()) && !world.IsSolid(pos.X(), pos.Y(), pos.Z())
}

func mineSightLineClear(world MineWorld, eye, aim mgl32.Vec3, target protocol.BlockPos) bool {
	delta := aim.Sub(eye)
	distance := delta.Len()
	if distance == 0 {
		return true
	}

	steps := int(math.Ceil(float64(distance / mineVisibilitySampleStep)))
	for i := 0; i < steps; i++ {
		point := eye.Add(delta.Mul(float32(i) / float32(steps)))
		pos := protocol.BlockPos{
			int32(math.Floor(float64(point.X()))),
			int32(math.Floor(float64(point.Y()))),
			int32(math.Floor(float64(point.Z()))),
		}
		if pos == target {
			continue
		}
		if !mineBlockClear(world, pos) {
			return false
		}
	}
	return true
}
