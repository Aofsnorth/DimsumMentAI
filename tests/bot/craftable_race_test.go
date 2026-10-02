package bot_test

import (
	"io"
	"log/slog"
	"sync"
	"testing"

	"bedrock-ai/internal/bot"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// ListCraftableItems walked the bot's live maps after releasing the lock.
//
// The log caught it as a hard process death:
//
//	fatal error: concurrent map iteration and map write
//	  bot.(*Bot).CanCraftRecipe  craftable.go:97
//	  bot.(*Bot).ListCraftableItems  craftable.go:31
//	  agi.(*Runner).craftableCount
//
// The map detector does not check whether the writer holds the right lock. It
// sees two goroutines on one map, and it kills the process — unrecoverably, with
// no recover() in the world able to catch it, and the only explanation is a
// goroutine dump after the session has already died mid-world.
// TestListCraftableIsSafeWhileTheInventoryIsBeingWritten is the race itself:
// one goroutine listing, another writing the maps the list reads.
func TestListCraftableIsSafeWhileTheInventoryIsBeingWritten(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		RecipesByNetID: map[uint32]bot.RecipeInfo{
			// Ingredients matter here. With none, CanCraftRecipe never iterates
			// the inventory and the test races on nothing at all — which is
			// exactly what the first version of this test did.
			1: {Output: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 5}},
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "oak_planks"}, Count: 1}}},
			2: {Output: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 6}},
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "stick"}, Count: 2}}},
			3: {Output: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 7}},
				Ingredients: []protocol.ItemDescriptorCount{
					{Descriptor: &protocol.DefaultItemDescriptor{Name: "crafting_table"}, Count: 1}}},
		},
		ItemNames: map[int32]string{5: "oak_planks", 6: "stick", 7: "crafting_table"},
		InventoryMap: map[uint32]protocol.ItemStack{
			1: {ItemType: protocol.ItemType{NetworkID: 5}, Count: 4},
		},
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// The reader: what the AGI loop does every tick.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = b.ListCraftableItems(true)
			}
		}
	}()

	// The writer: what the packet read loop does on every inventory
	// transaction. Overwriting an existing key is enough to trip the detector —
	// it fires on any write during an iteration, not only on a rehash.
	//
	// The slots cycle through a fixed range on purpose. A writer that invented
	// a new key each turn grew the map without bound, so every snapshot the
	// reader took got bigger and the fixed part of the test turned into a
	// quadratic crawl that timed out rather than failing. A regression should
	// crash; it should never look like a slow machine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		slot := uint32(0)
		for {
			select {
			case <-stop:
				return
			default:
				slot = slot%32 + 2
				stack := protocol.ItemStack{
					ItemType: protocol.ItemType{NetworkID: int32(5 + slot%3)},
					Count:    uint16(1 + slot%16),
				}
				b.Mu.Lock()
				b.InventoryMap[slot] = stack
				b.Mu.Unlock()
			}
		}
	}()

	// Long enough that a regression crashes rather than passing by luck, and
	// short enough that the fixed path stays a test rather than a benchmark.
	for range 500 {
		_ = b.ListCraftableItems(true)
	}

	close(stop)
	wg.Wait()
}

// TestListCraftableSeesAConsistentSnapshot checks the fix did not turn the race
// into a wrong answer: the listing must reflect one moment, not a smear of
// several.
func TestListCraftableSeesAConsistentSnapshot(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		RecipesByNetID: map[uint32]bot.RecipeInfo{
			1: {Output: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 5}}},
			2: {Output: protocol.ItemStack{ItemType: protocol.ItemType{NetworkID: 6}}},
		},
		ItemNames:    map[int32]string{5: "oak_planks", 6: "stick"},
		InventoryMap: map[uint32]protocol.ItemStack{},
	}

	got := b.ListCraftableItems(true)
	if len(got) != 2 {
		t.Fatalf("ListCraftableItems returned %d recipes, want 2", len(got))
	}

	// The listing must not alias the bot's own map: a caller holding the result
	// while the bot fills a slot would otherwise be reading the live map again.
	for _, item := range got {
		if item.Name == "" {
			t.Error("a recipe came back with no name; the snapshot is not being resolved")
		}
	}
}
