package bot

import (
	"fmt"
	"math"
	"time"

	"bedrock-ai/internal/bot/entity"

	"github.com/df-mc/dragonfly/server/world/chunk"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// FindPlayer returns the runtime ID and current position of a player by username (case-insensitive)
func (b *Bot) FindPlayer(username string) (uint64, mgl32.Vec3, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	for name, id := range b.PlayerEntityIDs {
		if playerNameMatches(name, username) {
			if pos, ok := b.PlayerPositions[id]; ok {
				return id, pos, true
			}
		}
	}
	return 0, mgl32.Vec3{}, false
}

func (b *Bot) FindPlayerView(username string) (uint64, mgl32.Vec3, float32, float32, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	for name, id := range b.PlayerEntityIDs {
		if playerNameMatches(name, username) {
			pos, ok := b.PlayerPositions[id]
			if !ok {
				return 0, mgl32.Vec3{}, 0, 0, false
			}
			return id, pos, b.PlayerYaws[id], b.PlayerPitches[id], true
		}
	}
	return 0, mgl32.Vec3{}, 0, 0, false
}

func (b *Bot) LookAtPlayer(username string, duration time.Duration) bool {
	_, _, ok := b.FindPlayer(username)
	if !ok {
		return false
	}
	if duration <= 0 {
		duration = 4 * time.Second
	}

	b.Mu.Lock()
	b.LookTargetName = username
	b.LookTargetUntil = time.Now().Add(duration)
	b.Mu.Unlock()

	return true
}

func (b *Bot) GetBlockName(x, y, z int32) (string, bool) {
	if b.WorldCache == nil {
		return "", false
	}
	rid, ok := b.WorldCache.GetBlockRID(x, y, z)
	if !ok {
		return "", false
	}
	name, _, ok := chunk.RuntimeIDToState(rid)
	return name, ok
}

// RecalculatePath computes the shortest path to targetPos using A* search.
func (b *Bot) RecalculatePath() {
	if RecalculatePathFunc != nil {
		RecalculatePathFunc(b)
	}
}

func (b *Bot) Close() error {
	if b.Conn != nil {
		return b.Conn.Close()
	}
	return nil
}

func (b *Bot) GetEntities() map[uint64]*entity.Info {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	snapshot := make(map[uint64]*entity.Info, len(b.Actors))
	for id, info := range b.Actors {
		if info == nil {
			continue
		}
		cp := *info
		snapshot[id] = &cp
	}
	return snapshot
}

func (b *Bot) GetHeldItemSlot() uint32 {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.HeldSlot
}

func (b *Bot) GetInventorySlots() map[uint32]protocol.ItemStack {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	snapshot := make(map[uint32]protocol.ItemStack, len(b.InventoryMap))
	for slot, stack := range b.InventoryMap {
		snapshot[slot] = stack
	}
	return snapshot
}

func (b *Bot) GetItemNames() map[int32]string {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	snapshot := make(map[int32]string, len(b.ItemNames))
	for id, name := range b.ItemNames {
		snapshot[id] = name
	}
	return snapshot
}

func (b *Bot) SendChat(msg string) {
	b.SendSafeChat(msg)
}

func (b *Bot) GetEntityRuntimeID() uint64 {
	return b.Conn.GameData().EntityRuntimeID
}

func (b *Bot) GetLocalWorldModel() entity.WorldModel {
	return b.WorldModel
}

func (b *Bot) NavigateTo(pos mgl32.Vec3) {
	if NavigateToFunc != nil {
		NavigateToFunc(b, pos)
	}
}

func (b *Bot) StopMovement() {
	if StopMovementFunc != nil {
		StopMovementFunc(b)
	}
}

func (b *Bot) NavigateToBlock(x, y, z int32, tolerance float32) bool {
	if NavigateToBlockFunc != nil {
		return NavigateToBlockFunc(b, x, y, z, tolerance)
	}
	return false
}

func (b *Bot) WritePacket(pk packet.Packet) error {
	return b.Conn.WritePacket(pk)
}

// SyncHeldEquipment publishes the current held-slot snapshot so the server and
// nearby clients render the same item as InventoryMap.
func (b *Bot) SyncHeldEquipment() error {
	if b.Conn == nil {
		return fmt.Errorf("sync held equipment: bot is not connected")
	}
	slot, item, _ := b.heldItemInstance()
	return b.Conn.WritePacket(buildHeldEquipmentPacket(b.GetEntityRuntimeID(), slot, item))
}

func buildHeldEquipmentPacket(entityRuntimeID uint64, slot uint32, item protocol.ItemInstance) *packet.MobEquipment {
	return &packet.MobEquipment{
		EntityRuntimeID: entityRuntimeID,
		NewItem:         item,
		InventorySlot:   byte(slot),
		HotBarSlot:      byte(slot),
		WindowID:        byte(protocol.WindowIDInventory),
	}
}

func (b *Bot) EquipItem(slot uint32) error {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	item, ok := b.InventoryMap[slot]
	if !ok || item.Count == 0 {
		return fmt.Errorf("slot %d empty", slot)
	}

	targetHotbarSlot := uint32(0)
	if b.HeldSlot < 9 {
		targetHotbarSlot = b.HeldSlot
	}

	itemStackNetworkID := b.StackNetworkIDs[slot]
	if slot >= 9 {
		hotbarItem := b.InventoryMap[targetHotbarSlot]
		hotbarStackNetworkID := b.StackNetworkIDs[targetHotbarSlot]
		tx := &packet.InventoryTransaction{
			Actions: []protocol.InventoryAction{
				{
					SourceType:    protocol.InventoryActionSourceContainer,
					WindowID:      protocol.WindowIDInventory,
					InventorySlot: slot,
					OldItem:       protocol.ItemInstance{StackNetworkID: itemStackNetworkID, Stack: item},
					NewItem:       protocol.ItemInstance{StackNetworkID: hotbarStackNetworkID, Stack: hotbarItem},
				},
				{
					SourceType:    protocol.InventoryActionSourceContainer,
					WindowID:      protocol.WindowIDInventory,
					InventorySlot: targetHotbarSlot,
					OldItem:       protocol.ItemInstance{StackNetworkID: hotbarStackNetworkID, Stack: hotbarItem},
					NewItem:       protocol.ItemInstance{StackNetworkID: itemStackNetworkID, Stack: item},
				},
			},
			TransactionData: &protocol.NormalTransactionData{},
		}
		if err := b.Conn.WritePacket(tx); err != nil {
			return fmt.Errorf("swap item to hotbar failed: %w", err)
		}

		b.InventoryMap[targetHotbarSlot] = item
		if itemStackNetworkID != 0 {
			b.StackNetworkIDs[targetHotbarSlot] = itemStackNetworkID
		} else {
			delete(b.StackNetworkIDs, targetHotbarSlot)
		}
		if hotbarItem.Count > 0 {
			b.InventoryMap[slot] = hotbarItem
			if hotbarStackNetworkID != 0 {
				b.StackNetworkIDs[slot] = hotbarStackNetworkID
			} else {
				delete(b.StackNetworkIDs, slot)
			}
		} else {
			delete(b.InventoryMap, slot)
			delete(b.StackNetworkIDs, slot)
		}
	} else {
		targetHotbarSlot = slot
	}

	b.HeldSlot = targetHotbarSlot

	pk := &packet.MobEquipment{
		EntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
		NewItem:         protocol.ItemInstance{StackNetworkID: itemStackNetworkID, Stack: item},
		InventorySlot:   byte(targetHotbarSlot),
		HotBarSlot:      byte(targetHotbarSlot),
		WindowID:        0,
	}
	return b.Conn.WritePacket(pk)
}

func (b *Bot) UnequipItem() error {
	pk := &packet.MobEquipment{
		EntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
		NewItem:         protocol.ItemInstance{},
		InventorySlot:   0,
		HotBarSlot:      0,
		WindowID:        0,
	}
	return b.Conn.WritePacket(pk)
}

func (b *Bot) LookAt(pos mgl32.Vec3) {
	if LookAtFunc != nil {
		LookAtFunc(b, pos)
	}
}

func (b *Bot) SetLookAngles(yaw, pitch float32) {
	b.Mu.Lock()
	b.Yaw = yaw
	b.HeadYaw = yaw
	b.Pitch = pitch
	// Pin the idle look target so the movement loop's applyIdleLook doesn't
	// override the gaze on the next tick. Without this, the eased look
	// interpolation can drift the body Yaw away from the forced value before
	// the next PlayerAuthInput is sent.
	b.IdleLookTargetYaw = yaw
	b.IdleLookTargetPitch = pitch
	b.IdleLookTargetType = "static"
	b.NextIdleLookChange = time.Now().Add(2 * time.Second)
	b.Mu.Unlock()
}

// OverrideLookPitch pins the bot's pitch to a specific value, bypassing the
// idle look loop's eye-corrected recomputation. Used to angle a toss upward
// so the dropped item arcs further than its tiny base velocity allows.
// Pair with a brief sleep (200-300ms) so the movement tick can interpolate
// to the new pitch before the drop transaction is sent.
func (b *Bot) OverrideLookPitch(pitch float32) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.Pitch = pitch
	b.IdleLookTargetPitch = pitch
	// Use a sentinel that applyIdleLook will treat as "use the static
	// IdleLookTargetYaw/Pitch as-is" rather than recomputing from block pos.
	b.IdleLookTargetType = "static"
	b.NextIdleLookChange = time.Now().Add(2 * time.Second)
}

