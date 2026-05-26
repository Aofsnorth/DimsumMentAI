package schematic

import (
	"encoding/json"
	"regexp"
	"strings"

	"bedrock-ai/internal/bot/building/common"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// ParsePlan extracts and repairs a JSON block array from raw AI output.
func ParsePlan(raw string) []common.BlockEntry {
	cleaned := strings.ReplaceAll(raw, "```json", "")
	cleaned = strings.ReplaceAll(cleaned, "```", "")
	cleaned = strings.TrimSpace(cleaned)

	firstBracket := strings.Index(cleaned, "[")
	if firstBracket == -1 {
		return nil
	}

	lastBracket := strings.LastIndex(cleaned, "]")
	if lastBracket == -1 || lastBracket <= firstBracket {
		lastBrace := strings.LastIndex(cleaned, "}")
		if lastBrace > firstBracket {
			cleaned = cleaned[firstBracket:lastBrace+1] + "]"
		} else {
			return nil
		}
	} else {
		cleaned = cleaned[firstBracket : lastBracket+1]
	}

	cleaned = regexp.MustCompile(`,\s*\]`).ReplaceAllString(cleaned, "]")
	cleaned = regexp.MustCompile(`}\s*,\s*$`).ReplaceAllString(cleaned, "}]")

	var plan []common.BlockEntry
	if err := json.Unmarshal([]byte(cleaned), &plan); err != nil {
		return nil
	}

	var valid []common.BlockEntry
	for _, entry := range plan {
		if entry.Block != "" {
			entry.Block = strings.ReplaceAll(entry.Block, "minecraft:", "")
			valid = append(valid, entry)
		}
	}
	return valid
}

// GetBuildItems returns a list of buildable items currently in the bot's inventory.
func GetBuildItems(inv map[uint32]protocol.ItemStack, names map[int32]string) []common.BuildItem {
	var items []common.BuildItem
	for slot, stack := range inv {
		if stack.Count <= 0 || stack.NetworkID == 0 {
			continue
		}
		name := names[stack.NetworkID]
		if name == "" {
			continue
		}
		name = strings.ReplaceAll(name, "minecraft:", "")
		if IsBuildable(name) {
			items = append(items, common.BuildItem{
				Slot:  slot,
				Name:  name,
				Count: int(stack.Count),
			})
		}
	}
	return items
}

// FindItemInSlots searches inventory for a specific item by name.
func FindItemInSlots(inv map[uint32]protocol.ItemStack, names map[int32]string, name string) (uint32, bool) {
	name = strings.ReplaceAll(name, "minecraft:", "")
	for slot, stack := range inv {
		if stack.Count <= 0 || stack.NetworkID == 0 {
			continue
		}
		iName := names[stack.NetworkID]
		iName = strings.ReplaceAll(iName, "minecraft:", "")
		if iName == name {
			return slot, true
		}
	}
	return 0, false
}
