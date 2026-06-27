package acquisition

import (
	"context"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/building/common"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (ia *InventoryAcquisition) placeAndStash(ctx context.Context, inv map[uint32]protocol.ItemStack, names map[int32]string, chestSlot uint32, buildSpot common.Vec3i, nonEssential map[string]bool) {
	botPos := ia.bot.GetCoords()
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	offsets := []common.Vec3i{
		{X: -3, Y: 0, Z: -3},
		{X: 3, Y: 0, Z: -3},
		{X: -3, Y: 0, Z: 3},
		{X: 3, Y: 0, Z: 3},
	}

	world := ia.bot.GetLocalWorldModel()
	var targetPos protocol.BlockPos
	placed := false

	for _, o := range offsets {
		tx := int32(buildSpot.X + o.X)
		ty := int32(buildSpot.Y + o.Y)
		tz := int32(buildSpot.Z + o.Z)

		if !world.IsSolid(tx, ty, tz) && world.IsSolid(tx, ty-1, tz) {
			targetPos = protocol.BlockPos{tx, ty, tz}
			placed = true
			break
		}
	}

	if !placed {
		targetPos = protocol.BlockPos{bx + 2, by, bz}
	}

	ia.bot.ReportActionStatus("", event.ActionStatus{Action: "materials", Item: "chest", Success: true})
	ia.logger.Info("Placing chest", "x", targetPos.X(), "y", targetPos.Y(), "z", targetPos.Z())

	_ = ia.bot.EquipItem(chestSlot)
	time.Sleep(200 * time.Millisecond)

	ia.bot.LookAt(mgl32.Vec3{float32(targetPos.X()) + 0.5, float32(targetPos.Y()) - 0.5, float32(targetPos.Z()) + 0.5})
	time.Sleep(200 * time.Millisecond)

	chestStack := inv[chestSlot]
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{targetPos.X(), targetPos.Y() - 1, targetPos.Z()},
			BlockFace:       1,
			HotBarSlot:      int32(ia.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{Stack: chestStack},
			Position:        ia.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	_ = ia.bot.WritePacket(tx)

	world.SetSolid(targetPos.X(), targetPos.Y(), targetPos.Z(), true)
	ia.chestPos = targetPos
	ia.hasChest = true

	if ia.scanner != nil {
		ia.scanner.TrackStructure("chest", int(targetPos.X()), int(targetPos.Y()), int(targetPos.Z()))
	}

	time.Sleep(800 * time.Millisecond)

	ia.logger.Info("Stashing non-essential items into chest", "pos", targetPos)
	ia.bot.LookAt(mgl32.Vec3{float32(targetPos.X()) + 0.5, float32(targetPos.Y()) + 0.5, float32(targetPos.Z()) + 0.5})
	time.Sleep(200 * time.Millisecond)

	_ = ia.bot.WritePacket(&packet.Interact{
		ActionType:            6,
		TargetEntityRuntimeID: ia.bot.GetEntityRuntimeID(),
		Position:              protocol.Option(mgl32.Vec3{float32(targetPos.X()), float32(targetPos.Y()), float32(targetPos.Z())}),
	})
	time.Sleep(500 * time.Millisecond)

	inv = ia.bot.GetInventorySlots()
	keepItems := []string{"axe", "pickaxe", "shovel", "sword", "food", "apple", "bread", "steak", "chest", "crafting_table"}
	stashed := 0

	for slot, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		name := strings.ToLower(strings.ReplaceAll(names[stack.NetworkID], "minecraft:", ""))
		shouldKeep := false
		for _, k := range keepItems {
			if strings.Contains(name, k) {
				shouldKeep = true
				break
			}
		}

		if !shouldKeep {
			depositTx := &packet.InventoryTransaction{
				Actions: []protocol.InventoryAction{
					{
						SourceType:    protocol.InventoryActionSourceContainer,
						InventorySlot: slot,
						OldItem:       protocol.ItemInstance{Stack: stack},
						NewItem:       protocol.ItemInstance{},
					},
				},
				TransactionData: &protocol.NormalTransactionData{},
			}
			_ = ia.bot.WritePacket(depositTx)
			stashed++
			time.Sleep(300 * time.Millisecond)
		}
	}

	_ = ia.bot.WritePacket(&packet.ContainerClose{WindowID: 0})
	ia.logger.Info("Emergency stashing complete", "stacks_stashed", stashed)

	ia.GatherDroppedItems(ctx, 6.0)
}
