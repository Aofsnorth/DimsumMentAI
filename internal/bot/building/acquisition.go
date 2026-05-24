package building

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// InventoryAcquisition handles raw materials checking, emergency crafting, and chest stashing.
type InventoryAcquisition struct {
	bot        BotInterface
	logger     *slog.Logger
	chestPos   protocol.BlockPos
	hasChest   bool
	scanner    *AreaScanner
}

// NewInventoryAcquisition creates a new InventoryAcquisition instance.
func NewInventoryAcquisition(bot BotInterface, logger *slog.Logger, scanner *AreaScanner) *InventoryAcquisition {
	return &InventoryAcquisition{
		bot:     bot,
		logger:  logger,
		scanner: scanner,
	}
}

// CraftPlanksIfNeeded checks plank counts and processes logs into planks.
func (ia *InventoryAcquisition) CraftPlanksIfNeeded() {
	if ia.bot == nil {
		return
	}

	inv := ia.bot.GetInventorySlots()
	names := ia.bot.GetItemNames()

	plankNames := []string{"oak_planks", "spruce_planks", "birch_planks", "jungle_planks", "acacia_planks", "dark_oak_planks"}
	totalPlanks := 0

	for _, p := range plankNames {
		for _, stack := range inv {
			if stack.Count <= 0 {
				continue
			}
			name := strings.ReplaceAll(names[stack.NetworkID], "minecraft:", "")
			if name == p {
				totalPlanks += int(stack.Count)
			}
		}
	}

	if totalPlanks >= 64 {
		ia.logger.Info("Sufficient planks already in inventory", "count", totalPlanks)
		return
	}

	logNames := []string{"oak_log", "spruce_log", "birch_log", "jungle_log", "acacia_log", "dark_oak_log"}
	recipes := ia.bot.GetRecipes()

	for _, log := range logNames {
		for _, stack := range inv {
			if stack.Count <= 0 {
				continue
			}
			name := strings.ReplaceAll(names[stack.NetworkID], "minecraft:", "")
			if name == log {
				// Determine resulting plank name
				targetPlank := strings.ReplaceAll(log, "_log", "_planks")
				recipeID, ok := recipes[targetPlank]
				if !ok {
					// Fallback lowercase try
					recipeID, ok = recipes[strings.ToLower(targetPlank)]
				}

				if ok {
					ia.logger.Info("Crafting planks from logs", "log", log, "count", stack.Count)
					ia.bot.SendSafeChat(fmt.Sprintf("Aku buat plank dulu ya dari %s.", strings.ReplaceAll(log, "_", " ")))
					
					// Equip logs slot first if needed, though autocraft handles it
					time.Sleep(200 * time.Millisecond)

					// Craft planks (each craft outputs 4)
					craftCount := int(stack.Count)
					if craftCount > 16 {
						craftCount = 16 // Cap crafting size to avoid packet drops
					}

					_ = ia.bot.CraftItem(recipeID, craftCount)
					time.Sleep(600 * time.Millisecond)
					return // Finished crafting one log type
				}
			}
		}
	}
}

// CraftToolsIfNeeded ensures the bot has basic stone/wood tools for building and leveling.
func (ia *InventoryAcquisition) CraftToolsIfNeeded() {
	if ia.bot == nil {
		return
	}

	inv := ia.bot.GetInventorySlots()
	names := ia.bot.GetItemNames()

	hasAxe := false
	hasPickaxe := false

	for _, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		name := strings.ToLower(strings.ReplaceAll(names[stack.NetworkID], "minecraft:", ""))
		if strings.Contains(name, "axe") && !strings.Contains(name, "pickaxe") {
			hasAxe = true
		}
		if strings.Contains(name, "pickaxe") {
			hasPickaxe = true
		}
	}

	if hasAxe && hasPickaxe {
		return
	}

	planksCount := 0
	sticksCount := 0
	for _, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		name := strings.ReplaceAll(names[stack.NetworkID], "minecraft:", "")
		if strings.Contains(name, "planks") {
			planksCount += int(stack.Count)
		}
		if name == "stick" {
			sticksCount += int(stack.Count)
		}
	}

	recipes := ia.bot.GetRecipes()

	// Craft sticks if missing
	if sticksCount < 4 && planksCount >= 2 {
		recipeID, ok := recipes["stick"]
		if ok {
			ia.logger.Info("Crafting sticks for tool crafting")
			_ = ia.bot.CraftItem(recipeID, 1)
			time.Sleep(500 * time.Millisecond)
			sticksCount += 4
			planksCount -= 2
		}
	}

	// Craft wooden axe
	if !hasAxe && planksCount >= 3 && sticksCount >= 2 {
		recipeID, ok := recipes["wooden_axe"]
		if ok {
			ia.logger.Info("Crafting wooden axe")
			ia.bot.SendSafeChat("Aku buat kapak kayu dulu.")
			_ = ia.bot.CraftItem(recipeID, 1)
			time.Sleep(500 * time.Millisecond)
			planksCount -= 3
			sticksCount -= 2
		}
	}

	// Craft wooden pickaxe
	if !hasPickaxe && planksCount >= 3 && sticksCount >= 2 {
		recipeID, ok := recipes["wooden_pickaxe"]
		if ok {
			ia.logger.Info("Crafting wooden pickaxe")
			_ = ia.bot.CraftItem(recipeID, 1)
			time.Sleep(500 * time.Millisecond)
		}
	}
}

