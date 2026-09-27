// Package player handles player, entity, and inventory-related packets.
package player

import (
	"log/slog"
	"math"
	"strings"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// isSelfEntry reports whether a PlayerList entry describes the bot itself.
//
// It exists because the obvious check — "EntityUniqueID matches ours" — is
// wrong in a way that hijacked the bot's identity. Servers routinely send
// PlayerList entries with EntityUniqueID 0, and the bot's own
// GameData().EntityUniqueID is also 0 until the server assigns one. Every such
// entry therefore "matched", and the bot adopted the first other player's name
// from the list. The visible failure was an instant kick: the bot then ran
// /register under a username that was already taken.
//
// The rules, in order:
//   - a matching UUID is authoritative
//   - a zero EntityUniqueID matches nothing, because it identifies nobody
func isSelfEntry(b *bot.Bot, entry protocol.PlayerListEntry) bool {
	if entry.UUID == b.PlayerUUID {
		return true
	}
	if entry.EntityUniqueID == 0 {
		return false
	}
	return b.Conn != nil && entry.EntityUniqueID == b.Conn.GameData().EntityUniqueID
}

// handlePlayerList caches the player list. The add/remove action moved from the
// packet onto each individual entry, so it is read per entry.
func handlePlayerList(b *bot.Bot, p *packet.PlayerList) {
	for _, entry := range p.Entries {
		b.Mu.Lock()
		switch entry.ActionType {
		case protocol.PlayerListActionAdd:
			b.PlayerUUIDs[entry.UUID] = entry.Username
			if isSelfEntry(b, entry) {
				b.PlayerUUID = entry.UUID
				if b.Name != entry.Username {
					b.Logger.Info("updating bot name from server PlayerList",
						slog.String("old", b.Name),
						slog.String("new", entry.Username),
					)
					// The server can hand the bot a different name than the config
					// asked for — that is what a SimpleLogin/Floodgate account
					// does, and what makes the bot collide with a real player of
					// the same name. The persona has to follow, or the model keeps
					// introducing itself by the configured name while the game
					// shows a different one.
					b.Name = entry.Username
					if b.AiClient != nil {
						b.AiClient.SetBotName(entry.Username)
					}
				}
				b.Logger.Info("server PlayerList entry for bot",
					slog.String("username", entry.Username),
					slog.String("uuid", entry.UUID.String()),
					slog.Int64("entity_unique_id", entry.EntityUniqueID),
				)
			}
		case protocol.PlayerListActionRemove:
			if username, ok := b.PlayerUUIDs[entry.UUID]; ok {
				forgetPlayer(b, username)
			}
		}
		b.Mu.Unlock()
	}
}

// forgetPlayer removes all tracked state for a player that left the world.
// Caller must hold b.Mu.
func forgetPlayer(b *bot.Bot, username string) {
	b.Logger.Debug("tracked player disconnected", slog.String("username", username))
	if id, hasID := b.PlayerEntityIDs[username]; hasID {
		delete(b.PlayerUsernames, id)
		delete(b.PlayerPositions, id)
		delete(b.PlayerYaws, id)
		delete(b.PlayerPitches, id)
	}
	delete(b.PlayerEntityIDs, username)
	for uuid, name := range b.PlayerUUIDs {
		if name == username {
			delete(b.PlayerUUIDs, uuid)
		}
	}
}

func handleCraftingData(b *bot.Bot, p *packet.CraftingData) {
	b.Mu.Lock()
	b.Recipes = make(map[string]uint32)
	b.RecipeCandidates = make(map[string][]uint32)
	b.RecipesByNetID = make(map[uint32]bot.RecipeInfo)

	for i := range p.ShapelessRecipes {
		recipe := p.ShapelessRecipes[i]
		if len(recipe.Output) == 0 {
			continue
		}
		outItem := recipe.Output[0]
		registerRecipeName(b, b.ItemNames[outItem.NetworkID], recipe.RecipeNetworkID)
		b.RecipesByNetID[recipe.RecipeNetworkID] = bot.RecipeInfo{
			Ingredients: recipe.Input,
			Output:      outItem,
			Block:       recipe.Block,
			Shapeless:   true,
		}
	}
	for i := range p.ShapedRecipes {
		recipe := p.ShapedRecipes[i]
		if len(recipe.Output) == 0 {
			continue
		}
		outItem := recipe.Output[0]
		registerRecipeName(b, b.ItemNames[outItem.NetworkID], recipe.RecipeNetworkID)
		b.RecipesByNetID[recipe.RecipeNetworkID] = bot.RecipeInfo{
			Ingredients: recipe.Input,
			Output:      outItem,
			Block:       recipe.Block,
			Shapeless:   false,
			Width:       recipe.Width,
			Height:      recipe.Height,
		}
	}
	b.Mu.Unlock()
	b.Logger.Debug("Crafting recipes cached", "count", len(b.Recipes))
}

// registerRecipeName indexes a recipe under its output item name. Names are
// registered both with and without the "minecraft:" prefix so lookups match
// regardless of which form the configured item name uses.
func registerRecipeName(b *bot.Bot, name string, recipeNetID uint32) {
	if name == "" {
		return
	}
	lower := strings.ToLower(name)
	b.Recipes[lower] = recipeNetID
	b.RecipeCandidates[lower] = append(b.RecipeCandidates[lower], recipeNetID)

	cleanName := strings.TrimPrefix(name, "minecraft:")
	cleanLower := strings.ToLower(cleanName)
	b.Recipes[cleanLower] = recipeNetID
	if cleanLower != lower {
		b.RecipeCandidates[cleanLower] = append(b.RecipeCandidates[cleanLower], recipeNetID)
	}
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
