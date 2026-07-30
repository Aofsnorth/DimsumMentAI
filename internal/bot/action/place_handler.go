package action

import (
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// handlePlace places a block in front of the bot.
// param format: "item_name" or "item_name,distance" (distance defaults to 1)
func handlePlace(b *bot.Bot, param, user string) {
	go func() {
		if strings.TrimSpace(param) == "" {
			return
		}
		parts := strings.Split(param, ",")
		itemName := normalizeItemName(parts[0])
		distance := 1
		if len(parts) >= 2 {
			_, _ = fmt.Sscanf(parts[1], "%d", &distance)
			if distance < 1 {
				distance = 1
			}
			if distance > 5 {
				distance = 5
			}
		}
		if itemName == "" {
			return
		}

		// Find item in inventory
		b.Mu.Lock()
		inv := b.InventoryMap
		names := b.ItemNames
		var targetSlot uint32
		var targetStack protocol.ItemStack
		found := false
		for slot, stack := range inv {
			if stack.Count <= 0 {
				continue
			}
			name := names[stack.NetworkID]
			if strings.Contains(strings.ToLower(name), strings.ToLower(itemName)) {
				targetSlot = slot
				targetStack = stack
				found = true
				break
			}
		}
		b.Mu.Unlock()

		if !found {
			b.Logger.Warn("handlePlace: item not found", "item", itemName)
			return
		}

		// Equip item
		if err := b.EquipItem(targetSlot); err != nil {
			b.Logger.Warn("handlePlace: equip failed", "error", err)
			return
		}
		time.Sleep(bot.EquipItemDelay)

		// Find valid placement target spot that does not collide with bot body AABB
		world := b.GetLocalWorldModel()
		botPos := b.GetCoords()
		bx := int32(math.Floor(float64(botPos.X())))
		by := int32(math.Floor(float64(botPos.Y())))
		bz := int32(math.Floor(float64(botPos.Z())))

		offsets := []protocol.BlockPos{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}}
		var placePos, supportPos protocol.BlockPos
		foundSpot := false

		for _, off := range offsets {
			p := protocol.BlockPos{bx + off.X(), by, bz + off.Z()}
			s := protocol.BlockPos{p.X(), p.Y() - 1, p.Z()}
			if !world.IsSolid(p.X(), p.Y(), p.Z()) && !world.IsSolid(p.X(), p.Y()+1, p.Z()) && world.IsSolid(s.X(), s.Y(), s.Z()) {
				placePos = p
				supportPos = s
				foundSpot = true
				break
			}
		}

		if !foundSpot {
			b.Logger.Warn("handlePlace: no valid adjacent solid support spot found", "item", itemName)
			return
		}

		// Look at support top face before placing
		targetLook := mgl32.Vec3{
			float32(supportPos.X()) + bot.BlockCenterOffset,
			float32(supportPos.Y()) + 1.0,
			float32(supportPos.Z()) + bot.BlockCenterOffset,
		}
		b.LookAt(targetLook)
		time.Sleep(100 * time.Millisecond)

		// Send placement transaction
		if err := b.Conn.WritePacket(&packet.InventoryTransaction{
			TransactionData: &protocol.UseItemTransactionData{
				ActionType:      protocol.UseItemActionClickBlock,
				BlockPosition:   supportPos,
				BlockFace:       bot.BlockFaceTop,
				HotBarSlot:      safecast.To[int32](b.GetHeldItemSlot()),
				HeldItem:        protocol.ItemInstance{Stack: targetStack},
				Position:        b.GetCoords(),
				ClickedPosition: mgl32.Vec3{bot.BlockCenterOffset, 1.0, bot.BlockCenterOffset},
			},
		}); err != nil {
			b.Logger.Warn("handlePlace: place transaction failed", "error", err)
			return
		}

		b.GetLocalWorldModel().SetSolid(placePos.X(), placePos.Y(), placePos.Z(), true)
		b.Logger.Info("handlePlace: placed block", "item", itemName, "pos", placePos)
	}()
}
