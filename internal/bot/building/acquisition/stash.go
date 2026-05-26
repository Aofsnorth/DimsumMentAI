package acquisition

import (
	"context"
	"strings"
	"time"

	"bedrock-ai/internal/bot/building/common"
	"bedrock-ai/internal/bot/building/schematic"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// HandleFullInventory places a chest and stashes junk/excess items if inventory space is low.
func (ia *InventoryAcquisition) HandleFullInventory(ctx context.Context, buildSpot common.Vec3i) {
	if ia.bot == nil {
		return
	}

	inv := ia.bot.GetInventorySlots()
	names := ia.bot.GetItemNames()

	occupied := 0
	for _, stack := range inv {
		if stack.Count > 0 && stack.NetworkID != 0 {
			occupied++
		}
	}

	if occupied < 30 {
		return
	}

	ia.logger.Info("Inventory is full, preparing emergency chest stash", "slots_occupied", occupied)

	nonEssential := map[string]bool{
		"grass_block": true, "dirt": true, "sand": true, "gravel": true, "clay": true,
		"coarse_dirt": true, "podzol": true, "seeds": true, "wheat_seeds": true,
		"beetroot_seeds": true, "short_grass": true, "tall_grass": true, "fern": true,
		"poppy": true, "dandelion": true, "cobblestone": true, "andesite": true,
		"diorite": true, "granite": true, "tuff": true, "deepslate": true,
	}

	chestSlot, hasChestItem := schematic.FindItemInSlots(inv, names, "chest")
	if !hasChestItem {
		ia.logger.Info("No chest found, trying to craft one")
		ia.CraftPlanksIfNeeded()
		recipes := ia.bot.GetRecipes()
		recipeID, ok := recipes["chest"]
		if ok {
			_ = ia.bot.CraftItem(recipeID, 1)
			time.Sleep(600 * time.Millisecond)

			inv = ia.bot.GetInventorySlots()
			chestSlot, hasChestItem = schematic.FindItemInSlots(inv, names, "chest")
		}
	}

	if !hasChestItem {
		ia.logger.Warn("Could not craft/acquire chest, performing emergency drop of non-essential items")
		ia.emergencyDropItems(inv, names, nonEssential)
		return
	}

	ia.placeAndStash(ctx, inv, names, chestSlot, buildSpot, nonEssential)
}

func (ia *InventoryAcquisition) emergencyDropItems(inv map[uint32]protocol.ItemStack, names map[int32]string, nonEssential map[string]bool) {
	dropped := 0
	for _, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		name := strings.ReplaceAll(names[stack.NetworkID], "minecraft:", "")
		if nonEssential[name] {
			_ = ia.bot.DropItem(name, int(stack.Count))
			dropped++
			time.Sleep(200 * time.Millisecond)
			if dropped >= 4 {
				break
			}
		}
	}
}
