package recipe

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Bot is the slice of the bot that a recipe-driven station needs. It mirrors the
// container session the furnace and chest drive, so all three agree on how a
// window is opened, addressed, and closed.
//
// CraftStationRecipe is the one genuinely new method. Everything else already
// exists on *bot.Bot; this interface exists so this package never has to
// import bedrock-ai/internal/bot.
type Bot interface {
	GetCoords() mgl32.Vec3
	GetBlockName(x, y, z int32) (string, bool)
	NavigateToBlock(x, y, z int32, tolerance float32) bool
	StopMovement()
	LookAt(pos mgl32.Vec3)
	ResetLook()

	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string

	// StationRecipes reports the recipes the server advertised that belong to
	// one of the station blocks in Stations(). It is the seam over the bot's
	// existing recipe cache, projected onto StationRecipe.
	StationRecipes() []StationRecipe

	BeginContainerWatch()
	ClickBlockAt(ctx context.Context, pos protocol.BlockPos) (bool, string)
	WaitContainerOpen(ctx context.Context, timeout time.Duration) (byte, protocol.BlockPos, bool)
	CloseContainerWindow(windowID byte)

	// PlaceIntoContainerSlot is the existing server-authoritative transfer,
	// unchanged: staging a plan is the same inventory -> window move the furnace
	// does when it loads a furnace.
	PlaceIntoContainerSlot(windowID byte, containerSlot uint32, destStackNetID int32, srcSlot uint32, count int) error

	// CraftStationRecipe turns a staged station window into a crafted result:
	// consume every staged input, create the result, and place it in the bag.
	// It is the single place the station stack request is built, so the existing
	// beginStackRequest/sendStackRequest machinery is reused rather than
	// re-implemented.
	CraftStationRecipe(req CraftRequest) error
}

const (
	// searchRadius is how far around the bot a station is looked for. It is
	// deliberately narrow: a smithing table across the map is not one this
	// action should walk the world for.
	searchRadius = int32(8)

	// openTimeout bounds the wait for the server to open the station window.
	openTimeout = 2 * time.Second

	// openReach is the distance beyond which the bot walks to the station
	// before clicking it.
	openReach = float32(3.5)

	// resultTimeout bounds the wait for the crafted result to land in the bag.
	resultTimeout = 3 * time.Second

	// pollInterval is how often the bag is re-read while waiting.
	pollInterval = 100 * time.Millisecond
)

// Manager runs the four recipe-driven stations.
type Manager struct {
	bot    Bot
	logger *slog.Logger

	// searchRadius, openTimeout, openReach, resultTimeout and pollInterval are
	// fields rather than constants so a test can shorten a wait that would
	// otherwise be measured in seconds. Production leaves them at the defaults
	// NewManager sets.
	searchRadius  int32
	openTimeout   time.Duration
	openReach     float32
	resultTimeout time.Duration
	pollInterval  time.Duration
}

// NewManager builds a station manager over the bot.
func NewManager(bot Bot, logger *slog.Logger) *Manager {
	return &Manager{
		bot:           bot,
		logger:        logger,
		searchRadius:  searchRadius,
		openTimeout:   openTimeout,
		openReach:     openReach,
		resultTimeout: resultTimeout,
		pollInterval:  pollInterval,
	}
}

// SetResultBudget overrides how long a craft waits for its result to land in the
// bag. Production leaves the budget at the package default; the override exists
// so a test can shorten a wait that is otherwise measured in seconds. It mirrors
// the furnace package's SetSmeltBudget for the same reason.
func (m *Manager) SetResultBudget(budget time.Duration) {
	m.resultTimeout = budget
}

