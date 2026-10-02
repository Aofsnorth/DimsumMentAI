// Package trading buys from and sells to villagers, and reports nothing it has
// not seen the server confirm.
//
// A trade is a sequence with a beginning, a middle and an end, and every step
// of it is server-driven: walk to the villager, send the interaction, wait for
// the window the server assigns, read the offers the server actually sent, stage
// the inputs into the trade containers, wait for the result the server
// computes, take it, and then check the inventory before saying anything.
//
// Two things this package refuses to do, because both are the failure this
// project exists to remove:
//
//   - It does not trade without offers. Offers arrive per villager in
//     packet.UpdateTrade, and nothing in the network layer records that packet
//     yet, so the observer is an explicit seam with an honest default. No
//     observer, or no observed window, means the trade fails with a reason
//     rather than proceeding against an invented price.
//
//   - It does not report a trade it did not see complete. The ItemStackRequest
//     that takes the result being accepted is not the result arriving; the
//     inventory is re-read until the output is there. A server that accepts a
//     request and then goes quiet produces a failure, not a sale.
//
// Experience is treated the same way. An XP level nothing observes is reported
// as unknown, never as zero, and an offer that costs levels is refused rather
// than attempted blind.
package trading

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot is the slice of the bot that trading needs.
//
// It is deliberately narrow and does not include the bot package itself, so a
// caller can supply a stand-in without a connection. Everything here is a
// method internal/bot already has.
type Bot interface {
	GetCoords() mgl32.Vec3
	GetEntities() map[uint64]*entity.Info
	NavigateTo(pos mgl32.Vec3)
	StopMovement()
	LookAt(pos mgl32.Vec3)
	ResetLook()

	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	WritePacket(pk packet.Packet) error

	BeginContainerWatch()
	WaitContainerOpen(ctx context.Context, timeout time.Duration) (byte, protocol.BlockPos, bool)
	ContainerItems() map[uint32]protocol.ItemInstance
	ContainerItemName(item protocol.ItemInstance) string
	CloseContainerWindow(windowID byte)

	// The two In variants take a protocol container ID rather than the window
	// ID. A chest's two coincide; a trade window's do not, and a transfer
	// addressed by window ID is refused by the server.
	PlaceIntoContainerSlotIn(containerID byte, containerSlot uint32, destStackNetID int32, srcSlot uint32, count int) error
	TakeFromContainerSlotIn(containerID byte, slot uint32, count int, stackNetID int32, itemName string) error
}

const (
	// searchRadius is how far around the bot a villager is looked for. It is
	// deliberately not a wide scan: a villager in the next village is not one
	// this action should walk the whole map for.
	searchRadius = 24.0

	// tradeReach is how close the villager has to be before it is clicked.
	// Anything further is walked to first, because an interaction sent from
	// across the room opens nothing.
	tradeReach = 2.5

	// approachDelay is the pause after walking toward the villager, aimDelay the
	// pause after turning to face it, and settle the pause after the
	// interaction before the window is read. All three are settling delays: they
	// exist so the movement tick and the network round trip keep up with the
	// click, and shrinking them changes how fast this runs and nothing about
	// what it decides.
	approachDelay = 600 * time.Millisecond
	aimDelay      = 200 * time.Millisecond
	settleDelay   = 150 * time.Millisecond

	// pollInterval is how often the open window and the inventory are re-read
	// while waiting.
	pollInterval = 100 * time.Millisecond

	// openTimeout bounds the wait for the server to open the trade window.
	openTimeout = 2 * time.Second

	// resultTimeout bounds the wait for the villager to compute a result once
	// the inputs are in, and confirmTimeout the wait for that result to reach
	// the inventory after the take.
	resultTimeout  = 3 * time.Second
	confirmTimeout = 3 * time.Second

	// inventorySlotLimit is how far the inventory scan looks for a stack. The
	// bot's own inventory is 36 slots; the margin covers a host that numbers
	// them differently.
	inventorySlotLimit = 64

	// villagerEyeOffset aims at a villager's middle rather than its feet.
	villagerEyeOffset = 1.0
)

