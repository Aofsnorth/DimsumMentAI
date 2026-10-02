// Package station drives the advanced block stations a survival bot needs:
// the brewing stand, the enchanting table, the anvil, and the grindstone.
//
// Every action here follows the same server-authoritative shape the furnace
// uses. A station is found by block name — never by IsSolid, which is how a
// bot ends up "brewing" in a wall — then the bot walks into reach, looks at
// it, arms the container watch before the click (ContainerOpen can arrive
// within a frame of the click), clicks, and waits for the server to assign a
// window ID. Items move with ItemStackRequests through PlaceIntoContainerSlot
// and TakeFromContainerSlot, never a legacy NormalTransactionData. The window
// is always closed by the ID the server assigned, and the action returns true
// only after the result has been read back out of server state.
//
// The four stations share one open/close/confirm helper rather than copying the
// furnace four times; what differs between them is the slot layout, the recipe,
// and the confirmation, and those live in the per-station files as pure
// functions so they can be tested without a connection.
//
// The package deliberately declares narrow interfaces instead of importing
// bedrock-ai/internal/bot, which would be an import cycle and is rejected by
// go run ./cmd/archcheck.
package station

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

const (
	// searchRadius is how far around the bot a station is looked for. It is
	// deliberately not a wide scan: a station across the room is not one this
	// action should walk the whole map for.
	searchRadius = int32(8)

	// openTimeout bounds the wait for the server to open a station window
	// after the click. A LAN host answers in well under a second.
	openTimeout = 2 * time.Second

	// openReach is the distance beyond which the bot walks to the station
	// before clicking it.
	openReach = 3.5

	// defaultPollInterval is how often an open window is re-read while
	// waiting for a result the server computes.
	defaultPollInterval = 200 * time.Millisecond
)

// Bot is the slice of the bot the station actions need. It mirrors the
// container session the storage package drives, so every subsystem agrees on
// how a window is opened, addressed, and closed.
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

	// The In variants address a container by its protocol container ID. A
	// station's slots resolve against per-station constants
	// (protocol.ContainerAnvilInput and friends), not against the window ID the
	// server assigned, so these are the methods a station must use.
	PlaceIntoContainerSlotIn(containerID byte, containerSlot uint32, destStackNetID int32, srcSlot uint32, count int) error
	TakeFromContainerSlotIn(containerID byte, slot uint32, count int, stackNetID int32, itemName string) error
}

// Manager runs the station actions against a bot. One manager serves all four
// stations: the open/close/confirm machinery is identical, and only the slot
// layout and the recipe differ.
type Manager struct {
	bot    Bot
	logger *slog.Logger

	// pollInterval is how often an open window is re-read while waiting for a
	// server-computed result.
	pollInterval time.Duration

	// The budgets below are fields rather than constants so a test can shorten
	// a wait that would otherwise be measured in seconds; production leaves
	// them zero and lets the per-action default stand.
	brewBudget       time.Duration
	anvilBudget      time.Duration
	grindstoneBudget time.Duration
	enchantBudget    time.Duration

	// enchanter is the seam for the enchanting table. See Enchanter.
	enchanter Enchanter
}

// NewManager builds a station manager for a bot.
func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{
		bot:          bot,
		logger:       logger,
		pollInterval: defaultPollInterval,
	}
}

// SetPollInterval overrides how often an open window is re-read.
func (m *Manager) SetPollInterval(d time.Duration) {
	if d > 0 {
		m.pollInterval = d
	}
}

// SetBrewBudget overrides the wait for a brewing stand cycle.
func (m *Manager) SetBrewBudget(d time.Duration) { m.brewBudget = d }

// SetAnvilBudget overrides the wait for an anvil to produce its output.
func (m *Manager) SetAnvilBudget(d time.Duration) { m.anvilBudget = d }

// SetGrindstoneBudget overrides the wait for a grindstone to produce its output.
func (m *Manager) SetGrindstoneBudget(d time.Duration) { m.grindstoneBudget = d }

// SetEnchantBudget overrides the wait for the enchanting table to return the
// enchanted item.
func (m *Manager) SetEnchantBudget(d time.Duration) { m.enchantBudget = d }

// SetEnchanter wires the enchanting table seam. Without it, EnchantItem refuses
// rather than inventing an enchantment.
func (m *Manager) SetEnchanter(e Enchanter) { m.enchanter = e }

// stationSession is one open station window, keyed by the window ID the server
// assigned rather than a hardcoded 0.
type stationSession struct {
	windowID byte
	pos      protocol.BlockPos
}

// openStation walks into reach of the block, looks at it, arms the container
// watch before the click, clicks, and waits for the window the server assigns.
//
// The watch-before-click ordering is not a style preference: ContainerOpen can
// arrive within a frame of the click, and a watch armed afterwards misses the
// window entirely and the action hangs until it times out.
func (m *Manager) openStation(ctx context.Context, pos protocol.BlockPos) (stationSession, error) {
	if m.bot.GetCoords().Sub(blockCenter(pos)).Len() > openReach {
		if !m.bot.NavigateToBlock(pos.X(), pos.Y(), pos.Z(), 3.0) {
			return stationSession{}, errors.New("could not walk to the station")
		}
		m.bot.StopMovement()
	}
	m.bot.LookAt(blockCenter(pos))

	m.bot.BeginContainerWatch()
	if ok, reason := m.bot.ClickBlockAt(ctx, pos); !ok {
		// The look has already been applied, so it has to be undone even on the
		// failure path: a bot left staring at a block it never opened will keep
		// staring there for every later action.
		m.bot.ResetLook()
		return stationSession{}, fmt.Errorf("click station: %s", reason)
	}

	windowID, openedPos, ok := m.bot.WaitContainerOpen(ctx, openTimeout)
	if !ok {
		m.bot.ResetLook()
		return stationSession{}, errors.New("server did not open the station window")
	}
	m.logger.Debug("opened station", "pos", openedPos, "window_id", windowID)
	return stationSession{windowID: windowID, pos: openedPos}, nil
}