// Inventory returns the bot's bag as a sorted, read-only snapshot. It is the
// same view every Plan function takes, so a plan and the bag it was made
// against can be compared without re-reading the live map.
func (m *Manager) Inventory() Inventory {
	slots := m.bot.GetInventorySlots()
	names := m.bot.GetItemNames()
	out := make(Inventory, 0, len(slots))
	for slot, stack := range slots {
		if stack.Count == 0 {
			continue
		}
		out = append(out, Item{
			Slot:  slot,
			Name:  NormalizeName(names[stack.NetworkID]),
			Count: int(stack.Count),
		})
	}
	sortInventory(out)
	return out
}

// UpgradeToNetherite upgrades a diamond item to netherite at a nearby smithing
// table. It reports success only once the netherite item is in the bag.
func (m *Manager) UpgradeToNetherite(ctx context.Context) (Plan, bool) {
	plan, err := PlanNetheriteUpgrade(m.Inventory(), m.bot.StationRecipes())
	if err != nil {
		m.logger.Warn("smithing: no upgrade to plan", "err", err)
		return Plan{}, false
	}
	return m.run(ctx, plan)
}

// Stonecut cuts result out of stone at a nearby stonecutter.
func (m *Manager) Stonecut(ctx context.Context, result string) (Plan, bool) {
	plan, err := PlanStonecut(m.Inventory(), m.bot.StationRecipes(), result)
	if err != nil {
		m.logger.Warn("stonecutter: no cut to plan", "result", result, "err", err)
		return Plan{}, false
	}
	return m.run(ctx, plan)
}

// ApplyBannerPattern applies a loom pattern to a banner at a nearby loom.
func (m *Manager) ApplyBannerPattern(ctx context.Context, pattern string) (Plan, bool) {
	plan, err := PlanBannerPattern(m.Inventory(), pattern)
	if err != nil {
		m.logger.Warn("loom: no pattern to apply", "pattern", pattern, "err", err)
		return Plan{}, false
	}
	return m.run(ctx, plan)
}

// CopyMap copies a map at a nearby cartography table. It is acceptance 4.8.
func (m *Manager) CopyMap(ctx context.Context) (Plan, bool) {
	return m.runCartography(ctx, CartographyCopy)
}

// RunCartography runs any cartography operation, so copy, extend, lock, clone,
// zoom and explorer share one server-authoritative path.
func (m *Manager) RunCartography(ctx context.Context, action CartographyAction) (Plan, bool) {
	return m.runCartography(ctx, action)
}

func (m *Manager) runCartography(ctx context.Context, action CartographyAction) (Plan, bool) {
	plan, err := PlanCartography(m.Inventory(), m.bot.StationRecipes(), action)
	if err != nil {
		m.logger.Warn("cartography: no operation to plan", "action", action, "err", err)
		return Plan{}, false
	}
	return m.run(ctx, plan)
}

// run executes a plan: find the station by block name, open it, stage the
// inputs, ask the server to craft, and wait for the result to actually land.
func (m *Manager) run(ctx context.Context, plan Plan) (Plan, bool) {
	pos, ok := m.FindStation(plan.Station.Kind)
	if !ok {
		m.logger.Warn("station: block not nearby", "station", plan.Station.Block)
		return Plan{}, false
	}

	// The count of the result before the craft. A cartography copy turns one
	// map into two, so "is a map in the bag" proves nothing; "did the map count
	// go up" does.
	before := m.Inventory().Count(plan.ResultName)

	windowID, err := m.openStation(ctx, pos)
	if err != nil {
		m.logger.Warn("station: could not open", "station", plan.Station.Block, "err", err)
		return Plan{}, false
	}
	// Close what the server opened, by the ID it opened it with, even when the
	// craft fails. A station left open server-side desyncs the next open.
	defer func() {
		m.bot.CloseContainerWindow(windowID)
		m.bot.ResetLook()
	}()

	if err := m.stage(windowID, plan); err != nil {
		m.logger.Warn("station: could not stage inputs", "station", plan.Station.Block, "err", err)
		return Plan{}, false
	}

	req := CraftRequest{Plan: plan, WindowID: windowID}
	if err := m.bot.CraftStationRecipe(req); err != nil {
		m.logger.Warn("station: server rejected the craft", "station", plan.Station.Block, "err", err)
		return Plan{}, false
	}

	if !m.waitForResult(ctx, plan, before) {
		m.logger.Warn("station: no result before timeout", "station", plan.Station.Block, "result", plan.ResultName)
		return plan, false
	}
	m.logger.Info("station craft confirmed",
		"station", plan.Station.Block,
		"result", plan.ResultName,
		"count", plan.ResultCount)
	return plan, true
}

