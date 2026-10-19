package gathering

import "strings"

func isLogBlockName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	if strings.Contains(name, "leaves") {
		return false
	}
	return strings.HasSuffix(name, "_log") ||
		strings.Contains(name, "_log[") ||
		strings.HasSuffix(name, "_wood") ||
		strings.Contains(name, "_wood[") ||
		strings.HasSuffix(name, "_stem") ||
		strings.Contains(name, "_stem[") ||
		strings.HasSuffix(name, "_hyphae") ||
		strings.Contains(name, "_hyphae[")
}

func matchesPreferredLog(blockName, preferred string) bool {
	preferred = strings.ToLower(strings.TrimSpace(preferred))
	if preferred == "" {
		return true
	}
	preferred = strings.ReplaceAll(preferred, " ", "_")
	preferred = strings.TrimPrefix(preferred, "minecraft:")
	blockName = strings.ToLower(strings.TrimPrefix(blockName, "minecraft:"))

	// Longest material name first: "oak" is a substring of "dark_oak", so the
	// old order accepted plain oak when the caller asked for dark oak. Sorting by
	// length makes the most specific name win.
	woodTypes := []string{"dark_oak", "mangrove", "crimson", "warped", "cherry", "spruce", "jungle", "acacia", "birch", "oak"}
	for _, woodType := range woodTypes {
		if strings.Contains(preferred, woodType) {
			return blockNameHasWoodType(blockName, woodType)
		}
	}
	return true
}

// blockNameHasWoodType matches a wood type as a whole name segment, so "oak"
// does not match "dark_oak_log".
func blockNameHasWoodType(blockName, woodType string) bool {
	return blockName == woodType ||
		strings.HasPrefix(blockName, woodType+"_") ||
		strings.Contains(blockName, "_"+woodType+"_") ||
		strings.HasSuffix(blockName, "_"+woodType)
}
