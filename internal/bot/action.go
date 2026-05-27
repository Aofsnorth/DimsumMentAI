package bot

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
)

// WalkTo directs the bot to walk to a coordinate
func (b *Bot) WalkTo(pos mgl32.Vec3) {
	b.Mu.Lock()
	b.MovementState = "walk_to"
	b.TargetPos = pos
	b.TargetPlayerName = ""
	b.LookTargetName = ""
	b.LookTargetUntil = time.Time{}
	b.Logger.Debug("WalkTo initiated", "x", pos.X(), "y", pos.Y(), "z", pos.Z())
	b.Mu.Unlock()
	b.RecalculatePath()
}

func (b *Bot) ComeToPlayer(username string) bool {
	target, ok := b.playerApproachPosition(username)
	if !ok {
		if _, pos, found := b.FindPlayer(username); found {
			target = pos
			ok = true
		}
	}
	if !ok {
		b.Logger.Warn("Player not found for come", "username", username)
		return false
	}

	b.Mu.Lock()
	b.MovementState = "walk_to"
	b.TargetPlayerName = ""
	b.TargetPos = target
	b.LookTargetName = username
	b.LookTargetUntil = time.Now().Add(12 * time.Second)
	b.LastPathRecalcTime = time.Now()
	b.Mu.Unlock()

	b.RecalculatePath()
	b.Logger.Debug("ComeToPlayer initiated", "username", username, "target", target)
	return true
}

// FollowPlayer directs the bot to follow a player
func (b *Bot) FollowPlayer(username string) {
	b.Mu.Lock()
	b.MovementState = "follow"
	b.TargetPlayerName = username
	b.LookTargetName = username
	b.LookTargetUntil = time.Now().Add(24 * time.Hour)
	b.Logger.Debug("FollowPlayer initiated", "username", username)
	b.Mu.Unlock()

	if _, pos, ok := b.FindPlayer(username); ok {
		b.Mu.Lock()
		b.TargetPos = pos
		b.LastPathRecalcTime = time.Now()
		b.Mu.Unlock()
		b.RecalculatePath()
		b.Logger.Debug("Player found for follow, setting target position", "username", username, "pos", pos)
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
	b.TargetPlayerName = ""
	b.LookTargetName = ""
	b.LookTargetUntil = time.Time{}
	b.Logger.Debug("Bot movement stopped")
}

// TriggerEmote triggers a custom bot animation
func (b *Bot) TriggerEmote(name string) {
	b.TriggerEmoteFor(name, 40)
}

func (b *Bot) TriggerEmoteFor(name string, ticks int) {
	if ticks <= 0 {
		ticks = 40
	}
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.EmoteState = name
	b.EmoteTicks = ticks
	b.Logger.Debug("Emote triggered", "name", name)
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

	for name, targetID := range b.PlayerEntityIDs {
		if playerNameMatches(name, username) {
			if pos, ok := b.PlayerPositions[targetID]; ok {
				return pos, true
			}
		}
	}
	return mgl32.Vec3{}, false
}
