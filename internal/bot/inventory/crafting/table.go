// Package crafting handles bench-required (3×3) crafting flows: locating or
// placing a crafting_table block, walking adjacent to it, opening its UI, and
// closing it after the craft transaction completes. 2×2 inventory recipes
// (recipe.Block == "") bypass this package entirely and call Bot.CraftItem
// directly.
package crafting

import (
	"context"
	"errors"
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

// Bot is the subset of *bot.Bot required to drive the crafting workflow.
type Bot interface {
	GetCoords() mgl32.Vec3
	GetBlockName(x, y, z int32) (string, bool)
	GetLocalWorldModel() entity.WorldModel
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	GetHeldItemSlot() uint32
	GetEntityRuntimeID() uint64
	EquipItem(slot uint32) error
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	WritePacket(pk packet.Packet) error
	SendChat(msg string)
	FindItemSlotByName(name string) (uint32, bool)
	CraftItem(recipeNetID uint32, count int) error
	GetRecipes() map[string]uint32
}

// Manager runs the EnsureCraftingTable / OpenCraftingTable / CloseWindow
// sequence required for 3×3 recipes.
type Manager struct {
	bot    Bot
	logger *slog.Logger

	// nextWindowID rotates through 1..127 (ContainerID range used for
	// player-opened blocks). Vanilla starts at 1 and increments.
	nextWindowID byte
}

func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{bot: bot, logger: logger, nextWindowID: 1}
}

// EnsureCraftingTable returns a crafting_table block position the bot can
// interact with. If a placed table exists within scanRadius blocks the bot
// navigates to its side and reuses it; otherwise the bot places one from its
// inventory in the nearest valid adjacent tile.
//
// Returns ok=false (and a logged warning + chat message) when no table is
// reachable AND no crafting_table item is available to place.
func (m *Manager) EnsureCraftingTable(ctx context.Context) (protocol.BlockPos, bool) {
	return m.EnsureCraftingTableForItem(ctx, "")
}

// EnsureCraftingTableForItem is like EnsureCraftingTable but takes targetItem
// to handle special cases like crafting_table itself. When targetItem is
// "crafting_table" and no table block/item is available, the bot first crafts
// oak_planks from oak_log using 2x2 inventory crafting, then places the planks
// to create a temporary table block.
func (m *Manager) EnsureCraftingTableForItem(ctx context.Context, targetItem string) (protocol.BlockPos, bool) {
	if pos, ok := m.findExistingTable(8); ok {
		// Walk to a tile adjacent to the table so we can interact.
		approach := pickStandableAdjacent(m.bot, pos)
		if approach == nil {
			m.logger.Warn("found existing crafting_table but no standable side", "pos", pos)
		} else {
			if !m.bot.NavigateToBlock(approach.X(), approach.Y(), approach.Z(), 1.5) {
				m.logger.Warn("could not reach existing crafting_table", "pos", pos)
			}
			m.bot.StopMovement()
			return pos, true
		}
	}

	// No reachable table — place one if we have it in inventory.
	tableSlot, ok := m.findCraftingTableInInventory()
	if !ok {
		// Special case: if target is "crafting_table" and we have oak_log,
		// craft oak_planks first using 2x2 inventory crafting, then place.
		if targetItem == "crafting_table" || targetItem == "crafting table" {
			if m.craftPlanksFromLogs(ctx, m.bot.GetRecipes()) {
				// Now retry finding a table item in inventory
				tableSlot, ok = m.findCraftingTableInInventory()
			}
		}
		if !ok {
			m.bot.SendChat("Aku gak punya crafting_table dan gak nemu juga di sekitar.")
			return protocol.BlockPos{}, false
		}
	}

	placePos, supportPos, faceID, found := m.findPlacementSpot()
	if !found {
		m.bot.SendChat("Aku gak nemu tempat kosong buat naruh crafting_table.")
		return protocol.BlockPos{}, false
	}

	if err := m.placeCraftingTable(ctx, tableSlot, placePos, supportPos, faceID); err != nil {
		m.logger.Warn("failed to place crafting_table", "err", err)
		m.bot.SendChat("Aku gagal naruh crafting_table.")
		return protocol.BlockPos{}, false
	}
	m.logger.Info("placed crafting_table", "pos", placePos)
	return placePos, true
}

