package player

import (
	"log/slog"
	"math"
	"strings"

	"bedrock-ai/internal/bot"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func handlePlayerList(b *bot.Bot, p *packet.PlayerList) {
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
					delete(b.PlayerYaws, id)
					delete(b.PlayerPitches, id)
				}
				delete(b.PlayerEntityIDs, username)
				delete(b.PlayerUUIDs, entry.UUID)
				b.Logger.Debug("tracked player disconnected", slog.String("username", username))
			}
			b.Mu.Unlock()
		}
	}
}

func handleCraftingData(b *bot.Bot, p *packet.CraftingData) {
	b.Mu.Lock()
	b.Recipes = make(map[string]uint32)
	b.RecipesByNetID = make(map[uint32]bot.RecipeInfo)
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
				b.RecipesByNetID[recipe.RecipeNetworkID] = bot.RecipeInfo{
					Ingredients: recipe.Input,
					Output:      outItem,
					Block:       recipe.Block,
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
				b.RecipesByNetID[recipe.RecipeNetworkID] = bot.RecipeInfo{
					Ingredients: recipe.Input,
					Output:      outItem,
					Block:       recipe.Block,
				}
			}
		}
	}
	b.Mu.Unlock()
	b.Logger.Debug("Crafting recipes cached", "count", len(b.Recipes))
}

func handleUpdateAttributes(b *bot.Bot, p *packet.UpdateAttributes) {
	if p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID {
		if p.Tick > 0 {
			syncServerTick(b, p.Tick, "UpdateAttributes")
		}
		b.Mu.Lock()
		prevHealth := b.Health
		for _, attr := range p.Attributes {
			if attr.Name == "minecraft:health" {
				b.Health = int(attr.Value)
			} else if attr.Name == "minecraft:player.hunger" {
				b.Hunger = int(attr.Value)
			}
		}

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
}
