// Package furnace smelts items in a nearby furnace, blast furnace, or smoker.
//
// It follows the same server-authoritative shape the storage package uses: click
// the block, wait for the server to assign a window, read the real slot layout,
// move items with an ItemStackRequest, and confirm the result before reporting
// success. The previous version of this file did none of that — it picked the
// first solid block it could see, guessed window 0, wrote a legacy
// NormalTransactionData, and reported success without ever looking at the
// output — so a smelt "succeeded" while nothing was smelted.
package furnace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Furnace window slot layout. Bedrock numbers a furnace window 0=input, 1=fuel,
// 2=output, and a blast furnace or smoker uses the same layout. Writing an item
// into the wrong one of these does nothing at all, so the layout lives in named
// constants that the tests pin.
const (
	SlotInput  uint32 = 0
	SlotFuel   uint32 = 1
	SlotOutput uint32 = 2
)

const (
	// searchRadius is how far around the bot a furnace is looked for. It is
	// deliberately not a wide scan: a furnace across the room is not one this
	// action should walk the whole map for.
	searchRadius = int32(8)

	// openTimeout bounds the wait for the server to open the furnace window
	// after the click. A LAN host answers in well under a second.
	openTimeout = 2 * time.Second

	// openReach is the distance beyond which the bot walks to the furnace
	// before clicking it.
	openReach = 3.5

	// smeltTimeoutPerItem is how long one item is allowed to take. A vanilla
	// furnace needs 10s per item, so a stack of ore genuinely takes a couple of
	// minutes and a single fixed timeout would report failure on a full stack.
	smeltTimeoutPerItem = 10 * time.Second

	// smeltTimeoutFloor is the budget for one item plus a margin, used when the
	// count is unknown or zero.
	smeltTimeoutFloor = 12 * time.Second
)

// fuels are the item names accepted as furnace fuel, most efficient first. One
// coal smelts eight items while one plank smelts one, so the order decides how
// quickly a stack of ore burns through its fuel.
var fuels = []string{
	"coal", "charcoal", "lava_bucket", "blaze_rod",
	"oak_planks", "spruce_planks", "birch_planks", "jungle_planks",
	"acacia_planks", "dark_oak_planks", "planks",
}

// Bot is the slice of the bot that smelting needs. It mirrors the container
// session the storage package drives, so both subsystems agree on how a window
// is opened, addressed, and closed.
type Bot interface {
	GetCoords() mgl32.Vec3
	GetBlockName(x, y, z int32) (string, bool)
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	ResetLook()

	BeginContainerWatch()
	ClickBlockAt(ctx context.Context, pos protocol.BlockPos) (bool, string)
	WaitContainerOpen(ctx context.Context, timeout time.Duration) (byte, protocol.BlockPos, bool)
	ContainerItems() map[uint32]protocol.ItemInstance
	ContainerItemName(item protocol.ItemInstance) string
	CloseContainerWindow(windowID byte)

	PlaceIntoContainerSlot(windowID byte, containerSlot uint32, destStackNetID int32, srcSlot uint32, count int) error
	TakeFromContainerSlot(windowID byte, slot uint32, count int, stackNetID int32, itemName string) error
}

// Manager runs the smelt sequence for a nearby furnace.
type Manager struct {
	bot    Bot
	logger *slog.Logger

	// smeltBudget overrides the computed wait for the output. It is a field so a
	// test can shorten the wait; production leaves it zero and lets
	// smeltTimeoutFor decide from the stack count.
	smeltBudget time.Duration
	// pollInterval is how often the open window is re-read while waiting.
	pollInterval time.Duration
}

func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{
		bot:          bot,
		logger:       logger,
		pollInterval: 200 * time.Millisecond,
	}
}

// SetSmeltBudget overrides the wait for the smelt output. Production leaves the
// budget zero and lets smeltTimeoutFor decide from the stack count; the override
// exists so a test can shorten a wait that would otherwise be measured in
// minutes.
func (fm *Manager) SetSmeltBudget(budget time.Duration) {
	fm.smeltBudget = budget
}

// IsFurnaceBlock reports whether a block name is a block that can be smelted
// in: a furnace, blast furnace, or smoker, lit or unlit.
//
// This is the fix for the original search, which returned the first block that
// was merely solid. Any wall, any stone, any plank the bot stood next to
// satisfied that test, and the bot then "smelted" in a wall.
func IsFurnaceBlock(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimPrefix(n, "minecraft:")
	switch n {
	case "furnace", "lit_furnace", "blast_furnace", "lit_blast_furnace", "smoker", "lit_smoker":
		return true
	}
	return false
}