// OpenCraftingTable sends the interact packet that asks the server to open
// the crafting_table window at pos. The bot must already be standing within
// 4 blocks. Waits briefly so the server's ContainerOpen response can arrive
// before the caller fires CraftItem.
func (m *Manager) OpenCraftingTable(ctx context.Context, pos protocol.BlockPos) error {
	m.bot.LookAt(mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5})
	if !sleepCtx(ctx, 150*time.Millisecond) {
		return errors.New("cancelled")
	}

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   pos,
			BlockFace:       1,
			HotBarSlot:      int32(m.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{},
			Position:        m.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 0.5, 0.5},
		},
	}
	if err := m.bot.WritePacket(tx); err != nil {
		return fmt.Errorf("interact crafting_table: %w", err)
	}
	m.logger.Info("opened crafting_table", "pos", pos)

	// Server typically replies with ContainerOpen within ~150ms. We don't
	// block on the actual packet — caller is fine with optimistic open since
	// CraftItem's StackRequest carries the recipe network ID server already
	// associates with crafting_table by class.
	if !sleepCtx(ctx, 200*time.Millisecond) {
		return errors.New("cancelled")
	}
	return nil
}

// CloseWindow sends a ContainerClose for the most recently opened
// crafting_table session. Called after CraftItem returns.
func (m *Manager) CloseWindow() {
	pk := &packet.ContainerClose{
		WindowID:   m.nextWindowID,
		ServerSide: false,
	}
	_ = m.bot.WritePacket(pk)
}

func (m *Manager) findExistingTable(radius int32) (protocol.BlockPos, bool) {
	pos := m.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	var best protocol.BlockPos
	bestDist := math.MaxFloat64
	found := false
	for dx := -radius; dx <= radius; dx++ {
		for dy := int32(-2); dy <= 3; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				name, ok := m.bot.GetBlockName(bx+dx, by+dy, bz+dz)
				if !ok || !strings.Contains(strings.ToLower(name), "crafting_table") {
					continue
				}
				d := float64(dx*dx + dy*dy + dz*dz)
				if d < bestDist {
					bestDist = d
					best = protocol.BlockPos{bx + dx, by + dy, bz + dz}
					found = true
				}
			}
		}
	}
	return best, found
}

func (m *Manager) findCraftingTableInInventory() (uint32, bool) {
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()
	for slot, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		name := strings.ToLower(names[stack.NetworkID])
		if strings.Contains(name, "crafting_table") {
			return slot, true
		}
	}
	return 0, false
}

// findPlacementSpot picks an empty tile in one of the 4 horizontal neighbours
// of the bot whose tile below is solid, the tile itself is empty, and the
// tile above is empty (so the table doesn't suffocate the bot's head).
//
// Returns (place, support, face, ok) where:
//   - place is the tile the table will occupy
//   - support is the solid tile under it (block we click)
//   - face is the face of `support` we click (always 1 = top)
func (m *Manager) findPlacementSpot() (protocol.BlockPos, protocol.BlockPos, int32, bool) {
	world := m.bot.GetLocalWorldModel()
	pos := m.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	offsets := []protocol.BlockPos{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}}
	for _, off := range offsets {
		place := protocol.BlockPos{bx + off.X(), by, bz + off.Z()}
		support := protocol.BlockPos{place.X(), place.Y() - 1, place.Z()}

		// Tile we want to place INTO must be empty.
		if world.IsSolid(place.X(), place.Y(), place.Z()) {
			continue
		}
		// Block above must also be empty (table is full-cube).
		if world.IsSolid(place.X(), place.Y()+1, place.Z()) {
			continue
		}
		// Support tile underneath must be solid so we have something to click.
		if !world.IsSolid(support.X(), support.Y(), support.Z()) {
			continue
		}
		return place, support, 1, true
	}
	return protocol.BlockPos{}, protocol.BlockPos{}, 0, false
}

