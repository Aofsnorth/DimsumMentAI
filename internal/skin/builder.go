package skin

import (
	"encoding/base64"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

type resourcePatch struct {
	Geometry struct {
		Default string `json:"default"`
	} `json:"geometry"`
}

// BuildClientData builds login.ClientData with the given skin image and geometry config.
// SkinGeometry is left empty intentionally so gophertunnel fills in its default
// (which already includes geometry.humanoid.customSlim).
func BuildClientData(img *ImageData, geometryName string, armSize string) login.ClientData {
	skinB64 := base64.StdEncoding.EncodeToString(img.RGBA)

	patch := resourcePatch{}
	patch.Geometry.Default = geometryName
	patchJSON, _ := json.Marshal(patch)
	patchB64 := base64.StdEncoding.EncodeToString(patchJSON)

	skinID := uuid.New().String() + "_" + geometryName

	return login.ClientData{
		SkinData:          skinB64,
		SkinImageHeight:   img.Height,
		SkinImageWidth:    img.Width,
		SkinID:            skinID,
		SkinResourcePatch: patchB64,
		ArmSize:           armSize,
		SkinColour:        "#b37b62",
		AnimatedImageData: []login.SkinAnimation{},
		PersonaPieces:     []login.PersonaPiece{},
		PieceTintColours:  []login.PersonaPieceTintColour{},
	}
}
