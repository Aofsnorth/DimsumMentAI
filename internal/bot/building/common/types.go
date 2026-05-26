package common

// BlockEntry represents a single block placement instruction from a blueprint or template.
type BlockEntry struct {
	X        int    `json:"x"`
	Y        int    `json:"y"`
	Z        int    `json:"z"`
	Block    string `json:"block"`
	Facing   string `json:"facing,omitempty"`
	Metadata *int   `json:"metadata,omitempty"`
}

// BuildItem represents an item available in the inventory that can be placed as a block.
type BuildItem struct {
	Slot  uint32
	Name  string
	Count int
}

// Vec3i represents integer 3D coordinates.
type Vec3i struct {
	X int `json:"x"`
	Y int `json:"y"`
	Z int `json:"z"`
}

// StructureInfo represents a scanned structure block in the world.
type StructureInfo struct {
	Name string
	X    int
	Y    int
	Z    int
}

// Concept represents the AI's high-level stylistic and architectural design.
type Concept struct {
	StructureType   string            `json:"structureType"`
	HouseType       string            `json:"houseType"`
	Style           string            `json:"style"`
	Complexity      string            `json:"complexity"`
	Dimensions      Vec3i             `json:"dimensions"`
	Sections        []string          `json:"sections"`
	Features        []string          `json:"features"`
	Materials       map[string]string `json:"materials"`
	BuildingFlow    string            `json:"buildingFlow"`
	SpecialRequests []string          `json:"specialRequests"`
}
