package skin

import (
	"fmt"

	"bedrock-ai/internal/config"

	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

type Provider struct {
	cfg config.SkinConfig
}

func NewProvider(cfg config.SkinConfig) *Provider {
	return &Provider{cfg: cfg}
}

func (p *Provider) Provide() (login.ClientData, error) {
	img, err := LoadImage(p.cfg.ImagePath)
	if err != nil {
		return login.ClientData{}, fmt.Errorf("load skin image: %w", err)
	}

	clientData := BuildClientData(img, p.cfg.GeometryName, p.cfg.ArmSize)
	return clientData, nil
}