// Timings is every wait and reach in a trade, gathered in one struct so a test
// can compress them and production can leave them alone.
type Timings struct {
	Approach   time.Duration
	Aim        time.Duration
	Settle     time.Duration
	Poll       time.Duration
	Open       time.Duration
	Result     time.Duration
	Confirm    time.Duration
	Reach      float32
	SearchFrom float32
}

// DefaultTimings returns the production timings.
func DefaultTimings() Timings {
	return Timings{
		Approach:   approachDelay,
		Aim:        aimDelay,
		Settle:     settleDelay,
		Poll:       pollInterval,
		Open:       openTimeout,
		Result:     resultTimeout,
		Confirm:    confirmTimeout,
		Reach:      tradeReach,
		SearchFrom: searchRadius,
	}
}

// FastTimings returns timings with every wait shrunk to a millisecond, for a
// test that is exercising the decision logic rather than the network.
func FastTimings() Timings {
	t := DefaultTimings()
	t.Approach = time.Millisecond
	t.Aim = time.Millisecond
	t.Settle = time.Millisecond
	t.Poll = time.Millisecond
	t.Open = 50 * time.Millisecond
	t.Result = 200 * time.Millisecond
	t.Confirm = 200 * time.Millisecond
	return t
}

// TradeResult is what a trade attempt actually did. OK is the verdict; Reason
// is why not, on every failure path.
type TradeResult struct {
	// OK is true only once the server's result reached the bot's inventory.
	OK bool

	// Reason explains every false. It is never empty on a failure.
	Reason string

	// VillagerID and VillagerName identify the villager that was traded with.
	VillagerID   uint64
	VillagerName string

	// DisplayName and TradeTier are what the server said about that villager.
	DisplayName string
	TradeTier   int32

	// WindowID is the trading window the server assigned, never assumed.
	WindowID byte

	// Offer is the row that was chosen, and Spent what was put into the
	// ingredient slots for it.
	Offer Offer
	Spent []Item

	// Gained is the result that arrived, counted from the server's own window
	// contents rather than from the request that asked for it.
	Gained Item

	// XPObserved reports whether the level was readable at all. When it is
	// false, XPBefore, XPAfter and XPDelta are meaningless and mean nothing;
	// they are not a claim of no change.
	XPObserved bool
	XPBefore   int32
	XPAfter    int32
	XPDelta    int32
}

// Manager runs trades for a nearby villager.
type Manager struct {
	bot    Bot
	logger *slog.Logger

	mu       sync.RWMutex
	observer TradeObserver
	xp       XPObserver
	timings  Timings
}

// NewManager builds a trading manager.
//
// The observer and the XP source are nil here, and nil is meaningful for both:
// they say the corresponding server state is not observable, and a trade that
// depends on either one refuses rather than assuming.
func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{
		bot:     bot,
		logger:  logger,
		timings: DefaultTimings(),
	}
}

// SetTradeObserver wires the UpdateTrade seam. Nil clears it, which puts the
// manager back to refusing trades for want of offers.
func (m *Manager) SetTradeObserver(observer TradeObserver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.observer = observer
}

// SetXPObserver wires the experience seam. Nil clears it, after which an
// XP-costing offer is refused and an XP-free one trades as usual.
func (m *Manager) SetXPObserver(xp XPObserver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.xp = xp
}

// SetTimings overrides every wait and reach. Production leaves them at
// DefaultTimings; the override exists so a test does not sit through settling
// delays measured in seconds.
func (m *Manager) SetTimings(t Timings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timings = t
}

func (m *Manager) currentTimings() Timings {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.timings
}

func (m *Manager) tradeObserver() TradeObserver {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.observer
}

func (m *Manager) xpObserver() XPObserver {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.xp
}

