package bot

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// WalkTo directs the bot to walk to a coordinate
func (b *Bot) WalkTo(pos mgl32.Vec3) {
	b.Mu.Lock()
	b.MovementState = "walk_to"
	b.TargetPos = pos
	b.Logger.Info("WalkTo initiated", "x", pos.X(), "y", pos.Y(), "z", pos.Z())
	b.Mu.Unlock()
	b.RecalculatePath()
}

// FollowPlayer directs the bot to follow a player
func (b *Bot) FollowPlayer(username string) {
	b.Mu.Lock()
	b.MovementState = "follow"
	b.TargetPlayerName = username
	b.Logger.Info("FollowPlayer initiated", "username", username)
	b.Mu.Unlock()

	if _, pos, ok := b.FindPlayer(username); ok {
		b.Mu.Lock()
		b.TargetPos = pos
		b.LastPathRecalcTime = time.Now()
		b.Mu.Unlock()
		b.RecalculatePath()
		b.Logger.Info("Player found for follow, setting target position", "username", username, "pos", pos)
	} else {
		b.Logger.Warn("Player not found for follow", "username", username)
	}
}

// Stop halts all bot movements
func (b *Bot) Stop() {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.MovementState = "idle"
	b.CurrentPath = nil
	b.Logger.Info("Bot movement stopped")
}

// TriggerEmote triggers a custom bot animation
func (b *Bot) TriggerEmote(name string) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.EmoteState = name
	b.EmoteTicks = 40 // 2 seconds duration at 20 ticks/sec
	b.Logger.Info("Emote triggered", "name", name)
}

// TrackBotMessage records a message sent by the bot to prevent echo-loops
func (b *Bot) TrackBotMessage(text string) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.RecentBotMessages[strings.ToLower(strings.TrimSpace(text))] = time.Now()
}

// IsBotEcho checks if the text matches a recently sent bot message (within 5 seconds)
func (b *Bot) IsBotEcho(text string) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	clean := strings.ToLower(strings.TrimSpace(text))
	t, ok := b.RecentBotMessages[clean]
	if !ok {
		return false
	}

	if time.Since(t) < 5*time.Second {
		return true
	}

	// Clean up old entries
	for k, v := range b.RecentBotMessages {
		if time.Since(v) > 10*time.Second {
			delete(b.RecentBotMessages, k)
		}
	}
	return false
}

// GetInventorySummary returns a human-readable list of items in the inventory
func (b *Bot) GetInventorySummary() string {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	if len(b.InventoryMap) == 0 {
		return "Inventory kosong"
	}

	var items []string
	itemCounts := make(map[string]int)
	for _, stack := range b.InventoryMap {
		name := b.ItemNames[stack.NetworkID]
		if name == "" {
			name = fmt.Sprintf("item_%d", stack.NetworkID)
		}
		name = strings.ReplaceAll(name, "minecraft:", "")
		name = strings.ReplaceAll(name, "_", " ")
		itemCounts[name] += int(stack.Count)
	}

	for name, count := range itemCounts {
		items = append(items, fmt.Sprintf("%s x%d", name, count))
	}

	return strings.Join(items, ", ")
}

// GetHeldItem returns the name of the item currently held by the bot
func (b *Bot) GetHeldItem() string {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	stack, ok := b.InventoryMap[b.HeldSlot]
	if !ok || stack.Count == 0 || stack.NetworkID == 0 {
		return "nothing"
	}

	name := b.ItemNames[stack.NetworkID]
	if name == "" {
		return fmt.Sprintf("item_%d", stack.NetworkID)
	}
	name = strings.ReplaceAll(name, "minecraft:", "")
	name = strings.ReplaceAll(name, "_", " ")
	return name
}

// GetStatusDetails returns current health, hunger, and coordinates
func (b *Bot) GetStatusDetails() (int, int, string) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	posStr := fmt.Sprintf("X:%.0f Y:%.0f Z:%.0f", b.Pos.X(), b.Pos.Y(), b.Pos.Z())
	return b.Health, b.Hunger, posStr
}

// GetCoords returns bot's coordinates as Vec3
func (b *Bot) GetCoords() mgl32.Vec3 {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.Pos
}

// GetPlayerCoords returns coordinates of player by username
func (b *Bot) GetPlayerCoords(username string) (mgl32.Vec3, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	if targetID, ok := b.PlayerEntityIDs[username]; ok {
		if pos, ok := b.PlayerPositions[targetID]; ok {
			return pos, true
		}
	}
	return mgl32.Vec3{}, false
}

