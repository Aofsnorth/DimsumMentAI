// Package protect answers one question, with a reason attached: may the bot do
// this to this block, here?
//
// The bot has four ways to destroy a block and every one of them asks the same
// question for the same reason. The miner wants the ore. The chopper wants the
// tree. The scaffolder wants the cell under its feet. The steering loop wants
// whatever is between it and where it was going. All four only ask "is this
// block in the way", and all four are therefore equally willing to cut a hole
// through the wall of the base the player spent an evening building, or to
// dismantle the chest holding everything the bot just spent an hour gathering.
//
// There is no single "break" function to put a check in, and that is the point.
// This package is a decision, not a brake on someone else's machinery: a pure,
// connection-free Policy that a caller consults before it swings. It cannot
// stop a caller that never asks it, and it says so here rather than pretending
// otherwise — but it makes the answer cheap, deterministic and — most
// importantly — explainable. A refusal names the rule that fired, so the log
// says "inside the home zone" instead of leaving a player to work out why the
// bot stopped.
//
// # The unconfigured default
//
// A Policy built from an empty Config is not a Policy that protects nothing, and
// it is not one that refuses everything. It protects no *place*, because a zone
// is a claim about where home is and a wrong guess is worse than no guess: a
// synthetic zone around the origin silently freezes a bot that spawns near it,
// and one around the bot freezes it wherever it happens to be standing. So an
// empty zone list means no spatial protection, exactly as it reads.
//
// It does still protect the bot's own material, because the valuable-block list
// is not a guess about where things are — it is a statement about what a block
// is, and it applies everywhere. That asymmetry is deliberate. The failure this
// package exists to prevent is not primarily the player losing a wall; it is the
// bot walking into its own storage room and mining the diamond block it placed
// there last week, because the cell was in the way. Losing that block is a loss
// the player cannot undo and the bot cannot re-mine, so the safe direction to
// fail is the one that costs the bot its progress rather than the player their
// build. An unconfigured bot keeps mining stone; it just stops eating its
// diamonds.
//
// A caller who wants neither says so explicitly — see Config.ValuableBlocks.
package protect

