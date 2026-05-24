package building

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// BlockPlacer handles low-level block placement, scaffolding, tower-up, and descend actions.
type BlockPlacer struct {
	bot             BotInterface
	logger          *slog.Logger
	ScaffoldHistory []protocol.BlockPos
}

// NewBlockPlacer creates a new BlockPlacer instance.
func NewBlockPlacer(bot BotInterface, logger *slog.Logger) *BlockPlacer {
	return &BlockPlacer{
		bot:    bot,
		logger: logger,
	}
}

// PlaceBlockAt attempts to place a block at the specified coordinates.
func (bp *BlockPlacer) PlaceBlockAt(ctx context.Context, x, y, z int, blockName string, cx, cz int, metadata *int) bool {
	if bp.bot == nil {
		return false
	}

	blockName = strings.ReplaceAll(blockName, "minecraft:", "")
	bp.logger.Info("Attempting to place block", "block", blockName, "x", x, "y", y, "z", z)

	// 1. Check if block at position is already what we want
	world := bp.bot.GetLocalWorldModel()
	// (Note: local world model is basic, but we check if we can skip if already solid and we placed it)

	// 2. Clear obstructing entities (e.g. mobs) or blocks (e.g. grass, flowers)
	bp.clearObstructions(ctx, x, y, z)

	// 3. Navigation/reach check
	botPos := bp.bot.GetCoords()
	by := int(math.Floor(float64(botPos.Y())))

	dx := float64(x) + 0.5 - float64(botPos.X())
	dy := float64(y) - float64(botPos.Y())
	dz := float64(z) + 0.5 - float64(botPos.Z())
	distH := math.Sqrt(dx*dx + dz*dz)

	// If too far horizontally or vertically, navigate closer
	if distH > 4.2 || math.Abs(dy) > 3.0 {
		bp.logger.Info("Target out of range, navigating closer", "distH", distH, "dy", dy)
		reached := bp.bot.NavigateToBlock(int32(x), int32(y), int32(z), 2.5)
		if !reached {
			bp.logger.Warn("Failed to navigate close to block placement target")
			// Try navigating near the center of the build area instead
			bp.bot.NavigateToBlock(int32(cx), int32(y), int32(cz), 3.0)
			time.Sleep(500 * time.Millisecond)
		}
		botPos = bp.bot.GetCoords()
		by = int(math.Floor(float64(botPos.Y())))
	}

	// 4. Height adjustment (towering up or descending)
	if y > by+2 {
		bp.logger.Info("Target is too high, towering up", "targetY", y, "botY", by)
		if !bp.TowerUp(ctx, y-1) {
			bp.logger.Warn("Tower up failed")
			return false
		}
		botPos = bp.bot.GetCoords()
		by = int(math.Floor(float64(botPos.Y())))
	} else if by > y+3 {
		bp.logger.Info("Target is too low, descending safely", "targetY", y, "botY", by)
		if !bp.DescendTo(ctx, y+1) {
			bp.logger.Warn("Descend failed")
			return false
		}
		botPos = bp.bot.GetCoords()
		by = int(math.Floor(float64(botPos.Y())))
	}

	// 5. Special tools handling for paths/farmland
	if blockName == "farmland" || blockName == "dirt_path" {
		return bp.placeSpecialBlock(ctx, x, y, z, blockName)
	}

	// 6. Equip block
	inv := bp.bot.GetInventorySlots()
	names := bp.bot.GetItemNames()
	slot, found := FindItemInSlots(inv, names, blockName)
	if !found {
		// Try fallback material substitution
		var buildItems []BuildItem
		for s, stack := range inv {
			if stack.Count > 0 {
				buildItems = append(buildItems, BuildItem{Slot: s, Name: names[stack.NetworkID], Count: int(stack.Count)})
			}
		}
		subName := FindSubstitute(blockName, buildItems)
		slot, found = FindItemInSlots(inv, names, subName)
		if !found {
			bp.logger.Warn("Required block not found in inventory", "block", blockName)
			return false
		}
		bp.logger.Info("Using substituted material", "original", blockName, "substitute", subName)
		blockName = subName
	}

	_ = bp.bot.EquipItem(slot)
	time.Sleep(150 * time.Millisecond)

	// 7. Find support/adjacent block to place against
	faces := []struct {
		offset protocol.BlockPos
		face   int32
	}{
		{protocol.BlockPos{0, -1, 0}, 1}, // Bottom
		{protocol.BlockPos{0, 1, 0}, 0},  // Top
		{protocol.BlockPos{0, 0, -1}, 3}, // North
		{protocol.BlockPos{0, 0, 1}, 2},  // South
		{protocol.BlockPos{-1, 0, 0}, 5}, // West
		{protocol.BlockPos{1, 0, 0}, 4},  // East
	}

	var placeTarget protocol.BlockPos
	var placeFace int32 = -1

	for _, f := range faces {
		adjX := int32(x) + f.offset.X()
		adjY := int32(y) + f.offset.Y()
		adjZ := int32(z) + f.offset.Z()

		if world.IsSolid(adjX, adjY, adjZ) {
			placeTarget = protocol.BlockPos{adjX, adjY, adjZ}
			placeFace = f.face
			break
		}
	}

	// If no solid adjacent block, we must jump-place a scaffold underneath (floating block)
	if placeFace == -1 {
		bp.logger.Info("Floating block detected, placing temporary scaffold underneath", "x", x, "y", y-1, "z", z)
		scaffSlot, scaffFound := FindScaffoldForTower(inv, names)
		if scaffFound {
			_ = bp.bot.EquipItem(scaffSlot)
			time.Sleep(100 * time.Millisecond)
			
			// Place scaffold block
			scaffPos := protocol.BlockPos{int32(x), int32(y - 1), int32(z)}
			bp.lookAtBlock(scaffPos)
			time.Sleep(100 * time.Millisecond)

			// Try placing against the ground below scaffold
			if world.IsSolid(int32(x), int32(y-2), int32(z)) {
				tx := &packet.InventoryTransaction{
					TransactionData: &protocol.UseItemTransactionData{
						ActionType:      protocol.UseItemActionClickBlock,
						BlockPosition:   protocol.BlockPos{int32(x), int32(y - 2), int32(z)},
						BlockFace:       1,
						HotBarSlot:      int32(bp.bot.GetHeldItemSlot()),
						HeldItem:        protocol.ItemInstance{Stack: inv[scaffSlot]},
						Position:        bp.bot.GetCoords(),
						ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
					},
				}
				_ = bp.bot.WritePacket(tx)
				world.SetSolid(int32(x), int32(y-1), int32(z), true)
				bp.ScaffoldHistory = append(bp.ScaffoldHistory, scaffPos)
				time.Sleep(150 * time.Millisecond)

				// Now we have a support block!
				placeTarget = scaffPos
				placeFace = 1
			}

			// Re-equip actual block
			_ = bp.bot.EquipItem(slot)
			time.Sleep(100 * time.Millisecond)
		}
	}

	if placeFace == -1 {
		bp.logger.Warn("Could not find suitable placement surface for block", "x", x, "y", y, "z", z)
		return false
	}

	// 8. Face placement target
	bp.lookAtBlock(placeTarget)
	time.Sleep(100 * time.Millisecond)

	// 9. Sneak if block we click against is interactable (e.g. chest, door, furnace)
	targetName := names[inv[slot].NetworkID]
	interactables := []string{"chest", "door", "furnace", "crafting_table", "hopper", "anvil", "trapdoor", "button", "lever"}
	shouldSneak := false
	for _, in := range interactables {
		if strings.Contains(strings.ToLower(targetName), in) {
			shouldSneak = true
			break
		}
	}

	if shouldSneak {
		_ = bp.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStartSneak,
		})
		time.Sleep(50 * time.Millisecond)
	}

	// 10. Perform placement transaction
	itemStack := inv[slot]
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   placeTarget,
			BlockFace:       placeFace,
			HotBarSlot:      int32(bp.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{Stack: itemStack},
			Position:        bp.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 0.5, 0.5},
		},
	}
	err := bp.bot.WritePacket(tx)
	if err != nil {
		bp.logger.Error("Failed to write place block packet", "err", err.Error())
		return false
	}

	if shouldSneak {
		_ = bp.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionStopSneak,
		})
	}

	world.SetSolid(int32(x), int32(y), int32(z), true)
	time.Sleep(150 * time.Millisecond)
	return true
}

