package bot

import (
	"fmt"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/affordance"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/scaffold"

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

// LookAtPlayer turns the head to a named player for a while.
//
// It returns false while the body is committed to something whose eyes are
// already spoken for.
//
// That refusal is the whole point. A person felling a tree keeps their eyes on
// the trunk; they do not track a friend's face mid-swing. This used to set the
// look target unconditionally, and because the movement tick reapplies that
// target every tick for the full hold, the head was pulled away from the block
// it was breaking — for seconds at a time, on a live run, while the arm swung at
// what was no longer under the crosshair. The swing was correct and the aim was
// not, which is why it read as a hit animation rather than as mining.
//
// The check goes before the lock rather than inside it: the predicate takes a
// second mutex of its own, and sync.Mutex is not reentrant. Holding one across
// the other is the deadlock that wedged this loop before.
//
// taskCommitted rather than IsBusy, and the difference matters. Breaking a block
// is done standing still, so a motion-based predicate reports a free body
// mid-swing and lets the head walk away from the trunk.
func (b *Bot) LookAtPlayer(username string, duration time.Duration) bool {
	if b.taskCommitted() {
		// Refusing is not enough: a target armed before the body committed
		// would survive into the work and keep the head on the player anyway.
		// The glance is released with the refusal, because the moment it was
		// for is gone.
		b.releaseLookTarget()
		return false
	}
	if _, _, ok := b.FindPlayer(username); !ok {
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
	// Routed through the cache: this is the entry point every block scan uses
	// (tree search, mining, interaction), and resolving a block state allocates a
	// name and a properties map every time.
	return b.WorldCache.BlockName(rid)
}

// GetBlockNetworkID returns the wire-format block ID at a position — numeric
// runtime ID or network hash, whichever the server selected. Transactions must
// echo this value back in UseItemTransactionData.BlockRuntimeID, where the
// server uses it to verify the client's world is synchronised with its own.
func (b *Bot) GetBlockNetworkID(x, y, z int32) (uint32, bool) {
	if b.WorldCache == nil {
		return 0, false
	}
	return b.WorldCache.GetBlockNetworkID(x, y, z)
}

// GetLastSentAim returns the yaw/pitch of the most recent PlayerAuthInput the
// server received. Interactions validate the crosshair direction against the
// target, so a click should only fire once the sent aim has actually turned
// toward what is being clicked — the value written by LookAt is eased, not
// instantaneous.
func (b *Bot) GetLastSentAim() (yaw, pitch float32) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.LastSentInputYaw, b.LastSentInputPitch
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

// BestPickaxeTier reports the mining tier of the best pickaxe the bot is
// carrying, not the one it happens to be holding.
//
// The distinction is the point. A bot that has a diamond pickaxe in slot 12 and
// a stack of cobblestone in the hand it is mining with is not a bot that cannot
// take obsidian, it is a bot that has not been asked to equip yet — and the
// scaffold executor, which does its breaking while movement is suspended and
// cannot afford a gather in the middle of a climb, needs to know that before
// it decides to abandon a step.
func (b *Bot) BestPickaxeTier() scaffold.ToolTier {
	slots := b.GetInventorySlots()
	names := b.GetItemNames()

	best := scaffold.TierHand
	// Slots are walked in index order rather than map order so the answer is the
	// same pickaxe on every call.
	for slot := uint32(0); slot < 64; slot++ {
		stack, held := slots[slot]
		if !held || stack.Count <= 0 {
			continue
		}
		if tier := scaffold.ToolTierOf(names[stack.NetworkID]); tier > best {
			best = tier
		}
	}
	return best
}

// DisableImplicitFollow records that a player does not want the bot drifting
// toward them unprompted, and ends any follow currently in force.
//
// The opt-out is sticky rather than a one-shot. A log showed a player typing
// "berhenti ikutin aku" — stop following me — and the bot following for the
// next four minutes, because nothing remembered the request and the next chat
// message pulled it back under. A player who has told a bot to leave them
// alone has to mean it for the rest of the session, not until they next speak.
func (b *Bot) DisableImplicitFollow(who string) {
	if who == "" {
		return
	}

	b.Mu.Lock()
	if b.ImplicitFollowOff == nil {
		b.ImplicitFollowOff = make(map[string]bool)
	}
	b.ImplicitFollowOff[who] = true
	if b.MovementState == "follow" && b.TargetPlayerName == who {
		b.MovementState = "idle"
		b.CurrentPath = nil
		b.TargetPlayerName = ""
	}
	b.Mu.Unlock()
}

// AllowImplicitFollow undoes an opt-out, so a player who changes their mind is
// not stuck with a decision made ten minutes ago.
func (b *Bot) AllowImplicitFollow(who string) {
	if who == "" {
		return
	}
	b.Mu.Lock()
	delete(b.ImplicitFollowOff, who)
	b.Mu.Unlock()
}

// ImplicitFollowAllowed reports whether the bot may drift toward a player
// without being asked.
func (b *Bot) ImplicitFollowAllowed(who string) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return !b.ImplicitFollowOff[who]
}

// RequestDrop authorises one deliberate leap off a ledge and reports whether it
// was granted.
//
// It mirrors SetSprintHint rather than being a field the movement layer reads
// directly, because the authority has to survive being set on the AGI goroutine
// and consumed on the 20Hz movement tick, and because it must be consumed on use
// — a latch that never clears turns into "walk off anything, from now on",
// which is the exact failure the gate was added to prevent.
func (b *Bot) RequestDrop() bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	// A drop only exists if there is somewhere to land. Authorising a leap into
	// a void is not a decision the model gets to make by accident.
	if !b.worldHasLandingAhead() {
		return false
	}
	b.dropOK = true
	return true
}

