package chest

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func (ic *Container) GiveItem(ctx context.Context, itemName string, playerName string, count int32) bool {
	botPos := ic.bot.GetCoords()

	// Use FindPlayer which searches the proper player tracking system
	// (PlayerEntityIDs/PlayerPositions), not the Actors map.
	_, playerPos, found := ic.bot.FindPlayer(playerName)
	if !found {
		ic.logger.Warn("GiveItem: target player not found", "name", playerName)
		return false
	}

	dist := ic.distance(botPos, playerPos)
	if dist > 2.0 {
		// Bedrock's base drop velocity is ~0.3 m/s — the item can only travel
		// a fraction of a block. Stand within 1.3 blocks so the tossed item
		// lands inside the player's pickup range rather than between us.
		reached := ic.bot.NavigateToBlock(
			int32(math.Floor(float64(playerPos.X()))),
			int32(math.Floor(float64(playerPos.Y()))),
			int32(math.Floor(float64(playerPos.Z()))),
			1.3,
		)
		if !reached {
			ic.logger.Warn("GiveItem: could not reach player", "name", playerName)
			return false
		}
	}

	// Force-stop any residual walk/follow state. Without this the look loop
	// keeps interpolating yaw toward path direction and the drop flies away
	// from the player.
	ic.bot.StopMovement()

	// Re-fetch position after stop (we may have stepped during nav).
	botPos = ic.bot.GetCoords()
	targetHead := playerPos.Add(mgl32.Vec3{0, 1.62, 0})

	// LookAt pins IdleLookTargetType="block" with target=targetHead for 3s, so
	// the movement loop will continuously interpolate yaw/pitch toward this
	// point (eye-corrected via setLookTarget in control.go) until the drop
	// transaction lands.
	ic.bot.LookAt(targetHead)

	// Compute the same yaw the look loop will converge to, then wait for the
	// next PlayerAuthInput tick to actually transmit it. Bedrock drop direction
	// comes from the last sent PlayerAuthInput.Yaw.
	dx := targetHead.X() - botPos.X()
	dz := targetHead.Z() - botPos.Z()
	yaw := float32(math.Atan2(float64(dz), float64(dx))*180/math.Pi) - 90
	for yaw < 0 {
		yaw += 360
	}
	// 800ms = up to 16 ticks of interpolation, enough to swing the bot through
	// a 180° rotation if it was facing away from the player.
	synced := ic.bot.WaitForYawSync(yaw, 800*time.Millisecond)

	// Aim with a slight upward angle so the item arcs forward into the
	// player's pickup radius instead of falling into our own. 28° gives
	// enough flight time for the ~0.3 m/s base velocity to cover a 1-block
	// toss; flatter pitches (~22°) land just in front of the bot.
	ic.bot.OverrideLookPitch(-28)
	time.Sleep(250 * time.Millisecond)
	ic.logger.Info("dropping item",
		"target_yaw", yaw,
		"target_player", playerName,
		"yaw_synced", synced,
		"bot_pos", botPos,
		"player_pos", playerPos,
	)

	inv := ic.bot.GetInventorySlots()
	names := ic.bot.GetItemNames()

	var targetSlot uint32
	foundItem := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := names[item.NetworkID]
		if strings.Contains(strings.ToLower(name), strings.ToLower(itemName)) {
			targetSlot = slot
			foundItem = true
			if count <= 0 || count > int32(item.Count) {
				count = int32(item.Count)
			}
			break
		}
	}

	if !foundItem {
		ic.logger.Warn("GiveItem: item not found in inventory", "name", itemName)
		return false
	}

	err := ic.bot.DropItem(names[inv[targetSlot].NetworkID], int(count))
	if err != nil {
		return false
	}

	// Brief pause so the drop transaction lands and the entity is on the
	// ground before we start moving. Without this, the bot's immediate
	// backward step can re-collect the item or push it out of the player's
	// pickup range.
	time.Sleep(150 * time.Millisecond)

	ic.logger.Info("Gave item successfully", "item", itemName, "count", count, "to", playerName)

	// Step back a short distance so the dropped item ends up outside the
	// bot's pickup radius. The look loop will swing yaw toward the new walk
	// direction, but by now the drop transaction has already left.
	yawWorldRad := float64(yaw+90) * math.Pi / 180
	forwardX := float32(math.Cos(yawWorldRad))
	forwardZ := float32(math.Sin(yawWorldRad))
	backPos := mgl32.Vec3{
		botPos.X() - forwardX*1.2,
		botPos.Y(),
		botPos.Z() - forwardZ*1.2,
	}
	ic.bot.NavigateTo(backPos)
	time.Sleep(500 * time.Millisecond)
	ic.bot.StopMovement()
	return true
}

func (ic *Container) StoreItem(ctx context.Context, itemName string, count int32) bool {
	chestPos := ic.findNearbyChest()
	if chestPos == (protocol.BlockPos{}) {
		ic.logger.Warn("StoreItem: no chests found nearby")
		return false
	}

	botPos := ic.bot.GetCoords()
	dist := ic.distance(botPos, mgl32.Vec3{float32(chestPos.X()), float32(chestPos.Y()), float32(chestPos.Z())})
	if dist > 3.5 {
		reached := ic.bot.NavigateToBlock(chestPos.X(), chestPos.Y(), chestPos.Z(), 3.0)
		if !reached {
			return false
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
	time.Sleep(500 * time.Millisecond)

	inv := ic.bot.GetInventorySlots()
	names := ic.bot.GetItemNames()

	var targetSlot uint32
	found := false
	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		name := names[item.NetworkID]
		if strings.Contains(strings.ToLower(name), strings.ToLower(itemName)) {
			targetSlot = slot
			found = true
			if count <= 0 || count > int32(item.Count) {
				count = int32(item.Count)
			}
			break
		}
	}

	if !found {
		return false
	}

	item := inv[targetSlot]
	tx := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{
			{
				SourceType:    protocol.InventoryActionSourceContainer,
				InventorySlot: targetSlot,
				OldItem:       protocol.ItemInstance{Stack: item},
				NewItem:       protocol.ItemInstance{},
			},
		},
		TransactionData: &protocol.NormalTransactionData{},
	}
	_ = ic.bot.WritePacket(tx)

	_ = ic.bot.WritePacket(&packet.ContainerClose{
		WindowID: 0,
	})

	ic.logger.Info("Stored item in chest successfully", "item", itemName, "count", count)
	return true
}
