// Package player handles player, entity, and inventory-related packets.
package player

import (
	"fmt"
	"log/slog"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/dimension"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/inventory/trading"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func handleAddPlayer(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.AddPlayer)
	b.Mu.Lock()
	// Several server NPCs can share one (or an empty) username, so the
	// username index is last-writer-wins; PlayerUsernames/PlayerPositions are
	// keyed by runtime ID and keep every one of them.
	b.PlayerEntityIDs[p.Username] = p.EntityRuntimeID
	b.PlayerUsernames[p.EntityRuntimeID] = p.Username
	b.PlayerPositions[p.EntityRuntimeID] = trackedPlayerFeetPosition(p.Position)
	b.PlayerYaws[p.EntityRuntimeID] = p.Yaw
	b.PlayerPitches[p.EntityRuntimeID] = p.Pitch
	b.PlayerUUIDs[p.UUID] = p.Username
	b.PlayerIDUUIDs[p.EntityRuntimeID] = p.UUID
	delete(b.PlayerHidden, p.EntityRuntimeID)
	b.Mu.Unlock()
	b.Logger.Info("tracked player spawned",
		slog.String("username", p.Username),
		slog.String("uuid", p.UUID.String()),
		slog.Uint64("runtime_id", p.EntityRuntimeID),
		slog.Any("pos", p.Position))
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

	// SearchingForSpawn is the packet a *death* starts with, and its Position
	// is where the body died — or the void. Adopting it would put the bot
	// straight back into the hole it had just died in, so only the later
	// states, which carry a real spawn point, move the body.
	adoptPos := p.State != packet.RespawnStateSearchingForSpawn

	b.Mu.Lock()
	if adoptPos {
		b.Pos = p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
		b.TeleportAckPending = true
		b.TeleportAckPos = b.Pos
	}
	feet := b.Pos
	b.Logger.Info("self respawn position",
		slog.Any("raw", p.Position),
		slog.Any("feet", feet),
		slog.Uint64("state", uint64(p.State)),
		slog.Bool("adopted_position", adoptPos),
	)
	b.Logger.Debug("bot respawned/teleported by server",
		slog.Float64("x", float64(feet.X())),
		slog.Float64("y", float64(feet.Y())),
		slog.Float64("z", float64(feet.Z())),
		slog.Uint64("state", uint64(p.State)),
	)
	b.Mu.Unlock()

	// Respawning is a handshake the client drives, and only the last step was
	// ever implemented — the one a login produces. A death begins with the
	// other packet, which arrived, was logged, and was then ignored. The bot
	// sat on the death screen forever with nothing to click.
	switch p.State {
	case packet.RespawnStateSearchingForSpawn:
		// Step 1: the player died. Say the client is ready to be told where to
		// spawn, and record where the body is so item recovery has somewhere to
		// walk back to.
		if b.SurvivalMgr != nil {
			b.SurvivalMgr.MarkDeath(feet)
			b.SurvivalMgr.NoteRespawnInFlight()
			// The health stream often stops on the death screen, so the retry
			// loop must be armed from this packet — the authoritative death
			// signal — rather than waiting for a zero reading that may never
			// come.
			b.SurvivalMgr.NoteDied(feet)
		}
		_ = b.WritePacket(&packet.Respawn{
			State:           packet.RespawnStateReadyToSpawn,
			Position:        p.Position,
			EntityRuntimeID: b.GetEntityRuntimeID(),
		})
	case packet.RespawnStateReadyToSpawn:
		// Step 3: the host has sent the spawn point. This is also the packet a
		// login produces, so it keeps behaving exactly as it already did.
		if b.SurvivalMgr != nil {
			b.SurvivalMgr.NoteRespawned()
		}
		_ = b.WritePacket(&packet.Respawn{
			State:           packet.RespawnStateClientReadyToSpawn,
			Position:        p.Position,
			EntityRuntimeID: b.GetEntityRuntimeID(),
		})
	}
	return true
}

func handleTransfer(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.Transfer)
	addr := fmt.Sprintf("%s:%d", p.Address, p.Port)
	b.Logger.Info("received transfer packet from server",
		slog.String("address", p.Address),
		slog.Uint64("port", uint64(p.Port)),
	)
	if err := b.RequestJoin(addr); err != nil {
		b.Logger.Error("failed to handle transfer packet", "address", addr, "error", err)
	}
	return true
}

