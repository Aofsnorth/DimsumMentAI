// Package player handles player, entity, and inventory-related packets.
package player

import (
	"log/slog"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// IsSelfEntry reports whether a PlayerList entry describes the bot itself.
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
func IsSelfEntry(b *bot.Bot, entry protocol.PlayerListEntry) bool {
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
			if IsSelfEntry(b, entry) {
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
	indexSmithingRecipes(b, p)
	b.Mu.Unlock()
	b.Logger.Debug("Crafting recipes cached", "count", len(b.Recipes))
}

// indexSmithingRecipes walks the smithing transform and trim recipes into the
// same tables the crafting ones go into.
//
// Without this the smithing table is unreachable no matter what the bot carries:
// handleCraftingData used to walk only the shapeless and shaped lists, so no
// smithing recipe ever entered RecipesByNetID, and a planner looking for a
// netherite upgrade was told the server advertised none — on every run, with
// the right template and the right base item in the bag.
//
// A transform takes three inputs where a crafting recipe takes a grid, so the
// template and the base are recorded as the first two ingredients and the
// addition as the third. Both kinds are shapeless — the slot order carries the
// meaning — so Width/Height stay zero.
//
// A trim recipe is indexed but deliberately not named. Its result is the base
// item wearing a trim, and the protocol carries no output stack for it, so the
// output item name is not knowable from the recipe. Registering a name anyway
// would mean inventing one, and every lookup in the bot is by output name — a
// fabricated entry here is a recipe that claims to make something it does not.
func indexSmithingRecipes(b *bot.Bot, p *packet.CraftingData) {
	for i := range p.SmithingTransformRecipes {
		r := p.SmithingTransformRecipes[i]
		if r.RecipeNetworkID == 0 {
			// The protocol says this field must never be 0, and a zero would
			// collide with the "no recipe" entry every lookup falls back to.
			continue
		}
		outName := b.ItemNames[r.Result.NetworkID]
		registerRecipeName(b, outName, r.RecipeNetworkID)
		b.RecipesByNetID[r.RecipeNetworkID] = bot.RecipeInfo{
			Ingredients: []protocol.ItemDescriptorCount{r.Template, r.Base, r.Addition},
			Output:      r.Result,
			Block:       r.Block,
			Shapeless:   true,
		}
	}
	for i := range p.SmithingTrimRecipes {
		r := p.SmithingTrimRecipes[i]
		if r.RecipeNetworkID == 0 {
			continue
		}
		b.RecipesByNetID[r.RecipeNetworkID] = bot.RecipeInfo{
			Ingredients: []protocol.ItemDescriptorCount{r.Template, r.Base, r.Addition},
			Block:       r.Block,
			Shapeless:   true,
		}
	}
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

// ApplyAttributeValues folds the attributes of an UpdateAttributes packet into
// the bot's current health and hunger.
//
// It is a pure function of the packet and the previous values, so the attribute
// naming lives in one place that can be tested without a live connection. The
// boolean reports whether the packet actually carried a hunger value: the
// protocol only sends attributes that changed, so a health-only packet must
// leave hunger where it was rather than resetting it to zero.
func ApplyAttributeValues(attrs []protocol.Attribute, prevHealth, prevHunger int) (health, hunger int, hungerSeen bool) {
	health, hunger = prevHealth, prevHunger
	for _, attr := range attrs {
		switch attr.Name {
		case "minecraft:health":
			health = int(attr.Value)
		case "minecraft:player.hunger":
			hunger = int(attr.Value)
			hungerSeen = true
		}
	}
	return health, hunger, hungerSeen
}

func handleUpdateAttributes(b *bot.Bot, p *packet.UpdateAttributes) {
	if p.EntityRuntimeID == b.Conn.GameData().EntityRuntimeID {
		if p.Tick > 0 {
			syncServerTick(b, p.Tick, "UpdateAttributes")
		}
		b.Mu.Lock()
		prevHealth := b.Health
		health, hunger, hungerSeen := ApplyAttributeValues(p.Attributes, b.Health, b.Hunger)
		b.Health = health
		b.Hunger = hunger

		// XP is computed by the pure helper BEFORE the lock and written back
		// AFTER it is released, below. SetExperienceLevel takes b.Mu itself, and
		// sync.Mutex is not reentrant: calling it from inside this critical
		// section made the packet loop wait on a lock it was already holding —
		// every other goroutine queued behind it, and the bot froze solid right
		// after spawn while the connection stayed up.
		level, levelSeen := bot.ApplyExperienceAttribute(p.Attributes, 0, false)

		if b.Health < prevHealth && b.Health > 0 {
			feetX := int32(math.Floor(float64(b.Pos.X())))
			feetY := int32(math.Floor(float64(b.Pos.Y())))
			feetZ := int32(math.Floor(float64(b.Pos.Z())))

			b.WorldModel.SetHazard(feetX, feetY-1, feetZ, true)
			b.Logger.Warn("bot took damage! marking block below feet as hazard", "x", feetX, "y", feetY-1, "z", feetZ)

			// Only re-path if the bot actually has somewhere to go. Re-pathing an
			// idle bot wastes a full A* budget over terrain that may not be loaded
			// yet, and the result is discarded immediately.
			hasDestination := b.MovementState == "walk_to" || b.MovementState == "follow"

			b.Mu.Unlock()
			// Off the read loop. Re-planning is an A* search, and A* is measured
			// in hundreds of milliseconds against a world that is still loading
			// right after a join. Packet handlers run inline, so doing it here
			// stalls every packet the bot has not read yet — a bot being hurt
			// repeatedly stops seeing the world entirely. The search reads the
			// route under b.Mu and publishes it at the end, so running it
			// concurrently is what it was written for.
			//
			// The timestamp is claimed BEFORE spawning the search, under the
			// same lock, so the movement tick's own guard (steering.go) sees
			// this repath and does not fire a second identical one ~30ms
			// later — the duplicate "recalculating path" lines in the log.
			// A stuck-recovery replan still goes through: it marks temp-solid
			// blockers first, so its search genuinely differs from this one.
			if hasDestination && time.Since(b.LastPathRecalcTime) > 500*time.Millisecond {
				b.LastPathRecalcTime = time.Now()
				go b.RecalculatePath()
			}
			b.Mu.Lock()
		}
		b.Mu.Unlock()

		// The survival manager keeps its own copy of the hunger level, and
		// nothing ever told it about these updates. It therefore stayed at the
		// 20 it was constructed with, so auto-eat never fired on real hunger and
		// the bot starved with food in its bag. SetHunger takes the manager's
		// own lock, so it is called off b.Mu rather than nested inside it.
		// SetExperienceLevel likewise owns its lock and is written here, outside
		// the critical section above.
		if levelSeen {
			b.SetExperienceLevel(level)
		}
		if hungerSeen && b.SurvivalMgr != nil {
			b.SurvivalMgr.SetHunger(hunger)
		}
	}
}
