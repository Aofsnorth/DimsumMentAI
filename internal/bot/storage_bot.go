package bot

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"bedrock-ai/internal/bot/fov"
	"bedrock-ai/internal/bot/storage"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

const (
	// containerOpenTimeout bounds the wait for the server to open the window
	// after the click.
	containerOpenTimeout = 2 * time.Second
	// containerContentTimeout bounds the wait for the window's contents.
	containerContentTimeout = 2 * time.Second
	// containerUnreadableCooldown is how long a chest is skipped after its
	// contents failed to arrive. Long enough that a busy server is not retried
	// in a tight loop, short enough that a rejoin can try again.
	containerUnreadableCooldown = 2 * time.Minute
)

// IsBusy reports whether the bot is already occupied with something.
//
// It is a method rather than a field check so the definition lives in one
// place: both the AGI loop (which must not start a deliberation while busy) and
// the survival loop (which must not start sleeping or building while busy) need
// the same answer, and two copies of it is how one of them ends up disagreeing
// with the other.
//
// "Busy" means the body is spoken for, not that the body is currently
// translating. Those are very different questions, and the second one is the
// wrong one: chopping a tree spends almost all of its time perfectly still,
// standing in front of the trunk with an axe swinging, and MovementState says
// "idle" for the whole of it. A definition that only counted motion reported a
// free body mid-swing, and the natural loop — which correctly refuses to start
// anything when the body is committed — started an exploration and walked the
// bot out from under its own swing. Gathering is therefore a commitment in its
// own right, and the gatherer has been tracking it all along.
func (b *Bot) IsBusy() bool {
	b.Mu.Lock()
	moving := b.MovementState != "idle"
	b.Mu.Unlock()

	// The gatherer guards its own flag with a separate lock, so it is read after
	// b.Mu is released rather than inside the critical section. sync.Mutex is not
	// reentrant and the two are not ordered; holding one across the other is the
	// deadlock that wedged this loop before.
	if b.Gatherer != nil && b.Gatherer.IsGathering() {
		return true
	}
	return moving || (b.Planner != nil && b.Planner.IsRunning())
}

// taskCommitted reports whether the body is inside a task that has already
// claimed its eyes — a gathering plan or a build plan.
//
// It deliberately omits plain motion, which is where it narrows IsBusy. A bot
// walking while someone talks to it still turns its head toward them; that is
// ordinary, and the AGI layer decides whether even that is wanted. What it must
// never do is tear its gaze off a block it is in the middle of breaking — and
// breaking happens while standing still, so "is it moving" is not the question.
//
// As in IsBusy, the gatherer's flag is read outside b.Mu: its lock and this one
// are not ordered and sync.Mutex is not reentrant.
func (b *Bot) taskCommitted() bool {
	if b.Gatherer != nil && b.Gatherer.IsGathering() {
		return true
	}
	return b.Planner != nil && b.Planner.IsRunning()
}

// InFieldOfView reports whether a world point is inside the bot's vision cone.
//
// This is the bridge the storage package needs, wired to the same cone the block
// summary uses. It deliberately calls the neutral fov package rather than the
// perception package: perception imports the bot type, so routing through it
// would close an import cycle, and the whole point of extracting fov was to
// give every subsystem one shared notion of "can the bot see this".
func (b *Bot) InFieldOfView(point mgl32.Vec3) bool {
	origin := b.GetCoords()
	eye := origin.Add(mgl32.Vec3{0, PlayerEyeHeight, 0})
	b.Mu.Lock()
	headYaw := b.HeadYaw
	b.Mu.Unlock()
	return fov.Within(point, eye, headYaw)
}

func errChestNotLoaded(p protocol.BlockPos) error {
	return errors.New("blok chest di " + blockPosKey(p) + " belum termuat di cache dunia")
}

func errNotAContainer(name string) error {
	return errors.New(name + " bukan container")
}

func errChestNotVisible(p protocol.BlockPos) error {
	return errors.New("chest di " + blockPosKey(p) + " tidak kelihatan dari sini")
}

// OpenContainer walks to the chest at pos, opens it, and returns the live
// session for reading and moving its contents.
//
// The click is delegated to the interactor, which owns the proven packet
// shapes for a block click. Doing it here instead would mean a chest opens on
// a different set of servers than a door or a button — the exact class of bug
// the interactor exists to have already been fixed.
func (b *Bot) OpenContainer(ctx context.Context, pos protocol.BlockPos) (storage.Container, error) {
	chest, err := b.storageChest(pos)
	if err != nil {
		return nil, err
	}
	clicked := false
	open := func(ctx context.Context, pos protocol.BlockPos) (storage.Container, error) {
		if !clicked {
			// Arm the session before the click: the server's ContainerOpen can
			// arrive within a frame of the click, and a watch armed afterwards
			// misses the window entirely.
			b.BeginContainerWatch()
			ok, reason := b.clickBlock(ctx, pos)
			clicked = true
			if !ok {
				return nil, &containerOpenError{pos: pos, reason: reason}
			}
		}
		windowID, openedPos, ok := b.WaitContainerOpen(ctx, containerOpenTimeout)
		if !ok {
			return nil, &containerOpenError{pos: pos, reason: "server tidak membuka chest"}
		}
		if _, seen := b.WaitContainerContent(ctx, containerContentTimeout); !seen {
			// An empty chest legitimately answers with zero items; a chest whose
			// contents never arrived is a different thing, and the caller should
			// be able to tell them apart. Record which happened and let the
			// search logic skip the chest rather than re-opening it forever.
			b.noteContainerUnreadable(openedPos)
		}
		return &botContainer{bot: b, windowID: windowID, pos: openedPos}, nil
	}

	return b.Storage().Open(ctx, chest, open)
}