func handleChangeDimension(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.ChangeDimension)
	b.Mu.Lock()
	b.ChunkDimension = p.Dimension
	b.Dimension = dimension.FromServerID(p.Dimension)
	// Geyser fakes a dimension switch with a bogus position (y=32767); the
	// real one follows in a MovePlayer. Adopting the bogus one only sends the
	// body falling from the sky.
	if p.Position.Y() <= 320 && p.Position.Y() >= -64 {
		b.Pos = p.Position
		b.TeleportAckPending = true
		b.TeleportAckPos = p.Position
	}
	b.VelY = 0
	b.MovementState = "idle"
	b.CurrentPath = nil
	b.Actors = make(map[uint64]*entity.Info)
	b.Mu.Unlock()

	b.Logger.Info("bot dimension changed by server",
		slog.Int64("dimension", int64(p.Dimension)),
		slog.Float64("x", float64(p.Position.X())),
		slog.Float64("y", float64(p.Position.Y())),
		slog.Float64("z", float64(p.Position.Z())),
	)

	_ = b.WritePacket(&packet.PlayerAction{
		ActionType:      protocol.PlayerActionDimensionChangeDone,
		EntityRuntimeID: b.GetEntityRuntimeID(),
	})
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
	name := p.EntityType
	if n, ok := p.EntityMetadata[uint32(protocol.EntityDataKeyName)].(string); ok && n != "" {
		name = n
	}
	b.Mu.Lock()
	b.Actors[p.EntityRuntimeID] = &entity.Info{
		ID:       p.EntityRuntimeID,
		Type:     p.EntityType,
		Name:     name,
		Position: p.Position,
		Health:   20,
	}
	b.UniqueIDToRuntimeID[p.EntityUniqueID] = p.EntityRuntimeID
	b.Mu.Unlock()
	// The metadata arrives with the spawn and is the only place a wolf's collar
	// or a crop-relevant flag is ever stated. Discarding it is why taming could
	// not be confirmed: the flag was in the packet and on the floor.
	b.RecordEntityMeta(p.EntityRuntimeID, p.EntityMetadata)
	b.Logger.Debug("tracked actor spawned", slog.String("type", p.EntityType), slog.String("name", name), slog.Uint64("runtime_id", p.EntityRuntimeID))
	return true
}

// handleUpdateTrade records a villager's trade offers.
//
// The offers are a serialised NBT compound the server sends, and nothing else
// carries them. Without this handler the trading manager had no source for the
// price list, the XP cost or the required level, and refused every trade as
// having no offers observed.
func handleUpdateTrade(b *bot.Bot, pk packet.Packet) bool {
	p, ok := pk.(*packet.UpdateTrade)
	if !ok {
		return false
	}

	// The packet names the villager by its unique ID; the bot tracks entities by
	// runtime ID. The offers can arrive before the spawn does, so an unresolved
	// mapping is a real case — drop it rather than file it against the wrong mob.
	b.Mu.Lock()
	runtimeID, known := b.UniqueIDToRuntimeID[p.VillagerUniqueID]
	b.Mu.Unlock()
	if !known {
		b.Logger.Debug("trade offers for an untracked villager",
			slog.Int64("villager_unique_id", p.VillagerUniqueID))
		return true
	}

	window, err := trading.TradeWindowFromPacket(p)
	if err != nil {
		b.Logger.Warn("could not read trade offers", slog.String("error", err.Error()))
		return true
	}
	b.RecordTradeWindow(runtimeID, window)
	b.Logger.Debug("recorded trade offers",
		slog.Uint64("villager", runtimeID),
		slog.Int("offers", int(window.OfferCount)),
		slog.Int("tier", int(window.TradeTier)))
	return true
}

// handlePlayerEnchantOptions records what an enchanting table is offering.
//
// Nothing else on the wire carries this. The table does not advertise its options
// in a container window, a recipe packet, or a transaction: this packet is the
// only place the server's three enchantment buttons exist as data. Without a
// handler for it the bot had no way to learn that a table had anything to sell,
// which is why the station manager's chooseOption could only ever time out.
//
// The empty case is the common one and is recorded rather than skipped. The
// vanilla server sends an empty packet the moment the table opens, with air in
// the input slot, and only a real list once an enchantable item is in. Recording
// it clears whatever the previous table offered, so a bot cannot select from a
// stale list after walking to a different table.
func handlePlayerEnchantOptions(b *bot.Bot, pk packet.Packet) bool {
	p, ok := pk.(*packet.PlayerEnchantOptions)
	if !ok {
		return false
	}

	bot.RecordEnchantOptions(b, p.Options)
	b.Logger.Debug("recorded enchantment options",
		slog.Int("options", len(p.Options)))
	return true
}