// SmeltItem smelts a stack of itemName in a nearby furnace and reports whether
// the output was actually confirmed.
//
// The contract is deliberately narrow: it returns true only when the server
// delivered the smelted result and the bot took it into its inventory. Every
// other outcome — no furnace, no window, no fuel, a timeout with no output —
// is a false with the reason logged. An action layer that reports a successful
// smelt that never happened is worse than one that admits failure.
func (fm *Manager) SmeltItem(ctx context.Context, itemName string) bool {
	pos, ok := fm.FindNearbyFurnace()
	if !ok {
		fm.logger.Warn("SmeltItem: no furnace nearby", "item", itemName)
		return false
	}

	rawSlot, rawCount, rawName, ok := fm.findInInventory(itemName)
	if !ok {
		fm.logger.Warn("SmeltItem: item not in inventory", "item", itemName)
		return false
	}

	windowID, err := fm.openFurnace(ctx, pos)
	if err != nil {
		fm.logger.Warn("SmeltItem: could not open furnace", "item", itemName, "err", err)
		return false
	}
	// Close the window the way a client does, by the ID the server assigned, and
	// close it even when the smelt fails — otherwise the furnace stays open
	// server-side and the next open is desynced.
	defer func() {
		fm.bot.CloseContainerWindow(windowID)
		fm.bot.ResetLook()
	}()

	if err := fm.loadFurnace(windowID, rawSlot, rawCount, rawName); err != nil {
		fm.logger.Warn("SmeltItem: could not load furnace", "item", itemName, "err", err)
		return false
	}
	return fm.waitForOutput(ctx, windowID, rawName, rawCount)
}

// FindNearbyFurnace returns the closest block that is actually a furnace, by
// name rather than by solidity.
func (fm *Manager) FindNearbyFurnace() (protocol.BlockPos, bool) {
	pos := fm.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	var best protocol.BlockPos
	bestDist := float64(-1)
	found := false

	for dx := -searchRadius; dx <= searchRadius; dx++ {
		for dy := int32(-2); dy <= 3; dy++ {
			for dz := -searchRadius; dz <= searchRadius; dz++ {
				x, y, z := bx+dx, by+dy, bz+dz
				name, ok := fm.bot.GetBlockName(x, y, z)
				if !ok || !IsFurnaceBlock(name) {
					continue
				}
				d := dist2(dx, dy, dz)
				if bestDist < 0 || d < bestDist {
					bestDist = d
					best = protocol.BlockPos{x, y, z}
					found = true
				}
			}
		}
	}
	return best, found
}

// openFurnace walks into reach, looks at the block, clicks it, and waits for
// the window the server assigns. The window ID is whatever the server says; it
// is never assumed to be 0.
func (fm *Manager) openFurnace(ctx context.Context, pos protocol.BlockPos) (byte, error) {
	if fm.bot.GetCoords().Sub(blockCenter(pos)).Len() > openReach {
		if !fm.bot.NavigateToBlock(pos.X(), pos.Y(), pos.Z(), 3.0) {
			return 0, errors.New("could not walk to the furnace")
		}
		fm.bot.StopMovement()
	}
	fm.bot.LookAt(blockCenter(pos))

	// Arm the session before the click: ContainerOpen can arrive within a frame
	// of the click, and a watch armed afterwards misses the window entirely.
	fm.bot.BeginContainerWatch()
	if ok, reason := fm.bot.ClickBlockAt(ctx, pos); !ok {
		return 0, fmt.Errorf("click furnace: %s", reason)
	}

	windowID, openedPos, ok := fm.bot.WaitContainerOpen(ctx, openTimeout)
	if !ok {
		return 0, errors.New("server did not open the furnace window")
	}
	fm.logger.Debug("opened furnace", "pos", openedPos, "window_id", windowID)
	return windowID, nil
}

// loadFurnace moves the raw material and one fuel item into the window through
// ItemStackRequests. Each placement is server-validated; nothing is assumed to
// have moved.
func (fm *Manager) loadFurnace(windowID byte, rawSlot uint32, rawCount int, rawName string) error {
	// destStackNetID 0 is the empty-slot convention the container session
	// already uses: the server cross-checks it, and a non-empty value here would
	// name a stack that does not exist yet.
	if err := fm.bot.PlaceIntoContainerSlot(windowID, SlotInput, 0, rawSlot, rawCount); err != nil {
		return fmt.Errorf("place %s in input: %w", rawName, err)
	}

	fuelSlot, fuelCount, fuelName, ok := fm.findFuel(rawSlot)
	if !ok {
		return errors.New("no fuel in inventory")
	}
	// A furnace only needs one fuel item to light. Sending the whole stack
	// would leave the remainder sitting in the fuel slot.
	if fuelCount > 1 {
		fuelCount = 1
	}
	if err := fm.bot.PlaceIntoContainerSlot(windowID, SlotFuel, 0, fuelSlot, fuelCount); err != nil {
		return fmt.Errorf("place %s in fuel slot: %w", fuelName, err)
	}
	return nil
}

