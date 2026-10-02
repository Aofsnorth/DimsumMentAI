package recipe_test

import (
	"context"
	"errors"
	"strings"
	"time"

	"bedrock-ai/internal/bot/inventory/recipe"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// fakeBot is a scripted stand-in for the live bot. It records the order the
// station flow asked for things, so a test can pin the packet discipline —
// arm, click, confirm, craft, close — without a server.
type fakeBot struct {
	pos    mgl32.Vec3
	blocks map[[3]int32]string
	names  map[int32]string
	items  map[uint32]protocol.ItemStack

	// windowID is the ID the server assigns on ContainerOpen. It is
	// deliberately non-zero in tests so a stray hardcoded window 0 cannot pass
	// by accident.
	windowID byte
	// openFails simulates a click the server never answers.
	openFails bool
	// clickOK/clickWhy simulate an interactor that refuses the target.
	clickOK  bool
	clickWhy string
	// navigateOK simulates a bot that cannot walk to the station.
	navigateOK bool

	// recipes is what the server advertised.
	recipes []recipe.StationRecipe

	// armBeforeClick records whether the container watch was armed before the
	// click, which is the ordering that a ContainerOpen within one frame
	// depends on.
	armBeforeClick bool
	clicks         []protocol.BlockPos
	closed         []byte
	placed         []placedStack
	crafts         []recipe.CraftRequest

	// placeErr/craftErr make a transfer fail so a test can prove the window is
	// still closed afterwards.
	placeErr error
	craftErr error

	// resultItem is the item the server delivers into the bag when the craft
	// succeeds. An empty name means the craft is accepted but nothing ever
	// arrives, which is the "server said yes and the bot believed it" case.
	resultItem string
	// resultCount is how many of resultItem the craft adds.
	resultCount int
	// craftDelay is how long the result takes to show up, so the confirmation
	// poll can be observed polling rather than passing on the first read.
	craftDelay time.Duration
}

type placedStack struct {
	windowID     byte
	slot         uint32
	count        int
	destStackNet int32
	srcSlot      uint32
}

func newFakeBot() *fakeBot {
	return &fakeBot{
		blocks:      map[[3]int32]string{},
		names:       map[int32]string{},
		items:       map[uint32]protocol.ItemStack{},
		windowID:    5,
		clickOK:     true,
		navigateOK:  true,
		resultItem:  "",
		resultCount: 1,
	}
}

func (f *fakeBot) GetCoords() mgl32.Vec3 { return f.pos }

func (f *fakeBot) GetBlockName(x, y, z int32) (string, bool) {
	name, ok := f.blocks[[3]int32{x, y, z}]
	return name, ok
}

func (f *fakeBot) NavigateToBlock(x, y, z int32, tolerance float32) bool { return f.navigateOK }
func (f *fakeBot) StopMovement()                                         {}
func (f *fakeBot) LookAt(pos mgl32.Vec3)                                 {}
func (f *fakeBot) ResetLook()                                            {}

func (f *fakeBot) GetInventorySlots() map[uint32]protocol.ItemStack { return f.items }
func (f *fakeBot) GetItemNames() map[int32]string                   { return f.names }

func (f *fakeBot) StationRecipes() []recipe.StationRecipe { return f.recipes }

func (f *fakeBot) BeginContainerWatch() { f.armed() }

func (f *fakeBot) armed() {
	if len(f.clicks) == 0 {
		f.armBeforeClick = true
	}
}

func (f *fakeBot) ClickBlockAt(ctx context.Context, pos protocol.BlockPos) (bool, string) {
	f.clicks = append(f.clicks, pos)
	return f.clickOK, f.clickWhy
}

func (f *fakeBot) WaitContainerOpen(ctx context.Context, timeout time.Duration) (byte, protocol.BlockPos, bool) {
	if f.openFails {
		return 0, protocol.BlockPos{}, false
	}
	return f.windowID, protocol.BlockPos{}, true
}

func (f *fakeBot) ContainerItemName(item protocol.ItemInstance) string {
	return f.names[item.Stack.NetworkID]
}

func (f *fakeBot) CloseContainerWindow(windowID byte) {
	f.closed = append(f.closed, windowID)
}

func (f *fakeBot) PlaceIntoContainerSlot(windowID byte, containerSlot uint32, destStackNetID int32, srcSlot uint32, count int) error {
	if f.placeErr != nil {
		return f.placeErr
	}
	f.placed = append(f.placed, placedStack{
		windowID:     windowID,
		slot:         containerSlot,
		count:        count,
		destStackNet: destStackNetID,
		srcSlot:      srcSlot,
	})
	return nil
}

// CraftStationRecipe stands in for the server-side validation. When it is told
// to deliver a result, the result is added to the bag after craftDelay so the
// confirmation poll is genuinely exercised.
func (f *fakeBot) CraftStationRecipe(req recipe.CraftRequest) error {
	f.crafts = append(f.crafts, req)
	if f.craftErr != nil {
		return f.craftErr
	}
	if f.resultItem == "" {
		return nil
	}
	deliver(f, req.Plan.ResultName, f.resultCount, f.craftDelay)
	return nil
}

// deliver schedules the server's authoritative result into the bag.
func deliver(f *fakeBot, name string, count int, after time.Duration) {
	netID := int32(len(f.names) + 1)
	f.names[netID] = qualify(name)
	qualified := qualify(name)
	add := func() {
		slot := uint32(35)
		for s, stack := range f.items {
			if stack.Count == 0 {
				slot = s
				break
			}
			if f.names[stack.NetworkID] == qualified {
				f.items[s] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: netID}, Count: uint16(stack.Count) + uint16(count)}
				return
			}
		}
		f.items[slot] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: netID}, Count: uint16(count)}
	}
	if after <= 0 {
		add()
		return
	}
	time.AfterFunc(after, add)
}

// put seeds an inventory slot with count of name and returns its network ID.
// The name is namespaced the way a server's item registry reports it, and an
// already-namespaced name is left alone rather than prefixed twice.
func (f *fakeBot) put(slot uint32, name string, count int) int32 {
	netID := int32(len(f.names) + 1)
	f.names[netID] = qualify(name)
	f.items[slot] = protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: netID}, Count: uint16(count)}
	return netID
}

// qualify adds the minecraft namespace once, the way a server's item registry
// does.
func qualify(name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	return "minecraft:" + name
}

// placeBlock seeds a block in the world.
func (f *fakeBot) placeBlock(x, y, z int32, name string) {
	f.blocks[[3]int32{x, y, z}] = name
}

// itemNameAt resolves the name of a bag slot.
func (f *fakeBot) itemNameAt(slot uint32) string {
	stack, ok := f.items[slot]
	if !ok {
		return ""
	}
	return f.names[stack.NetworkID]
}

var errRejected = errors.New("server rejected the stack request")