// FindStation returns the closest block that is actually the given station, by
// name rather than by solidity.
func (m *Manager) FindStation(kind Kind) (protocol.BlockPos, bool) {
	if _, ok := StationByKind(kind); !ok {
		return protocol.BlockPos{}, false
	}
	pos := m.bot.GetCoords()
	bx := int32(math.Floor(float64(pos.X())))
	by := int32(math.Floor(float64(pos.Y())))
	bz := int32(math.Floor(float64(pos.Z())))

	var best protocol.BlockPos
	bestDist := float64(-1)
	found := false

	for dx := -m.searchRadius; dx <= m.searchRadius; dx++ {
		for dy := int32(-2); dy <= 3; dy++ {
			for dz := -m.searchRadius; dz <= m.searchRadius; dz++ {
				x, y, z := bx+dx, by+dy, bz+dz
				name, ok := m.bot.GetBlockName(x, y, z)
				if !ok {
					continue
				}
				foundStation, ok := StationByBlockName(name)
				if !ok || foundStation.Kind != kind {
					continue
				}
				d := float64(dx*dx + dy*dy + dz*dz)
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

// openStation walks into reach, looks at the block, clicks it, and waits for
// the window the server assigns. The window ID is whatever the server says; it
// is never assumed.
func (m *Manager) openStation(ctx context.Context, pos protocol.BlockPos) (byte, error) {
	if m.bot.GetCoords().Sub(blockCenter(pos)).Len() > m.openReach {
		if !m.bot.NavigateToBlock(pos.X(), pos.Y(), pos.Z(), 3.0) {
			return 0, errCannotReach
		}
		m.bot.StopMovement()
	}
	m.bot.LookAt(blockCenter(pos))

	// Arm the session before the click: ContainerOpen can arrive within a frame
	// of the click, and a watch armed afterwards misses the window entirely.
	m.bot.BeginContainerWatch()
	if ok, reason := m.bot.ClickBlockAt(ctx, pos); !ok {
		return 0, &clickError{reason: reason}
	}

	windowID, openedPos, ok := m.bot.WaitContainerOpen(ctx, m.openTimeout)
	if !ok {
		return 0, errNoWindow
	}
	m.logger.Debug("opened station", "pos", openedPos, "window_id", windowID)
	return windowID, nil
}

// stage moves every planned input into its station slot.
//
// destStackNetID is 0: an empty station slot has no stack ID, and sending a
// non-zero one names a stack the server has never seen.
func (m *Manager) stage(windowID byte, plan Plan) error {
	for _, in := range plan.Inputs {
		if err := m.bot.PlaceIntoContainerSlot(windowID, in.Slot.Index, 0, in.SourceSlot, in.Count); err != nil {
			return err
		}
	}
	return nil
}

// waitForResult polls the bag until the result count reaches the count the
// craft had to add, then reports true. Anything else is a failure: a bot that
// reports a smithing upgrade it never received burns the template and gets
// nothing.
func (m *Manager) waitForResult(ctx context.Context, plan Plan, before int) bool {
	want := before + plan.ResultCount
	deadline := time.NewTimer(m.resultTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		if m.Inventory().Count(plan.ResultName) >= want {
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

// blockCenter is the middle of a block, which is what the bot has to aim at.
func blockCenter(pos protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(pos.X()) + 0.5,
		float32(pos.Y()) + 0.5,
		float32(pos.Z()) + 0.5,
	}
}
