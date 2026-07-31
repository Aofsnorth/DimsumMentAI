// Package player handles player, entity, and inventory-related packets.
package player

import (
	"log/slog"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func handleAddPlayer(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.AddPlayer)
	b.Mu.Lock()
	b.PlayerEntityIDs[p.Username] = p.EntityRuntimeID
	b.PlayerUsernames[p.EntityRuntimeID] = p.Username
	b.PlayerPositions[p.EntityRuntimeID] = trackedPlayerFeetPosition(p.Position)
	b.PlayerYaws[p.EntityRuntimeID] = p.Yaw
	b.PlayerPitches[p.EntityRuntimeID] = p.Pitch
	b.PlayerUUIDs[p.UUID] = p.Username
	b.Mu.Unlock()
	b.Logger.Debug("tracked player spawned", slog.String("username", p.Username), slog.Uint64("runtime_id", p.EntityRuntimeID))
	return true
}

func handleMovePlayerPacket(b *bot.Bot, pk packet.Packet) bool {
	handleMovePlayer(b, pk.(*packet.MovePlayer))
	return true
}

func handleCorrectPredictionPacket(b *bot.Bot, pk packet.Packet) bool {
	handleCorrectPrediction(b, pk.(*packet.CorrectPlayerMovePrediction))
	return true
}

func handlePlayerListPacket(b *bot.Bot, pk packet.Packet) bool {
	handlePlayerList(b, pk.(*packet.PlayerList))
	return true
}

func handleCraftingDataPacket(b *bot.Bot, pk packet.Packet) bool {
	handleCraftingData(b, pk.(*packet.CraftingData))
	return true
}

func handleUpdateAttributesPacket(b *bot.Bot, pk packet.Packet) bool {
	handleUpdateAttributes(b, pk.(*packet.UpdateAttributes))
	return true
}

func handleRespawn(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.Respawn)
	b.Mu.Lock()
	b.Pos = p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
	b.Logger.Debug("bot respawned/teleported by server",
		slog.Float64("x", float64(b.Pos.X())),
		slog.Float64("y", float64(b.Pos.Y())),
		slog.Float64("z", float64(b.Pos.Z())),
	)
	b.Mu.Unlock()
	return true
}

func handleAddActor(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.AddActor)
	b.Mu.Lock()
	b.Actors[p.EntityRuntimeID] = &entity.Info{
		ID:       p.EntityRuntimeID,
		Type:     p.EntityType,
		Name:     p.EntityType,
		Position: p.Position,
		Health:   20,
	}
	b.UniqueIDToRuntimeID[p.EntityUniqueID] = p.EntityRuntimeID
	b.Mu.Unlock()
	b.Logger.Debug("tracked actor spawned", slog.String("type", p.EntityType), slog.Uint64("runtime_id", p.EntityRuntimeID))
	return true
}

func handleAddItemActor(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.AddItemActor)
	b.Mu.Lock()
	itemName := "minecraft:item"
	if nameVal, ok := b.ItemNames[p.Item.Stack.NetworkID]; ok {
		itemName = nameVal
	}
	b.Actors[p.EntityRuntimeID] = &entity.Info{
		ID:       p.EntityRuntimeID,
		Type:     "minecraft:item",
		Name:     itemName,
		Position: p.Position,
		Health:   1,
	}
	b.UniqueIDToRuntimeID[p.EntityUniqueID] = p.EntityRuntimeID
	b.Mu.Unlock()
	b.Logger.Debug("tracked item drop spawned", slog.String("name", itemName), slog.Uint64("runtime_id", p.EntityRuntimeID))
	return true
}

func handleMoveActorDelta(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.MoveActorDelta)
	b.Mu.Lock()
	if act, ok := b.Actors[p.EntityRuntimeID]; ok {
		act.Position = mergeMoveActorDeltaPosition(act.Position, p.Position, p.Flags)
	}
	b.Mu.Unlock()
	return true
}

// mergeMoveActorDeltaPosition applies only the axes flagged as present in a
// MoveActorDelta packet. As of Bedrock 1.16.100 the packet zeroes any axis it
// does not carry, so blindly assigning the whole vector teleports entities to a
// zeroed coordinate and breaks position-dependent logic such as combat target
// selection and grounded visibility.
func mergeMoveActorDeltaPosition(current, incoming mgl32.Vec3, flags uint16) mgl32.Vec3 {
	merged := current
	if flags&packet.MoveActorDeltaFlagHasX != 0 {
		merged[0] = incoming[0]
	}
	if flags&packet.MoveActorDeltaFlagHasY != 0 {
		merged[1] = incoming[1]
	}
	if flags&packet.MoveActorDeltaFlagHasZ != 0 {
		merged[2] = incoming[2]
	}
	return merged
}

// mergeActorDeltaPosition applies only axes flagged present in the delta
// packet. Unflagged axes decode as zero and must not reset the tracked value.
func mergeActorDeltaPosition(act *entity.Info, p *packet.MoveActorDelta) {
	pos := act.Position
	if p.Flags&packet.MoveActorDeltaFlagHasX != 0 {
		pos[0] = p.Position.X()
	}
	if p.Flags&packet.MoveActorDeltaFlagHasY != 0 {
		pos[1] = p.Position.Y()
	}
	if p.Flags&packet.MoveActorDeltaFlagHasZ != 0 {
		pos[2] = p.Position.Z()
	}
	act.Position = pos
}

