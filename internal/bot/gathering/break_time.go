// Package gathering provides helpers for collecting blocks and resources.
package gathering

import (
	"strings"
	"time"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type blockRule struct {
	match    func(name string) bool
	hardness float64
	tool     string
}

// blockRules are matched in order. The first matching rule determines the
// block's hardness and preferred tool.
var blockRules = []blockRule{
	{match: func(name string) bool { return strings.Contains(name, "obsidian") }, hardness: 50.0, tool: "pickaxe"},
	{match: func(name string) bool {
		return strings.Contains(name, "ore") && !strings.Contains(name, "redstone") && !strings.Contains(name, "coal")
	}, hardness: 3.0, tool: "pickaxe"},
	{match: func(name string) bool {
		return strings.Contains(name, "redstone_ore") || strings.Contains(name, "coal_ore")
	}, hardness: 3.0, tool: "pickaxe"},
	{match: func(name string) bool { return strings.Contains(name, "deepslate") }, hardness: 3.5, tool: "pickaxe"},
	{match: func(name string) bool { return strings.Contains(name, "cobble") || name == "stone" }, hardness: 1.5, tool: "pickaxe"},
	{match: func(name string) bool { return strings.Contains(name, "stone") }, hardness: 1.5, tool: "pickaxe"},
	{match: func(name string) bool { return strings.Contains(name, "iron_block") }, hardness: 5.0, tool: "pickaxe"},
	{match: func(name string) bool {
		return strings.Contains(name, "log") || strings.Contains(name, "wood") || strings.Contains(name, "planks")
	}, hardness: 2.0, tool: "axe"},
	{match: func(name string) bool { return strings.Contains(name, "leaves") }, hardness: 0.2, tool: "shears"},
	{match: func(name string) bool { return strings.Contains(name, "grass") && !strings.Contains(name, "block") }, hardness: 0.1, tool: "shears"},
	{match: func(name string) bool { return strings.Contains(name, "sand") || strings.Contains(name, "gravel") }, hardness: 0.5, tool: "shovel"},
	{match: func(name string) bool {
		return strings.Contains(name, "dirt") || strings.Contains(name, "grass_block") || strings.Contains(name, "podzol") || strings.Contains(name, "mycelium")
	}, hardness: 0.5, tool: "shovel"},
	{match: func(name string) bool { return strings.Contains(name, "snow") }, hardness: 0.2, tool: "shovel"},
	{match: func(name string) bool { return strings.Contains(name, "clay") }, hardness: 0.6, tool: "shovel"},
}

// matchBlock returns the hardness and preferred tool for a block name.
func matchBlock(name string) (float64, string) {
	for _, rule := range blockRules {
		if rule.match(name) {
			return rule.hardness, rule.tool
		}
	}
	return 1.0, ""
}

// toolSpeed returns the mining speed multiplier for a tool.
func toolSpeed(tool, preferredTool string) float64 {
	if preferredTool == "" || !strings.Contains(tool, preferredTool) {
		return 1.0
	}
	switch {
	case strings.Contains(tool, "netherite"):
		return 9.0
	case strings.Contains(tool, "diamond"):
		return 8.0
	case strings.Contains(tool, "iron"):
		return 6.0
	case strings.Contains(tool, "stone"):
		return 4.0
	case strings.Contains(tool, "wooden"), strings.Contains(tool, "wood"):
		return 2.0
	case strings.Contains(tool, "golden"), strings.Contains(tool, "gold"):
		return 12.0
	}
	return 1.0
}

// blockBreakDuration returns how long the bot should swing before sending
// PredictDestroyBlock. Servers reject destroy packets that arrive earlier than
// the expected hardness×toolSpeed time, leaving the block intact and the swing
// animation looking pointless. Values track vanilla Bedrock hardness with a
// small tolerance subtracted (servers typically accept ~50ms early).
//
// blockName is matched as a substring (e.g. "minecraft:oak_log" matches "log").
// toolName is the currently equipped item; empty string means bare-handed.
func blockBreakDuration(blockName, toolName string) time.Duration {
	name := strings.ToLower(strings.TrimPrefix(blockName, "minecraft:"))
	tool := strings.ToLower(toolName)

	hardness, preferredTool := matchBlock(name)
	speed := toolSpeed(tool, preferredTool)

	// Vanilla formula: base = hardness × (canHarvest ? 1.5 : 5.0). Bot is
	// considered eligible to harvest its target (we already pick the right
	// tool category), so use 1.5.
	seconds := hardness * 1.5 / speed
	if seconds < 0.15 {
		seconds = 0.15 // floor so the swing has at least a tick to animate
	}

	// Subtract a small tolerance (server accepts destroys ~50ms early), but
	// never below the floor.
	ms := int(seconds*1000) - 80
	if ms < 150 {
		ms = 150
	}
	return time.Duration(ms) * time.Millisecond
}

// sabdBreakMargin pads the computed break duration on servers that negotiated
// server-authoritative block breaking. Those servers validate that the full
// vanilla break time elapsed before honouring PredictDestroy, and
// blockBreakDuration is biased 80ms EARLY for legacy servers. An early
// PredictDestroy is silently rejected — the block stays while the bot walks
// off convinced it chopped. Empirically on the LAN host: 1.5s of ContinueDestroy
// for a 3.0s oak log was rejected, 3.5s was honoured.
const sabdBreakMargin = 300 * time.Millisecond

// sabdBreakDuration returns how long the bot must keep mining before finishing
// a break: the legacy duration, plus the server-auth margin when the current
// server requires the PlayerAuthInput block-action form.
func sabdBreakDuration(serverAuthBreaking bool, blockName, toolName string) time.Duration {
	d := blockBreakDuration(blockName, toolName)
	if serverAuthBreaking {
		d += sabdBreakMargin
	}
	return d
}

// equippedToolName returns the item name currently in the bot's held slot.
// Returns empty string when the held slot is empty or unknown.
func (bm *BlockMiner) equippedToolName() string {
	bot := bm.rg.bot
	slot := bot.GetHeldItemSlot()
	inv := bot.GetInventorySlots()
	item, ok := inv[slot]
	if !ok || item.Count == 0 {
		return ""
	}
	names := bot.GetItemNames()
	return names[item.NetworkID]
}

// BreakDuration reports how long the bot must actually work on a block before
// the server will let it predict the destroy.
//
// It is exported because three build paths were each sleeping a hardcoded
// 300-500ms and calling that the break. A fixed sleep is right for exactly one
// block: for obsidian it finishes the "break" long before the server agrees the
// block is gone, and an early PredictDestroy on a server-authoritative host is
// silently rejected — so the block survives, silently, forever.
func BreakDuration(b BreakProbe, blockName string) time.Duration {
	serverAuth := false
	if sabd, ok := b.(interface{ ServerAuthBlockBreaking() bool }); ok {
		serverAuth = sabd.ServerAuthBlockBreaking()
	}
	return sabdBreakDuration(serverAuth, blockName, heldToolName(b))
}

// BreakProbe is the part of a bot the break duration needs: what is in its hand,
// and whether this host wants its destroys predicted.
//
// It is deliberately three methods wide rather than the full gathering.Bot. The
// build paths hold a common.BotInterface, which is a smaller interface, and
// widening it to satisfy a mining abstraction would have meant every builder
// implementing mining methods it has no use for.
type BreakProbe interface {
	GetHeldItemSlot() uint32
	GetInventorySlots() map[uint32]protocol.ItemStack
	GetItemNames() map[int32]string
}

// heldToolName is equippedToolName for a bot rather than for a miner: the tool
// in the hand is a property of the body, not of the thing doing the mining.
func heldToolName(b BreakProbe) string {
	slot := b.GetHeldItemSlot()
	item, ok := b.GetInventorySlots()[slot]
	if !ok || item.Count == 0 {
		return ""
	}
	return b.GetItemNames()[item.NetworkID]
}
