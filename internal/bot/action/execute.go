package action

import (
	"context"
	"fmt"
	"strings"

	"bedrock-ai/internal/bot"

	"github.com/go-gl/mathgl/mgl32"
)

// Execute maps an AI-parsed action tag to bot behaviors
func Execute(b *bot.Bot, label string, param string, user string) {
	label = strings.ToLower(strings.TrimSpace(label))
	param = strings.TrimSpace(param)

	switch label {
	case "build":
		go b.BuilderAgent.Build(context.Background(), user, param)

	case "stopbuild", "stopbuilding":
		b.BuilderAgent.StopBuilding()

	case "undo":
		count := 0
		if param != "" {
			_, _ = fmt.Sscanf(param, "%d", &count)
		}
		go b.BuilderAgent.UndoBuild(context.Background(), count)

	case "come", "follow":
		target := param
		if target == "" {
			target = user
		}
		b.FollowPlayer(target)

	case "goto":
		if param == "" {
			return
		}
		parts := strings.Split(param, ",")
		if len(parts) == 3 {
			var coords [3]float32
			valid := true
			for i, p := range parts {
				var val float32
				if _, err := fmt.Sscanf(strings.TrimSpace(p), "%f", &val); err != nil {
					valid = false
					break
				}
				coords[i] = val
			}
			if valid {
				b.WalkTo(mgl32.Vec3{coords[0], coords[1], coords[2]})
			}
		}

	case "stop", "stay":
		b.Stop()

	case "emote":
		parts := strings.Split(param, ",")
		emoteName := parts[0]
		b.TriggerEmote(emoteName)

	case "attack", "hunt", "pvp", "guard":
		handleAttack(b, param, user)

	case "gather":
		count := 10
		itemName := "wood"
		parts := strings.Split(param, ",")
		if len(parts) >= 1 && parts[0] != "" {
			itemName = strings.ToLower(strings.TrimSpace(parts[0]))
		}
		if len(parts) >= 2 {
			_, _ = fmt.Sscanf(parts[1], "%d", &count)
		}

		if strings.Contains(itemName, "wood") || strings.Contains(itemName, "log") {
			go b.Gatherer.GatherWood(context.Background(), count)
		} else {
			go b.Gatherer.GatherBlock(context.Background(), itemName, count)
		}

	case "mine", "automine":
		count := 10
		itemName := "cobblestone"
		parts := strings.Split(param, ",")
		if len(parts) >= 1 && parts[0] != "" {
			itemName = strings.ToLower(strings.TrimSpace(parts[0]))
		}
		if len(parts) >= 2 {
			_, _ = fmt.Sscanf(parts[1], "%d", &count)
		}
		go b.Gatherer.GatherBlock(context.Background(), itemName, count)

	case "clear", "scan":
		go func() {
			collected := b.Gatherer.CollectAllDrops(context.Background(), 12.0)
			b.Logger.Info("Swept drops complete", "collected", collected)
		}()

	case "craft":
		handleCraft(b, param)

	case "smelt":
		go func() {
			itemName := strings.ToLower(strings.TrimSpace(param))
			success := b.InventoryMgr.Furnace().SmeltItem(context.Background(), itemName)
			b.Logger.Info("Smelt complete", "success", success, "item", itemName)
		}()

	case "store", "storeall":
		go func() {
			itemName := strings.ToLower(strings.TrimSpace(param))
			success := b.InventoryMgr.Chest().StoreItem(context.Background(), itemName, 0)
			b.Logger.Info("Store complete", "success", success, "item", itemName)
		}()

	case "take", "retrieve":
		handleTake(b, param, user)

	default:
		b.Logger.Info("unknown or unhandled action label", "label", label, "param", param)
	}
}