// IsVillagerType reports whether an entity type name is a villager. Detection is
// by type, never by proximity: a pig standing where a villager would is not a
// trading partner, and a bot that offers it emeralds reports a purchase that
// never happened.
func IsVillagerType(name string) bool {
	return entity.NormalizeName(name) == "villager"
}

// FindVillager returns the nearest living villager within the search radius, or
// false when the world holds none. Nearest is resolved explicitly so the answer
// does not depend on map iteration order.
func (m *Manager) FindVillager() (*entity.Info, bool) {
	origin := m.bot.GetCoords()
	radius := m.currentTimings().SearchFrom

	best, bestDist := (*entity.Info)(nil), float32(math.MaxFloat32)
	for _, info := range m.bot.GetEntities() {
		if info == nil || !IsVillagerType(info.Type) {
			continue
		}
		dist := origin.Sub(info.Position).Len()
		if dist > radius || dist >= bestDist {
			continue
		}
		best, bestDist = info, dist
	}
	return best, best != nil
}

// OpenTradeWindow walks to the nearest villager, interacts with it, reads the
// trade window the server assigns, and closes it again.
//
// The window is always closed, on the failure paths as much as the successful
// one: a trade UI left open server-side desyncs the next open and every other
// viewer sees it hanging there. The TradeWindow is a copy of what the server
// said, so the caller can read the offers after the UI has gone.
func (m *Manager) OpenTradeWindow(ctx context.Context) (TradeWindow, byte, error) {
	window, windowID, _, err := m.openTradeWindow(ctx)
	return window, windowID, err
}

// openTradeWindow is OpenTradeWindow with the villager it found, so a caller
// that already needs the villager's identity does not have to search again and
// risk getting a different answer the second time.
func (m *Manager) openTradeWindow(ctx context.Context) (TradeWindow, byte, *entity.Info, error) {
	var window TradeWindow

	villager, ok := m.FindVillager()
	if !ok {
		return window, 0, nil, errors.New("no villager nearby to trade with")
	}

	timings := m.currentTimings()

	// Arm the session before the interaction: the server's ContainerOpen can
	// arrive within a frame of the click, and a watch armed afterwards misses
	// the window entirely.
	m.bot.BeginContainerWatch()

	if m.bot.GetCoords().Sub(villager.Position).Len() > timings.Reach {
		m.bot.NavigateTo(villager.Position)
		sleep(ctx, timings.Approach)
	}
	m.bot.StopMovement()
	// mgl32.Vec3 is [3]float32, so the eye offset is positional, not keyed.
	m.bot.LookAt(villager.Position.Add(mgl32.Vec3{0, villagerEyeOffset, 0}))
	sleep(ctx, timings.Aim)

	if err := m.interact(villager); err != nil {
		m.bot.ResetLook()
		return window, 0, villager, fmt.Errorf("interact with villager %d: %w", villager.ID, err)
	}
	sleep(ctx, timings.Settle)

	windowID, _, opened := m.bot.WaitContainerOpen(ctx, timings.Open)
	if !opened {
		m.bot.ResetLook()
		return window, 0, villager, fmt.Errorf("the server did not open a trade window for villager %d", villager.ID)
	}
	defer func() {
		m.bot.CloseContainerWindow(windowID)
		m.bot.ResetLook()
	}()

	observer := m.tradeObserver()
	if observer == nil {
		return window, windowID, villager, fmt.Errorf(
			"trade offers are not observable: nothing records packet.UpdateTrade for villager %d", villager.ID)
	}

	observed, ok := observer.LastTradeWindow(villager.ID)
	if !ok {
		return window, windowID, villager, fmt.Errorf("no trade window was observed for villager %d", villager.ID)
	}
	if observed.WindowID != 0 && observed.WindowID != windowID {
		// The content packets are matched against the ID ContainerOpen assigned,
		// so that stays the one used to address the window. The disagreement is
		// worth a line, not a branch.
		m.logger.Warn("trade window ID disagrees with the opened container",
			"update_trade_window", observed.WindowID, "container_open_window", windowID)
	}
	return observed, windowID, villager, nil
}