// ConsumeDrop reports whether a drop is authorised, and spends the authority.
func (b *Bot) ConsumeDrop() bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()

	if !b.dropOK {
		return false
	}
	b.dropOK = false
	return true
}

// worldHasLandingAhead reports whether the ground in front of the body ends in a
// drop that has a floor under it. It runs under b.Mu, so it reads the world
// model directly rather than through a method that would take the lock again.
func (b *Bot) worldHasLandingAhead() bool {
	if b.WorldModel == nil {
		return false
	}
	y := int32(math.Floor(float64(b.Pos.Y())))
	x := int32(math.Floor(float64(b.Pos.X())))
	z := int32(math.Floor(float64(b.Pos.Z())))
	for down := y - 4; down > y-24; down-- {
		if b.WorldModel.IsSolid(x, down, z) {
			return true
		}
	}
	return false
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

// SetGazeHint latches Jev's attention for a little while: what kind of thing
// the head should settle on next time the bot is standing still.
//
// The movement tick reads it only where the head is already free — a bot that is
// travelling turns its head where it is going, and nothing here can change that.
// The hint names a strategy, not a target: the tick still finds the thing, still
// decides how long to hold it, and still adds the micro-saccades, so the result
// is the same motion the bot would have made on its own.
func (b *Bot) SetGazeHint(kind string, until time.Time) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.GazeHint = kind
	b.GazeUntil = until
}

// ClearGazeHint drops the latch back to the movement rules' own idle gaze.
func (b *Bot) ClearGazeHint() {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.GazeHint = ""
	b.GazeUntil = time.Time{}
}

// GazePreference reports the latched attention, or "" when there is none or it
// has expired. Expiry is checked here rather than by a timer so an instruction
// can never outlive its own usefulness just because nothing happened to clear
// it.
func (b *Bot) GazePreference() string {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.GazeHint == "" || time.Now().After(b.GazeUntil) {
		return ""
	}
	return b.GazeHint
}

// SetSprintHint latches Jev's travel style for the trip that starts now:
// run, with an optional hop. The movement tick reads it every tick it walks a
// path; arrival (or a new trip, or ClearSprintHint) drops it. Hopping near an
// unmeasured ledge is the caller's mistake to avoid — the tick jumps
// blindly while the latch is set.
func (b *Bot) SetSprintHint(hop bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	v := true
	b.sprintHint = &v
	b.sprintHop = hop
}

// ClearSprintHint drops the latched travel style back to the movement rules'
// own judgement. Called when Jev picks a gait the latch does not carry
// (walk/auto), and whenever a trip ends.
func (b *Bot) ClearSprintHint() {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.clearSprintHintLocked()
}

// ClearSprintHintLocked is ClearSprintHint for callers that already hold b.Mu.
// sync.Mutex is not reentrant: a movement tick that drops the latch from inside
// its own critical section would wait for a lock only it can release, freezing
// every other bot goroutine behind the movement mutex.
func (b *Bot) ClearSprintHintLocked() {
	b.clearSprintHintLocked()
}

func (b *Bot) clearSprintHintLocked() {
	b.sprintHint = nil
	b.sprintHop = false
}

// SprintHint reports the latched travel style: run or not, hop or not, and
// whether any hint is latched at all.
func (b *Bot) SprintHint() (sprint, hop, latched bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.sprintHint == nil {
		return false, false, false
	}
	return *b.sprintHint, b.sprintHop, true
}

func (b *Bot) NavigateToBlock(x, y, z int32, tolerance float32) bool {
	if NavigateToBlockFunc != nil {
		return NavigateToBlockFunc(b, x, y, z, tolerance)
	}
	return false
}

