package bot

import (
	"context"
	"fmt"
	"math"
	"time"

	"bedrock-ai/internal/bot/placement"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// PlaceBlock equips the requested inventory slot, sends the vanilla placement
// packet sequence, and returns only after the server confirms the destination.
func (b *Bot) PlaceBlock(ctx context.Context, request placement.Request) error {
	b.placeMu.Lock()
	defer b.placeMu.Unlock()

	if err := ValidatePlacementRequest(request); err != nil {
		return err
	}
	if err := b.EquipItem(request.InventorySlot); err != nil {
		return fmt.Errorf("equip placement item: %w", err)
	}
	if !sleepWithContext(ctx, EquipItemDelay) {
		return context.Cause(ctx)
	}

	heldSlot, heldItem, ok := b.heldItemInstance()
	if !ok {
		return fmt.Errorf("held slot %d is empty after equip", heldSlot)
	}
	supportNetworkID, ok := b.WorldCache.GetBlockNetworkID(request.Support.X(), request.Support.Y(), request.Support.Z())
	if !ok {
		return fmt.Errorf("support block is not loaded at %v", request.Support)
	}
	initialRID, initialLoaded := b.WorldCache.GetBlockRID(request.Destination.X(), request.Destination.Y(), request.Destination.Z())

	lookTarget := mgl32.Vec3{
		float32(request.Support.X()) + request.ClickedOffset.X(),
		float32(request.Support.Y()) + request.ClickedOffset.Y(),
		float32(request.Support.Z()) + request.ClickedOffset.Z(),
	}
	b.LookAt(lookTarget)
	defer b.ResetLook()
	b.Mu.Lock()
	targetYaw := b.Yaw
	b.Mu.Unlock()
	if !b.WaitForYawSync(targetYaw, YawSyncTimeout) {
		return fmt.Errorf("placement look direction did not reach the network tick")
	}
	if !sleepWithContext(ctx, AngleStabilizationDelay) {
		return context.Cause(ctx)
	}

	updates, unsubscribe := b.subscribeBlockUpdates(request.Destination)
	defer unsubscribe()
	start, transaction, stop := BuildPlacementPackets(
		b.GetEntityRuntimeID(),
		request,
		heldSlot,
		heldItem,
		b.GetCoords().Add(mgl32.Vec3{0, PlayerEyeHeight, 0}),
		supportNetworkID,
	)
	b.Logger.Info("placing block",
		"slot", heldSlot,
		"item_network_id", heldItem.Stack.NetworkID,
		"stack_network_id", heldItem.StackNetworkID,
		"support", request.Support,
		"destination", request.Destination,
		"face", request.Face,
		"support_network_id", supportNetworkID,
		"client_prediction", protocol.ClientPredictionSuccess,
	)
	if err := b.Conn.WritePacket(start); err != nil {
		return fmt.Errorf("start block placement: %w", err)
	}
	defer func() {
		if err := b.Conn.WritePacket(stop); err != nil {
			b.Logger.Warn("failed to stop block placement", "error", err)
		}
	}()
	if err := b.Conn.WritePacket(transaction); err != nil {
		return fmt.Errorf("send block placement transaction: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, BlockPlacementTimeout)
	defer cancel()
	for {
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("server did not confirm block placement at %v: %w", request.Destination, waitCtx.Err())
		case rid := <-updates:
			if initialLoaded && rid == initialRID {
				continue
			}
			name, loaded := b.GetBlockName(request.Destination.X(), request.Destination.Y(), request.Destination.Z())
			if !loaded || name == "minecraft:air" {
				continue
			}
			return nil
		}
	}
}

func ValidatePlacementRequest(request placement.Request) error {
	if request.Face < 0 || request.Face > 5 {
		return fmt.Errorf("invalid block face %d", request.Face)
	}
	for axis, value := range []float32{request.ClickedOffset.X(), request.ClickedOffset.Y(), request.ClickedOffset.Z()} {
		if value < 0 || value > 1 {
			return fmt.Errorf("clicked offset axis %d is outside block bounds: %f", axis, value)
		}
	}
	return nil
}

// HeldItemInstance is the held slot plus the item the server attributes a
// placement to, public for the scaffold package's narrow Bot interface.
func (b *Bot) HeldItemInstance() (uint32, protocol.ItemInstance, bool) {
	return b.heldItemInstance()
}

func (b *Bot) heldItemInstance() (uint32, protocol.ItemInstance, bool) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	slot := b.HeldSlot
	stack, ok := b.InventoryMap[slot]
	if !ok || stack.Count == 0 {
		return slot, protocol.ItemInstance{}, false
	}
	return slot, protocol.ItemInstance{StackNetworkID: b.StackNetworkIDs[slot], Stack: stack}, true
}

func BuildPlacementPackets(entityRuntimeID uint64, request placement.Request, heldSlot uint32, heldItem protocol.ItemInstance, playerPosition mgl32.Vec3, supportNetworkID uint32) (*packet.PlayerAction, *packet.InventoryTransaction, *packet.PlayerAction) {
	start := &packet.PlayerAction{
		EntityRuntimeID: entityRuntimeID,
		ActionType:      protocol.PlayerActionStartItemUseOn,
		BlockPosition:   request.Support,
		ResultPosition:  request.Destination,
		BlockFace:       request.Face,
	}
	transaction := &packet.InventoryTransaction{
		Actions: []protocol.InventoryAction{},
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:       protocol.UseItemActionClickBlock,
			TriggerType:      protocol.TriggerTypePlayerInput,
			BlockPosition:    request.Support,
			BlockFace:        request.Face,
			HotBarSlot:       int32(heldSlot),
			HeldItem:         heldItem,
			Position:         playerPosition,
			ClickedPosition:  request.ClickedOffset,
			BlockRuntimeID:   supportNetworkID,
			ClientPrediction: protocol.ClientPredictionSuccess,
		},
	}
	stop := &packet.PlayerAction{
		EntityRuntimeID: entityRuntimeID,
		ActionType:      protocol.PlayerActionStopItemUseOn,
		BlockPosition:   request.Destination,
	}
	return start, transaction, stop
}