// SendSafeChat sends a message in chunks if it exceeds 250 characters.
func (b *Bot) SendSafeChat(msg string) {
	chunks := splitMessage(msg, 220)
	for _, chunk := range chunks {
		if chunk == "" {
			continue
		}
		// Track to avoid echo loops
		b.TrackBotMessage(chunk)

		b.Mu.Lock()
		botName := b.Name
		b.Mu.Unlock()

		pk := &packet.Text{
			TextType:         packet.TextTypeChat,
			SourceName:       botName,
			Message:          chunk,
			NeedsTranslation: false,
			XUID:             "",
			PlatformChatID:   "",
		}
		_ = b.Conn.WritePacket(pk)
		b.Logger.Info("sent chat message", "message", chunk)
		time.Sleep(300 * time.Millisecond) // brief delay to prevent packet flooding
	}
}

// splitMessage splits a long string into chunks at punctuation bounds or spaces
func splitMessage(msg string, maxLen int) []string {
	if len(msg) <= maxLen {
		return []string{msg}
	}

	var chunks []string
	runes := []rune(msg)
	for len(runes) > 0 {
		if len(runes) <= maxLen {
			chunks = append(chunks, string(runes))
			break
		}

		// Look for a punctuation split point in the last 40 runes of the window
		splitIdx := maxLen - 1
		found := false
		for i := maxLen - 1; i >= maxLen-40 && i > 0; i-- {
			r := runes[i]
			if r == '.' || r == '!' || r == '?' || r == ';' {
				splitIdx = i + 1
				found = true
				break
			}
		}

		if !found {
			// Look for space boundary
			for i := maxLen - 1; i >= maxLen-20 && i > 0; i-- {
				if runes[i] == ' ' {
					splitIdx = i
					found = true
					break
				}
			}
		}

		chunks = append(chunks, strings.TrimSpace(string(runes[:splitIdx])))
		runes = runes[splitIdx:]
	}

	return chunks
}

// ExecuteAction maps an AI-parsed action tag to bot behaviors
func (b *Bot) ExecuteAction(label string, param string, user string) {
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

	case "come":
		target := param
		if target == "" {
			target = user
		}
		b.FollowPlayer(target)

	case "follow":
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
		b.Mu.Lock()
		targetID := uint64(0)
		closestDist := float32(math.MaxFloat32)
		botPos := b.Pos

		for username, id := range b.PlayerEntityIDs {
			if strings.EqualFold(username, param) || (param == "" && strings.EqualFold(username, user)) {
				targetID = id
				break
			}
		}

		if targetID == 0 {
			for id, actor := range b.Actors {
				if param == "" || strings.Contains(strings.ToLower(actor.Name), strings.ToLower(param)) || strings.Contains(strings.ToLower(actor.Type), strings.ToLower(param)) {
					dx := actor.Position.X() - botPos.X()
					dy := actor.Position.Y() - botPos.Y()
					dz := actor.Position.Z() - botPos.Z()
					dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
					if dist < closestDist {
						closestDist = dist
						targetID = id
					}
				}
			}
		}
		b.Mu.Unlock()

		if targetID != 0 {
			b.CombatMgr.EngageTarget(targetID)
		} else {
			b.Logger.Warn("ExecuteAction: no target found to attack", "param", param)
		}

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
		parts := strings.Split(param, ",")
		itemName := strings.ToLower(strings.TrimSpace(parts[0]))
		count := 1
		if len(parts) >= 2 {
			_, _ = fmt.Sscanf(parts[1], "%d", &count)
		}

		b.Mu.Lock()
		recipeID, ok := b.Recipes[itemName]
		b.Mu.Unlock()

		if ok {
			b.Logger.Info("Executing craft action", "item", itemName, "recipeID", recipeID, "count", count)
			_ = b.CraftItem(recipeID, count)
		} else {
			b.Logger.Warn("ExecuteAction: recipe not found for item", "item", itemName)
			var guessed uint32
			if _, err := fmt.Sscanf(itemName, "%d", &guessed); err == nil && guessed != 0 {
				_ = b.CraftItem(guessed, count)
			}
		}

	case "smelt":
		go func() {
			itemName := strings.ToLower(strings.TrimSpace(param))
			success := b.InventoryMgr.Container().SmeltItem(context.Background(), itemName)
			b.Logger.Info("Smelt complete", "success", success, "item", itemName)
		}()

	case "store", "storeall":
		go func() {
			itemName := strings.ToLower(strings.TrimSpace(param))
			success := b.InventoryMgr.Container().StoreItem(context.Background(), itemName, 0)
			b.Logger.Info("Store complete", "success", success, "item", itemName)
		}()

	case "take", "retrieve":
		go func() {
			parts := strings.Split(param, ",")
			itemName := strings.ToLower(strings.TrimSpace(parts[0]))
			count := int32(0)
			if len(parts) >= 2 {
				var parsed int
				if _, err := fmt.Sscanf(parts[1], "%d", &parsed); err == nil {
					count = int32(parsed)
				}
			}
			success := b.InventoryMgr.Container().GiveItem(context.Background(), itemName, user, count)
			b.Logger.Info("Give complete", "success", success, "item", itemName)
		}()

	default:
		b.Logger.Info("unknown or unhandled action label", "label", label, "param", param)
	}
}
