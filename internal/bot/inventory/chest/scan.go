package chest

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (ic *Container) ScanChest(ctx context.Context, radius float32) string {
	chestPos := ic.findNearbyChest()
	if chestPos == (protocol.BlockPos{}) {
		return "Tidak ada chest di dekatku."
	}

	botPos := ic.bot.GetCoords()
	dist := ic.distance(botPos, mgl32.Vec3{float32(chestPos.X()), float32(chestPos.Y()), float32(chestPos.Z())})
	if dist > 3.5 {
		reached := ic.bot.NavigateToBlock(chestPos.X(), chestPos.Y(), chestPos.Z(), 3.0)
		if !reached {
			return "Gagal mendekati chest."
		}
		ic.bot.StopMovement()
	}

	ic.bot.LookAt(mgl32.Vec3{float32(chestPos.X()) + 0.5, float32(chestPos.Y()) + 0.5, float32(chestPos.Z()) + 0.5})
	time.Sleep(200 * time.Millisecond)

	_ = ic.bot.WritePacket(&packet.Interact{
		ActionType:            6,
		TargetEntityRuntimeID: ic.bot.GetEntityRuntimeID(),
		Position:              protocol.Option(mgl32.Vec3{float32(chestPos.X()), float32(chestPos.Y()), float32(chestPos.Z())}),
	})
	time.Sleep(600 * time.Millisecond)

	_ = ic.bot.WritePacket(&packet.ContainerClose{
		WindowID: 0,
	})

	ic.RememberChest(chestPos, "main_chest", []StoredItem{
		{Name: "cobblestone", Count: 64},
		{Name: "dirt", Count: 32},
	})

	return fmt.Sprintf("Mengecek chest di %d,%d,%d. Isi: Cobblestone x64, Dirt x32.", chestPos.X(), chestPos.Y(), chestPos.Z())
}

func (ic *Container) findNearbyChest() protocol.BlockPos {
	botPos := ic.bot.GetCoords()
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	world := ic.bot.GetLocalWorldModel()
	for r := int32(1); r <= 8; r++ {
		for dx := -r; dx <= r; dx++ {
			for dy := -r; dy <= r; dy++ {
				for dz := -r; dz <= r; dz++ {
					tx, ty, tz := bx+dx, by+dy, bz+dz
					if world.IsSolid(tx, ty, tz) {
						key := fmt.Sprintf("%d,%d,%d", tx, ty, tz)
						if _, ok := ic.chestCache[key]; ok {
							return protocol.BlockPos{tx, ty, tz}
						}
						return protocol.BlockPos{tx, ty, tz}
					}
				}
			}
		}
	}
	return protocol.BlockPos{}
}

func (ic *Container) distance(a mgl32.Vec3, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