// HandleFullInventory places a chest and stashes junk/excess items if inventory space is low.
func (ia *InventoryAcquisition) HandleFullInventory(ctx context.Context, buildSpot Vec3i) {
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
		return // Sufficient free space
	}

	ia.logger.Info("Inventory is full, preparing emergency chest stash", "slots_occupied", occupied)

	nonEssential := map[string]bool{
		"grass_block": true, "dirt": true, "sand": true, "gravel": true, "clay": true,
		"coarse_dirt": true, "podzol": true, "seeds": true, "wheat_seeds": true,
		"beetroot_seeds": true, "short_grass": true, "tall_grass": true, "fern": true,
		"poppy": true, "dandelion": true, "cobblestone": true, "andesite": true,
		"diorite": true, "granite": true, "tuff": true, "deepslate": true,
	}

	// 1. Find or craft chest
	chestSlot, hasChestItem := FindItemInSlots(inv, names, "chest")
	if !hasChestItem {
		ia.logger.Info("No chest found, trying to craft one")
		ia.CraftPlanksIfNeeded()
		recipes := ia.bot.GetRecipes()
		recipeID, ok := recipes["chest"]
		if ok {
			_ = ia.bot.CraftItem(recipeID, 1)
			time.Sleep(600 * time.Millisecond)

			// Refresh slots
			inv = ia.bot.GetInventorySlots()
			chestSlot, hasChestItem = FindItemInSlots(inv, names, "chest")
		}
	}

	if !hasChestItem {
		ia.logger.Warn("Could not craft/acquire chest, performing emergency drop of non-essential items")
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
		return
	}

	// 2. Place Chest outside building zone
	placed := false
	botPos := ia.bot.GetCoords()
	bx := int32(math.Floor(float64(botPos.X())))
	by := int32(math.Floor(float64(botPos.Y())))
	bz := int32(math.Floor(float64(botPos.Z())))

	// Try cardinal coordinates offset by 3 blocks from build spot
	offsets := []Vec3i{
		{X: -3, Y: 0, Z: -3},
		{X: 3, Y: 0, Z: -3},
		{X: -3, Y: 0, Z: 3},
		{X: 3, Y: 0, Z: 3},
	}

	world := ia.bot.GetLocalWorldModel()
	var targetPos protocol.BlockPos

	for _, o := range offsets {
		tx := int32(buildSpot.X + o.X)
		ty := int32(buildSpot.Y + o.Y)
		tz := int32(buildSpot.Z + o.Z)

		// Check if spot is empty (air) and block below is solid
		if !world.IsSolid(tx, ty, tz) && world.IsSolid(tx, ty-1, tz) {
			targetPos = protocol.BlockPos{tx, ty, tz}
			placed = true
			break
		}
	}

	if !placed {
		// Fallback: place in front of bot
		targetPos = protocol.BlockPos{bx + 2, by, bz}
	}

	ia.bot.SendSafeChat("Inventory penuh, aku buat chest dulu ya buat simpan barang.")
	ia.logger.Info("Placing chest", "x", targetPos.X(), "y", targetPos.Y(), "z", targetPos.Z())

	// Equip chest
	_ = ia.bot.EquipItem(chestSlot)
	time.Sleep(200 * time.Millisecond)

	// Look at block below chest
	ia.bot.LookAt(mgl32.Vec3{float32(targetPos.X()) + 0.5, float32(targetPos.Y()) - 0.5, float32(targetPos.Z()) + 0.5})
	time.Sleep(200 * time.Millisecond)

	// Click placement transaction
	chestStack := inv[chestSlot]
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   protocol.BlockPos{targetPos.X(), targetPos.Y() - 1, targetPos.Z()},
			BlockFace:       1, // Top face
			HotBarSlot:      int32(ia.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{Stack: chestStack},
			Position:        ia.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	_ = ia.bot.WritePacket(tx)

	world.SetSolid(targetPos.X(), targetPos.Y(), targetPos.Z(), true)
	ia.chestPos = targetPos
	ia.hasChest = true

	if ia.scanner != nil {
		ia.scanner.TrackStructure("chest", int(targetPos.X()), int(targetPos.Y()), int(targetPos.Z()))
	}

	time.Sleep(800 * time.Millisecond)

	// 3. Stash non-essentials inside placed chest
	ia.logger.Info("Stashing non-essential items into chest", "pos", targetPos)
	ia.bot.LookAt(mgl32.Vec3{float32(targetPos.X()) + 0.5, float32(targetPos.Y()) + 0.5, float32(targetPos.Z()) + 0.5})
	time.Sleep(200 * time.Millisecond)

	// Interact to open
	_ = ia.bot.WritePacket(&packet.Interact{
		ActionType:            6,
		TargetEntityRuntimeID: ia.bot.GetEntityRuntimeID(),
		Position:              protocol.Option(mgl32.Vec3{float32(targetPos.X()), float32(targetPos.Y()), float32(targetPos.Z())}),
	})
	time.Sleep(500 * time.Millisecond)

	// Deposit
	inv = ia.bot.GetInventorySlots()
	keepItems := []string{"axe", "pickaxe", "shovel", "sword", "food", "apple", "bread", "steak", "chest", "crafting_table"}
	stashed := 0

	for slot, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		name := strings.ToLower(strings.ReplaceAll(names[stack.NetworkID], "minecraft:", ""))
		shouldKeep := false
		for _, k := range keepItems {
			if strings.Contains(name, k) {
				shouldKeep = true
				break
			}
		}

		// Deposit non-essential
		if !shouldKeep {
			depositTx := &packet.InventoryTransaction{
				Actions: []protocol.InventoryAction{
					{
						SourceType:    protocol.InventoryActionSourceContainer,
						InventorySlot: slot,
						OldItem:       protocol.ItemInstance{Stack: stack},
						NewItem:       protocol.ItemInstance{},
					},
				},
				TransactionData: &protocol.NormalTransactionData{},
			}
			_ = ia.bot.WritePacket(depositTx)
			stashed++
			time.Sleep(300 * time.Millisecond)
		}
	}

	// Close chest
	_ = ia.bot.WritePacket(&packet.ContainerClose{WindowID: 0})
	ia.logger.Info("Emergency stashing complete", "stacks_stashed", stashed)

	// Acknowledge pickup of drops
	ia.GatherDroppedItems(ctx, 6.0)
}

