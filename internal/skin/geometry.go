package skin

import (
	"encoding/json"
	"fmt"
	"os"
)

type geometryEntry18 struct {
	VisibleBoundsWidth   float64        `json:"visible_bounds_width"`
	VisibleBoundsHeight  float64        `json:"visible_bounds_height"`
	VisibleBoundsOffset  []float64      `json:"visible_bounds_offset"`
	TextureWidth         float64        `json:"texturewidth"`
	TextureHeight        float64        `json:"textureheight"`
	Bones                []Bone         `json:"bones"`
}

type Bone struct {
	Name         string     `json:"name"`
	Parent       string     `json:"parent,omitempty"`
	Pivot        []float64  `json:"pivot,omitempty"`
	Cubes        []Cube     `json:"cubes,omitempty"`
	Mirror       bool       `json:"mirror,omitempty"`
	Inflate      float64    `json:"inflate,omitempty"`
	NeverRender  bool       `json:"neverRender,omitempty"`
	Locators     map[string][]float64 `json:"locators,omitempty"`
	BindPoseRotation []float64 `json:"bind_pose_rotation,omitempty"`
	Rotation     []float64  `json:"rotation,omitempty"`
	Reset        bool       `json:"reset,omitempty"`
}

type Cube struct {
	Origin []float64 `json:"origin"`
	Size   []float64 `json:"size"`
	UV     interface{} `json:"uv,omitempty"`
	Inflate float64  `json:"inflate,omitempty"`
	Mirror bool      `json:"mirror,omitempty"`
}

type geometryDescription struct {
	Identifier         string    `json:"identifier"`
	TextureWidth       float64   `json:"texture_width"`
	TextureHeight      float64   `json:"texture_height"`
	VisibleBoundsWidth float64   `json:"visible_bounds_width"`
	VisibleBoundsHeight float64  `json:"visible_bounds_height"`
	VisibleBoundsOffset []float64 `json:"visible_bounds_offset"`
}

type geometryWrapper struct {
	Description geometryDescription `json:"description"`
	Bones       []Bone             `json:"bones"`
}

type format112 struct {
	FormatVersion     string             `json:"format_version"`
	MinecraftGeometry []geometryWrapper  `json:"minecraft:geometry"`
}

func LoadGeometry(path string, geometryName string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read geometry file: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse geometry file: %w", err)
	}

	version, hasVersion := raw["format_version"]
	if hasVersion {
		var v string
		if err := json.Unmarshal(version, &v); err == nil && v == "1.12.0" {
			return data, nil
		}
	}

	entryRaw, exists := raw[geometryName]
	if !exists {
		return nil, fmt.Errorf("geometry %q not found in file", geometryName)
	}

	var entry geometryEntry18
	if err := json.Unmarshal(entryRaw, &entry); err != nil {
		return nil, fmt.Errorf("parse geometry entry %q: %w", geometryName, err)
	}

	texW := entry.TextureWidth
	texH := entry.TextureHeight
	if texW == 0 {
		texW = 64
	}
	if texH == 0 {
		texH = 64
	}

	offset := entry.VisibleBoundsOffset
	if len(offset) == 0 {
		offset = []float64{0, 1, 0}
	}

	wrapper := format112{
		FormatVersion: "1.12.0",
		MinecraftGeometry: []geometryWrapper{
			{
				Description: geometryDescription{
					Identifier:         geometryName,
					TextureWidth:       texW,
					TextureHeight:      texH,
					VisibleBoundsWidth: entry.VisibleBoundsWidth,
					VisibleBoundsHeight: entry.VisibleBoundsHeight,
					VisibleBoundsOffset: offset,
				},
				Bones: entry.Bones,
			},
		},
	}

	result, err := json.Marshal(wrapper)
	if err != nil {
		return nil, fmt.Errorf("marshal converted geometry: %w", err)
	}

	return result, nil
}