// TowerUp builds a vertical tower under the bot to climb up.
func (bp *BlockPlacer) TowerUp(ctx context.Context, targetY int) bool {
	inv := bp.bot.GetInventorySlots()
	names := bp.bot.GetItemNames()

	scaffSlot, found := FindScaffoldForTower(inv, names)
	if !found {
		bp.logger.Warn("TowerUp failed: no scaffold materials found in inventory")
		return false
	}

	_ = bp.bot.EquipItem(scaffSlot)
	time.Sleep(100 * time.Millisecond)

	botPos := bp.bot.GetCoords()
	by := int(math.Floor(float64(botPos.Y())))

	for by < targetY {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		// Clear block above head first if solid
		bx := int32(math.Floor(float64(botPos.X())))
		bz := int32(math.Floor(float64(botPos.Z())))
		world := bp.bot.GetLocalWorldModel()

		if world.IsSolid(bx, int32(by+2), bz) {
			bp.digBlock(ctx, protocol.BlockPos{bx, int32(by + 2), bz})
		}

		// Look down at feet
		bp.bot.LookAt(mgl32.Vec3{botPos.X(), botPos.Y() - 1.0, botPos.Z()})
		time.Sleep(100 * time.Millisecond)

		// Jump
		_ = bp.bot.WritePacket(&packet.PlayerAction{
			EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
			ActionType:      protocol.PlayerActionJump,
		})
		
		// Update position upward
		botPos = mgl32.Vec3{botPos.X(), botPos.Y() + 1.1, botPos.Z()}
		
		// Place block under feet
		scaffPos := protocol.BlockPos{bx, int32(by), bz}
		tx := &packet.InventoryTransaction{
			TransactionData: &protocol.UseItemTransactionData{
				ActionType:      protocol.UseItemActionClickBlock,
				BlockPosition:   protocol.BlockPos{bx, int32(by - 1), bz},
				BlockFace:       1,
				HotBarSlot:      int32(bp.bot.GetHeldItemSlot()),
				HeldItem:        protocol.ItemInstance{Stack: inv[scaffSlot]},
				Position:        botPos,
				ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
			},
		}
		_ = bp.bot.WritePacket(tx)
		world.SetSolid(bx, int32(by), bz, true)
		bp.ScaffoldHistory = append(bp.ScaffoldHistory, scaffPos)

		time.Sleep(250 * time.Millisecond)
		botPos = bp.bot.GetCoords()
		by = int(math.Floor(float64(botPos.Y())))
	}

	return true
}