// ResetLook clears any pinned/static look target and levels the pitch to the
// horizon so the head returns to a neutral forward gaze. Call this after a
// give/drop that forced an upward pitch (SetLookAngles) — otherwise the head
// stays stuck looking up because the idle look loop keeps re-applying the
// pinned static angles.
func (b *Bot) ResetLook() {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.Pitch = 0
	b.IdleLookTargetPitch = 0
	b.IdleLookTargetType = ""
	b.IdleLookTargetID = 0
	b.LookTargetName = ""
	b.LookTargetUntil = time.Time{}
	// Expire the pinned window immediately so applyIdleLook picks a fresh,
	// natural target on the next tick instead of holding the forced pose.
	b.NextIdleLookChange = time.Time{}
}

// WaitForYawSync polls until the movement tick has actually sent a
// PlayerAuthInput carrying the target yaw to the server, or the timeout
// elapses. Returns true when sync confirmed. Used before drop transactions
// so item drop direction matches the bot's intended camera direction.
func (b *Bot) WaitForYawSync(targetYaw float32, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b.Mu.Lock()
		sent := b.LastSentInputYaw
		b.Mu.Unlock()
		diff := math.Abs(float64(targetYaw - sent))
		if diff > 180 {
			diff = 360 - diff
		}
		if diff < 2.0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func (b *Bot) playerApproachPosition(username string) (mgl32.Vec3, bool) {
	_, pos, yaw, _, ok := b.FindPlayerView(username)
	if !ok {
		return mgl32.Vec3{}, false
	}
	yawWorldRad := float64(yaw+90) * math.Pi / 180
	front := mgl32.Vec3{
		float32(math.Cos(yawWorldRad)) * 1.6,
		0,
		float32(math.Sin(yawWorldRad)) * 1.6,
	}
	return pos.Add(front), true
}
