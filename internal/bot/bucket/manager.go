package bucket

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sync"
	"time"

	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	// searchRadius is how far around the bot a fill source or a cauldron is
	// looked for. A cauldron in the next room is not one this action should
	// cross a village for.
	searchRadius int32 = 8

	// verticalBelow and verticalAbove bound the vertical part of the scan.
	verticalBelow int32 = -3
	verticalAbove int32 = 3

	// topFace is the block face a bucket is aimed at. The top of a cauldron is
	// what a player clicks to fill it, and the top of the ground is what a
	// bucket is poured onto.
	topFace int32 = 1

	// eyeHeight is the camera offset above the feet, matching the
	// PlayerAuthInput the bot is sending. A click transaction whose Position
	// disagrees with the input heartbeat is a click the server can reject.
	eyeHeight float32 = 1.62
)

// Timings is every wait in a bucket operation.
type Timings struct {
	// Aim is the pause between turning toward the target and clicking it.
	Aim time.Duration
	// Confirm is how long the manager waits for the bucket and the world to
	// both change. Expiry is a failure, never a success.
	Confirm time.Duration
	// Poll is the sampling interval while waiting.
	Poll time.Duration
}

// DefaultTimings returns the production timings.
func DefaultTimings() Timings {
	return Timings{Aim: 120 * time.Millisecond, Confirm: 2 * time.Second, Poll: 50 * time.Millisecond}
}

// Compressed divides every wait by factor, so a test does not have to sit
// through the production confirmation budget.
func (t Timings) Compressed(factor int) Timings {
	if factor <= 0 {
		return t
	}
	d := time.Duration(factor)
	return Timings{Aim: t.Aim / d, Confirm: t.Confirm / d, Poll: t.Poll / d}
}

// Bot is the slice of the bot this package needs.
//
// Every method already exists on *bot.Bot, so wiring the package costs nothing
// outside it. The two things it cannot supply — block state properties and
// ItemStackResponse arrivals — are separate interfaces for exactly that reason.
type Bot interface {
	GetCoords() mgl32.Vec3
	GetBlockName(x, y, z int32) (string, bool)
	GetBlockNetworkID(x, y, z int32) (uint32, bool)
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	GetHeldItemSlot() uint32
	GetEntities() map[uint64]*entity.Info
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	ResetLook()
	EquipItem(slot uint32) error
	WritePacket(pk packet.Packet) error
	GetEntityRuntimeID() uint64
	ReportActionStatus(user string, status event.ActionStatus)
}

// Manager runs bucket and cauldron operations.
type Manager struct {
	bot    Bot
	logger *slog.Logger

	mu      sync.Mutex
	timings Timings
	states  BlockStateSource
	stacks  StackResponseSource
	reports []event.ActionStatus
	// target is the cell the last click was aimed at, so the confirmation
	// loop knows which cell to re-read, and targetRID is that cell's network
	// ID as it was before the click. A cauldron keeps its name whatever its
	// level, so for a pour into one the network ID is the only evidence.
	target       protocol.BlockPos
	targetRID    uint32
	targetHasRID bool
}

// NewManager creates a new bucket manager.
func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{bot: bot, logger: logger, timings: DefaultTimings()}
}

// SetTimings overrides the operation timings. Production leaves them at
// DefaultTimings.
func (m *Manager) SetTimings(t Timings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timings = t
}

// SetBlockStateSource wires the block-properties seam. With it a cauldron's
// level is readable; without it, only the block state change is.
func (m *Manager) SetBlockStateSource(src BlockStateSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = src
}

// SetStackResponseSource wires the ItemStackResponse seam. With it, a bucket
// operation additionally requires the server to have answered its request.
func (m *Manager) SetStackResponseSource(src StackResponseSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stacks = src
}

// Reports returns the statuses this manager has reported.
func (m *Manager) Reports() []event.ActionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]event.ActionStatus(nil), m.reports...)
}

// --- Finding targets ---------------------------------------------------