// DescendTo digs down vertical scaffolding safely to lower height.
func (bp *BlockPlacer) DescendTo(ctx context.Context, targetY int) bool {
	botPos := bp.bot.GetCoords()
	by := int(math.Floor(float64(botPos.Y())))
	bx := int32(math.Floor(float64(botPos.X())))
	bz := int32(math.Floor(float64(botPos.Z())))
	world := bp.bot.GetLocalWorldModel()

	for by > targetY {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		feetPos := protocol.BlockPos{bx, int32(by - 1), bz}
		if world.IsSolid(bx, int32(by-1), bz) {
			bp.digBlock(ctx, feetPos)
			world.SetSolid(bx, int32(by-1), bz, false)
		}

		// Wait for gravity fall
		time.Sleep(300 * time.Millisecond)
		botPos = bp.bot.GetCoords()
		by = int(math.Floor(float64(botPos.Y())))
	}

	return true
}

// CleanupScaffolds removes all placed scaffolding blocks in reverse order of placement.
func (bp *BlockPlacer) CleanupScaffolds(ctx context.Context) {
	if len(bp.ScaffoldHistory) == 0 {
		return
	}

	bp.logger.Info("Starting scaffolding cleanup", "count", len(bp.ScaffoldHistory))
	bp.bot.SendSafeChat("Aku beresin scaffolding temporary dulu ya.")

	// Reverse order cleanup
	for i := len(bp.ScaffoldHistory) - 1; i >= 0; i-- {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pos := bp.ScaffoldHistory[i]
		botPos := bp.bot.GetCoords()

		dx := float32(pos.X()) + 0.5 - botPos.X()
		dy := float32(pos.Y()) + 0.5 - botPos.Y()
		dz := float32(pos.Z()) + 0.5 - botPos.Z()
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

		if dist > 4.5 {
			bp.bot.NavigateToBlock(pos.X(), pos.Y(), pos.Z(), 3.0)
			time.Sleep(300 * time.Millisecond)
		}

		bp.digBlock(ctx, pos)
		bp.bot.GetLocalWorldModel().SetSolid(pos.X(), pos.Y(), pos.Z(), false)
		time.Sleep(200 * time.Millisecond)
	}

	bp.ScaffoldHistory = nil
	bp.logger.Info("Scaffold cleanup complete")
}

