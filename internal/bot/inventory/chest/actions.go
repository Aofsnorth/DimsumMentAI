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
	if dist > 3.0 {
		// Only walk closer when the player is genuinely far. The upward-pitch
		// throw below reliably lands within ~4 blocks, so nearby players need
		// no navigation at all. (The old 1.3-block tolerance frequently failed
		// its path check and aborted the give even when the bot stood right
		// next to the player.)
		ic.bot.NavigateToBlock(
			int32(math.Floor(float64(playerPos.X()))),
			int32(math.Floor(float64(playerPos.Y()))),
			int32(math.Floor(float64(playerPos.Z()))),
			2.0,
		)
		// Proceed as long as we're within throw range, regardless of whether
		// navigation reported an exact "reached"; only abort if still too far.
		if ic.distance(ic.bot.GetCoords(), playerPos) > 4.5 {
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

	// Force-set both body yaw AND head yaw to the exact target, plus a slight
	// upward pitch so the item arcs forward into the player's pickup radius.
	// Using SetLookAngles instead of OverrideLookPitch ensures the body Yaw
	// (which Bedrock uses for drop direction) is pinned to the target, not
	// left lagging behind HeadYaw through the eased look interpolation.
	ic.bot.SetLookAngles(yaw, -28)
	// Wait for at least 2 movement ticks (50ms each) so the PlayerAuthInput
	// carrying these exact values is transmitted before we drop.
	time.Sleep(120 * time.Millisecond)
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

	// Give the server time to actually process the drop transaction and spawn
	// the item entity BEFORE we start walking. If we navigate immediately,
	// applyMoveLookTarget rotates the body yaw toward backPos within one tick
	// and the server applies THAT yaw when spawning the drop — the item flies
	// backward instead of toward the player.
	time.Sleep(450 * time.Millisecond)

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
	// Release the forced upward look so the head returns to a neutral gaze
	// instead of staying stuck pointing up after the toss.
	ic.bot.ResetLook()
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
