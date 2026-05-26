package network

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func SendLoadingScreenDone(b *bot.Bot) {
	// Type 1: loading screen started (real client sends this first)
	_ = b.Conn.WritePacket(&packet.ServerBoundLoadingScreen{
		Type: packet.LoadingScreenTypeStart,
	})
	// Type 2: loading screen finished
	_ = b.Conn.WritePacket(&packet.ServerBoundLoadingScreen{
		Type: packet.LoadingScreenTypeEnd,
	})
	b.Logger.Info("sent loading screen packets")
}

func ChunkRequesterLoop(ctx context.Context, b *bot.Bot) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// Track requested chunks to avoid spamming the server
	requested := make(map[string]time.Time)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.Mu.Lock()
			pos := b.Pos
			mState := b.MovementState
			tPlayer := b.TargetPlayerName
			b.Mu.Unlock()

			chunkX := int32(math.Floor(float64(pos.X()) / 16.0))
			chunkZ := int32(math.Floor(float64(pos.Z()) / 16.0))
			dim := int32(0) // overworld

			type chunkCoord struct {
				x, z int32
			}
			var targets []chunkCoord

			// 1. Chunks in a 5x5 grid around the bot
			for dx := int32(-2); dx <= 2; dx++ {
				for dz := int32(-2); dz <= 2; dz++ {
					targets = append(targets, chunkCoord{chunkX + dx, chunkZ + dz})
				}
			}

			// 2. Chunks in a 3x3 grid around the target player if following
			if mState == "follow" && tPlayer != "" {
				if _, pPos, ok := b.FindPlayer(tPlayer); ok {
					pChunkX := int32(math.Floor(float64(pPos.X()) / 16.0))
					pChunkZ := int32(math.Floor(float64(pPos.Z()) / 16.0))
					for dx := int32(-1); dx <= 1; dx++ {
						for dz := int32(-1); dz <= 1; dz++ {
							targets = append(targets, chunkCoord{pChunkX + dx, pChunkZ + dz})
						}
					}
				}
			}

			// Filter out duplicates
			uniqueTargets := make(map[chunkCoord]bool)
			for _, tc := range targets {
				uniqueTargets[tc] = true
			}

			for tc := range uniqueTargets {
				k := fmt.Sprintf("%d,%d", tc.x, tc.z)

				// Only request if not requested recently (e.g., within last 5 seconds)
				if lastReq, ok := requested[k]; ok && time.Since(lastReq) < 5*time.Second {
					continue
				}

				requested[k] = time.Now()

				var offsets []protocol.SubChunkOffset
				// Request all subchunks from Y=-64 to Y=319 (indices -4 to 24)
				for y := int32(-4); y <= 25; y++ {
					offsets = append(offsets, protocol.SubChunkOffset{0, int8(y), 0})
				}

				_ = b.Conn.WritePacket(&packet.SubChunkRequest{
					Dimension: dim,
					Position: protocol.SubChunkPos{
						tc.x,
						0, // Center Y is relative
						tc.z,
					},
					Offsets: offsets,
				})
			}
		}
	}
}

func SendPlayerSkin(b *bot.Bot) {
	if len(b.ProtoSkin.SkinData) == 0 {
		return
	}
	b.ProtoSkin.OverrideAppearance = true
	b.ProtoSkin.PrimaryUser = true
	b.ProtoSkin.Trusted = true

	b.Mu.Lock()
	targetUUID := b.PlayerUUID
	b.Mu.Unlock()

	_ = b.Conn.WritePacket(&packet.PlayerSkin{
		UUID: targetUUID,
		Skin: b.ProtoSkin,
	})
	b.Logger.Info("sent PlayerSkin packet",
		slog.String("uuid", targetUUID.String()),
		slog.Int("skin_data_len", len(b.ProtoSkin.SkinData)),
		slog.String("arm_size", b.ProtoSkin.ArmSize),
	)
}

