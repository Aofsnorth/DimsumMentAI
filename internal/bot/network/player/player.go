package player

import (
	"log/slog"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func HandlePlayerPacket(b *bot.Bot, pk packet.Packet) bool {
	switch p := pk.(type) {
	case *packet.AddPlayer:
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

	case *packet.MovePlayer:
		handleMovePlayer(b, p)
		return true

	case *packet.CorrectPlayerMovePrediction:
		handleCorrectPrediction(b, p)
		return true

	case *packet.Respawn:
		b.Mu.Lock()
		b.Pos = p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
		b.Logger.Debug("bot respawned/teleported by server",
			slog.Float64("x", float64(b.Pos.X())),
			slog.Float64("y", float64(b.Pos.Y())),
			slog.Float64("z", float64(b.Pos.Z())),
		)
		b.Mu.Unlock()
		return true

	case *packet.PlayerList:
		handlePlayerList(b, p)
		return true

	case *packet.AddActor:
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

	case *packet.AddItemActor:
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

	case *packet.CraftingData:
		handleCraftingData(b, p)
		return true

	case *packet.MoveActorDelta:
		b.Mu.Lock()
		if act, ok := b.Actors[p.EntityRuntimeID]; ok {
			act.Position = p.Position
		}
		b.Mu.Unlock()
		return true

	case *packet.RemoveActor:
		b.Mu.Lock()
		if runtimeID, ok := b.UniqueIDToRuntimeID[p.EntityUniqueID]; ok {
			delete(b.Actors, runtimeID)
			delete(b.UniqueIDToRuntimeID, p.EntityUniqueID)
		}
		id := uint64(p.EntityUniqueID)
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

	case *packet.InventoryContent:
		isPlayerInv := isPlayerInventoryContent(p)
		var containerID byte
		containerID = p.Container.ContainerID
		b.Logger.Info("received InventoryContent",
			slog.Uint64("window_id", uint64(p.WindowID)),
			slog.Uint64("container_id", uint64(containerID)),
			slog.Bool("is_player_inv", isPlayerInv),
			slog.Int("items_count", len(p.Content)),
		)
		if isPlayerInv {
			b.Mu.Lock()
			b.InventoryMap = make(map[uint32]protocol.ItemStack)
			for slot, item := range p.Content {
				if item.Stack.Count > 0 && item.Stack.NetworkID != 0 {
					b.InventoryMap[uint32(slot)] = item.Stack
				}
			}
			b.Mu.Unlock()
		}
		return true

	case *packet.InventorySlot:
		isPlayerInv := isPlayerInventorySlot(p)
		var containerID byte
		if container, ok := p.Container.Value(); ok {
			containerID = container.ContainerID
		}
		b.Logger.Info("received InventorySlot",
			slog.Uint64("window_id", uint64(p.WindowID)),
			slog.Uint64("container_id", uint64(containerID)),
			slog.Bool("is_player_inv", isPlayerInv),
			slog.Uint64("slot", uint64(p.Slot)),
			slog.Int("count", int(p.NewItem.Stack.Count)),
		)
		if isPlayerInv {
			b.Mu.Lock()
			if p.NewItem.Stack.Count > 0 && p.NewItem.Stack.NetworkID != 0 {
				b.InventoryMap[p.Slot] = p.NewItem.Stack
			} else {
				delete(b.InventoryMap, p.Slot)
			}
			b.Mu.Unlock()
		}
		return true

	case *packet.MobEquipment:
		if p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID {
			b.Mu.Lock()
			b.HeldSlot = uint32(p.HotBarSlot)
			b.Mu.Unlock()
		}
		return true

	case *packet.UpdateAttributes:
		handleUpdateAttributes(b, p)
		return true
	}
	return false
}
