// Package crafting handles bench-required (3×3) crafting flows: locating or
// placing a crafting_table block, walking adjacent to it, opening its UI, and
// closing it after the craft transaction completes. 2×2 inventory recipes
// (recipe.Block == "") bypass this package entirely and call Bot.CraftItem
// directly.
package crafting

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/placement"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// containerOpenTimeout bounds the wait for the server to open the workbench
// window after the click. A LAN host answers in well under a second; a host
// that never opens it would otherwise be waited on for the length of a craft.
const containerOpenTimeout = 2 * time.Second

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
	ReportActionStatus(user string, status event.ActionStatus)
	FindItemSlotByName(name string) (uint32, bool)
	CraftItem(recipeNetID uint32, count int) error
	GetRecipes() map[string]uint32
	PlaceBlock(ctx context.Context, request placement.Request) error
	// BeginContainerWatch arms the packet capture that WaitContainerOpen reads,
	// so the server's ContainerOpen cannot be missed between click and wait.
	BeginContainerWatch()
	// ClickBlockAt runs the proven block click (aim convergence, wire block ID,
	// swing, and the inline PlayerAuthInput fallback for hosts that ignore a
	// standalone transaction).
	ClickBlockAt(ctx context.Context, pos protocol.BlockPos) (bool, string)
	// WaitContainerOpen blocks until the server assigns a window ID, which is
	// the only proof the workbench is actually open.
	WaitContainerOpen(ctx context.Context, timeout time.Duration) (byte, protocol.BlockPos, bool)
	// CloseContainerWindow closes the window the server assigned, by that ID.
	CloseContainerWindow(windowID byte)
}

// Manager runs the EnsureCraftingTable / OpenCraftingTable / CloseWindow
// sequence required for 3×3 recipes.
type Manager struct {
	bot    Bot
	logger *slog.Logger

	// windowID is the workbench window the server assigned. Zero means no
	// workbench is open, so CloseWindow has nothing to close. It is never
	// guessed: the bot used to close window 1 on every craft while the server
	// had assigned a different ID, which left the workbench open server-side.
	windowID byte
}

func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{bot: bot, logger: logger}
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
			m.bot.ReportActionStatus("", event.ActionStatus{Action: "craft", Item: "crafting_table", Success: false, Error: "no crafting table available"})
			return protocol.BlockPos{}, false
		}
	}

	placePos, supportPos, faceID, found := m.findPlacementSpot()
	if !found {
		m.bot.ReportActionStatus("", event.ActionStatus{Action: "craft", Item: "crafting_table", Success: false, Error: "no empty spot"})
		return protocol.BlockPos{}, false
	}

	if err := m.placeCraftingTable(ctx, tableSlot, placePos, supportPos, faceID); err != nil {
		m.logger.Warn("failed to place crafting_table", "err", err)
		m.bot.ReportActionStatus("", event.ActionStatus{Action: "craft", Item: "crafting_table", Success: false, Error: "failed to place"})
		return protocol.BlockPos{}, false
	}
	m.logger.Info("placed crafting_table", "pos", placePos)
	return placePos, true
}

// OpenCraftingTable opens the crafting_table at pos and returns only once the
// server has confirmed the window. The bot must already be standing within
// 4 blocks.
//
// Confirmation is the point: staging ingredients into the 3×3 input is only
// legal while the workbench is open, and a click that never registers leaves
// the server looking at the personal 2×2 grid. Sleeping a fixed interval and
// assuming the best is what made those crafts fail.
func (m *Manager) OpenCraftingTable(ctx context.Context, pos protocol.BlockPos) error {
	if m.windowID != 0 {
		m.CloseWindow()
	}

	// Arm the capture before the click: ContainerOpen can arrive within a frame.
	m.bot.BeginContainerWatch()
	if ok, reason := m.bot.ClickBlockAt(ctx, pos); !ok {
		return fmt.Errorf("interact crafting_table: %s", reason)
	}

	windowID, openedPos, ok := m.bot.WaitContainerOpen(ctx, containerOpenTimeout)
	if !ok {
		return fmt.Errorf("server tidak membuka crafting table di %d,%d,%d", pos.X(), pos.Y(), pos.Z())
	}
	m.windowID = windowID
	m.logger.Info("opened crafting_table", "pos", openedPos, "window_id", windowID)
	return nil
}

// WindowID reports the workbench window the server assigned, or zero when no
// workbench is open.
func (m *Manager) WindowID() byte {
	return m.windowID
}

// CloseWindow closes the workbench window the server assigned. Called after
// CraftItem returns. A real client always closes what it opens, and the ID has
// to be the assigned one — a guessed ID closes nothing and leaves the server
// holding an open workbench.
func (m *Manager) CloseWindow() {
	if m.windowID == 0 {
		return
	}
	m.bot.CloseContainerWindow(m.windowID)
	m.windowID = 0
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

// findPlacementSpot picks an empty tile in one of the 4 horizontal neighbors
// of the bot whose tile below is solid, the tile itself is empty, and the
// tile above is empty (so the table doesn't suffocate the bot's head).
//
// Returns (place, support, face, ok) where:
//   - place is the tile the table will occupy
//   - support is the solid tile under it (block we click)
//   - face is the face of `support` we click (always 1 = top)
func blockCollidesWithBot(blockPos protocol.BlockPos, botPos mgl32.Vec3) bool {
	botMinX := botPos.X() - 0.3
	botMaxX := botPos.X() + 0.3
	botMinY := botPos.Y()
	botMaxY := botPos.Y() + 1.8
	botMinZ := botPos.Z() - 0.3
	botMaxZ := botPos.Z() + 0.3

	bMinX := float32(blockPos.X())
	bMaxX := float32(blockPos.X() + 1)
	bMinY := float32(blockPos.Y())
	bMaxY := float32(blockPos.Y() + 1)
	bMinZ := float32(blockPos.Z())
	bMaxZ := float32(blockPos.Z() + 1)

	overlapX := botMinX < bMaxX && botMaxX > bMinX
	overlapY := botMinY < bMaxY && botMaxY > bMinY
	overlapZ := botMinZ < bMaxZ && botMaxZ > bMinZ

	return overlapX && overlapY && overlapZ
}

func (m *Manager) findPlacementSpot() (protocol.BlockPos, protocol.BlockPos, int32, bool) {
	world := m.bot.GetLocalWorldModel()
	pos := m.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	offsets := []protocol.BlockPos{
		{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1},
		{2, 0, 0}, {-2, 0, 0}, {0, 0, 2}, {0, 0, -2},
	}
	for _, off := range offsets {
		place := protocol.BlockPos{bx + off.X(), by, bz + off.Z()}
		support := protocol.BlockPos{place.X(), place.Y() - 1, place.Z()}

		if blockCollidesWithBot(place, pos) {
			continue
		}
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
	return m.bot.PlaceBlock(ctx, placement.Request{
		InventorySlot: slot,
		Destination:   place,
		Support:       support,
		Face:          face,
		ClickedOffset: mgl32.Vec3{0.5, 1, 0.5},
	})
}

// pickStandableAdjacent picks the first 4-cardinal neighbor of pos where the
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
