// The adapter between a live bot and the affordance layer's World interface.
//
// It exists so the policy in the affordance package can be tested against a
// described world instead of a running server. Every rule there — you cannot
// fish without water, you cannot chop without an axe — is a sentence a player
// would recognise, and a sentence nobody can check without a world to check it
// against is a sentence that quietly stops being true.
//
// Nothing here decides anything. It reports what is true and the affordance
// package decides what follows, which is the split that keeps the policy
// testable and this adapter boring.

package bot

import (
	"sort"
	"strings"

	"bedrock-ai/internal/bot/affordance"
	"bedrock-ai/internal/bot/storage"

	"github.com/go-gl/mathgl/mgl32"
)

// affordanceReach is how far the bot has to be from a container to be offered
// "take" or "store".
//
// Interaction range, not sight range. A chest across the room is visible and
// useless, and offering "take" for it produces a bot that walks into a wall to
// reach something it could never have opened.
const affordanceReach = 4

// botWorld answers the affordance layer's questions from the live bot.
//
// Every method tolerates a bot with no world model and no inventory, because
// the derivation runs on every tick including the ones during reconnect where
// none of that exists yet.
type botWorld struct {
	b         *Bot
	water     bool
	container bool
	stackable bool
	tool      bool
	freeSlots int
	craftable int
	materials map[string]bool
}

func (w *botWorld) CanSeeWater() bool      { return w.water }
func (w *botWorld) FacingContainer() bool  { return w.container }
func (w *botWorld) NearChest() bool        { return w.container }
func (w *botWorld) HoldingStackable() bool { return w.stackable }
func (w *botWorld) HoldingTool() bool      { return w.tool }
func (w *botWorld) FreeSlots() int         { return w.freeSlots }

func (w *botWorld) HasMaterial(name string) bool {
	if w == nil || w.materials == nil {
		return false
	}
	return w.materials[strings.ToLower(name)]
}

// NewAffordanceWorld builds the adapter and resolves the facts the derivation
// needs.
//
// They are resolved here rather than lazily because the whole set has to come
// from one consistent instant. A set built from three different moments can
// offer "take from a chest" after the chest has already been emptied, and the
// model will believe it.
func NewAffordanceWorld(b *Bot) affordance.World {
	w := &botWorld{b: b}
	if b == nil {
		return w
	}

	slots := b.GetInventorySlots()
	names := b.GetItemNames()
	w.materials = make(map[string]bool, len(slots))
	for _, stack := range slots {
		name := strings.ToLower(names[stack.NetworkID])
		if name == "" {
			continue
		}
		w.materials[name] = true
		if isGatheringTool(name) {
			w.tool = true
		}
		if isPlaceableItem(name) {
			w.stackable = true
		}
	}

	// The recipe count comes from the same call the snapshot uses. It is read
	// here rather than passed in because the world adapter is also built
	// directly, and an unread field would silently be zero — which for this
	// field means "craft is never legal", the worst possible default.
	w.craftable = len(b.ListCraftableItems(strings.Contains(b.GetHeldItem(), "crafting_table")))

	// Free slots are counted across the whole inventory, not across the map.
	//
	// The inventory map holds only the slots that have something in them, so
	// counting the empty entries in it reports zero free slots for every bot that
	// is actually carrying something — which is every bot, and meant that "craft"
	// and "give" were permanently refused. The snapshot's own freeInventorySlots
	// walks the full 36 and is right; this now agrees with it, because a prompt
	// that says there is room while the menu says there is not is the same class
	// of bug as the one this layer was built to stop.
	w.freeSlots = 0
	for slot := uint32(0); slot < 36; slot++ {
		stack, ok := slots[slot]
		if !ok || stack.Count <= 0 {
			w.freeSlots++
		}
	}

	pos := b.GetCoords()
	// One sweep answers both questions. The old code scanned the same 486 cells
	// twice on every tick to ask two questions, which is the cost of asking
	// instead of listening.
	b.forEachBlockWithin(pos, affordanceReach, func(name string) {
		if name == "water" {
			w.water = true
		}
		if storage.IsContainerBlock(name) {
			w.container = true
		}
	})
	return w
}