func handleMoveActorAbsolute(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.MoveActorAbsolute)
	b.Logger.Debug("MoveActorAbsolute packet received",
		slog.Uint64("runtime_id", p.EntityRuntimeID),
		slog.Float64("x", float64(p.Position.X())),
		slog.Float64("y", float64(p.Position.Y())),
		slog.Float64("z", float64(p.Position.Z())),
	)
	b.Mu.Lock()
	if p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID {
		b.Pos = p.Position
		b.Logger.Info("Bot moved by MoveActorAbsolute", "pos", p.Position)
	} else if act, ok := b.Actors[p.EntityRuntimeID]; ok {
		act.Position = p.Position
	}
	b.Mu.Unlock()
	return true
}

func handleSetActorMotion(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.SetActorMotion)
	b.Mu.Lock()
	if p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID {
		b.VelY = p.Velocity.Y()
		b.IsGrounded = false
		b.Logger.Info("Bot motion set by SetActorMotion", "velocity", p.Velocity)
	}
	b.Mu.Unlock()
	return true
}

func handleTakeItemActor(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.TakeItemActor)
	b.Mu.Lock()
	delete(b.Actors, p.ItemEntityRuntimeID)
	b.Mu.Unlock()
	return true
}

func handleRemoveActor(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.RemoveActor)
	b.Mu.Lock()
	if runtimeID, ok := b.UniqueIDToRuntimeID[p.EntityUniqueID]; ok {
		delete(b.Actors, runtimeID)
		delete(b.UniqueIDToRuntimeID, p.EntityUniqueID)
	}
	id := safecast.To[uint64](p.EntityUniqueID)
	if username, ok := b.PlayerUsernames[id]; ok {
		delete(b.PlayerEntityIDs, username)
		delete(b.PlayerUsernames, id)
		delete(b.PlayerPositions, id)
		delete(b.PlayerYaws, id)
		delete(b.PlayerPitches, id)
		b.Logger.Debug("tracked player left view distance", slog.String("username", username))
	}
	b.Mu.Unlock()
	return true
}

func handleInventoryContent(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.InventoryContent)
	isPlayerInv := isPlayerInventoryContent(p)
	containerID := p.Container.ContainerID
	b.Logger.Debug("received InventoryContent",
		slog.Uint64("window_id", uint64(p.WindowID)),
		slog.Uint64("container_id", uint64(containerID)),
		slog.Bool("is_player_inv", isPlayerInv),
		slog.Int("items_count", len(p.Content)),
	)
	if isPlayerInv {
		syncHeldEquipmentIfUpdated(b, applyInventoryContent(b, p))
	}
	return true
}

func handleInventorySlot(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.InventorySlot)
	isPlayerInv := isPlayerInventorySlot(p)
	containerID := byte(0)
	if container, ok := p.Container.Value(); ok {
		containerID = container.ContainerID
	}
	b.Logger.Debug("received InventorySlot",
		slog.Uint64("window_id", uint64(p.WindowID)),
		slog.Uint64("container_id", uint64(containerID)),
		slog.Bool("is_player_inv", isPlayerInv),
		slog.Uint64("slot", uint64(p.Slot)),
		slog.Int("count", int(p.NewItem.Stack.Count)),
	)
	if isPlayerInv {
		syncHeldEquipmentIfUpdated(b, applyInventorySlot(b, p))
	}
	return true
}

func handleItemStackResponse(b *bot.Bot, pk packet.Packet) bool {
	syncHeldEquipmentIfUpdated(b, applyItemStackResponse(b, pk.(*packet.ItemStackResponse)))
	return true
}

func handleInventoryTransaction(b *bot.Bot, pk packet.Packet) bool {
	syncHeldEquipmentIfUpdated(b, applyInventoryTransaction(b, pk.(*packet.InventoryTransaction)))
	return true
}

func syncHeldEquipmentIfUpdated(b *bot.Bot, updated bool) {
	if !updated {
		return
	}
	if err := b.SyncHeldEquipment(); err != nil {
		b.Logger.Warn("failed to sync held equipment", slog.Any("error", err))
	}
}

func handleMobEquipment(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.MobEquipment)
	if p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID {
		b.Mu.Lock()
		b.HeldSlot = uint32(p.HotBarSlot)
		b.Mu.Unlock()
	}
	return true
}

func handleNetworkStackLatency(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.NetworkStackLatency)
	b.Logger.Debug("received ping from server", slog.Int64("timestamp", p.Timestamp), slog.Bool("needs_response", p.NeedsResponse))
	if p.NeedsResponse {
		_ = b.Conn.WritePacket(&packet.NetworkStackLatency{
			Timestamp:     p.Timestamp,
			NeedsResponse: false,
		})
		b.Logger.Debug("sent pong back to server", slog.Int64("timestamp", p.Timestamp))
	}
	return true
}

func handlePacketViolationWarning(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.PacketViolationWarning)
	b.Logger.Error("SERVER SENT PACKET VIOLATION WARNING - BOT WILL BE DISCONNECTED",
		slog.Int("type", int(p.Type)),
		slog.Int("severity", int(p.Severity)),
		slog.Int("packet_id", int(p.PacketID)),
		slog.String("context", p.ViolationContext),
	)
	return true
}