// Trade buys or sells with the nearest villager in one call.
//
// want names the item wanted — matched against the offer's output and against
// its inputs, so "trade for a map" and "trade my wheat away" both work. An
// empty want takes the best affordable offer.
//
// It returns true only when the result the server produced reached the bot's
// inventory. Everything else is false with a reason, including the cases a
// lazier implementation reports as success: no villager, no observed offers, no
// affordable offer, a refused transfer, and a result that never arrived.
func (m *Manager) Trade(ctx context.Context, want string) (TradeResult, bool) {
	result := TradeResult{}

	// The level is read before anything moves. A trade that costs levels and
	// then fails must not be reported as having spent them.
	xpBefore, xpKnown := m.readXP()
	result.XPObserved = xpKnown
	result.XPBefore = xpBefore

	window, windowID, villager, err := m.openTradeWindow(ctx)
	result.WindowID = windowID
	if villager != nil {
		result.VillagerID = villager.ID
		result.VillagerName = entity.NormalizeName(villager.Name)
	}
	if err != nil {
		return fail(result, err.Error())
	}
	result.DisplayName = window.DisplayName
	result.TradeTier = window.TradeTier

	if len(window.Offers) == 0 {
		return fail(result, fmt.Sprintf("%s offers nothing this bot can read", describeVillager(window)))
	}

	budget := m.budget()
	offer, ok := PickOffer(window.Offers, budget, wantFilter(want))
	if !ok {
		return fail(result, fmt.Sprintf("no affordable offer for %s at %s", wantOrAnything(want), describeVillager(window)))
	}
	result.Offer = offer

	before := m.snapshot()
	spent, err := m.stage(ctx, offer)
	result.Spent = spent
	if err != nil {
		return fail(result, err.Error())
	}

	gained, ok := m.awaitResult(ctx)
	if !ok {
		return fail(result, fmt.Sprintf("%s never produced a result for those inputs", describeVillager(window)))
	}

	if err := m.bot.TakeFromContainerSlotIn(ResultContainerID, SlotResult, gained.count, gained.stackNetID, gained.name); err != nil {
		return fail(result, fmt.Sprintf("take %s from the trade result: %v", gained.name, err))
	}

	if !m.confirm(ctx, gained, spent, before) {
		return fail(result, fmt.Sprintf(
			"the server took the trade request but %s never reached the inventory", gained.name))
	}

	result.Gained = Item{Name: gained.name, Count: gained.count}

	// The level is read again only now, and only compared when both readings
	// were real. An unobserved level is left unobserved rather than filled in.
	if xpKnown {
		if xpAfter, afterKnown := m.readXP(); afterKnown {
			result.XPAfter = xpAfter
			result.XPDelta = xpAfter - xpBefore
		} else {
			result.XPObserved = false
			result.XPBefore, result.XPAfter = 0, 0
		}
	}

	result.OK = true
	m.logger.Info("traded with a villager",
		"villager", result.VillagerName,
		"offer_index", offer.Index,
		"gained", gained.name,
		"gained_count", gained.count,
		"xp_observed", result.XPObserved,
		"xp_delta", result.XPDelta,
	)
	return result, true
}