func PacketLoop(ctx context.Context, b *bot.Bot) error {
	for {
		select {
		case <-ctx.Done():
			b.Logger.Info("shutting down", slog.String("reason", ctx.Err().Error()))
			return nil
		default:
		}

		pk, err := b.Conn.ReadPacket()
		if err != nil {
			var disc minecraft.DisconnectError
			if errors.As(err, &disc) {
				b.Logger.Info("disconnected by server",
					slog.String("reason", disc.Error()),
				)
				b.Bus.Publish(event.SpawnEvent{}) // Publish empty event or trigger disconnect
				return nil
			}
			return fmt.Errorf("read packet: %w", err)
		}

		switch p := pk.(type) {
		case *packet.LevelChunk:
			b.WorldCache.HandleLevelChunk(p)

			// Request sub-chunks if the server prompts us to
			if p.SubChunkCount == protocol.SubChunkRequestModeLimitless || p.SubChunkCount == protocol.SubChunkRequestModeLimited {
				highestY := int32(25) // Default to standard max height subchunk (Y=319 is subchunk index 24, so up to 25)
				if p.SubChunkCount == protocol.SubChunkRequestModeLimited {
					highestY = int32(p.HighestSubChunk)
				}

				var offsets []protocol.SubChunkOffset
				// Request subchunks from index -4 (Y=-64) up to highestY
				for y := int32(-4); y <= highestY; y++ {
					offsets = append(offsets, protocol.SubChunkOffset{0, int8(y), 0})
				}

				_ = b.Conn.WritePacket(&packet.SubChunkRequest{
					Dimension: p.Dimension,
					Position: protocol.SubChunkPos{
						p.Position[0],
						0, // Center subchunk Y, offsets handle the rest
						p.Position[1],
					},
					Offsets: offsets,
				})
			}

		case *packet.SubChunk:
			b.WorldCache.HandleSubChunk(p)

		case *packet.AddPlayer:
			b.Mu.Lock()
			b.PlayerEntityIDs[p.Username] = p.EntityRuntimeID
			b.PlayerUsernames[p.EntityRuntimeID] = p.Username
			b.PlayerPositions[p.EntityRuntimeID] = p.Position
			b.PlayerUUIDs[p.UUID] = p.Username
			b.Mu.Unlock()
			b.Logger.Info("tracked player spawned", slog.String("username", p.Username), slog.Uint64("runtime_id", p.EntityRuntimeID))

		case *packet.MovePlayer:
			b.Mu.Lock()
			isSelf := p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID
			b.Logger.Debug("MovePlayer packet received",
				slog.Uint64("runtime_id", p.EntityRuntimeID),
				slog.Uint64("self_runtime_id", b.Conn.GameData().EntityRuntimeID),
				slog.Bool("is_self", isSelf),
				slog.Float64("x", float64(p.Position.X())),
				slog.Float64("y", float64(p.Position.Y())),
				slog.Float64("z", float64(p.Position.Z())),
			)
			if isSelf {
				newPos := p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
				if newPos.Y() <= 320 && newPos.Y() >= -64 {
					fell := b.Pos.Y()-newPos.Y() > 0.4
					if fell {
						feetX := int32(math.Floor(float64(b.Pos.X())))
						feetY := int32(math.Floor(float64(b.Pos.Y())))
						feetZ := int32(math.Floor(float64(b.Pos.Z())))
						b.WorldModel.SetSolid(feetX, feetY-1, feetZ, false)
						if b.MovementState != "idle" {
							b.CurrentPath = nil
						}
					}

					b.Pos = newPos
					b.VelY = 0.0
				}
			} else {
				b.PlayerPositions[p.EntityRuntimeID] = p.Position
			}
			b.Mu.Unlock()

		case *packet.CorrectPlayerMovePrediction:
			b.Mu.Lock()
			correctedPos := p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
			if correctedPos.Y() <= 320 && correctedPos.Y() >= -64 {
				posDiff := float64(b.Pos.X()-correctedPos.X())*float64(b.Pos.X()-correctedPos.X()) +
					float64(b.Pos.Y()-correctedPos.Y())*float64(b.Pos.Y()-correctedPos.Y()) +
					float64(b.Pos.Z()-correctedPos.Z())*float64(b.Pos.Z()-correctedPos.Z())

				// Only clear path on major corrections (> 2 blocks distance)
				if posDiff > 4.0 {
					if b.Pos.Y()-correctedPos.Y() > 0.4 {
						feetX := int32(math.Floor(float64(b.Pos.X())))
						feetY := int32(math.Floor(float64(b.Pos.Y())))
						feetZ := int32(math.Floor(float64(b.Pos.Z())))
						b.WorldModel.SetSolid(feetX, feetY-1, feetZ, false)
					}
					if b.MovementState != "idle" {
						b.CurrentPath = nil
					}
				}

				b.Pos = correctedPos
				// Preserve VelY when on a ladder — don't reset climbing velocity
				if !b.IsOnLadder {
					b.VelY = 0.0
				}
			}
			b.Mu.Unlock()

		case *packet.Respawn:
			b.Mu.Lock()
			b.Pos = p.Position.Sub(mgl32.Vec3{0, 1.62, 0})
			b.Logger.Info("bot respawned/teleported by server",
				slog.Float64("x", float64(b.Pos.X())),
				slog.Float64("y", float64(b.Pos.Y())),
				slog.Float64("z", float64(b.Pos.Z())),
			)
			b.Mu.Unlock()

		case *packet.PlayerList:
			if p.ActionType == packet.PlayerListActionAdd {
				for _, entry := range p.Entries {
					b.Mu.Lock()
					b.PlayerUUIDs[entry.UUID] = entry.Username
					isSelf := entry.UUID == b.PlayerUUID || entry.EntityUniqueID == b.Conn.GameData().EntityUniqueID
					if isSelf {
						b.PlayerUUID = entry.UUID
						if b.Name != entry.Username {
							b.Logger.Info("updating bot name from server PlayerList",
								slog.String("old", b.Name),
								slog.String("new", entry.Username),
							)
							b.Name = entry.Username
						}
						b.Logger.Info("server PlayerList entry for bot",
							slog.String("username", entry.Username),
							slog.String("uuid", entry.UUID.String()),
							slog.Int64("entity_unique_id", entry.EntityUniqueID),
						)
					}
					b.Mu.Unlock()
				}
			} else if p.ActionType == packet.PlayerListActionRemove {
				for _, entry := range p.Entries {
					b.Mu.Lock()
					if username, ok := b.PlayerUUIDs[entry.UUID]; ok {
						if id, hasID := b.PlayerEntityIDs[username]; hasID {
							delete(b.PlayerUsernames, id)
							delete(b.PlayerPositions, id)
						}
						delete(b.PlayerEntityIDs, username)
						delete(b.PlayerUUIDs, entry.UUID)
						b.Logger.Info("tracked player disconnected", slog.String("username", username))
					}
					b.Mu.Unlock()
				}
			}

		case *packet.AddActor:
			b.Mu.Lock()
			b.Actors[p.EntityRuntimeID] = &entity.Info{
				ID:       p.EntityRuntimeID,
				Type:     p.EntityType,
				Name:     p.EntityType,
				Position: p.Position,
				Health:   20, // default
			}
			b.UniqueIDToRuntimeID[p.EntityUniqueID] = p.EntityRuntimeID
			b.Mu.Unlock()
			b.Logger.Info("tracked actor spawned", slog.String("type", p.EntityType), slog.Uint64("runtime_id", p.EntityRuntimeID))

		case *packet.CraftingData:
			b.Mu.Lock()
			b.Recipes = make(map[string]uint32)
			for _, r := range p.Recipes {
				switch recipe := r.(type) {
				case *protocol.ShapelessRecipe:
					if len(recipe.Output) > 0 {
						outItem := recipe.Output[0]
						name := b.ItemNames[outItem.NetworkID]
						if name != "" {
							b.Recipes[strings.ToLower(name)] = recipe.RecipeNetworkID
							cleanName := strings.TrimPrefix(name, "minecraft:")
							b.Recipes[strings.ToLower(cleanName)] = recipe.RecipeNetworkID
						}
					}
				case *protocol.ShapedRecipe:
					if len(recipe.Output) > 0 {
						outItem := recipe.Output[0]
						name := b.ItemNames[outItem.NetworkID]
						if name != "" {
							b.Recipes[strings.ToLower(name)] = recipe.RecipeNetworkID
							cleanName := strings.TrimPrefix(name, "minecraft:")
							b.Recipes[strings.ToLower(cleanName)] = recipe.RecipeNetworkID
						}
					}
				}
			}
			b.Mu.Unlock()
			b.Logger.Info("Crafting recipes cached", "count", len(b.Recipes))

		case *packet.MoveActorDelta:
			b.Mu.Lock()
			if act, ok := b.Actors[p.EntityRuntimeID]; ok {
				act.Position = p.Position
			}
			b.Mu.Unlock()

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
				b.Logger.Info("tracked player left view distance", slog.String("username", username))
			}
			b.Mu.Unlock()

		case *packet.InventoryContent:
			if p.WindowID == 0 {
				b.Mu.Lock()
				b.InventoryMap = make(map[uint32]protocol.ItemStack)
				for slot, item := range p.Content {
					if item.Stack.Count > 0 && item.Stack.NetworkID != 0 {
						b.InventoryMap[uint32(slot)] = item.Stack
					}
				}
				b.Mu.Unlock()
			}

		case *packet.InventorySlot:
			if p.WindowID == 0 {
				b.Mu.Lock()
				if p.NewItem.Stack.Count > 0 && p.NewItem.Stack.NetworkID != 0 {
					b.InventoryMap[p.Slot] = p.NewItem.Stack
				} else {
					delete(b.InventoryMap, p.Slot)
				}
				b.Mu.Unlock()
			}

		case *packet.MobEquipment:
			if p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID {
				b.Mu.Lock()
				b.HeldSlot = uint32(p.HotBarSlot)
				b.Mu.Unlock()
			}

		case *packet.UpdateAttributes:
			if p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID {
				b.Mu.Lock()
				prevHealth := b.Health
				for _, attr := range p.Attributes {
					if attr.Name == "minecraft:health" {
						b.Health = int(attr.Value)
					} else if attr.Name == "minecraft:player.hunger" {
						b.Hunger = int(attr.Value)
					}
				}

				// Self-learning hazard detection: if health decreases, mark the block below as hazard
				if b.Health < prevHealth && b.Health > 0 {
					feetX := int32(math.Floor(float64(b.Pos.X())))
					feetY := int32(math.Floor(float64(b.Pos.Y())))
					feetZ := int32(math.Floor(float64(b.Pos.Z())))

					b.WorldModel.SetHazard(feetX, feetY-1, feetZ, true)
					b.Logger.Warn("bot took damage! marking block below feet as hazard", "x", feetX, "y", feetY-1, "z", feetZ)

					b.Mu.Unlock()
					b.RecalculatePath()
					b.Mu.Lock()
				}
				b.Mu.Unlock()
			}

		case *packet.UpdateBlock:
			isSolid := b.WorldCache.IsRIDSolid(p.NewBlockRuntimeID)
			b.WorldModel.SetSolid(p.Position.X(), p.Position.Y(), p.Position.Z(), isSolid)
		}

		if handleErr := b.Registry.Handle(ctx, pk); handleErr != nil {
			b.Logger.Error("handle packet",
				slog.String("error", handleErr.Error()),
			)
		}
	}
}
