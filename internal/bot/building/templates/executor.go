package templates

import (
	"fmt"
	"os"
	"strings"

	"bedrock-ai/internal/bot/building/common"
)

// TemplateExecutor instantiates a build plan from templates.
type TemplateExecutor struct {
	library *TemplateLibrary
	bot     common.BotInterface
}

// NewTemplateExecutor creates a new TemplateExecutor.
func NewTemplateExecutor(library *TemplateLibrary, bot common.BotInterface) *TemplateExecutor {
	return &TemplateExecutor{
		library: library,
		bot:     bot,
	}
}

func (te *TemplateExecutor) getRotationMatrix(orientation string) [][]int {
	switch strings.ToLower(orientation) {
	case "north":
		return [][]int{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	case "east":
		return [][]int{{0, 0, -1}, {0, 1, 0}, {1, 0, 0}}
	case "south":
		return [][]int{{-1, 0, 0}, {0, 1, 0}, {0, 0, -1}}
	case "west":
		return [][]int{{0, 0, 1}, {0, 1, 0}, {-1, 0, 0}}
	default:
		return [][]int{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	}
}

func (te *TemplateExecutor) applyRotation(block common.TemplateBlock, matrix [][]int) common.Vec3i {
	return common.Vec3i{
		X: block.X*matrix[0][0] + block.Y*matrix[0][1] + block.Z*matrix[0][2],
		Y: block.X*matrix[1][0] + block.Y*matrix[1][1] + block.Z*matrix[1][2],
		Z: block.X*matrix[2][0] + block.Y*matrix[2][1] + block.Z*matrix[2][2],
	}
}

var stairRotations = map[string][4]int{
	"east":  {2, 3, 1, 0},
	"south": {1, 0, 3, 2},
	"west":  {3, 2, 0, 1},
}

func (te *TemplateExecutor) rotateStairs(metadata *int, orientation string) *int {
	if metadata == nil {
		return nil
	}

	dir := *metadata & 3
	upsideDown := *metadata & 4

	rotation, ok := stairRotations[strings.ToLower(orientation)]
	if !ok {
		return metadata
	}

	res := rotation[dir] | upsideDown
	return &res
}

// TransformTemplate applies rotation and translations to a template relative to origin.
func (te *TemplateExecutor) TransformTemplate(tmpl *common.Template, position common.Vec3i, orientation string) []common.BlockEntry {
	matrix := te.getRotationMatrix(orientation)
	transformed := make([]common.BlockEntry, 0, len(tmpl.Blocks))

	for _, block := range tmpl.Blocks {
		rot := te.applyRotation(block, matrix)
		finalMeta := block.Metadata

		if strings.Contains(block.Type, "stairs") {
			finalMeta = te.rotateStairs(block.Metadata, orientation)
		}

		transformed = append(transformed, common.BlockEntry{
			X:        rot.X + position.X,
			Y:        rot.Y + position.Y,
			Z:        rot.Z + position.Z,
			Block:    block.Type,
			Metadata: finalMeta,
		})
	}
	return transformed
}

// ExecuteTemplate generates the final BlockEntry list, sorting bottom-up.
func (te *TemplateExecutor) ExecuteTemplate(plan *common.BuildPlan) ([]common.BlockEntry, error) {
	if plan == nil || !plan.IsValid() {
		return nil, fmt.Errorf("invalid build plan")
	}

	tmpl := te.library.GetTemplate(plan.StructureType, plan.SizeCategory)
	if tmpl == nil {
		return nil, fmt.Errorf("template not found: %s_%s", plan.StructureType, plan.SizeCategory)
	}

	transformed := te.TransformTemplate(tmpl, plan.Position, plan.Orientation)
	resolved := te.resolveMaterials(transformed, plan.Materials.Primary, plan.Materials.Secondary)
	sortByLayerBottomUp(resolved)
	return resolved, nil
}

func (te *TemplateExecutor) resolveMaterials(entries []common.BlockEntry, primaryOverride, secondaryOverride string) []common.BlockEntry {
	allowOverride := os.Getenv("AI_MATERIAL_OVERRIDE") == "true"
	resolved := make([]common.BlockEntry, 0, len(entries))
	for _, entry := range entries {
		entry.Block = strings.ReplaceAll(resolveBlockType(entry.Block, allowOverride, primaryOverride, secondaryOverride), "minecraft:", "")
		resolved = append(resolved, entry)
	}
	return resolved
}

func resolveBlockType(blockType string, allowOverride bool, primaryOverride, secondaryOverride string) string {
	if !allowOverride {
		return blockType
	}
	if primaryOverride != "" && strings.Contains(blockType, "planks") && !strings.Contains(blockType, primaryOverride) {
		blockType = primaryOverride
	}
	if secondaryOverride != "" && (strings.Contains(blockType, "log") || strings.Contains(blockType, "stone") || strings.Contains(blockType, "cobblestone")) && !strings.Contains(blockType, secondaryOverride) {
		blockType = secondaryOverride
	}
	return blockType
}

func sortByLayerBottomUp(entries []common.BlockEntry) {
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			a, b := entries[i], entries[j]
			if a.Y > b.Y || (a.Y == b.Y && a.Z > b.Z) || (a.Y == b.Y && a.Z == b.Z && a.X > b.X) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}
}