// stage moves the offer's inputs into the ingredient containers. destStackNetID
// is 0: those slots are empty, and a real stack ID there names a stack the
// window does not have, which the server refuses.
func (m *Manager) stage(ctx context.Context, offer Offer) ([]Item, error) {
	spent := make([]Item, 0, len(offer.Inputs))

	for index, input := range offer.Inputs {
		if err := ctx.Err(); err != nil {
			return spent, err
		}
		containerID, ok := ContainerIDForSlot(uint32(index))
		if !ok {
			return spent, fmt.Errorf("trade input %d (%s) has no container in the trade window", index, input.Name)
		}
		slot, ok := m.findSlotFor(input)
		if !ok {
			return spent, fmt.Errorf("no stack of %s to put into the trade slot", input.Name)
		}
		if err := m.bot.PlaceIntoContainerSlotIn(containerID, uint32(index), 0, slot, input.Count); err != nil {
			return spent, fmt.Errorf("stage %s into the trade: %w", input.Name, err)
		}
		spent = append(spent, Item{Name: input.Name, Count: input.Count})
	}
	return spent, nil
}

// awaitResult polls the open window until the server's result preview appears.
func (m *Manager) awaitResult(ctx context.Context) (resultStack, bool) {
	timings := m.currentTimings()
	deadline := time.NewTimer(timings.Result)
	defer deadline.Stop()
	ticker := time.NewTicker(timings.Poll)
	defer ticker.Stop()

	for {
		if gained, ok := m.readResult(); ok {
			return gained, true
		}
		select {
		case <-ctx.Done():
			return resultStack{}, false
		case <-deadline.C:
			return resultStack{}, false
		case <-ticker.C:
		}
	}
}

// readResult re-reads the open window and reports the result slot when the
// server has filled it. The read is the evidence; nothing is assumed from the
// request that asked for it.
func (m *Manager) readResult() (resultStack, bool) {
	item, ok := m.bot.ContainerItems()[SlotResult]
	if !ok || item.Stack.Count <= 0 {
		return resultStack{}, false
	}
	return resultStack{
		name:       m.bot.ContainerItemName(item),
		count:      int(item.Stack.Count),
		stackNetID: item.StackNetworkID,
	}, true
}