// FindFillTarget returns the nearest thing nearby that a bucket can be filled
// from, together with what it would yield.
func (m *Manager) FindFillTarget() (protocol.BlockPos, FillSource, bool) {
	var best protocol.BlockPos
	var bestSource FillSource
	bestDist := float32(math.MaxFloat32)
	found := false

	forEachNearbyBlock(m.bot, func(pos protocol.BlockPos, name string) {
		kind, ok := BucketFillPlan(BlockSource(name))
		if !ok || kind == BucketUnknown {
			return
		}
		if d := m.distanceTo(pos); d < bestDist {
			bestDist = d
			best = pos
			bestSource = BlockSource(name)
			found = true
		}
	})
	return best, bestSource, found
}

// FindNearbyCauldron returns the closest cauldron within the search radius.
func (m *Manager) FindNearbyCauldron() (protocol.BlockPos, bool) {
	var best protocol.BlockPos
	bestDist := float32(math.MaxFloat32)
	found := false

	forEachNearbyBlock(m.bot, func(pos protocol.BlockPos, name string) {
		if !IsCauldronBlock(name) {
			return
		}
		if d := m.distanceTo(pos); d < bestDist {
			bestDist = d
			best = pos
			found = true
		}
	})
	return best, found
}

// --- Filling and emptying ----------------------------------------------

// FillBucket fills the bot's empty bucket from the block at pos and returns
// what it now holds.
//
// It returns an error unless BOTH happened: the bucket's contents changed, and
// the source block changed. The first is the ItemStackResponse landing in the
// inventory; the second is the world actually losing the water. A click that
// only moves the client-side prediction produces neither, and is reported as
// the failure it is.
func (m *Manager) FillBucket(ctx context.Context, pos protocol.BlockPos) (BucketKind, error) {
	sourceName, ok := m.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		return BucketUnknown, errors.New("blok sumber tidak termuat di cache dunia")
	}
	kind, ok := BucketFillPlan(BlockSource(sourceName))
	if !ok {
		return BucketUnknown, errors.New("tidak bisa mengisi bucket dari " + sourceName)
	}

	slot, ok := m.findBucket(BucketEmpty)
	if !ok {
		return BucketUnknown, errors.New("tidak punya bucket kosong")
	}
	if err := m.bot.EquipItem(slot); err != nil {
		return BucketUnknown, err
	}

	before := m.inventory()
	started := time.Now()
	m.clickBlock(ctx, pos)

	res := m.waitForChange(ctx, started, before, sourceName, BucketEmpty, kind, BucketUnknown)
	m.report("bucket_fill", kind.String(), res)
	return kind, resultError(res, "bucket tidak terisi")
}

// EmptyBucket pours the bot's filled bucket onto the block at pos.
//
// The mirror image of FillBucket: the bucket must go back to empty AND the
// target must show the liquid. A released fish or axolotl becomes an entity
// rather than a block name, so for those the requirement is that the cell
// changed at all.
func (m *Manager) EmptyBucket(ctx context.Context, pos protocol.BlockPos) (BucketKind, error) {
	targetName, ok := m.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		return BucketUnknown, errors.New("blok tujuan tidak termuat di cache dunia")
	}

	slot, kind, ok := m.findAnyFilledBucket()
	if !ok {
		return BucketUnknown, errors.New("tidak punya bucket terisi")
	}
	if can, reason := BucketEmptyPlan(kind, BlockSource(targetName)); !can {
		return kind, errors.New(reason)
	}
	if err := m.bot.EquipItem(slot); err != nil {
		return kind, err
	}

	before := m.inventory()
	started := time.Now()
	m.clickBlock(ctx, pos)

	res := m.waitForChange(ctx, started, before, targetName, kind, BucketEmpty, kind)
	m.report("bucket_empty", kind.String(), res)
	return kind, resultError(res, "bucket tidak dituang")
}

// --- Cauldron ----------------------------------------------------------

// FillCauldron pours the bot's filled bucket into the cauldron at pos.
//
// The returned CauldronChange says what was observed. Changed is the
// confirmation: the cauldron's block state demonstrably moved. LevelKnown
// additionally says the level itself was readable, and is false unless a
// BlockStateSource is wired, because Bedrock keeps the level in the block
// state's properties and *bot.Bot exposes names only.
func (m *Manager) FillCauldron(ctx context.Context, pos protocol.BlockPos) (CauldronChange, error) {
	return m.useOnCauldron(ctx, pos, true)
}

