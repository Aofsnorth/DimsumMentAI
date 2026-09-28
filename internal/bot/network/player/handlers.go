// Package player handles player, entity, and inventory-related packets.
package player

import (
	"fmt"
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

// handleSetTime keeps the world clock current.
//
// It was listed as an "expected unhandled" packet, which meant SurvivalMgr's
// worldTime never moved off zero: every query reported dawn, forever. The
// autonomy brain needs a real clock — whether it is night decides whether
// wandering somewhere new is a sensible thing to do — so a clock frozen at dawn
// is not a cosmetic gap.
func handleSetTime(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.SetTime)
	if b.SurvivalMgr != nil {
		b.SurvivalMgr.SetWorldTime(int64(p.Time))
	}
	return true
}

// handleLevelEvent tracks weather changes from the server.
//
// LevelEvent carries hundreds of event types (sounds, particles, block edits).
// Only the four weather transitions are consumed here; everything else is
// acknowledged and ignored so the packet does not fall through as unhandled.
func handleLevelEvent(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.LevelEvent)
	if b.SurvivalMgr == nil {
		return true
	}
	switch p.EventType {
	case packet.LevelEventStartRaining:
		b.SurvivalMgr.SetWeather(true, b.SurvivalMgr.IsThundering())
		b.Logger.Info("weather: rain started")
	case packet.LevelEventStartThunderstorm:
		b.SurvivalMgr.SetWeather(true, true)
		b.Logger.Info("weather: thunderstorm started")
	case packet.LevelEventStopRaining:
		b.SurvivalMgr.SetWeather(false, b.SurvivalMgr.IsThundering())
		b.Logger.Info("weather: rain stopped")
	case packet.LevelEventStopThunderstorm:
		b.SurvivalMgr.SetWeather(b.SurvivalMgr.IsRaining(), false)
		b.Logger.Info("weather: thunderstorm stopped")
	}
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
		act.Position = mergeMoveActorDeltaPosition(act.Position, p)
	}
	b.Mu.Unlock()
	return true
}

// mergeMoveActorDeltaPosition applies only the axes carried by a MoveActorDelta
// packet. Each axis is a separate optional value, so an axis the server omits
// must keep its previous value rather than resetting to zero. Blindly
// assigning the whole vector would teleport entities to the origin and break
// position-dependent logic such as combat target selection.
func mergeMoveActorDeltaPosition(current mgl32.Vec3, p *packet.MoveActorDelta) mgl32.Vec3 {
	merged := current
	if x, ok := p.PositionX.Value(); ok {
		merged[0] = x
	}
	if y, ok := p.PositionY.Value(); ok {
		merged[1] = y
	}
	if z, ok := p.PositionZ.Value(); ok {
		merged[2] = z
	}
	return merged
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
		return true
	}
	// A container the bot opened: feed the chest session so the action layer
	// can read real contents instead of guessing.
	if b.ContainerMatchesWindow(p.WindowID) {
		b.ContainerContent(p.WindowID, p.Content)
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
		return true
	}
	if b.ContainerMatchesWindow(p.WindowID) {
		b.ContainerSlot(p.WindowID, p.Slot, p.NewItem)
	}
	return true
}

// handleContainerOpen records the window the server assigned to the container
// the bot just clicked. The chest session waits on this before reading items.
func handleContainerOpen(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.ContainerOpen)
	b.Logger.Info("container opened",
		slog.Uint64("window_id", uint64(p.WindowID)),
		slog.Uint64("container_type", uint64(p.ContainerType)),
		slog.String("pos", fmt.Sprintf("%d,%d,%d", p.ContainerPosition.X(), p.ContainerPosition.Y(), p.ContainerPosition.Z())),
	)
	b.ContainerOpened(p.WindowID, p.ContainerType, p.ContainerPosition)
	return true
}

// handleContainerClose clears the chest session when the server closes the
// window (or echoes the bot's own close).
func handleContainerClose(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.ContainerClose)
	b.MarkContainerClosed(p.WindowID)
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
	// Geyser front-ends die on this echo: captured live on
	// play.nexusone.fun, the session went permanently silent milliseconds
	// after the bot echoed a server-pushed Geyser custom item (GeyserHash NBT)
	// back in a MobEquipment, while the same bot on a Geyser server whose
	// inventory stays empty (so the echo never fires) held its connection.
	// A real Bedrock client only sends MobEquipment when the local player
	// switches slots, so skipping the echo here matches vanilla behaviour.
	b.Mu.Lock()
	noEcho := b.GeyserNoHeldItemEcho
	b.Mu.Unlock()
	if noEcho {
		b.Logger.Debug("skipping held equipment echo (Geyser profile)")
		return
	}
	if err := b.SyncHeldEquipment(); err != nil {
		b.Logger.Warn("failed to sync held equipment", slog.Any("error", err))
	}
}

// handlePlayStatus answers the server's spawn notification the way a real
// client does. A vanilla Bedrock client replies to PlayStatus(3) with
// SetLocalPlayerAsInitialised, which marks the client as fully initialised;
// Geyser front-ends use that reply to mark the upstream session initialised
// (without it, cumulus forms such as SimpleLogin's login window are never
// delivered) and to forward ServerboundPlayerLoadedPacket to the Java server.
// A BDS does not punish its absence, but it also expects it, so sending it is
// plain client behaviour rather than a Geyser workaround.
func handlePlayStatus(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.PlayStatus)
	if p.Status != packet.PlayStatusPlayerSpawn {
		return true
	}
	if b.Conn == nil {
		return true
	}
	runtimeID := b.Conn.GameData().EntityRuntimeID
	if err := b.Conn.WritePacket(&packet.SetLocalPlayerAsInitialised{EntityRuntimeID: runtimeID}); err != nil {
		b.Logger.Warn("failed to send SetLocalPlayerAsInitialised", slog.Any("error", err))
		return true
	}
	b.Logger.Info("sent SetLocalPlayerAsInitialised after PlayStatus PLAYER_SPAWN",
		slog.Uint64("runtime_id", runtimeID))
	return true
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
