package chest

import (
	"context"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/rand"
	"bedrock-ai/internal/bot/storage"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// chestSearchRadius is how far around the bot a container is looked for when
// storing. It matches the reach the interaction that follows needs, so the
// finder never returns a block the bot would then have to walk a long way to.
const chestSearchRadius = 12

// findNearbyChest returns the position of the closest real container block, or
// the zero BlockPos when there is none.
//
// It filters on the block's name, not on solidity. The previous version
// returned the first solid block it touched — a wall, a floor, the ground under
// the bot's feet — because "solid" was the only thing the world model could
// answer cheaply. The result was that the bot confidently walked up to a
// cobblestone block and tried to store items in it.
//
// storage.IsContainerBlock is the same predicate the authoritative storage
// service uses, so the two agree on what counts as a chest.
func (ic *Container) findNearbyChest() protocol.BlockPos {
	botPos := ic.bot.GetCoords()
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	best := protocol.BlockPos{}
	bestDist := float32(math.MaxFloat32)
	found := false

	for dx := -int32(chestSearchRadius); dx <= chestSearchRadius; dx++ {
		for dy := int32(-4); dy <= 4; dy++ {
			for dz := -int32(chestSearchRadius); dz <= chestSearchRadius; dz++ {
				pos := protocol.BlockPos{bx + dx, by + dy, bz + dz}
				name, ok := ic.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
				if !ok || !storage.IsContainerBlock(name) {
					continue
				}
				dist := ic.distance(botPos, mgl32.Vec3{
					float32(pos.X()) + 0.5,
					float32(pos.Y()) + 0.5,
					float32(pos.Z()) + 0.5,
				})
				if !found || dist < bestDist {
					best, bestDist, found = pos, dist, true
				}
			}
		}
	}

	if !found {
		return protocol.BlockPos{}
	}
	return best
}

// distance returns the euclidean distance between two points.
func (ic *Container) distance(a mgl32.Vec3, b mgl32.Vec3) float32 {
	dx := a.X() - b.X()
	dy := a.Y() - b.Y()
	dz := a.Z() - b.Z()
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

func (ic *Container) GiveItem(ctx context.Context, itemName string, playerName string, count int32) bool {
	botPos := ic.bot.GetCoords()

	// Use FindPlayer which searches the proper player tracking system
	// (PlayerEntityIDs/PlayerPositions), not the Actors map.
	_, playerPos, found := ic.bot.FindPlayer(playerName)
	if !found {
		ic.logger.Warn("GiveItem: target player not found", "name", playerName)
		return false
	}

	// Roll the natural variation ONCE so the navigation standoff and the toss
	// aim agree on whether this is a point-blank approach or a normal standoff.
	approachRoll := float32(rand.Float64())
	distanceRoll := float32(rand.Float64())
	pitchRoll := float32(rand.Float64()*2 - 1) // [-1,1)

	dist := ic.distance(botPos, playerPos)
	if dist > entity.DropStandoffDistance+0.5 {
		// Walk to a standoff point a short distance from the recipient along the
		// bot->player line, rather than to the player's own cell. A Bedrock item
		// toss has a small initial velocity and short reach, so ending at a
		// consistent close distance is what makes the arc land in the pickup
		// radius. The distance carries a little human-like variation, and the
		// bot sometimes walks right up for a point-blank drop.
		standoff := entity.DropStandoffTargetNatural(
			[3]float32{botPos.X(), botPos.Y(), botPos.Z()},
			[3]float32{playerPos.X(), playerPos.Y(), playerPos.Z()},
			approachRoll, distanceRoll,
		)
		ic.bot.NavigateToBlock(
			int32(math.Floor(float64(standoff[0]))),
			int32(math.Floor(float64(standoff[1]))),
			int32(math.Floor(float64(standoff[2]))),
			1.0,
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

	// Re-fetch BOTH positions after navigation/stop. The recipient may have
	// moved while we walked, and our own position certainly did — aiming with
	// the pre-navigation snapshot is what made drops fly off to the side or
	// land at the wrong distance. FindPlayer returns the latest tracked feet
	// position.
	botPos = ic.bot.GetCoords()
	if _, refreshed, ok := ic.bot.FindPlayer(playerName); ok {
		playerPos = refreshed
	}
	targetHead := playerPos.Add(mgl32.Vec3{0, 1.62, 0})

	// LookAt pins IdleLookTargetType="block" with target=targetHead, so the
	// movement loop keeps interpolating yaw/pitch toward this point until the
	// drop transaction lands.
	ic.bot.LookAt(targetHead)

	// Compute yaw + distance/terrain-aware pitch from the refreshed positions.
	// A cliff or gap beyond the recipient yields a gentle, short toss so the
	// item can't sail into the void; nearer recipients get a flatter throw and
	// farther ones a higher arc. Bedrock drop direction comes from the last
	// sent PlayerAuthInput.Yaw, so we force-set and sync before dropping.
	aim := entity.ComputeDropAimWithJitter(
		ic.bot.GetLocalWorldModel(),
		[3]float32{botPos.X(), botPos.Y(), botPos.Z()},
		[3]float32{playerPos.X(), playerPos.Y(), playerPos.Z()},
		pitchRoll,
	)
	yaw := aim.Yaw
	ic.bot.SetLookAngles(yaw, aim.Pitch)
	synced := ic.bot.WaitForYawSync(yaw, 800*time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	ic.logger.Info("dropping item",
		"target_yaw", yaw,
		"target_pitch", aim.Pitch,
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

	ic.logger.Info("Gave item successfully", "item", itemName, "count", count, "to", playerName)

	// No backstep: the bot already stands one block farther than the recipient
	// (see the standoff navigation above), so the tossed item lands in the
	// recipient's pickup radius without the bot re-collecting it. Just release
	// the forced upward look so the head returns to a neutral gaze.
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