// DrainCauldron scoops the cauldron at pos back into the bot's empty bucket.
func (m *Manager) DrainCauldron(ctx context.Context, pos protocol.BlockPos) (CauldronChange, error) {
	return m.useOnCauldron(ctx, pos, false)
}

// useOnCauldron is the shared body of fill and drain.
//
// Fill needs a filled, pourable, non-milk bucket; drain needs an empty one,
// because the cauldron is the thing being emptied. Either way the cauldron's
// block state is what proves it happened, and the inventory is the corroborating
// half.
func (m *Manager) useOnCauldron(ctx context.Context, pos protocol.BlockPos, fill bool) (CauldronChange, error) {
	action := "cauldron_drain"
	if fill {
		action = "cauldron_fill"
	}

	name, ok := m.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		return CauldronChange{}, errors.New("cauldron tidak termuat di cache dunia")
	}
	if !IsCauldronBlock(name) {
		return CauldronChange{}, errors.New(name + " bukan cauldron")
	}

	var kind BucketKind
	slot := uint32(0)
	if fill {
		var found bool
		slot, kind, found = m.findAnyFilledBucket()
		if !found {
			return CauldronChange{}, errors.New("tidak punya bucket terisi")
		}
		if kind == BucketMilk {
			return CauldronChange{}, errors.New("susu tidak bisa masuk ke cauldron")
		}
	} else {
		var found bool
		slot, found = m.findBucket(BucketEmpty)
		if !found {
			return CauldronChange{}, errors.New("tidak punya bucket kosong untuk menyedot")
		}
	}
	if err := m.bot.EquipItem(slot); err != nil {
		return CauldronChange{}, err
	}
	from, to := kind, BucketEmpty
	if !fill {
		from, to = BucketEmpty, kind
	}

	beforeCauldron := m.observeCauldron(pos)
	beforeInv := m.inventory()
	started := time.Now()
	m.clickBlock(ctx, pos)

	t := m.currentTimings()
	deadline := time.Now().Add(t.Confirm)
	change := CauldronChange{}
	res := Result{Kind: kind}
	for {
		change = CauldronLevelChange(beforeCauldron, m.observeCauldron(pos))
		res.BucketChanged = BucketContentsChanged(beforeInv, m.inventory(), from, to)
		res.BlockChanged, res.BlockReadable = change.Changed, change.Changed
		if change.Changed {
			break
		}
		if !sleepCtx(ctx, t.Poll) {
			break
		}
		if time.Now().After(deadline) {
			break
		}
	}

	if src := m.stackSource(); src != nil {
		res.StackResponseSeen = true
		res.StackResponse = accepted(src.StackResponsesSince(started))
	}
	m.reportCauldron(action, res)
	if !change.LevelKnown {
		m.logger.Debug("cauldron change seen, level not readable",
			"pos", pos,
			"evidence", change.StateChanged,
			"bucket_moved", res.BucketChanged,
			"block_state_seam", m.stateSource() != nil)
	}
	if !change.Changed {
		return change, errors.New("cauldron tidak berubah level")
	}
	return change, nil
}

// --- The transaction ---------------------------------------------------

// clickBlock sends the use-item transaction a vanilla client sends to work a
// bucket on a block.
//
// It is written here rather than delegated to the interactor because the
// interactor confirms a click by watching for a block state change and, for a
// target it does not expect to change, queues a second inline click. A bucket
// that filled on the first click would then be emptied by the second, and the
// bot would end a "fill" action holding an empty bucket and a puddle.
func (m *Manager) clickBlock(ctx context.Context, pos protocol.BlockPos) {
	rid, hasRID := m.bot.GetBlockNetworkID(pos.X(), pos.Y(), pos.Z())
	m.mu.Lock()
	m.target = pos
	m.targetRID = rid
	m.targetHasRID = hasRID
	m.mu.Unlock()

	slot := m.bot.GetHeldItemSlot()
	held, ok := m.bot.GetInventorySlots()[slot]
	if !ok {
		held = protocol.ItemStack{}
	}

	m.bot.NavigateToBlock(pos.X(), pos.Y(), pos.Z(), 3.0)
	m.bot.StopMovement()
	m.bot.LookAt(blockCenter(pos).Add(mgl32.Vec3{0, eyeHeight, 0}))
	if !sleepCtx(ctx, m.currentTimings().Aim) {
		return
	}

	tx := protocol.UseItemTransactionData{
		ActionType:      protocol.UseItemActionClickBlock,
		TriggerType:     protocol.TriggerTypePlayerInput,
		BlockPosition:   pos,
		BlockFace:       topFace,
		HotBarSlot:      safecast.To[int32](slot),
		HeldItem:        protocol.ItemInstance{Stack: held},
		Position:        m.bot.GetCoords().Add(mgl32.Vec3{0, eyeHeight, 0}),
		ClickedPosition: mgl32.Vec3{0.5, 0.9, 0.5},
	}
	if hasRID {
		tx.BlockRuntimeID = rid
	}
	_ = m.bot.WritePacket(&packet.InventoryTransaction{TransactionData: &tx})
	m.bot.ResetLook()
}

