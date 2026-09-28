// Where the bot is, and what the thing it is looking at actually is.
//
// Dimension travel is the one part of this bot that has almost nothing to go on
// after the fact. A portal does not announce itself: the bot walks into a
// purple cell, the world reloads, and by the time anything is logged the only
// evidence is a pile of blocks that belong to somewhere else. The two questions
// this package answers are therefore both about establishing facts rather than
// acting on them:
//
//   - which dimension is the terrain in front of me made of
//   - is that frame a working portal, an unlit one, or not a portal at all
//
// Both are pure functions over block names. That is deliberate. A classification
// that has to be right about obsidian-versus-end-stone is exactly the kind of
// thing that gets written straight into an action handler, where it can only be
// tested by connecting to a server and walking into things.

package dimension

import "strings"

// Dimension is one of the three places a Bedrock world has.
type Dimension int

const (
	// Unknown is the honest answer before the bot has seen enough terrain to
	// tell. It is a supported state, not a failure: a bot that has just joined
	// genuinely does not know yet, and guessing here would put a Nether block
	// set in front of an overworld goal.
	Unknown Dimension = iota
	Overworld
	Nether
	TheEnd
)

// String names the dimension the way a player would say it.
func (d Dimension) String() string {
	switch d {
	case Overworld:
		return "the Overworld"
	case Nether:
		return "the Nether"
	case TheEnd:
		return "the End"
	default:
		return "an unknown dimension"
	}
}

// serverDimensionIDs maps the ID a Bedrock server puts in LevelChunk to a
// dimension. These are fixed by the protocol, not by this server: 0 is the
// overworld, 1 the Nether, 2 the End.
var serverDimensionIDs = map[int32]Dimension{
	0: Overworld,
	1: Nether,
	2: TheEnd,
}

// FromServerID maps a LevelChunk dimension ID to a dimension.
//
// An ID the protocol does not define maps to Unknown rather than to a guess.
// Falling back to the overworld because a server sent something new would mean
// the bot confidently plans a trip home from a world that is not home.
func FromServerID(id int32) Dimension {
	if d, ok := serverDimensionIDs[id]; ok {
		return d
	}
	return Unknown
}

// netherMarkers are blocks that essentially only exist in the Nether.
var netherMarkers = map[string]bool{
	"netherrack": true, "soul_sand": true, "soul_soil": true,
	"nether_bricks": true, "nether_brick": true, "red_nether_bricks": true,
	"nether_wart_block": true, "crimson_nylium": true, "warped_nylium": true,
	"basalt": true, "blackstone": true, "crying_obsidian": true,
	"glowstone": true, "magma": true, "shroomlight": true,
	"nether_gold_ore": true, "nether_quartz_ore": true, "warped_wart_block": true,
}

// endMarkers are blocks that essentially only exist in the End.
var endMarkers = map[string]bool{
	"end_stone": true, "end_stone_bricks": true, "chorus_flower": true,
	"chorus_plant": true, "purpur_block": true, "purpur_pillar": true,
	"end_rod": true, "end_gateway": true,
}

// Detect infers the dimension from the block names in view.
//
// It counts markers rather than taking the first hit, because one stray block —
// a chest made of nether brick in someone's storage room, an end stone brick
// somebody placed as decoration — must not move the bot's map. Only a
// consistent majority moves it, and anything short of that is Unknown, which is
// the answer that costs the least when it is wrong: the bot keeps its previous
// belief instead of acting on a new one.
//
// Obsidian is deliberately absent from both sets. It is the Nether portal frame
// in one world and the End's floor in the other, and using it would make a bot
// standing next to any portal declare itself to be in the End.
func Detect(blocks []string) Dimension {
	if len(blocks) == 0 {
		return Unknown
	}

	var nether, end int
	for _, raw := range blocks {
		name := Normalise(raw)
		if netherMarkers[name] {
			nether++
		}
		if endMarkers[name] {
			end++
		}
	}

	// A majority, not just a lead. Two netherrack among forty overworld blocks
	// is a netherrack decoration, not a Nether.
	half := len(blocks) / 2
	switch {
	case nether > half:
		return Nether
	case end > half:
		return TheEnd
	default:
		return Unknown
	}
}

// PortalState is what a portal-shaped structure actually is.
type PortalState int

const (
	// NotPortal means the blocks do not describe a portal at all.
	NotPortal PortalState = iota
	// NeedsLighting means an obsidian frame with air inside and no portal in
	// it. A flint and steel is the whole difference between this and working.
	NeedsLighting
	// Lit means a portal is open and can be walked into.
	Lit
	// EndPortalFrame means end portal frames with no eye of ender in them, which
	// is a different job entirely: they are filled by being thrown, not lit.
	EndPortalFrame
	// EndPortalOpen means an end portal is already active.
	EndPortalOpen
)

// String names the state for a log line.
func (s PortalState) String() string {
	switch s {
	case NeedsLighting:
		return "an unlit portal frame"
	case Lit:
		return "a lit portal"
	case EndPortalFrame:
		return "end portal frames"
	case EndPortalOpen:
		return "an active end portal"
	default:
		return "not a portal"
	}
}

// portalBlocks are the blocks a working portal is made of.
var portalBlocks = map[string]bool{
	"nether_portal": true,
	"portal":        true,
}

// Classify works out whether a set of block names is a portal, and which kind.
//
// The distinction that matters is Lit versus NeedsLighting, because they call
// for completely different behaviour: one is "walk in", the other is "find
// flint and steel, then walk in". A bot that cannot tell them apart will walk
// confidently into an empty obsidian ring and report that it tried to travel.
//
// The end portal is separated out because it cannot be lit at all — its frames
// are filled by throwing an eye of ender into them — and a bot that tried to
// apply flint and steel to one would stand there forever.
func Classify(blocks []string) PortalState {
	if len(blocks) == 0 {
		return NotPortal
	}

	var lit, frames, obsidian, interior int
	for _, raw := range blocks {
		switch name := Normalise(raw); {
		case portalBlocks[name]:
			lit++
		case name == "end_portal":
			return EndPortalOpen
		case name == "end_portal_frame":
			frames++
		case name == "obsidian":
			obsidian++
		case name == "air" || name == "cave_air" || name == "void_air":
			interior++
		}
	}

	switch {
	case lit > 0:
		return Lit
	case frames > 0:
		return EndPortalFrame
	case obsidian > 0 && interior > 0:
		return NeedsLighting
	default:
		return NotPortal
	}
}

// Normalise strips the namespace and lowercases a block name, so a lookup
// written in vanilla terms still matches a server that namespaces its blocks —
// which is every server that is not a stock Bedrock one.
func Normalise(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// strongholdHints are blocks that exist only in or beside a stronghold.
var strongholdHints = map[string]bool{
	"end_portal_frame":      true,
	"end_portal":            true,
	"end_gateway":           true,
	"chiseled_stone_bricks": true,
}

// IsStrongholdHint reports whether a block suggests a stronghold is nearby.
//
// These are the blocks that exist nowhere else, so one in view is real evidence
// rather than a guess. The bot does not act on it — an agent that decided to go
// hunting strongholds from a single block would abandon whatever it was doing on
// every ruined portal it walked past. It is here to be reported, so the decision
// to go and look stays with whoever is actually deciding.
func IsStrongholdHint(name string) bool {
	return strongholdHints[Normalise(name)]
}