// confirm is the last gate. The take being accepted is not the item arriving, so
// the inventory is re-read until the output is actually there and each spent
// input has actually gone down. Both halves matter: emeralds spent with nothing
// received is exactly the trade this package refuses to report.
func (m *Manager) confirm(ctx context.Context, gained resultStack, spent []Item, before inventorySnapshot) bool {
	timings := m.currentTimings()
	deadline := time.NewTimer(timings.Confirm)
	defer deadline.Stop()
	ticker := time.NewTicker(timings.Poll)
	defer ticker.Stop()

	for {
		if m.confirmed(gained, spent, before) {
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

func (m *Manager) confirmed(gained resultStack, spent []Item, before inventorySnapshot) bool {
	after := m.snapshot()

	// The output arrives, matched on the exact name the server gave it. A
	// substring match would let "emerald" be satisfied by "emerald_block".
	if after[NormalizeItemName(gained.name)] < gained.count {
		return false
	}
	for _, input := range spent {
		key := NormalizeItemName(input.Name)
		if after[key] > before[key]-input.Count {
			return false
		}
	}
	return true
}

// interact sends the entity-interaction transaction that opens the trade UI.
// The held item has to be echoed in step with the held slot or the server
// refuses the interaction.
func (m *Manager) interact(villager *entity.Info) error {
	heldSlot := m.bot.GetHeldItemSlot()
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: villager.ID,
			ActionType:            protocol.UseItemOnEntityActionInteract,
			HotBarSlot:            safecast.To[int32](heldSlot),
			HeldItem:              protocol.ItemInstance{Stack: m.heldStack(heldSlot)},
			Position:              m.bot.GetCoords(),
			ClickedPosition:       mgl32.Vec3{},
		},
	}
	return m.bot.WritePacket(tx)
}

func (m *Manager) heldStack(slot uint32) protocol.ItemStack {
	return m.bot.GetInventorySlots()[slot]
}

// budget is a flattened reading of what the bot is carrying, plus whatever the
// experience seam can genuinely see.
func (m *Manager) budget() Budget {
	budget := Budget{Have: map[string]int{}}
	for name, count := range m.snapshot() {
		budget.Have[name] = count
	}
	if level, ok := m.readXP(); ok {
		budget.XPLevel, budget.XPObserved = level, true
	}
	return budget
}

// snapshot is a count of every item the bot carries, keyed by normalised name.
// Going through a snapshot rather than the raw slot map is what makes the
// confirmation check stable while the server is still rewriting slots.
func (m *Manager) snapshot() inventorySnapshot {
	out := inventorySnapshot{}
	slots := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()
	for _, stack := range slots {
		if stack.Count <= 0 {
			continue
		}
		name, ok := names[stack.NetworkID]
		if !ok {
			continue
		}
		key := NormalizeItemName(name)
		if key == "" {
			continue
		}
		out[key] += int(stack.Count)
	}
	return out
}

// findSlotFor locates the lowest inventory slot holding a full enough stack of
// the wanted item. Slots are walked in order because a map walk would make the
// chosen stack depend on iteration order.
func (m *Manager) findSlotFor(want Item) (uint32, bool) {
	name := NormalizeItemName(want.Name)
	slots := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()

	for slot := uint32(0); slot < inventorySlotLimit; slot++ {
		stack, ok := slots[slot]
		if !ok || stack.Count <= 0 || int(stack.Count) < want.Count {
			continue
		}
		if NormalizeItemName(names[stack.NetworkID]) == name {
			return slot, true
		}
	}
	return 0, false
}

// readXP reads the experience level, reporting false when nothing observes it.
// That false is the honest answer and is never turned into a zero.
func (m *Manager) readXP() (int32, bool) {
	observer := m.xpObserver()
	if observer == nil {
		return 0, false
	}
	return observer.ExperienceLevel()
}

// CloseTradeWindow closes a trade window by the ID the server assigned.
func (m *Manager) CloseTradeWindow(windowID byte) {
	m.bot.CloseContainerWindow(windowID)
}

// inventorySnapshot counts items by normalised name.
type inventorySnapshot map[string]int

// resultStack is a confirmed result sitting in the trade window's result slot.
type resultStack struct {
	name       string
	count      int
	stackNetID int32
}

// wantFilter turns a wanted item name into the predicate PickOffer applies. A
// nil want matches everything, which is what "trade with the villager" means.
//
// The match runs in both directions so "emerald" finds "emerald_block" and
// "emerald_block" finds "emerald", which is how people actually ask for things.
func wantFilter(want string) func(Offer) bool {
	target := NormalizeItemName(want)
	if target == "" {
		return nil
	}
	return func(offer Offer) bool {
		if nameMatches(offer.Output.Name, target) {
			return true
		}
		for _, input := range offer.Inputs {
			if nameMatches(input.Name, target) {
				return true
			}
		}
		return false
	}
}

// nameMatches compares a normalised item name against a normalised want. An
// exact match counts, and so does a partial one in either direction, so that
// "emerald" finds "emerald_block" and "emerald_block" finds "emerald" — which is
// how people actually ask for things.
func nameMatches(name, target string) bool {
	n := NormalizeItemName(name)
	if n == "" || target == "" {
		return false
	}
	return n == target || strings.Contains(n, target) || strings.Contains(target, n)
}

func wantOrAnything(want string) string {
	if NormalizeItemName(want) == "" {
		return "anything on offer"
	}
	return NormalizeItemName(want)
}

// describeVillager names the villager in a failure message, falling back to the
// entity type when the server sent no display name.
func describeVillager(window TradeWindow) string {
	if name := NormalizeItemName(window.DisplayName); name != "" {
		return fmt.Sprintf("the villager (%s, tier %d)", name, window.TradeTier)
	}
	return fmt.Sprintf("the villager (tier %d)", window.TradeTier)
}

// fail stamps a reason onto a result and returns it alongside false.
func fail(result TradeResult, reason string) (TradeResult, bool) {
	result.OK = false
	result.Reason = reason
	return result, false
}

// sleep waits for d, giving up early if the context ends.
func sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