// --- Confirmation ------------------------------------------------------

// waitForChange polls until the bucket and the world both show the expected
// change, or the confirmation budget runs out.
//
// becomes is what the target cell is supposed to turn into. BucketUnknown
// means "any change will do", which is the right bar for a fill: scooping water
// out leaves air or flowing water depending on the flow, and demanding one
// specific name would fail a fill that worked.
//
// Budget expiry returns whatever was observed, with Confirmed() false. It never
// returns true: a wait that ran out is a wait that saw nothing.
func (m *Manager) waitForChange(
	ctx context.Context,
	started time.Time,
	beforeInv Inventory,
	beforeName string,
	from, to BucketKind,
	becomes BucketKind,
) Result {
	t := m.currentTimings()
	deadline := time.Now().Add(t.Confirm)

	res := Result{Kind: to}
	src := m.stackSource()
	if src != nil {
		res.StackResponseSeen = true
	}

	for {
		res.BucketChanged = BucketContentsChanged(beforeInv, m.inventory(), from, to)
		res.BlockChanged, res.BlockReadable = m.blockDidChange(beforeName, becomes)
		if res.StackResponseSeen {
			res.StackResponse = accepted(src.StackResponsesSince(started))
		}
		if res.Confirmed() {
			return res
		}
		if !sleepCtx(ctx, t.Poll) {
			return res
		}
		if time.Now().After(deadline) {
			return res
		}
	}
}

// blockDidChange re-reads the clicked cell and says whether the world moved.
//
// "Not readable" is a third answer, not a false one: a cell missing from the
// world cache is a gap in what the bot knows, and reading it as "unchanged"
// would report a failure for a fill that actually happened.
//
// A pour is held to a stricter bar than a fill. Filling removes the source, so
// "the cell is no longer what it was" is the whole of it. Pouring has to
// produce the liquid — or, when the target is a cauldron, to move the
// cauldron's state, because a cauldron is named "minecraft:cauldron" at every
// level and only its network ID gives it away.
func (m *Manager) blockDidChange(beforeName string, becomes BucketKind) (changed, readable bool) {
	m.mu.Lock()
	pos, ridBefore, hadRID := m.target, m.targetRID, m.targetHasRID
	m.mu.Unlock()

	name, ok := m.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		return false, false
	}
	before := normalise(beforeName)
	after := normalise(name)
	ridNow, hasRIDNow := m.bot.GetBlockNetworkID(pos.X(), pos.Y(), pos.Z())
	ridChanged := hadRID && hasRIDNow && ridNow != ridBefore

	if becomes == BucketUnknown {
		return before != after || ridChanged, true
	}

	switch becomes {
	case BucketFish, BucketAxolotl:
		// A released catch becomes an entity, not a block name, so the only
		// world-side evidence is that the cell it was aimed at is no longer
		// what it was.
		return before != after || ridChanged, true
	}

	if isCauldronName(before) {
		return ridChanged, true
	}
	return blockBecameFluid(beforeName, name, becomes), true
}

// observeCauldron reads whatever the seams allow about a cauldron.
func (m *Manager) observeCauldron(pos protocol.BlockPos) CauldronObservation {
	name, ok := m.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		return CauldronObservation{}
	}
	rid, hasRID := m.bot.GetBlockNetworkID(pos.X(), pos.Y(), pos.Z())
	if src := m.stateSource(); src != nil {
		if _, props, ok := src.GetBlockState(pos.X(), pos.Y(), pos.Z()); ok {
			return ReadCauldron(name, props, rid, hasRID)
		}
	}
	return ReadCauldron(name, nil, rid, hasRID)
}