type containerOpenError struct {
	pos    protocol.BlockPos
	reason string
}

func (e *containerOpenError) Error() string {
	return e.reason + " (chest " + blockPosKey(e.pos) + ")"
}

// storageChest describes the chest at pos to the search layer, re-checking
// line of sight at open time. Positions can be cached for a few seconds
// between discovery and use, and terrain can change in between.
func (b *Bot) storageChest(pos protocol.BlockPos) (storage.Chest, error) {
	name, ok := b.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		return storage.Chest{}, errChestNotLoaded(pos)
	}
	if !storage.IsContainerBlock(name) {
		return storage.Chest{}, errNotAContainer(name)
	}
	chest, visible := b.Storage().ChestAt(pos, name)
	if !visible {
		return storage.Chest{}, errChestNotVisible(pos)
	}
	return chest, nil
}

// clickBlock runs the proven block click through the interactor.
func (b *Bot) clickBlock(ctx context.Context, pos protocol.BlockPos) (bool, string) {
	if b.Interactor == nil {
		return false, "interactor tidak siap"
	}
	return b.Interactor.ClickBlockAt(ctx, pos)
}

// ClickBlockAt is clickBlock as an interface method, for subsystems that open a
// UI through the interactor without owning the container session themselves.
func (b *Bot) ClickBlockAt(ctx context.Context, pos protocol.BlockPos) (bool, string) {
	return b.clickBlock(ctx, pos)
}

// noteContainerUnreadable records a chest whose contents never arrived, so the
// multi-chest search does not keep re-opening the same silent window.
func (b *Bot) noteContainerUnreadable(pos protocol.BlockPos) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	if b.UnreadableContainers == nil {
		b.UnreadableContainers = make(map[string]time.Time)
	}
	b.UnreadableContainers[blockPosKey(pos)] = time.Now()
}

// ContainerUnreadable reports whether this chest already failed to deliver its
// contents, within the cool-down.
func (b *Bot) ContainerUnreadable(pos protocol.BlockPos) bool {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	at, ok := b.UnreadableContainers[blockPosKey(pos)]
	if !ok {
		return false
	}
	return time.Since(at) < containerUnreadableCooldown
}

// botContainer is the live session for one open chest.
type botContainer struct {
	bot      *Bot
	windowID byte
	pos      protocol.BlockPos
}

func (c *botContainer) WindowID() byte { return c.windowID }

func (c *botContainer) Items() map[uint32]protocol.ItemInstance {
	return c.bot.ContainerItems()
}

func (c *botContainer) ItemName(item protocol.ItemInstance) string {
	return c.bot.ContainerItemName(item)
}

func (c *botContainer) Take(ctx context.Context, slot uint32, count int, stackNetID int32, itemName string) error {
	return c.bot.TakeFromContainerSlot(c.windowID, slot, count, stackNetID, itemName)
}

func (c *botContainer) Store(ctx context.Context, containerSlot uint32, destStackNetID int32, srcSlot uint32, count int) error {
	return c.bot.PlaceIntoContainerSlot(c.windowID, containerSlot, destStackNetID, srcSlot, count)
}

func (c *botContainer) FindInventorySlotFor(name string) (uint32, bool) {
	return c.bot.FindInventorySlotFor(name)
}

func (c *botContainer) FindInventoryItem(name string) (uint32, string, int, bool) {
	c.bot.Mu.Lock()
	defer c.bot.Mu.Unlock()
	want := strings.ToLower(name)
	for slot := uint32(0); slot < 36; slot++ {
		stack, ok := c.bot.InventoryMap[slot]
		if !ok || stack.Count <= 0 {
			continue
		}
		itemName := c.bot.ItemNames[stack.NetworkID]
		if strings.Contains(strings.ToLower(itemName), want) {
			return slot, itemName, int(stack.Count), true
		}
	}
	return 0, "", 0, false
}

func (c *botContainer) Close() {
	c.bot.CloseContainerWindow(c.windowID)
	c.bot.ResetLook()
}

func blockPosKey(p protocol.BlockPos) string {
	return strconv.Itoa(int(p.X())) + "," + strconv.Itoa(int(p.Y())) + "," + strconv.Itoa(int(p.Z()))
}

var _ storage.Bot = (*Bot)(nil)