import (
	"fmt"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// Bot is the slice of a live bot that the policy needs in order to answer for
// a position it was not handed a name for.
//
// It is narrow and read-only on purpose. The policy has no business holding a
// connection, writing a packet, or knowing anything about what the bot is
// currently doing; the one thing it cannot do for itself is look up a block, and
// that is the one thing that has been added. Every internal bot package
// satisfies this already, which is what lets Check be a convenience rather than
// a port.
type Bot interface {
	GetBlockName(x, y, z int32) (string, bool)
}

// Action is what the bot intends to do to a block.
//
// The zero value is not Break. A Decision that arrived without an action has to
// be refused, and it cannot be refused by falling through to a sensible-looking
// default, so Break and Build both start at 1.
type Action uint8

const (
	// Break is removing an existing block: mining, chopping, clearing a cell.
	Break Action = iota + 1

	// Build is placing a block into a cell.
	Build
)

// String names the action the way a log line wants to read it.
func (a Action) String() string {
	switch a {
	case Break:
		return "break"
	case Build:
		return "build"
	default:
		return fmt.Sprintf("action(%d)", uint8(a))
	}
}

// Decision is the answer, and the reason for it.
//
// Reason is populated on both outcomes. A refusal that says nothing sends the
// reader to the source; an allow with no reason produces a blank line in a log,
// which reads as a fault in the thing that wrote it.
type Decision struct {
	// OK is whether the action may proceed.
	OK bool

	// Reason names every rule that fired, or "no rule applies" when none did.
	Reason string
}

// Allow is the Decision returned when nothing objects. It is a value rather
// than a fresh allocation so that a caller looping over a cell grid does not
// allocate a string per cell.
var Allow = Decision{OK: true, Reason: "no rule applies"}

// deny builds a refusal. A refusal with no rules named is a bug, so the reason
// is not optional in practice.
func deny(reason string) Decision {
	return Decision{Reason: reason}
}

// Permissions is the set of actions a zone refuses inside its bounds.
//
// A bit set rather than two bools, so that a Zone literal is explicit about
// what it protects. The zero value protects nothing, which is the honest
// reading of a zero value and also the trap: a zone declared without permissions
// is inert, and says so at the point of declaration rather than at the point of
// use. Use HomeZone for the ordinary "protect everything here" case.
type Permissions uint8

const (
	// NoBreak refuses to break blocks inside the zone.
	NoBreak Permissions = 1 << iota

	// NoBuild refuses to place blocks inside the zone.
	NoBuild
)

// Config is what a caller hands to New. The zero Config is meaningful — see the
// package doc — so nothing here is required and nothing here is guessed at
// except where the guess is the safe direction.
type Config struct {
	// Zones are the protected regions, in the order they should be reported.
	// An empty list protects no place at all.
	Zones []Zone

	// ValuableBlocks names blocks that are never broken, anywhere — not only
	// inside a zone.
	//
	// nil means "not said", and gets DefaultValuableBlocks. A non-nil empty
	// slice means "I say nothing is valuable" and turns the built-in list off
	// completely. The distinction is deliberate and it is the one that matters:
	// a caller who types nothing gets protection, and a caller who types
	// nothing *on purpose* is obeyed. Set it to
	// append(DefaultValuableBlocks(), "diamond_ore") to extend rather than
	// replace.
	ValuableBlocks []string
}

// Policy is a compiled, read-only set of protection rules.
//
// It is safe for concurrent use: New copies everything it is given and the
// Policy is never mutated afterwards, so a single Policy can be shared by the
// miner, the chopper, the scaffolder and the steering loop without a lock.
type Policy struct {
	zones    []Zone
	valuable map[string]bool
}

// New compiles a Config into a Policy.
//
// The returned Policy shares nothing with cfg: the zone slice is copied by
// value and the valuable names are interned into a map. A caller that keeps its
// parsed config around — which is the normal case, since it usually came from
// YAML — cannot change what the bot is allowed to destroy by editing it later.
func New(cfg Config) *Policy {
	valuable := cfg.ValuableBlocks
	if valuable == nil {
		valuable = DefaultValuableBlocks()
	}

	set := make(map[string]bool, len(valuable))
	for _, name := range valuable {
		if key := normalise(name); key != "" {
			set[key] = true
		}
	}

	return &Policy{
		zones:    append([]Zone(nil), cfg.Zones...),
		valuable: set,
	}
}

// Allowed reports whether the bot may carry out action on the block named
// blockName at pos, and why not if it may not.
//
// It is pure: no world, no clock, no logging, and the same answer every time.
// A caller that has a block name calls this; a caller that does not has a
// position only, and calls Check.
//
// The order of the rules is fixed and every rule that fires is reported, not
// just the first. Two zones may both have something to say about one cell, and a
// refusal that names only one of them means the caller comes back after "fixing"
// the wrong one and is refused again by the other.
func (p *Policy) Allowed(action Action, pos protocol.BlockPos, blockName string) Decision {
	if action != Break && action != Build {
		return deny(fmt.Sprintf("unknown %s", action))
	}

	name := normalise(blockName)
	if action == Break && name == "" {
		// Nothing is known about this cell. A caller holding a position in a
		// chunk the world model has not seen passes "", and reading that as
		// "not valuable" is a hole exactly the shape of an unloaded chunk.
		return deny("block name unknown")
	}

	if p == nil {
		return deny("no protection policy configured")
	}

	var reasons []string
	for _, z := range p.zones {
		if z.Enabled && z.Contains(pos) && z.Refuses(action) {
			reasons = append(reasons, fmt.Sprintf("inside the %s zone: %s refused", z.Name, action))
		}
	}
	if action == Break && p.isValuable(name) {
		reasons = append(reasons, "valuable block: "+name)
	}

	if len(reasons) > 0 {
		return deny(strings.Join(reasons, "; "))
	}
	return Allow
}

// Check is Allowed for a caller holding a position and a bot rather than a
// position and a block name.
//
// It exists because forgetting the name lookup is the easiest mistake to make at
// a call site, and forgetting it at a break site means the policy sees "" and —
// correctly — refuses. That is a safe failure, but it is a confusing one, so
// the lookup lives here instead.
//
// An unloaded cell is reported as unknown rather than as empty, because the two
// are different situations that happen to produce the same string.
func (p *Policy) Check(bot Bot, action Action, pos protocol.BlockPos) Decision {
	if bot == nil {
		return deny("no world to ask about the block")
	}
	name, ok := bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		name = ""
	}
	return p.Allowed(action, pos, name)
}

// ZoneAt returns the first enabled zone whose bounds contain pos.
//
// "First" is in configuration order, not map order, so a cell claimed by two
// zones always resolves to the same one and a log line can be compared against
// the config that produced it.
func (p *Policy) ZoneAt(pos protocol.BlockPos) (Zone, bool) {
	if p == nil {
		return Zone{}, false
	}
	for _, z := range p.zones {
		if z.Enabled && z.Contains(pos) {
			return z, true
		}
	}
	return Zone{}, false
}

// IsValuable reports whether a block name is on the protected list, with the
// same normalisation the policy uses.
//
// It is a query rather than a policy: a storage sweep deciding what to pick up,
// or a planner sorting a list of targets, needs the same answer the break path
// would get, and should not have to invent a position to ask for it.
func (p *Policy) IsValuable(blockName string) bool {
	if p == nil {
		return false
	}
	return p.isValuable(normalise(blockName))
}

func (p *Policy) isValuable(name string) bool {
	if name == "" {
		return false
	}
	if p.valuable[name] {
		return true
	}
	for _, suffix := range valuableSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
