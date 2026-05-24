package building

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
)

// Vec3i represents integer 3D coordinates.
type Vec3i struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

// TemplateBlock represents a single block inside a template.
type TemplateBlock struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Z        int    `json:"z"`
	Type     string `json:"type"`
	Metadata *int   `json:"metadata,omitempty"`
}

// Template represents a static build structure blueprint.
type Template struct {
	StructureType string          `json:"structureType"`
	SizeCategory  string          `json:"sizeCategory"`
	Dimensions    Vec3i           `json:"dimensions"`
	Blocks        []TemplateBlock `json:"blocks"`
}

// BuildMaterials defines primary and secondary materials for a build plan.
type BuildMaterials struct {
	Primary   string `json:"primary"`
	Secondary string `json:"secondary,omitempty"`
}

// BuildPlan specifies the target template, location, orientation, and materials.
type BuildPlan struct {
	StructureType string         `json:"structureType"`
	SizeCategory  string         `json:"sizeCategory"`
	Materials     BuildMaterials `json:"materials"`
	Position      Vec3i          `json:"position"`
	Orientation   string         `json:"orientation"`
	Mode          string         `json:"mode"`
}

// IsValid checks if all required fields are present in the build plan.
func (bp *BuildPlan) IsValid() bool {
	return bp.StructureType != "" &&
		bp.SizeCategory != "" &&
		bp.Materials.Primary != "" &&
		bp.Orientation != ""
}

// ValidationResult represents the output of template compliance checks.
type ValidationResult struct {
	Valid    bool
	Errors   []string
	Warnings []string
}

func (vr *ValidationResult) AddError(msg string) {
	vr.Valid = false
	vr.Errors = append(vr.Errors, msg)
}

func (vr *ValidationResult) AddWarning(msg string) {
	vr.Warnings = append(vr.Warnings, msg)
}

// TemplateLibrary manages loaded templates.
type TemplateLibrary struct {
	templates map[string]*Template
}

// NewTemplateLibrary creates a new TemplateLibrary instance.
func NewTemplateLibrary() *TemplateLibrary {
	return &TemplateLibrary{
		templates: make(map[string]*Template),
	}
}

func (tl *TemplateLibrary) getKey(structureType, sizeCategory string) string {
	return fmt.Sprintf("%s_%s", strings.ToLower(structureType), strings.ToLower(sizeCategory))
}

// RegisterTemplate registers a template in the library.
func (tl *TemplateLibrary) RegisterTemplate(structureType, sizeCategory string, template *Template) {
	key := tl.getKey(structureType, sizeCategory)
	tl.templates[key] = template
}

// HasTemplate checks if a template exists in the library.
func (tl *TemplateLibrary) HasTemplate(structureType, sizeCategory string) bool {
	return tl.templates[tl.getKey(structureType, sizeCategory)] != nil
}

// GetTemplate retrieves a copy of the registered template.
func (tl *TemplateLibrary) GetTemplate(structureType, sizeCategory string) *Template {
	tmpl := tl.templates[tl.getKey(structureType, sizeCategory)]
	if tmpl == nil {
		return nil
	}

	// Deep copy by marshal/unmarshal to prevent mutation of library templates
	data, err := json.Marshal(tmpl)
	if err != nil {
		return nil
	}

	var copyTmpl Template
	_ = json.Unmarshal(data, &copyTmpl)
	return &copyTmpl
}

// LoadEmbeddedTemplates loads templates packaged in the Go binary using embed.FS.
func (tl *TemplateLibrary) LoadEmbeddedTemplates() error {
	entries, err := templateFS.ReadDir("templates/house")
	if err != nil {
		return fmt.Errorf("read embedded dir: %w", err)
	}

	loaded := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := "templates/house/" + entry.Name()
		data, err := templateFS.ReadFile(path)
		if err != nil {
			continue
		}

		var tmpl Template
		if err := json.Unmarshal(data, &tmpl); err != nil {
			continue
		}

		val := tl.ValidateTemplate(&tmpl)
		if !val.Valid {
			continue
		}

		tl.RegisterTemplate(tmpl.StructureType, tmpl.SizeCategory, &tmpl)
		loaded++
	}

	return nil
}