// handleSetActorData refreshes an entity's metadata after it changes.
//
// A wolf that is collared long after it spawned — the whole point of taming —
// never respawns, so reading the metadata only on AddActor would miss the one
// moment that matters.
func handleSetActorData(b *bot.Bot, pk packet.Packet) bool {
	p, ok := pk.(*packet.SetActorData)
	if !ok {
		return false
	}
	b.RecordEntityMeta(p.EntityRuntimeID, p.EntityMetadata)
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
		act.Position = MergeMoveActorDeltaPosition(act.Position, p)
	}
	b.Mu.Unlock()
	return true
}

// MergeMoveActorDeltaPosition applies only the axes carried by a MoveActorDelta
// packet. Each axis is a separate optional value, so an axis the server omits
// must keep its previous value rather than resetting to zero. Blindly
// assigning the whole vector would teleport entities to the origin and break
// position-dependent logic such as combat target selection.
func MergeMoveActorDeltaPosition(current mgl32.Vec3, p *packet.MoveActorDelta) mgl32.Vec3 {
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
		// The horizontal half is what a PvP hit actually is. A body that keeps
		// its X/Z through a SetActorMotion never slides when hit — from the
		// outside that is indistinguishable from an anti-knockback cheat, and
		// the user was right to call it out ("kok ga kena knockback"). The
		// movement tick folds this into the claimed position and bleeds it off
		// Bedrock-style, exactly like the vanilla client does.
		b.KnockbackX = p.Velocity.X()
		b.KnockbackZ = p.Velocity.Z()
		// The carried-motion decay runs out in ~8 ticks, but the arc the hit
		// bought — rise, apex, fall — lasts longer. Corrections are skipped for
		// this whole window; past it, server authority resumes whatever the
		// decay left behind.
		b.KnockbackUntil = time.Now().Add(1200 * time.Millisecond)
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
	// Resolve the runtime ID before deleting the mapping: Player maps below are
	// keyed by runtime ID, and reading the table after this delete was how the
	// lookup silently fell back to the wrong identifier.
	runtimeID, mapped := b.UniqueIDToRuntimeID[p.EntityUniqueID]
	if mapped {
		delete(b.Actors, runtimeID)
		delete(b.UniqueIDToRuntimeID, p.EntityUniqueID)
		// A runtime ID can be reused by a later spawn, so the collar has to go
		// with the wolf. Leaving it would let the next occupant of the ID
		// inherit the previous one's metadata.
		defer b.ForgetEntityMeta(runtimeID)
	}
	// When the server never sent an AddActor for this entity the table has no
	// entry, and the unique ID is the only identifier available — keeping it as
	// a fallback avoids leaving a departed player tracked forever.
	id := runtimeID
	if !mapped {
		id = safecast.To[uint64](p.EntityUniqueID)
	}
	if username, ok := b.PlayerUsernames[id]; ok {
		if b.PlayerEntityIDs[username] == id {
			delete(b.PlayerEntityIDs, username)
		}
		delete(b.PlayerUsernames, id)
		delete(b.PlayerIDUUIDs, id)
		delete(b.PlayerHidden, id)
		delete(b.PlayerPositions, id)
		delete(b.PlayerYaws, id)
		delete(b.PlayerPitches, id)
		b.Logger.Info("tracked player left view distance", slog.String("username", username), slog.Uint64("runtime_id", id))
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
		syncHeldEquipmentIfUpdated(b, ApplyInventoryContent(b, p))
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
		syncHeldEquipmentIfUpdated(b, ApplyInventorySlot(b, p))
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
	syncHeldEquipmentIfUpdated(b, ApplyItemStackResponse(b, pk.(*packet.ItemStackResponse)))
	return true
}

func handleInventoryTransaction(b *bot.Bot, pk packet.Packet) bool {
	syncHeldEquipmentIfUpdated(b, ApplyInventoryTransaction(b, pk.(*packet.InventoryTransaction)))
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

func handleSetActorLink(b *bot.Bot, pk packet.Packet) bool {
	p := pk.(*packet.SetActorLink)

	b.Mu.Lock()
	defer b.Mu.Unlock()

	if p.EntityLink.RiderEntityUniqueID == b.Conn.GameData().EntityUniqueID {
		if p.EntityLink.Type == protocol.EntityLinkRemove {
			b.VehicleID = 0
			b.Logger.Info("dismounted vehicle", "vehicle_id", p.EntityLink.RiddenEntityUniqueID)
		} else {
			b.VehicleID = p.EntityLink.RiddenEntityUniqueID
			b.Logger.Info("mounted vehicle", "vehicle_id", p.EntityLink.RiddenEntityUniqueID, "type", p.EntityLink.Type)
		}
	}
	return true
}
