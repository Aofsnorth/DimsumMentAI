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

func (te *TemplateExecutor) rotateStairs(metadata *int, orientation string) *int {
	if metadata == nil {
		return nil
	}

	dir := *metadata & 3
	upsideDown := *metadata & 4

	newDir := dir
	switch strings.ToLower(orientation) {
	case "east":
		if dir == 3 {
			newDir = 0
		} else if dir == 0 {
			newDir = 2
		} else if dir == 2 {
			newDir = 1
		} else if dir == 1 {
			newDir = 3
		}
	case "south":
		if dir == 3 {
			newDir = 2
		} else if dir == 2 {
			newDir = 3
		} else if dir == 0 {
			newDir = 1
		} else if dir == 1 {
			newDir = 0
		}
	case "west":
		if dir == 3 {
			newDir = 1
		} else if dir == 1 {
			newDir = 2
		} else if dir == 2 {
			newDir = 0
		} else if dir == 0 {
			newDir = 3
		}
	}

	res := newDir | upsideDown
	return &res
}

// TransformTemplate applies rotation and translations to a template relative to origin.
func (te *TemplateExecutor) TransformTemplate(tmpl *common.Template, position common.Vec3i, orientation string) []common.BlockEntry {
	matrix := te.getRotationMatrix(orientation)
	var transformed []common.BlockEntry

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

	allowOverride := os.Getenv("AI_MATERIAL_OVERRIDE") == "true"
	primaryOverride := plan.Materials.Primary
	secondaryOverride := plan.Materials.Secondary

	var resolved []common.BlockEntry
	for _, entry := range transformed {
		blockType := entry.Block

		if allowOverride {
			if primaryOverride != "" && strings.Contains(blockType, "planks") && !strings.Contains(blockType, primaryOverride) {
				blockType = primaryOverride
			}
			if secondaryOverride != "" && (strings.Contains(blockType, "log") || strings.Contains(blockType, "stone") || strings.Contains(blockType, "cobblestone")) && !strings.Contains(blockType, secondaryOverride) {
				blockType = secondaryOverride
			}
		}

		entry.Block = strings.ReplaceAll(blockType, "minecraft:", "")
		resolved = append(resolved, entry)
	}

	for i := 0; i < len(resolved); i++ {
		for j := i + 1; j < len(resolved); j++ {
			a, b := resolved[i], resolved[j]
			if a.Y > b.Y || (a.Y == b.Y && a.Z > b.Z) || (a.Y == b.Y && a.Z == b.Z && a.X > b.X) {
				resolved[i], resolved[j] = resolved[j], resolved[i]
			}
		}
	}

	return resolved, nil
}
