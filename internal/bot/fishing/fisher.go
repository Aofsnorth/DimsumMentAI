package fishing

import (
	"context"
	"log/slog"
	"math"
	"strings"
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
	// waterSearchRadius is how far around the bot a fishable tile is looked
	// for. A pond across the map is not one this action should walk to.
	waterSearchRadius int32 = 12

	// bobberSearchRadius is how far from the cast point the hook may be found.
	// The line is at most a few blocks long, and anything further is a
	// different actor that happens to look like a hook.
	bobberSearchRadius float32 = 12.0

	// settleDelay is the pause between aiming at the water and casting.
	settleDelay = 300 * time.Millisecond
)

// Timings is every wait in a fishing cycle, gathered in one place so a test can
// compress minutes into milliseconds and production can leave them alone.
//
// These are durations, not the trigger. The old 8-20 second "wait for a bite"
// lived here and was the bug: it decided a fish had arrived without anything
// having been observed. A timeout here now only bounds patience.
type Timings struct {
	// PostCast is the pause between the cast going out and the first sample of
	// the bobber. The hook needs a moment to spawn and settle.
	PostCast time.Duration
	// BiteWatch is how long one cast watches for a bite before retrieving the
	// line and casting again. Expiry is not a catch.
	BiteWatch time.Duration
	// CatchWait is how long the bot waits for the reeled catch to appear in
	// the inventory. Expiry here means the catch is not confirmed, and the
	// trip reports nothing.
	CatchWait time.Duration
	// Poll is the sampling interval for both watches above.
	Poll time.Duration
	// Between is the pause between one retrieved line and the next cast.
	Between time.Duration
}

// DefaultTimings returns the production timings.
//
// BiteWatch is generous because a vanilla bite can take a minute of nothing.
// That is fine: the loop spends the time watching the bobber, not sleeping
// through it, so a slow server costs nothing but time and a fast one is caught
// on the first sample where the hook actually moves.
func DefaultTimings() Timings {
	return Timings{
		PostCast:  time.Second,
		BiteWatch: 60 * time.Second,
		CatchWait: 3 * time.Second,
		Poll:      100 * time.Millisecond,
		Between:   time.Second,
	}
}

// Bot interface for fishing subsystem.
//
// Deliberately unchanged from the version it replaces: every method here
// already exists on *bot.Bot, so wiring this package costs nothing outside it.
// The one thing it could not take is an observation seam, and that is a
// separate interface (ActorEventSource) precisely so it does not have to.
type Bot interface {
	GetCoords() mgl32.Vec3
	WritePacket(pk packet.Packet) error
	GetEntities() map[uint64]*entity.Info
	NavigateTo(pos mgl32.Vec3)
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
	EquipItem(slot uint32) error
	SendChat(msg string)
	ReportActionStatus(user string, status event.ActionStatus)
	GetEntityRuntimeID() uint64
	GetLocalWorldModel() entity.WorldModel
	GetBlockName(x, y, z int32) (string, bool)
}

// Fisher handles fishing rod operations.
type Fisher struct {
	bot    Bot
	logger *slog.Logger

	mu        sync.Mutex
	isFishing bool
	timings   Timings
	// events is the optional bite-event seam. Nil means the packet is not
	// being observed, and the motion predicate is the only bite signal left.
	events ActorEventSource
}

// NewFisher builds a fisher for the given bot.
func NewFisher(bot Bot, logger *slog.Logger) *Fisher {
	return &Fisher{
		bot:     bot,
		logger:  logger,
		timings: DefaultTimings(),
	}
}

// SetTimings overrides the cycle timings. Production leaves them at
// DefaultTimings; the override exists so a test does not have to sit through a
// minute of watching a float that never moves.
func (f *Fisher) SetTimings(t Timings) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.timings = t
}

// SetActorEventSource wires the bite-event seam.
//
// Passing nil is meaningful: it says "the server's fishhook-tease events are
// not observable", and the fisherman stops waiting for them instead of assuming
// they would have arrived.
func (f *Fisher) SetActorEventSource(src ActorEventSource) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = src
}

