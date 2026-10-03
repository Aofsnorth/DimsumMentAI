package bot

import (
	"fmt"
	"strings"
	"time"

	"bedrock-ai/internal/bot/gathering"

	"github.com/go-gl/mathgl/mgl32"
)

// WalkToReplanEpsilon is how far a walk_to target may drift before the route
// is worth re-planning. A dropped item settles and creeps, and re-planning for
// every centimetre of that creep re-runs A* several times a second for a
// destination one block away.
const WalkToReplanEpsilon = 0.25

// ShouldReplanForWalkTo decides whether a WalkTo call needs a fresh route.
//
// Walking somewhere is a continuous activity, not a series of commands: callers
// re-assert the same destination while they wait (the looter polls its target,
// actions re-issue a destination before waiting on it). Re-planning on every
// call cost a full A* run per poll and, worse, reset the stuck-detection
// bookkeeping each time, so a bot wedged against a wall re-planned the
// identical route into the identical blocker forever and never escalated.
//
// It is pure so the policy is unit testable without a live bot.
func ShouldReplanForWalkTo(state string, hasPath bool, current, next mgl32.Vec3) bool {
	if state != "walk_to" {
		return true
	}
	if !hasPath {
		return true
	}
	dx := float64(next.X() - current.X())
	dy := float64(next.Y() - current.Y())
	dz := float64(next.Z() - current.Z())
	return dx*dx+dy*dy+dz*dz > WalkToReplanEpsilon*WalkToReplanEpsilon
}

// WalkTo directs the bot to walk to a coordinate
func (b *Bot) WalkTo(pos mgl32.Vec3) {
	b.Mu.Lock()
	replan := ShouldReplanForWalkTo(b.MovementState, len(b.CurrentPath) > 0, b.TargetPos, pos)
	b.MovementState = "walk_to"
	b.TargetPos = pos
	b.TargetPlayerName = ""
	b.LookTargetName = ""
	b.LookTargetUntil = time.Time{}
	b.Logger.Debug("WalkTo initiated", "x", pos.X(), "y", pos.Y(), "z", pos.Z())
	b.Mu.Unlock()
	if !replan {
		return
	}
	b.RecalculatePath()
}

// SetTargetTolerance sets the arrival tolerance (in blocks) used by the
// steering loop when checking whether a walk_to target has been reached.
// The default is 2.0; callers that need sub-block precision (e.g. item
// pickup) can tighten it and restore the default afterwards.
func (b *Bot) SetTargetTolerance(t float32) {
	b.Mu.Lock()
	b.TargetTolerance = t
	b.Mu.Unlock()
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

// SnapshotJourney captures the movement intent so a detour can put it back.
func (b *Bot) SnapshotJourney() gathering.Journey {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return gathering.Journey{
		State:        b.MovementState,
		Target:       b.TargetPos,
		TargetPlayer: b.TargetPlayerName,
		LookTarget:   b.LookTargetName,
		LookUntil:    b.LookTargetUntil,
	}
}

// RestoreJourney puts back the movement intent a detour interrupted and starts
// the path again when there is somewhere to go. Without this, a detour that
// re-aims navigation — the item sweep walking to a drop — kills the journey it
// interrupted, and the bot stands still where that journey died.
func (b *Bot) RestoreJourney(j gathering.Journey) {
	b.Mu.Lock()
	b.MovementState = j.State
	b.TargetPos = j.Target
	b.TargetPlayerName = j.TargetPlayer
	b.LookTargetName = j.LookTarget
	b.LookTargetUntil = j.LookUntil
	b.CurrentPath = nil
	b.PathIndex = 0
	b.Mu.Unlock()
	if j.State == "walk_to" || j.State == "follow" {
		b.RecalculatePath()
	}
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
	b.EmoteJumpSpent = false
	b.Logger.Debug("Emote triggered", "name", name)
	if name == "jump" {
		b.jumpRequestedAt = time.Now()
	}
}

// FormatItemName converts a raw Minecraft item/block ID (e.g.
// "minecraft:oak_planks" or "oak_planks") into a human-friendly display name
// (e.g. "Oak Planks"). Use this everywhere the bot prints item names in chat.
func FormatItemName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "minecraft:")
	name = strings.ReplaceAll(name, "_", " ")
	words := strings.Fields(name)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}

// FormatItemName is a method wrapper so *Bot satisfies the Bot interfaces in
// subpackages (gathering, husbandry, etc.) without those subpackages needing to
// import the bot package.
func (b *Bot) FormatItemName(name string) string {
	return FormatItemName(name)
}

// GetInventorySummary returns a human-readable list of items in the inventory
func (b *Bot) GetInventorySummary() string {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	if len(b.InventoryMap) == 0 {
		return "Inventory kosong"
	}

	itemCounts := make(map[string]int)
	for _, stack := range b.InventoryMap {
		name := b.ItemNames[stack.NetworkID]
		if name == "" {
			name = fmt.Sprintf("item_%d", stack.NetworkID)
		}
		itemCounts[FormatItemName(name)] += int(stack.Count)
	}

	items := make([]string, 0, len(itemCounts))
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
	return FormatItemName(name)
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

// GetYaw returns the body yaw in degrees, using the project convention
// yaw = atan2(dz, dx) * 180/pi - 90. Interaction needs it to resolve "whatever
// is in front of you" into an actual direction.
func (b *Bot) GetYaw() float32 {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.Yaw
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