// ValidateTemplate runs safety checks on structural connectivity and dimensions.
func (tl *TemplateLibrary) ValidateTemplate(tmpl *Template) ValidationResult {
	result := ValidationResult{Valid: true}

	if tmpl == nil {
		result.AddError("Template is nil")
		return result
	}

	if tmpl.StructureType == "" || tmpl.SizeCategory == "" {
		result.AddError("Template must have structureType and sizeCategory")
	}

	if len(tmpl.Blocks) == 0 {
		result.AddError("Template must have a blocks array")
		return result
	}

	coords := make(map[string]bool)
	minX, minY, minZ := math.MaxInt, math.MaxInt, math.MaxInt
	maxX, maxY, maxZ := math.MinInt, math.MinInt, math.MinInt

	for _, block := range tmpl.Blocks {
		minX = min(minX, block.X)
		minY = min(minY, block.Y)
		minZ = min(minZ, block.Z)

		maxX = max(maxX, block.X)
		maxY = max(maxY, block.Y)
		maxZ = max(maxZ, block.Z)

		if block.Type == "" {
			result.AddError(fmt.Sprintf("Block at (%d,%d,%d) has invalid empty type", block.X, block.Y, block.Z))
		}

		key := fmt.Sprintf("%d,%d,%d", block.X, block.Y, block.Z)
		if coords[key] {
			result.AddError(fmt.Sprintf("Duplicate block at coordinates: %s", key))
		}
		coords[key] = true
	}

	if tmpl.Dimensions.X > 0 {
		spanX := maxX - minX + 1
		spanY := maxY - minY + 1
		spanZ := maxZ - minZ + 1

		if spanX > tmpl.Dimensions.X || spanY > tmpl.Dimensions.Y || spanZ > tmpl.Dimensions.Z {
			result.AddError("Template blocks exceed declared dimensions")
		}
	}

	if !tl.isConnected(tmpl.Blocks) {
		result.AddWarning("Template contains disconnected blocks (floating blocks detected)")
	}

	return result
}

func (tl *TemplateLibrary) isConnected(blocks []TemplateBlock) bool {
	if len(blocks) <= 1 {
		return true
	}

	blockMap := make(map[string]bool)
	for _, b := range blocks {
		blockMap[fmt.Sprintf("%d,%d,%d", b.X, b.Y, b.Z)] = true
	}

	visited := make(map[string]bool)
	start := blocks[0]
	startKey := fmt.Sprintf("%d,%d,%d", start.X, start.Y, start.Z)
	queue := []string{startKey}
	visited[startKey] = true

	directions := [][]int{
		{1, 0, 0}, {-1, 0, 0},
		{0, 1, 0}, {0, -1, 0},
		{0, 0, 1}, {0, 0, -1},
	}

	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]

		var x, y, z int
		_, _ = fmt.Sscanf(curr, "%d,%d,%d", &x, &y, &z)

		for _, dir := range directions {
			nx := x + dir[0]
			ny := y + dir[1]
			nz := z + dir[2]
			nKey := fmt.Sprintf("%d,%d,%d", nx, ny, nz)

			if blockMap[nKey] && !visited[nKey] {
				visited[nKey] = true
				queue = append(queue, nKey)
			}
		}
	}

	return len(visited) == len(blockMap)
}

// GetLibrarySummary returns all templates registered.
func (tl *TemplateLibrary) GetLibrarySummary() string {
	var summary []string
	for key, template := range tl.templates {
		counts := make(map[string]int)
		for _, b := range template.Blocks {
			counts[b.Type]++
		}

		var reqs []string
		for name, count := range counts {
			reqs = append(reqs, fmt.Sprintf("%s:%d", name, count))
		}
		summary = append(summary, fmt.Sprintf("%s: %s", key, strings.Join(reqs, ", ")))
	}
	return strings.Join(summary, " | ")
}

// TemplateExecutor instantiates a build plan from templates with correct rotations and offsets.
type TemplateExecutor struct {
	library *TemplateLibrary
	bot     BotInterface
}

// NewTemplateExecutor creates a new TemplateExecutor.
func NewTemplateExecutor(library *TemplateLibrary, bot BotInterface) *TemplateExecutor {
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

func (te *TemplateExecutor) applyRotation(block TemplateBlock, matrix [][]int) Vec3i {
	return Vec3i{
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
func (te *TemplateExecutor) TransformTemplate(tmpl *Template, position Vec3i, orientation string) []BlockEntry {
	matrix := te.getRotationMatrix(orientation)
	var transformed []BlockEntry

	for _, block := range tmpl.Blocks {
		rot := te.applyRotation(block, matrix)
		finalMeta := block.Metadata

		if strings.Contains(block.Type, "stairs") {
			finalMeta = te.rotateStairs(block.Metadata, orientation)
		}

		transformed = append(transformed, BlockEntry{
			X:        rot.X + position.X,
			Y:        rot.Y + position.Y,
			Z:        rot.Z + position.Z,
			Block:    block.Type,
			Metadata: finalMeta,
		})
	}

	return transformed
}

// ExecuteTemplate generates the final BlockEntry list, sorting bottom-up, with material override handling.
func (te *TemplateExecutor) ExecuteTemplate(plan *BuildPlan) ([]BlockEntry, error) {
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

	var resolved []BlockEntry
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

	// Sort: Bottom to top (Y), then Z, then X
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