func (m *Manager) placeCraftingTable(ctx context.Context, slot uint32, place, support protocol.BlockPos, face int32) error {
	if err := m.bot.EquipItem(slot); err != nil {
		return fmt.Errorf("equip: %w", err)
	}
	if !sleepCtx(ctx, 150*time.Millisecond) {
		return errors.New("cancelled")
	}
	m.bot.LookAt(mgl32.Vec3{float32(support.X()) + 0.5, float32(support.Y()) + 1.0, float32(support.Z()) + 0.5})
	if !sleepCtx(ctx, 100*time.Millisecond) {
		return errors.New("cancelled")
	}

	inv := m.bot.GetInventorySlots()
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:      protocol.UseItemActionClickBlock,
			BlockPosition:   support,
			BlockFace:       face,
			HotBarSlot:      int32(m.bot.GetHeldItemSlot()),
			HeldItem:        protocol.ItemInstance{Stack: inv[slot]},
			Position:        m.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{0.5, 1.0, 0.5},
		},
	}
	if err := m.bot.WritePacket(tx); err != nil {
		return fmt.Errorf("place packet: %w", err)
	}
	// Optimistic world-model update so subsequent EnsureCraftingTable scans
	// see this block even before the server's chunk diff lands.
	m.bot.GetLocalWorldModel().SetSolid(place.X(), place.Y(), place.Z(), true)
	if !sleepCtx(ctx, 250*time.Millisecond) {
		return errors.New("cancelled")
	}
	return nil
}

// pickStandableAdjacent picks the first 4-cardinal neighbour of pos where the
// bot can stand (tile empty, tile above empty, tile below solid). Returns
// nil when no side qualifies.
func pickStandableAdjacent(bot Bot, pos protocol.BlockPos) *protocol.BlockPos {
	world := bot.GetLocalWorldModel()
	offsets := []protocol.BlockPos{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}}
	for _, off := range offsets {
		x := pos.X() + off.X()
		y := pos.Y()
		z := pos.Z() + off.Z()
		if world.IsSolid(x, y, z) || world.IsSolid(x, y+1, z) {
			continue
		}
		if !world.IsSolid(x, y-1, z) {
			continue
		}
		p := protocol.BlockPos{x, y, z}
		return &p
	}
	return nil
}

// craftPlanksFromLogs crafts oak_planks from oak_log using 2x2 inventory
// crafting (no crafting table needed). Returns true if planks were crafted.
func (m *Manager) craftPlanksFromLogs(ctx context.Context, botRecipes map[string]uint32) bool {
	// Find oak_log in inventory
	logSlot, ok := m.bot.FindItemSlotByName("oak_log")
	if !ok {
		m.logger.Info("no oak_log found, cannot craft planks for crafting_table")
		return false
	}

	// Find oak_planks recipe (2x2, Block == "")
	planksRecipeID, ok := botRecipes["oak_planks"]
	if !ok {
		planksRecipeID, ok = botRecipes["minecraft:oak_planks"]
	}
	if !ok {
		m.logger.Warn("no oak_planks recipe found")
		return false
	}

	// Equip the oak_log
	if err := m.bot.EquipItem(logSlot); err != nil {
		m.logger.Warn("failed to equip oak_log", "err", err)
		return false
	}
	if !sleepCtx(ctx, 150*time.Millisecond) {
		return false
	}

	// Craft oak_planks using 2x2 inventory crafting
	if err := m.bot.CraftItem(planksRecipeID, 1); err != nil {
		m.logger.Warn("failed to craft oak_planks", "err", err)
		return false
	}
	m.logger.Info("crafted oak_planks from oak_log for crafting_table setup")
	return true
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