// FindWater finds the nearest water block within radius.
func (f *Fisher) FindWater(radius int32) (protocol.BlockPos, bool) {
	pos := f.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	var best protocol.BlockPos
	bestDist := math.MaxFloat64
	found := false

	for dx := -radius; dx <= radius; dx++ {
		for dy := int32(-3); dy <= 3; dy++ {
			for dz := -radius; dz <= radius; dz++ {
				name, ok := f.bot.GetBlockName(bx+dx, by+dy, bz+dz)
				if !ok {
					continue
				}
				if !IsWaterBlock(name) {
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

// IsWaterBlock reports whether a block name is a fishable water tile.
//
// A substring match on "water" rather than an exact list, for the same reason
// the perception package matches water that way: Bedrock carries a long tail of
// water-like blocks and a modded server names them in ways no enumeration
// anticipated.
func IsWaterBlock(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "water") || strings.Contains(n, "bubble_column")
}

// FindFishingRod finds a fishing rod in the inventory.
func (f *Fisher) FindFishingRod() (uint32, bool) {
	inv := f.bot.GetInventorySlots()
	names := f.bot.GetItemNames()

	for slot, item := range inv {
		if item.Count <= 0 {
			continue
		}
		if IsFishingRod(names[item.NetworkID]) {
			return slot, true
		}
	}
	return 0, false
}

// IsFishingRod reports whether an item name is a fishing rod.
func IsFishingRod(name string) bool {
	n := normalise(name)
	return n == "fishing_rod" || n == "fishingrod" || n == "rod"
}

// GoFish performs a fishing cycle and returns the number of catches that were
// confirmed in the inventory.
//
// The returned count is the acceptance criterion for this action: it is the
// number of fish that actually arrived, and it is zero whenever the bot could
// not see a bite or the inventory did not change. The old version returned the
// number of times its timer expired, which is how "caught 5 fish" happened on
// an empty lake.
func (f *Fisher) GoFish(ctx context.Context, maxCatches int) int {
	f.mu.Lock()
	if f.isFishing {
		f.mu.Unlock()
		return 0
	}
	f.isFishing = true
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.isFishing = false
		f.mu.Unlock()
	}()

	rodSlot, found := f.FindFishingRod()
	if !found {
		f.reportFailure("fishing_rod", "tidak punya joran")
		return 0
	}

	waterPos, found := f.FindWater(waterSearchRadius)
	if !found {
		f.reportFailure("water", "tidak ada air di sekitar")
		return 0
	}

	approach := protocol.BlockPos{waterPos.X(), waterPos.Y() + 1, waterPos.Z() + 1}
	if !f.bot.NavigateToBlock(approach.X(), approach.Y(), approach.Z(), 3.0) {
		f.reportFailure("water", "tidak bisa sampai ke air")
		return 0
	}
	f.bot.StopMovement()

	if err := f.bot.EquipItem(rodSlot); err != nil {
		f.reportFailure("fishing_rod", "tidak bisa memegang joran")
		return 0
	}

	f.bot.LookAt(blockCenter(waterPos))
	if !sleepCtx(ctx, settleDelay) {
		return 0
	}

	caught := 0
	var unconfirmed int
	for caught+unconfirmed < maxCatches {
		if ctx.Err() != nil {
			break
		}
		if f.fishOnce(ctx, rodSlot, waterPos) {
			caught++
			f.logger.Info("fish confirmed in inventory", "caught", caught)
			continue
		}
		// A cast that produced nothing is not an error, it is a cast. It
		// still counts against the request so a command asking for 20 fish
		// cannot hold the rod out forever.
		unconfirmed++
		if !sleepCtx(ctx, f.currentTimings().Between) {
			break
		}
	}

	if caught == 0 {
		f.reportFailure("fish", "tidak ada ikan yang terkonfirmasi")
		return 0
	}
	f.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "fish",
		Item:    "fish",
		Count:   caught,
		Success: true,
	})
	return caught
}

// fishOnce casts, watches for a bite, reels only if it saw one, and then waits
// for the inventory to prove the catch. It returns true only when both happened.
func (f *Fisher) fishOnce(ctx context.Context, rodSlot uint32, waterPos protocol.BlockPos) bool {
	t := f.currentTimings()
	before := f.inventory()

	castAt := f.castLine(rodSlot)
	if !sleepCtx(ctx, t.PostCast) {
		f.retrieve(rodSlot)
		return false
	}

	bite, how := f.waitForBite(ctx, castAt, blockCenter(waterPos))
	if !bite {
		// Retrieve the line so the rod is not left cast, but do not count it.
		// Nothing was reeled on a signal, so nothing is a catch.
		f.retrieve(rodSlot)
		f.logger.Debug("no bite observed", "hook", how)
		return false
	}
	f.logger.Debug("bite observed", "signal", how)
	f.reelIn(rodSlot)

	gained := f.waitForCatch(ctx, before, t.CatchWait)
	if gained == 0 {
		f.logger.Warn("reeled on a bite but no catch arrived in the inventory", "signal", how)
	}
	return gained > 0
}

// waitForBite watches for a fishhook tease or for the bobber to jerk toward the
// player. It returns whether a bite was observed and, for the log, which signal
// said so. An empty signal with false means the watch simply expired.
//
// A watch that expires is not a fish. The old code treated the same expiry as
// one, and that single line is the whole defect.
func (f *Fisher) waitForBite(ctx context.Context, castAt time.Time, water mgl32.Vec3) (bool, string) {
	t := f.currentTimings()
	deadline := time.Now().Add(t.BiteWatch)

	var prev BobberSample
	havePrev := false
	seenHook := false

	for {
		if events := f.actorEvents(); events != nil {
			// A runtime ID of 0 asks for every entity: when the hook was never
			// tracked as an actor there is no other ID to ask about.
			hookID := uint64(0)
			if prev.ID != 0 {
				hookID = prev.ID
			}
			if BiteObserved(events.ActorEventsSince(hookID, castAt), castAt) {
				return true, "fishhook_tease"
			}
		}

		if sample, ok := f.sampleBobber(water); ok {
			seenHook = true
			if havePrev && ShouldReel(prev, sample, f.bot.GetCoords()) {
				return true, "bobber_motion"
			}
			prev = sample
			havePrev = true
		}

		if !sleepCtx(ctx, t.Poll) {
			return false, "cancelled"
		}
		if time.Now().After(deadline) {
			if !seenHook && f.actorEvents() == nil {
				return false, "no observation channel"
			}
			return false, "timeout"
		}
	}
}

// waitForCatch polls the inventory until loot appears, and returns how much.
func (f *Fisher) waitForCatch(ctx context.Context, before Inventory, budget time.Duration) int {
	t := f.currentTimings()
	deadline := time.Now().Add(budget)
	for {
		if gained := CaughtDelta(before, f.inventory()); gained > 0 {
			return gained
		}
		if !sleepCtx(ctx, t.Poll) {
			return 0
		}
		if time.Now().After(deadline) {
			return 0
		}
	}
}

// sampleBobber finds the fishing hook near the cast point and reads where it is.
//
// Nearest-to-the-cast wins, because a server may track several hook-typed actors
// and the one that matters is the one this cast put in the water.
func (f *Fisher) sampleBobber(water mgl32.Vec3) (BobberSample, bool) {
	var best BobberSample
	bestDist := bobberSearchRadius
	found := false

	for id, e := range f.bot.GetEntities() {
		if e == nil || !IsBobberType(e.Type) {
			continue
		}
		d := e.Position.Sub(water).Len()
		if d > bestDist {
			continue
		}
		bestDist = d
		best = BobberSample{ID: id, Position: e.Position, At: time.Now()}
		found = true
	}
	return best, found
}

// castLine sends the rod use that puts the line in the water and returns the
// moment it went out, which is the boundary the bite watch is measured from.
func (f *Fisher) castLine(rodSlot uint32) time.Time {
	f.useRod(rodSlot)
	f.logger.Debug("fishing rod cast")
	return time.Now()
}

// reelIn retracts a line that has a confirmed bite on it.
func (f *Fisher) reelIn(rodSlot uint32) {
	f.useRod(rodSlot)
	f.logger.Debug("fishing rod reeled in on a bite")
}

// retrieve pulls a line back with no fish on it. It is the same packet as a
// reel — a real client cannot tell them apart — and it is deliberately named
// differently so the log never reads as a catch.
func (f *Fisher) retrieve(rodSlot uint32) {
	f.useRod(rodSlot)
	f.logger.Debug("fishing line retrieved with no bite")
}

// useRod writes the use-item transaction a vanilla client sends to work a rod.
func (f *Fisher) useRod(rodSlot uint32) {
	inv := f.bot.GetInventorySlots()
	item, ok := inv[rodSlot]
	if !ok {
		item = inv[f.bot.GetHeldItemSlot()]
	}
	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemTransactionData{
			ActionType:    protocol.UseItemActionClickBlock,
			BlockPosition: protocol.BlockPos{0, -1, 0},
			BlockFace:     255,
			HotBarSlot:    safecast.To[int32](rodSlot),
			HeldItem:      protocol.ItemInstance{Stack: item},
			Position:      f.bot.GetCoords(),
			ClickedPosition: mgl32.Vec3{
				0, 0, 0,
			},
		},
	}
	_ = f.bot.WritePacket(tx)
}

func (f *Fisher) inventory() Inventory {
	return NewInventory(f.bot.GetInventorySlots(), f.bot.GetItemNames())
}

func (f *Fisher) actorEvents() ActorEventSource {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.events
}

func (f *Fisher) currentTimings() Timings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.timings
}

func (f *Fisher) reportFailure(item, reason string) {
	f.bot.ReportActionStatus("", event.ActionStatus{
		Action:  "fish",
		Item:    item,
		Count:   0,
		Success: false,
		Error:   reason,
	})
}

// Stop stops the current fishing operation.
func (f *Fisher) Stop() {
	f.mu.Lock()
	f.isFishing = false
	f.mu.Unlock()
	f.bot.StopMovement()
}

// IsFishing returns whether currently fishing.
func (f *Fisher) IsFishing() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.isFishing
}

func blockCenter(pos protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{float32(pos.X()) + 0.5, float32(pos.Y()) + 0.5, float32(pos.Z()) + 0.5}
}

// sleepCtx waits for d, returning false if the context ended first. Every wait
// in this package goes through it, so cancelling a fishing trip stops the bot
// between polls rather than after the current sleep.
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
