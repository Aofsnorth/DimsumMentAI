package action

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/placement"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// handlePlace places a block in front of the bot.
// param format: "item_name" or "item_name,distance" (distance defaults to 1)
//
// Every early exit reports, because a placement that quietly gave up used to
// leave the step with nothing to go on and read as a timeout.
func handlePlace(b *bot.Bot, param, user string) {
	go func() {
		itemName, distance, ok := parsePlaceParams(param)
		if !ok {
			ReportStatus(b, user, event.ActionStatus{
				Action:  "place",
				Success: false,
				Error:   "butuh nama blok, contoh: place:cobblestone",
			})
			return
		}
		targetSlot, found := b.FindItemSlotByName(itemName)
		if !found {
			b.Logger.Warn("handlePlace: item not found", "item", itemName)
			ReportStatus(b, user, event.ActionStatus{
				Action:  "place",
				Item:    itemName,
				Success: false,
				Error:   fmt.Sprintf("tidak punya %s", itemName),
			})
			return
		}

		placePos, supportPos, found := findPlacementTarget(b, user, distance)
		if !found {
			b.Logger.Warn("handlePlace: no valid adjacent solid support spot found", "item", itemName)
			ReportStatus(b, user, event.ActionStatus{
				Action:  "place",
				Item:    itemName,
				Success: false,
				Error:   "tidak ada tempat yang bisa diletakkan",
			})
			return
		}
		request := placement.Request{
			InventorySlot: targetSlot,
			Destination:   placePos,
			Support:       supportPos,
			Face:          bot.BlockFaceTop,
			ClickedOffset: mgl32.Vec3{bot.BlockCenterOffset, 1, bot.BlockCenterOffset},
		}
		if err := b.PlaceBlock(context.Background(), request); err != nil {
			b.Logger.Warn("handlePlace: placement failed", "item", itemName, "pos", placePos, "error", err)
			ReportStatus(b, user, event.ActionStatus{
				Action:  "place",
				Item:    itemName,
				Success: false,
				Error:   err.Error(),
			})
			return
		}
		b.Logger.Info("handlePlace: placed block", "item", itemName, "pos", placePos)
		ReportStatus(b, user, event.ActionStatus{Action: "place", Item: itemName, Success: true})
	}()
}

func parsePlaceParams(param string) (string, int, bool) {
	parts := strings.Split(param, ",")
	itemName := NormalizeItemName(parts[0])
	if itemName == "" {
		return "", 0, false
	}
	distance := 1
	if len(parts) >= 2 {
		_, _ = fmt.Sscanf(parts[1], "%d", &distance)
	}
	if distance < 1 {
		distance = 1
	}
	if distance > 5 {
		distance = 5
	}
	return itemName, distance, true
}

type placementCandidate struct {
	offset    protocol.BlockPos
	alignment float32
	distance  float32
}

func findPlacementTarget(b *bot.Bot, user string, radius int) (protocol.BlockPos, protocol.BlockPos, bool) {
	botPos := b.GetCoords()
	forwardX, forwardZ := placementForwardVector(b, user, botPos)
	candidates := placementCandidates(radius, forwardX, forwardZ)
	world := b.GetLocalWorldModel()
	baseX := int32(math.Floor(float64(botPos.X())))
	baseY := int32(math.Floor(float64(botPos.Y())))
	baseZ := int32(math.Floor(float64(botPos.Z())))

	for _, candidate := range candidates {
		offset := candidate.offset
		destination := protocol.BlockPos{baseX + offset.X(), baseY, baseZ + offset.Z()}
		support := protocol.BlockPos{destination.X(), destination.Y() - 1, destination.Z()}
		if bot.BlockCollidesWithBot(destination, botPos) {
			continue
		}
		if world.IsSolid(destination.X(), destination.Y(), destination.Z()) || world.IsSolid(destination.X(), destination.Y()+1, destination.Z()) {
			continue
		}
		if world.IsSolid(support.X(), support.Y(), support.Z()) {
			return destination, support, true
		}
	}
	return protocol.BlockPos{}, protocol.BlockPos{}, false
}

func placementForwardVector(b *bot.Bot, user string, botPos mgl32.Vec3) (float32, float32) {
	if user != "" {
		if playerPos, ok := b.GetPlayerCoords(user); ok {
			dx := playerPos.X() - botPos.X()
			dz := playerPos.Z() - botPos.Z()
			distance := float32(math.Hypot(float64(dx), float64(dz)))
			if distance > bot.FloatEpsilon {
				return dx / distance, dz / distance
			}
		}
	}
	b.Mu.Lock()
	yaw := b.Yaw
	b.Mu.Unlock()
	radians := float64(yaw+bot.YawOffsetDegrees) * bot.DegreesToRadians
	return float32(math.Cos(radians)), float32(math.Sin(radians))
}

func placementCandidates(radius int, forwardX, forwardZ float32) []placementCandidate {
	candidates := make([]placementCandidate, 0, (radius*2+1)*(radius*2+1)-1)
	for x := -radius; x <= radius; x++ {
		for z := -radius; z <= radius; z++ {
			if x == 0 && z == 0 {
				continue
			}
			distance := float32(math.Hypot(float64(x), float64(z)))
			if distance > float32(radius) {
				continue
			}
			alignment := (float32(x)*forwardX + float32(z)*forwardZ) / distance
			candidates = append(candidates, placementCandidate{
				offset:    protocol.BlockPos{int32(x), 0, int32(z)},
				alignment: alignment,
				distance:  distance,
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if math.Abs(float64(candidates[i].alignment-candidates[j].alignment)) > 0.01 {
			return candidates[i].alignment > candidates[j].alignment
		}
		return candidates[i].distance < candidates[j].distance
	})
	return candidates
}
