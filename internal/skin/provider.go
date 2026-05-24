package skin

import (
	"fmt"

	"bedrock-ai/internal/config"
)

type Provider struct {
	cfg config.SkinConfig
}

func NewProvider(cfg config.SkinConfig) *Provider {
	return &Provider{cfg: cfg}
}

func (p *Provider) Provide() (*SkinAssets, error) {
	img, err := LoadImage(p.cfg.ImagePath)
	if err != nil {
		return nil, fmt.Errorf("load skin image: %w", err)
	}

	// Determine the geometry name based on arm size
	var geometryName string
	switch p.cfg.ArmSize {
	case "slim":
		geometryName = "geometry.humanoid.customSlim"
	case "wide":
		geometryName = "geometry.humanoid.custom"
	default:
		return nil, fmt.Errorf("unknown arm size: %q", p.cfg.ArmSize)
	}

	assets, err := BuildAssets(img, geometryName, p.cfg.ArmSize, DefaultGeometry)
	if err != nil {
		return nil, fmt.Errorf("build skin assets: %w", err)
	}

	return assets, nil
}