// GatherDroppedItems scans for item entities on ground and drives bot towards them.
func (ia *InventoryAcquisition) GatherDroppedItems(ctx context.Context, radius float32) {
	if ia.bot == nil {
		return
	}

	botPos := ia.bot.GetCoords()
	entities := ia.bot.GetEntities()

	var closestItem *entity.Info
	closestDist := float32(math.MaxFloat32)

	for _, ent := range entities {
		if ent.Type == "item" || strings.Contains(strings.ToLower(ent.Type), "item") || strings.Contains(strings.ToLower(ent.Name), "item") {
			dx := ent.Position.X() - botPos.X()
			dy := ent.Position.Y() - botPos.Y()
			dz := ent.Position.Z() - botPos.Z()
			dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

			if dist <= radius && dist < closestDist {
				closestDist = dist
				closestItem = ent
			}
		}
	}

	if closestItem != nil {
		ia.logger.Info("Walking to pick up dropped item on ground", "name", closestItem.Name, "dist", closestDist)
		
		// Navigate to item
		reached := ia.bot.NavigateToBlock(
			int32(math.Floor(float64(closestItem.Position.X()))),
			int32(math.Floor(float64(closestItem.Position.Y()))),
			int32(math.Floor(float64(closestItem.Position.Z()))),
			1.2,
		)
		if reached {
			ia.bot.StopMovement()
			time.Sleep(500 * time.Millisecond) // Give time for server vacuum pickup
		}
	}
}