// waitForOutput polls the open window until the output slot holds something,
// then takes it into the inventory. It returns true only once that take is
// confirmed by the server.
func (fm *Manager) waitForOutput(ctx context.Context, windowID byte, rawName string, count int) bool {
	budget := fm.smeltBudget
	if budget <= 0 {
		budget = smeltTimeoutFor(count)
	}

	deadline := time.NewTimer(budget)
	defer deadline.Stop()
	ticker := time.NewTicker(fm.pollInterval)
	defer ticker.Stop()

	for {
		// Read the window fresh every tick. The smelt is driven by the server,
		// so the only honest question is what the server has sent.
		if out, ok := fm.readOutput(); ok {
			if err := fm.bot.TakeFromContainerSlot(windowID, SlotOutput, out.count, out.stackNetID, out.name); err != nil {
				fm.logger.Warn("smelt: could not take output", "output", out.name, "err", err)
				return false
			}
			fm.logger.Info("smelted item", "input", rawName, "output", out.name, "count", out.count)
			return true
		}

		select {
		case <-ctx.Done():
			fm.logger.Warn("smelt: cancelled while waiting for output", "input", rawName)
			return false
		case <-deadline.C:
			fm.logger.Warn("smelt: no output before timeout", "input", rawName, "count", count)
			return false
		case <-ticker.C:
		}
	}
}

// outputStack is a confirmed result sitting in the furnace's output slot.
type outputStack struct {
	name       string
	count      int
	stackNetID int32
}

// readOutput re-reads the open window and reports the output slot when the
// server has filled it.
func (fm *Manager) readOutput() (outputStack, bool) {
	for slot, item := range fm.bot.ContainerItems() {
		if uint32(slot) != SlotOutput || item.Stack.Count <= 0 {
			continue
		}
		return outputStack{
			name:       fm.bot.ContainerItemName(item),
			count:      int(item.Stack.Count),
			stackNetID: item.StackNetworkID,
		}, true
	}
	return outputStack{}, false
}

// findInInventory locates a stack in the bot's inventory by name.
func (fm *Manager) findInInventory(itemName string) (slot uint32, count int, name string, ok bool) {
	inv := fm.bot.GetInventorySlots()
	names := fm.bot.GetItemNames()
	for s, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		n := names[stack.NetworkID]
		if matchesItem(n, itemName) {
			return s, int(stack.Count), n, true
		}
	}
	return 0, 0, "", false
}

// findFuel picks the best available fuel, skipping the slot already spoken for
// by the raw material so one stack is never used as its own fuel.
func (fm *Manager) findFuel(skip uint32) (slot uint32, count int, name string, ok bool) {
	inv := fm.bot.GetInventorySlots()
	names := fm.bot.GetItemNames()
	for _, fuel := range fuels {
		for s, stack := range inv {
			if stack.Count <= 0 || s == skip {
				continue
			}
			n := names[stack.NetworkID]
			if matchesItem(n, fuel) {
				return s, int(stack.Count), n, true
			}
		}
	}
	return 0, 0, "", false
}

// matchesItem compares a runtime name against a wanted name in either
// direction, so "iron" finds "iron_ore" and "iron_ore" finds "raw_iron".
func matchesItem(have, want string) bool {
	h := strings.ToLower(have)
	w := strings.ToLower(want)
	if h == "" || w == "" {
		return false
	}
	return strings.Contains(h, w) || strings.Contains(w, h)
}

// smeltTimeoutFor budgets the wait from the stack size, because the honest
// answer for a full stack is minutes, not seconds.
func smeltTimeoutFor(count int) time.Duration {
	if count <= 1 {
		return smeltTimeoutFloor
	}
	return time.Duration(count)*smeltTimeoutPerItem + 5*time.Second
}

func blockCenter(pos protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(pos.X()) + 0.5,
		float32(pos.Y()) + 0.5,
		float32(pos.Z()) + 0.5,
	}
}

func dist2(dx, dy, dz int32) float64 {
	return float64(dx*dx + dy*dy + dz*dz)
}