func (b *Bot) WritePacket(pk packet.Packet) error {
	// On servers that negotiated server-authoritative block breaking, break
	// actions must ride the next PlayerAuthInput instead of going out as
	// standalone packets — a vanilla host tears the connection down on the
	// legacy form. See breaking.go for the mapping.
	if b.serverAuthBlockBreaking {
		if action, ok := pk.(*packet.PlayerAction); ok && b.RouteBreakAction(action) {
			return nil
		}
	}
	return b.Conn.WritePacket(pk)
}

// SyncHeldEquipment publishes the current held-slot snapshot so the server and
// nearby clients render the same item as InventoryMap.
func (b *Bot) SyncHeldEquipment() error {
	if b.Conn == nil {
		return fmt.Errorf("sync held equipment: bot is not connected")
	}
	slot, item, _ := b.heldItemInstance()
	return b.Conn.WritePacket(BuildHeldEquipmentPacket(b.GetEntityRuntimeID(), slot, item))
}

func BuildHeldEquipmentPacket(entityRuntimeID uint64, slot uint32, item protocol.ItemInstance) *packet.MobEquipment {
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
					WindowID:      protocol.Option(int8(protocol.WindowIDInventory)),
					InventorySlot: slot,
					OldItem:       protocol.ItemInstance{StackNetworkID: itemStackNetworkID, Stack: item},
					NewItem:       protocol.ItemInstance{StackNetworkID: hotbarStackNetworkID, Stack: hotbarItem},
				},
				{
					SourceType:    protocol.InventoryActionSourceContainer,
					WindowID:      protocol.Option(int8(protocol.WindowIDInventory)),
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

// EmptyHotbarSlotLocked returns the lowest empty hotbar slot, or 9 when the
// hotbar is full. Callers hold b.Mu.
func (b *Bot) EmptyHotbarSlotLocked() uint32 {
	for slot := uint32(0); slot < 9; slot++ {
		if item, ok := b.InventoryMap[slot]; !ok || item.Count == 0 {
			return slot
		}
	}
	return 9
}

func (b *Bot) UnequipItem() error {
	b.Mu.Lock()
	// A vanilla client cannot hold "nothing": the hand shows the selected
	// hotbar slot's content. Unequipping therefore means switching to an
	// actually empty hotbar slot. Faking it with HotBarSlot 0 (the old
	// behaviour) desyncs the server-side selection from b.HeldSlot: the next
	// MobEquipment echo re-broadcast the old slot's item and the hand flickered
	// back (ghost item).
	emptySlot := b.EmptyHotbarSlotLocked()
	if emptySlot >= 9 {
		b.Mu.Unlock()
		return fmt.Errorf("unequip: no empty hotbar slot available")
	}
	b.HeldSlot = emptySlot
	b.Mu.Unlock()

	pk := &packet.MobEquipment{
		EntityRuntimeID: b.Conn.GameData().EntityRuntimeID,
		NewItem:         protocol.ItemInstance{},
		InventorySlot:   byte(emptySlot),
		HotBarSlot:      byte(emptySlot),
		WindowID:        0,
	}
	return b.Conn.WritePacket(pk)
}

// LookAt aims the head at a point in the world.
//
// It also releases any tracked look target, and that is not incidental. The
// movement tick reapplies a tracked target every tick for its whole hold —
// twenty times a second, against the four times a second a break rhythm aims at
// the block. The target won every time, so a body felling a tree swung at a
// crosshair that had moved to a friend's face. The swing was right and the aim
// was not, which is why it read as a hit animation rather than as mining.
//
// A deliberate aim is exactly the signal that the head is spoken for, so it
// clears the tracking the same way SetLookAngles pins the idle look. Following
// is unaffected: it aims through the tick's own target rather than through
// here, so it never cancels itself.
func (b *Bot) LookAt(pos mgl32.Vec3) {
	b.releaseLookTarget()
	if LookAtFunc != nil {
		LookAtFunc(b, pos)
	}
}

// releaseLookTarget drops any tracked look target, so the movement tick stops
// steering the head at a player. Both the deliberate aim above and a refused
// vision reflex use it: the first because the head is now spoken for, the
// second because the moment the glance was for has passed.
func (b *Bot) releaseLookTarget() {
	b.Mu.Lock()
	b.LookTargetName = ""
	b.LookTargetUntil = time.Time{}
	b.Mu.Unlock()
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

// AimAtPlayerForDrop turns the bot to face a player and holds that bearing,
// re-reading the player's LIVE position on every iteration so a player who is
// walking around the bot is tracked right up to the instant of the drop.
//
// Why this exists: Bedrock derives a dropped item's direction from the yaw/pitch
// of the bot's LAST PlayerAuthInput. The old drop flow computed the bearing once
// and then pinned it through several hundred ms of fixed sleeps before dropping —
// so if the player moved during that window (which they do constantly while
// testing) the item flew toward where they used to be, scattering to the
// back/left/right. This method instead loops: aim → confirm the server received
// the yaw → re-check the player; it returns the moment the bot is genuinely
// facing the player AND the player has stopped drifting. The caller MUST invoke
// DropItem immediately after this returns — any extra delay reintroduces the
// same staleness.
//
// pitch is the downward look angle to use (positive = looking down, which
// shortens the toss so it lands at the player's feet). Returns the final aim yaw
// and ok=false only if the player cannot be found at all.
func (b *Bot) AimAtPlayerForDrop(target string, pitch float32) (float32, bool) {
	var lastYaw float32
	for i := 0; i < 12; i++ {
		_, playerPos, ok := b.FindPlayer(target)
		if !ok {
			return lastYaw, i > 0
		}

		botPos := b.GetCoords()
		dx := playerPos.X() - botPos.X()
		dz := playerPos.Z() - botPos.Z()
		if dx*dx+dz*dz < 0.0004 {
			// Player is essentially on top of us — any horizontal yaw is fine.
			b.Mu.Lock()
			cur := b.Yaw
			b.Mu.Unlock()
			b.SetLookAngles(cur, pitch)
			b.WaitForYawSync(cur, 200*time.Millisecond)
			return cur, true
		}

		yaw := float32(math.Atan2(float64(dz), float64(dx))*180/math.Pi) - 90
		for yaw < 0 {
			yaw += 360
		}
		lastYaw = yaw

		// Force the body yaw directly and wait for the next PlayerAuthInput to
		// carry it to the server.
		b.SetLookAngles(yaw, pitch)
		b.WaitForYawSync(yaw, 300*time.Millisecond)

		// Re-read the player. If they barely moved while we were turning, we are
		// locked on — commit. Otherwise loop and re-aim at their new position.
		_, newPos, ok2 := b.FindPlayer(target)
		if !ok2 {
			return yaw, true
		}
		movedSq := (newPos.X()-playerPos.X())*(newPos.X()-playerPos.X()) +
			(newPos.Z()-playerPos.Z())*(newPos.Z()-playerPos.Z())
		if movedSq < 0.25 { // < 0.5 block of horizontal drift since we aimed
			return yaw, true
		}
	}
	return lastYaw, true
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

// SetAppetite records how much risk the bot should take on purpose.
//
// It lives on the bot rather than on the combat manager because the disposition
// is not a combat setting: it decides whether the bot walks a cliff or jumps
// it, whether it backs off a creeper or watches, and it has to be readable from
// the movement layer and the brain as well as from combat.
func (b *Bot) SetAppetite(a affordance.Appetite) {
	b.Mu.Lock()
	b.appetite = a
	b.Mu.Unlock()
}

// Appetite reports the current disposition.
func (b *Bot) Appetite() affordance.Appetite {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.appetite == affordance.Careful && !b.appetiteSet {
		// Zero is Careful, so an untouched bot reads as careful. That is the
		// right default and it is worth being explicit rather than relying on
		// the zero value, because the alternative — a bot that starts reckless
		// because its field happens to be uninitialised — is not a state anyone
		// would want to debug from a log.
		return affordance.Careful
	}
	return b.appetite
}

// FindScaffoldItem locates a block the bot is carrying that it could place.
//
// It lives on the bot rather than only on the gatherer because the composed
// defences are not the gatherer's business: a combat tactic reaching for a wall
// and a scaffold step reaching for a support block want the same answer.
func (b *Bot) FindScaffoldItem() (uint32, protocol.ItemStack, bool) {
	if b.Gatherer == nil {
		return 0, protocol.ItemStack{}, false
	}
	return b.Gatherer.FindScaffoldItem()
}

// CountInventoryItemsFor totals the stacks in the bot's inventory whose name
// matches want, namespaces normalised on both sides.
func (b *Bot) CountInventoryItemsFor(want string) int {
	slots := b.GetInventorySlots()
	names := b.GetItemNames()

	total := 0
	wanted := strings.ToLower(strings.TrimPrefix(want, "minecraft:"))
	for _, stack := range slots {
		if stack.Count == 0 {
			continue
		}
		name := strings.ToLower(strings.TrimPrefix(names[stack.NetworkID], "minecraft:"))
		if name == wanted {
			total += int(stack.Count)
		}
	}
	return total
}
