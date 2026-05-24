package skin

import (
	_ "embed"

	"encoding/json"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

//go:embed data/geometry.json
var DefaultGeometry []byte

type resourcePatch struct {
	Geometry struct {
		Default string `json:"default"`
	} `json:"geometry"`
}

// SkinAssets holds everything needed to apply a skin both at login time
// (via login.ClientData, base64) and after spawn (via protocol.Skin, raw bytes).
type SkinAssets struct {
	ClientData   login.ClientData
	ProtocolSkin protocol.Skin
	GeometryName string
}

// BuildAssets builds both login.ClientData and protocol.Skin from the same source data.
// Uses gophertunnel's built-in geometry (includes geometry.humanoid.customSlim).
func BuildAssets(img *ImageData, geometryName string, armSize string, geometryData []byte) (*SkinAssets, error) {
	skinID := uuid.New().String() + "_Custom"

	// --- Resource patch ---
	patch := resourcePatch{}
	patch.Geometry.Default = geometryName
	patchJSON, _ := json.Marshal(patch)

	// --- login.ClientData (base64 encoded fields) ---
	clientData := login.ClientData{
		SkinID:              skinID,
		SkinData:            b64(img.RGBA),
		SkinImageHeight:     img.Height,
		SkinImageWidth:      img.Width,
		SkinResourcePatch:   b64(patchJSON),
		SkinGeometry:        b64(geometryData),
		SkinGeometryVersion: "1.12.0",
		SkinColour:          "#b37b62",
		ArmSize:             armSize,
		TrustedSkin:         true,
		PremiumSkin:         true,
		AnimatedImageData:   []login.SkinAnimation{},
		PersonaPieces:       []login.PersonaPiece{},
		PieceTintColours:    []login.PersonaPieceTintColour{},
	}

	// --- protocol.Skin (raw bytes, for PlayerSkin packet) ---
	protoSkin := protocol.Skin{
		SkinID:                    skinID,
		PlayFabID:                 "",
		SkinData:                  img.RGBA,
		SkinImageWidth:            uint32(img.Width),
		SkinImageHeight:           uint32(img.Height),
		SkinResourcePatch:         patchJSON,
		SkinGeometry:              geometryData,
		GeometryDataEngineVersion: []byte("1.12.0"),
		SkinColour:                "#b37b62",
		ArmSize:                   armSize,
		Trusted:                   true,
		PersonaPieces:             []protocol.PersonaPiece{},
		PieceTintColours:          []protocol.PersonaPieceTintColour{},
		Animations:                []protocol.SkinAnimation{},
	}

	return &SkinAssets{
		ClientData:   clientData,
		ProtocolSkin: protoSkin,
		GeometryName: geometryName,
	}, nil
}