// closeStation releases the window and the look. Both are deferred on every
// path, including failure: a station left open server-side desyncs the next
// open, and a bot still staring at the anvil never looks at anything again.
func (m *Manager) closeStation(s stationSession) {
	if s.windowID != 0 {
		m.bot.CloseContainerWindow(s.windowID)
	}
	m.bot.ResetLook()
}

// FindNearbyStation returns the closest block the predicate accepts, by name.
func (m *Manager) FindNearbyStation(matches func(string) bool) (protocol.BlockPos, bool) {
	pos := m.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	var best protocol.BlockPos
	bestDist := float64(-1)
	found := false

	for dx := -searchRadius; dx <= searchRadius; dx++ {
		for dy := int32(-2); dy <= 3; dy++ {
			for dz := -searchRadius; dz <= searchRadius; dz++ {
				name, ok := m.bot.GetBlockName(bx+dx, by+dy, bz+dz)
				if !ok || !matches(name) {
					continue
				}
				d := dist2(dx, dy, dz)
				if bestDist < 0 || d < bestDist {
					bestDist = d
					best = protocol.BlockPos{bx + dx, by + dy, bz + dz}
					found = true
				}
			}
		}
	}
	return best, found
}

// stackView is a slot's contents as read from the server.
type stackView struct {
	name       string
	count      int
	stackNetID int32
}

// readSlot re-reads the open window and reports one slot's contents. The
// window is re-read fresh every time because every result here is
// server-computed, so the only honest question is what the server has sent.
func (m *Manager) readSlot(slot uint32) (stackView, bool) {
	for s, item := range m.bot.ContainerItems() {
		if s != slot || item.Stack.Count <= 0 {
			continue
		}
		return stackView{
			name:       m.bot.ContainerItemName(item),
			count:      int(item.Stack.Count),
			stackNetID: item.StackNetworkID,
		}, true
	}
	return stackView{}, false
}

// waitForSlot polls an open window until slot holds a stack the predicate
// accepts, or the budget runs out. It returns false rather than guessing.
func (m *Manager) waitForSlot(ctx context.Context, slot uint32, budget time.Duration, accept func(stackView) bool) (stackView, bool) {
	deadline := time.NewTimer(budget)
	defer deadline.Stop()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		if view, ok := m.readSlot(slot); ok && accept(view) {
			return view, true
		}

		select {
		case <-ctx.Done():
			return stackView{}, false
		case <-deadline.C:
			return stackView{}, false
		case <-ticker.C:
		}
	}
}

// waitForSlotEmpty polls an open window until slot holds nothing. This is the
// signal a brewing stand gives for a finished cycle: the server consumes the
// ingredient, so an empty ingredient slot is the cycle's own confirmation
// rather than a sleep the bot has to guess at.
func (m *Manager) waitForSlotEmpty(ctx context.Context, slot uint32, budget time.Duration) bool {
	deadline := time.NewTimer(budget)
	defer deadline.Stop()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		if _, ok := m.readSlot(slot); !ok {
			return true
		}

		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

// takeSlot moves a confirmed result out of the window into the bot's
// inventory. The take is a server-authoritative ItemStackRequest; a refusal
// here is a failure, not a silent no-op.
//
// containerID is the station's own result container, not the window ID — see the
// constants in layout.go for why the two are not interchangeable.
func (m *Manager) takeSlot(s stationSession, containerID byte, slot uint32) (stackView, error) {
	view, ok := m.readSlot(slot)
	if !ok {
		return stackView{}, fmt.Errorf("slot %d is empty", slot)
	}
	if err := m.bot.TakeFromContainerSlotIn(containerID, slot, view.count, view.stackNetID, view.name); err != nil {
		return stackView{}, err
	}
	return view, nil
}

// invStack is an inventory slot as read from the server.
type invStack struct {
	slot  uint32
	count int
	name  string
}

// findInInventory locates a stack in the bot's inventory by exact name.
func (m *Manager) findInInventory(itemName string) (invStack, bool) {
	want := NormalizeItemName(itemName)
	inv := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()
	for s, stack := range inv {
		if stack.Count <= 0 {
			continue
		}
		name := NormalizeItemName(names[stack.NetworkID])
		if name == want {
			return invStack{slot: s, count: int(stack.Count), name: name}, true
		}
	}
	return invStack{}, false
}

// NormalizeItemName strips the namespace and case from a runtime item name, so
// "minecraft:Lapis_Lazuli" and "lapis_lazuli" are the same key.
func NormalizeItemName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.TrimPrefix(n, "minecraft:")
}

// ItemMatches reports whether a runtime name is the wanted item. It is an exact
// match on the normalised name on purpose: the furnace's substring matcher
// treats "iron" as matching "iron_ingot", which is right for fuel and wrong for
// a repair material, where an iron pickaxe is emphatically not an iron ingot.
func ItemMatches(have, want string) bool {
	h := NormalizeItemName(have)
	w := NormalizeItemName(want)
	return h != "" && h == w
}

// normalizeBlockName strips the namespace and case from a block name.
func normalizeBlockName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.TrimPrefix(n, "minecraft:")
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