func (b *Bot) subscribeBlockUpdates(pos protocol.BlockPos) (<-chan uint32, func()) {
	b.blockUpdateMu.Lock()
	defer b.blockUpdateMu.Unlock()
	if b.blockUpdateWaiters == nil {
		b.blockUpdateWaiters = make(map[protocol.BlockPos]map[uint64]chan uint32)
	}
	b.nextBlockUpdateWaiter++
	id := b.nextBlockUpdateWaiter
	ch := make(chan uint32, 4)
	if b.blockUpdateWaiters[pos] == nil {
		b.blockUpdateWaiters[pos] = make(map[uint64]chan uint32)
	}
	b.blockUpdateWaiters[pos][id] = ch
	return ch, func() {
		b.blockUpdateMu.Lock()
		defer b.blockUpdateMu.Unlock()
		delete(b.blockUpdateWaiters[pos], id)
		if len(b.blockUpdateWaiters[pos]) == 0 {
			delete(b.blockUpdateWaiters, pos)
		}
	}
}

// NotifyBlockUpdate publishes an authoritative server block update to any
// operation waiting on that position.
func (b *Bot) NotifyBlockUpdate(pos protocol.BlockPos, rid uint32) {
	b.blockUpdateMu.Lock()
	defer b.blockUpdateMu.Unlock()
	for _, ch := range b.blockUpdateWaiters[pos] {
		select {
		case ch <- rid:
		default:
		}
	}
}

func sleepWithContext(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// PlaceShield puts a block between the body and a threat, and reports whether
// the server put it there.
//
// This is the composed answer to being ambushed: a bot reflexively runs from a
// creeper, and a player puts a block down first and walks away behind it. The
// difference is visible from outside — one of them was ready for the next one.
//
// It goes through PlaceBlock rather than writing a transaction, so it inherits
// the confirmation: the return value is whether the world changed, not whether a
// packet was sent. A bot that believed it had walled itself off when it had not
// would stand in the blast, which is the specific outcome this exists to avoid.
//
// Failing to place is not an error. The caller is already fleeing, and running is
// still the right answer; this is the better answer when it works, never the
// only answer.
func (b *Bot) PlaceShield(ctx context.Context, threat mgl32.Vec3) bool {
	if b == nil {
		return false
	}

	feet := b.GetCoords()
	// Between the body and the threat: one block along the bearing, at feet
	// height, so the body has something to duck behind rather than something to
	// trip over.
	dx, dz := threat.X()-feet.X(), threat.Z()-feet.Z()
	d := float32(math.Sqrt(float64(dx*dx + dz*dz)))
	if d < 1e-3 {
		// Standing inside the threat: any direction is an improvement, so pick
		// one rather than standing still.
		d, dx, dz = 1, 1, 0
	}

	cell := protocol.BlockPos{
		int32(math.Floor(float64(feet.X() + dx/d))),
		int32(math.Floor(float64(feet.Y()))),
		int32(math.Floor(float64(feet.Z() + dz/d))),
	}
	support := protocol.BlockPos{cell.X(), cell.Y() - 1, cell.Z()}

	slot, _, ok := b.FindScaffoldItem()
	if !ok {
		return false
	}

	if err := b.PlaceBlock(ctx, placement.Request{
		InventorySlot: slot,
		Destination:   cell,
		Support:       support,
		Face:          BlockFaceTop,
		ClickedOffset: mgl32.Vec3{BlockCenterOffset, 1, BlockCenterOffset},
	}); err != nil {
		b.Logger.Debug("shield placement failed, the caller is fleeing anyway",
			"cell", cell, "error", err.Error())
		return false
	}
	return true
}