// --- Inventory helpers -------------------------------------------------

func (m *Manager) inventory() Inventory {
	return NewInventory(m.bot.GetInventorySlots(), m.bot.GetItemNames())
}

// findBucket returns the slot holding a bucket of exactly the given kind.
func (m *Manager) findBucket(kind BucketKind) (uint32, bool) {
	names := m.bot.GetItemNames()
	for slot, stack := range m.bot.GetInventorySlots() {
		if stack.Count == 0 {
			continue
		}
		if ClassifyBucket(names[stack.NetworkID]) == kind {
			return slot, true
		}
	}
	return 0, false
}

// findAnyFilledBucket returns the first filled bucket in the inventory.
//
// "First" is arbitrary by map order, and that is fine: the caller re-checks the
// plan against the target and reports when the bucket it picked cannot go
// there. For a cauldron the contents do not matter beyond "not milk".
func (m *Manager) findAnyFilledBucket() (uint32, BucketKind, bool) {
	names := m.bot.GetItemNames()
	for slot, stack := range m.bot.GetInventorySlots() {
		if stack.Count == 0 {
			continue
		}
		if kind := ClassifyBucket(names[stack.NetworkID]); kind.IsFilled() {
			return slot, kind, true
		}
	}
	return 0, BucketUnknown, false
}

// --- Reporting ---------------------------------------------------------

func (m *Manager) reportCauldron(action string, res Result) {
	m.report(action, res.Kind.String(), res)
}

func (m *Manager) report(action, item string, res Result) {
	status := event.ActionStatus{
		Action:  action,
		Item:    item,
		Count:   1,
		Success: res.Confirmed(),
	}
	if !res.Confirmed() {
		status.Error = confirmFailureReason(res)
	}
	m.mu.Lock()
	m.reports = append(m.reports, status)
	m.mu.Unlock()
	m.bot.ReportActionStatus("", status)
}

// confirmFailureReason names which half of the confirmation is missing, so a
// log says "the server ignored it" rather than the useless "failed".
func confirmFailureReason(res Result) string {
	switch {
	case !res.BucketChanged && !res.BlockChanged:
		return "server tidak merespons pemakaian bucket"
	case !res.BucketChanged:
		return "isi bucket tidak berubah"
	case !res.BlockReadable:
		return "blok tujuan tidak bisa dibaca, tidak terkonfirmasi"
	case !res.BlockChanged:
		return "blok tujuan tidak berubah"
	default:
		return "tidak terkonfirmasi"
	}
}

func resultError(res Result, msg string) error {
	if res.Confirmed() {
		return nil
	}
	return errors.New(msg + ": " + confirmFailureReason(res))
}

// --- Small helpers -----------------------------------------------------

func (m *Manager) currentTimings() Timings {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.timings
}

func (m *Manager) stateSource() BlockStateSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states
}

func (m *Manager) stackSource() StackResponseSource {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stacks
}

func (m *Manager) distanceTo(pos protocol.BlockPos) float32 {
	return m.bot.GetCoords().Sub(blockCenter(pos)).Len()
}

func blockCenter(pos protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5}
}

// forEachNearbyBlock walks the loaded blocks around the bot.
func forEachNearbyBlock(b Bot, fn func(pos protocol.BlockPos, name string)) {
	origin := b.GetCoords()
	bx := int32(math.Floor(float64(origin.X())))
	by := int32(math.Floor(float64(origin.Y())))
	bz := int32(math.Floor(float64(origin.Z())))

	for dx := -searchRadius; dx <= searchRadius; dx++ {
		for dy := verticalBelow; dy <= verticalAbove; dy++ {
			for dz := -searchRadius; dz <= searchRadius; dz++ {
				pos := protocol.BlockPos{bx + dx, by + dy, bz + dz}
				name, ok := b.GetBlockName(pos.X(), pos.Y(), pos.Z())
				if !ok {
					continue
				}
				fn(pos, name)
			}
		}
	}
}

// sleepCtx waits for d, returning false if the context ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