// Snapshot reports the inventory facts the snapshot already established.
//
// They are not recomputed here because the snapshot is what the prompt was
// rendered from; deriving from a second, fresher read of the same inventory
// would mean the menu and the prompt could disagree, and a bot shown a craft it
// has no recipe for is the exact bug this layer exists to prevent.
func (w *botWorld) Snapshot() affordance.Facts {
	if w == nil {
		return affordance.Facts{}
	}
	return affordance.Facts{
		FreeSlots: w.freeSlots,
		Craftable: w.craftable,
	}
}

// forEachBlockWithin visits every solid-free cell within reach of pos.
//
// It exists instead of a boolean scan because water and a container are two
// questions about the same 486 cells, and the old code walked them twice on every
// tick. One walk answers both.
func (b *Bot) forEachBlockWithin(pos mgl32.Vec3, reach int32, visit func(name string)) {
	if b.WorldModel == nil {
		return
	}
	x, y, z := int32(pos.X()), int32(pos.Y()), int32(pos.Z())
	for dx := int32(-reach); dx <= reach; dx++ {
		for dz := int32(-reach); dz <= reach; dz++ {
			for dy := int32(-3); dy <= 2; dy++ {
				cx, cy, cz := x+dx, y+dy, z+dz
				// A solid cell cannot be looked into or reached into, so a block
				// "inside" a wall is not something the bot can act on.
				if b.WorldModel.IsSolid(cx, cy, cz) {
					continue
				}
				name, ok := b.GetBlockName(cx, cy, cz)
				if !ok {
					continue
				}
				visit(strings.ToLower(strings.TrimPrefix(name, "minecraft:")))
			}
		}
	}
}

// isGatheringTool reports whether an item is a tool that gathers something.
//
// It is a suffix test on purpose. The list of tools that can fell a tree or
// break a stone is open — every mod adds one — and a bot that refuses to chop
// because it has never heard of a particular pickaxe is a bot that stands in a
// forest doing nothing.
func isGatheringTool(name string) bool {
	for _, suffix := range []string{"_axe", "_pickaxe", "_shovel", "shears"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// isPlaceableItem reports whether an item could be put down as a block.
//
// Same reasoning as the tool test: an exact list of placeable items goes stale
// the first time a server ships a texture pack. What cannot be placed is the
// short list — food, tools, armour, string — and that one is stable.
func isPlaceableItem(name string) bool {
	switch name {
	case "air", "water", "lava", "fire", "soul_fire":
		return false
	}
	for _, suffix := range []string{
		"_sword", "_axe", "_pickaxe", "_shovel", "_hoe",
		"_helmet", "_chestplate", "_leggings", "_boots",
		"_apple", "_bread", "_cooked_beef", "_cooked_chicken", "_cooked_mutton",
		"_cooked_rabbit", "_cooked_porkchop", "_cooked_salmon", "_carrot",
		"_potato", "_beetroot", "_melon_slice", "_sweet_berries",
		"_stick", "_string", "_feather", "_leather", "_bone", "_gunpowder",
		"_flint", "_bowl", "_bucket", "_arrow", "_shield", "_totem_of_undying",
	} {
		if strings.HasSuffix(name, suffix) {
			return false
		}
	}
	return true
}

// DeriveAffordances narrows the catalogue to what this bot can actually do.
//
// changesWorld is the intent class being decided between. It narrows the menu;
// it does not relax the gate, which is intrinsic to each verb and applies
// regardless.
func (b *Bot) DeriveAffordances(changesWorld bool) affordance.Set {
	return affordance.Derive(NewAffordanceWorld(b), changesWorld)
}

// ContainerContents lists the items in the container the bot is facing.
//
// It reads the snapshot rather than opening anything: the point is to tell the
// model what taking would mean, and a container it has not opened has not been
// observed by this code. Offering "take oak_log" from a container the bot has
// never seen the contents of would be offering a guess.
func (w *botWorld) ContainerContents() []string {
	if w == nil || w.b == nil || !w.container {
		return nil
	}
	items := w.b.ContainerItems()
	if len(items) == 0 {
		return nil
	}
	names := w.b.GetItemNames()
	seen := make(map[string]bool, len(items))
	out := make([]string, 0, len(items))
	for _, stack := range items {
		name := strings.ToLower(strings.TrimPrefix(names[stack.Stack.NetworkID], "minecraft:"))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// SlotContents lists what the bot is carrying, for the same reason: "store"
// with no argument is a decision the handler has to guess at.
func (w *botWorld) SlotContents() []string {
	if w == nil || w.b == nil || w.materials == nil {
		return nil
	}
	out := make([]string, 0, len(w.materials))
	for name := range w.materials {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
