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

	woodTypes := []string{"oak", "spruce", "birch", "jungle", "acacia", "dark_oak", "mangrove", "cherry", "crimson", "warped"}
	for _, woodType := range woodTypes {
		if strings.Contains(preferred, woodType) {
			return strings.Contains(blockName, woodType)
		}
	}
	return true
}
