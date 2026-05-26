package common

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
