package recipe

// Item is one inventory slot reduced to what a station plan needs: which slot it
// is, what it is called, and how many of it there are.
type Item struct {
	// Slot is the bot inventory slot index (0-35, hotbar first).
	Slot uint32
	// Name is the server's item name, already namespace-stripped.
	Name string
	// Count is how many of the item the stack holds.
	Count int
}

// Inventory is a read-only view of the bot's bag, one Item per occupied slot.
//
// It is a slice rather than the bot's map on purpose. Map iteration order is
// random, so a planner that resolved "the first matching slot" from the map
// produced a different plan on every call — and that is how a smithing table
// ends up with the template in one run and the sword in the next. Every lookup
// here goes through Lowest, so the answer does not depend on slice order either
// way; Manager.Inventory additionally sorts, which makes a snapshot readable
// and comparable.
type Inventory []Item

// Find returns the lowest-numbered slot holding name, so a plan that needs the
// same item twice stages it from the same place and a plan run twice on the same
// bag stages from the same place again.
func (inv Inventory) Find(name string) (Item, bool) {
	want := NormalizeName(name)
	if want == "" {
		return Item{}, false
	}
	return inv.Lowest(func(item Item) bool { return item.Name == want })
}

// Lowest returns the lowest-numbered non-empty slot satisfying a predicate.
//
// Every resolution in this package goes through it, rather than returning the
// first slice entry: Inventory is built from the bot's map, so relying on
// iteration order would make a plan differ between two identical bags.
func (inv Inventory) Lowest(match func(Item) bool) (Item, bool) {
	var found Item
	ok := false
	for _, item := range inv {
		if item.Count <= 0 || !match(item) {
			continue
		}
		if !ok || item.Slot < found.Slot {
			found, ok = item, true
		}
	}
	return found, ok
}

// Count totals how many of name the bag holds across every slot.
func (inv Inventory) Count(name string) int {
	want := NormalizeName(name)
	if want == "" {
		return 0
	}
	total := 0
	for _, item := range inv {
		if item.Name == want {
			total += item.Count
		}
	}
	return total
}

// Has reports whether the bag holds at least one of name.
func (inv Inventory) Has(name string) bool {
	_, ok := inv.Find(name)
	return ok
}

// StationRecipe is one recipe the server advertised for a station block.
//
// It is a projection of the protocol's recipe types onto the only two questions
// a plan asks — which block makes this recipe usable, and what does it produce.
// RecipeNetworkID is what goes back to the server in a CraftRecipeStackRequestAction.
type StationRecipe struct {
	// NetworkID is the server's recipe network ID, used in
	// CraftRecipeStackRequestAction.RecipeNetworkID.
	NetworkID uint32
	// Block is the station block the recipe belongs to ("stonecutter"), or ""
	// for a recipe that needs no station. A smithing, stonecutter or cartography
	// recipe may share an output with an ordinary crafting-table recipe, and
	// sending the wrong network ID is accepted by nothing.
	Block string
	// ResultName is the un-namespaced name of the item the recipe produces.
	ResultName string
	// ResultCount is how many of that item one craft yields.
	ResultCount int
}

// normalize fills in the defaults a caller left out, so a test or an adapter
// can describe a recipe with only the fields it cares about.
func (r StationRecipe) normalize() StationRecipe {
	r.Block = NormalizeName(r.Block)
	r.ResultName = NormalizeName(r.ResultName)
	if r.ResultCount <= 0 {
		r.ResultCount = 1
	}
	return r
}

// FindStationRecipe picks the server recipe that a station block makes for the
// given result.
//
// It is deliberately not "the first recipe with a matching output": stone bricks
// and stone brick slabs can be made at a crafting table too, and crafting them
// through the crafting table is not the same act as cutting them. Filtering on
// Block is what makes 4.6 mean "crafted via stonecutter".
func FindStationRecipe(recipes []StationRecipe, station Station, result string) (StationRecipe, bool) {
	want := NormalizeName(result)
	if want == "" {
		return StationRecipe{}, false
	}
	for _, raw := range recipes {
		r := raw.normalize()
		if r.Block != station.Block || r.ResultName != want {
			continue
		}
		return r, true
	}
	return StationRecipe{}, false
}

// StagedInput is one inventory stack on its way into a station slot.
type StagedInput struct {
	// Slot is the station slot it is going into.
	Slot Slot
	// SourceSlot is the bot inventory slot it comes from.
	SourceSlot uint32
	// ItemName is the un-namespaced name of the item being staged.
	ItemName string
	// Count is how many are staged.
	Count int
}

// Plan is a fully resolved station craft: every input with the station slot it
// belongs in, the recipe it maps to, and the result that has to appear in the
// bag before the bot may call the craft a success.
type Plan struct {
	// Station is the station this plan runs at.
	Station Station
	// RecipeNetworkID is the server recipe the staged inputs select. It is zero
	// for the loom, which sends a pattern identifier rather than a recipe ID.
	RecipeNetworkID uint32
	// Pattern is the loom pattern identifier from
	// CraftLoomRecipeStackRequestAction.Pattern. Empty for every other station.
	Pattern string
	// Inputs are the stacks to stage, in the station's own slot order.
	Inputs []StagedInput
	// ResultName is the item the station must produce.
	ResultName string
	// ResultCount is how many of it one craft yields.
	ResultCount int
}

// CraftRequest is the single call a plan turns into on the wire: the open
// station window, the recipe (or loom pattern), the exact slots holding the
// staged inputs, and the result the server should produce.
//
// It is the seam the bot implements on top of the existing ItemStackRequest
// machinery. Keeping the whole station craft behind one method is what stops
// this package from growing a second copy of beginStackRequest/sendStackRequest.
type CraftRequest struct {
	Plan
	// WindowID is the ID the server assigned when it opened the station.
	WindowID byte
}

// UsesRecipe reports whether the craft is addressed by a recipe network ID
// rather than a pattern identifier.
func (p Plan) UsesRecipe() bool {
	return p.RecipeNetworkID != 0
}

// Input returns the staged input for a station role.
func (p Plan) Input(role Role) (StagedInput, bool) {
	for _, in := range p.Inputs {
		if in.Slot.Role == role {
			return in, true
		}
	}
	return StagedInput{}, false
}

// InputContainerIDs lists the protocol container each staged input lives in, in
// plan order. The bot-side CraftStationRecipe needs them to build the consume
// actions; a test pins them so a wrong constant cannot slip through.
func (p Plan) InputContainerIDs() []byte {
	ids := make([]byte, 0, len(p.Inputs))
	for _, in := range p.Inputs {
		ids = append(ids, in.Slot.ContainerID)
	}
	return ids
}

// ResultContainerID is the protocol container the crafted result is taken from.
func (p Plan) ResultContainerID() byte {
	return p.Station.Result.ContainerID
}