// helper tools

func (bp *BlockPlacer) clearObstructions(ctx context.Context, x, y, z int) {
	world := bp.bot.GetLocalWorldModel()
	pos := protocol.BlockPos{int32(x), int32(y), int32(z)}

	// In bedrock-ai local world model, if the block is solid but it's a soft obstruction
	// (like grass, flower, double plant), we want to break it first.
	// Since we don't have perfect block metadata, we check if it is solid or not.
	// If it has solid=true, we try to clear it.
	if world.IsSolid(int32(x), int32(y), int32(z)) {
		bp.logger.Info("Clearing block obstruction at placement site", "x", x, "y", y, "z", z)
		bp.digBlock(ctx, pos)
		world.SetSolid(int32(x), int32(y), int32(z), false)
	}
}

func (bp *BlockPlacer) digBlock(ctx context.Context, pos protocol.BlockPos) {
	_ = bp.bot.WritePacket(&packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
	})
	_ = bp.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionStartBreak,
		BlockPosition:   pos,
		BlockFace:       1,
	})
	time.Sleep(300 * time.Millisecond)
	_ = bp.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionCrackBreak,
		BlockPosition:   pos,
		BlockFace:       1,
	})
	_ = bp.bot.WritePacket(&packet.PlayerAction{
		EntityRuntimeID: bp.bot.GetEntityRuntimeID(),
		ActionType:      protocol.PlayerActionPredictDestroyBlock,
		BlockPosition:   pos,
		BlockFace:       1,
	})
	time.Sleep(100 * time.Millisecond)
}

func (bp *BlockPlacer) lookAtBlock(pos protocol.BlockPos) {
	bp.bot.LookAt(mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5})
}

func (bp *BlockPlacer) placeSpecialBlock(ctx context.Context, x, y, z int, name string) bool {
	inv := bp.bot.GetInventorySlots()
	names := bp.bot.GetItemNames()

	var toolName string
	if name == "farmland" {
		toolName = "hoe"
	} else if name == "dirt_path" {
		toolName = "shovel"
	}

	var toolSlot uint32
	found := false

	for slot, stack := range inv {
		if stack.Count > 0 {
			n := strings.ToLower(names[stack.NetworkID])
			if strings.Contains(n, toolName) {
				toolSlot = slot
				found = true
				break
			}
		}
	}

	if !found {
		bp.logger.Warn("Special block requested but tool not found in inventory", "block", name, "tool", toolName)
		return false
	}

	_ = bp.bot.EquipItem(toolSlot)
	time.Sleep(150 * time.Millisecond)

	targetPos := protocol.BlockPos{int32(x), int32(y - 1), int32(z)}
	bp.lookAtBlock(targetPos)
	time.Sleep(100 * time.Millisecond)

	// Use item on block click transaction to till/path
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   targetPos,
			BlockFace:       1,
			HotBarSlot:      int32(bp.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{Stack: inv[toolSlot]},
			Position:        bp.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	_ = bp.bot.WritePacket(tx)
	time.Sleep(200 * time.Millisecond)

	bp.bot.GetLocalWorldModel().SetSolid(int32(x), int32(y), int32(z), true)
	return true
}
