package connection

import (
	"fmt"

	"bedrock-ai/internal/config"

	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
)

type Dialer struct {
	cfg          config.ServerConfig
	identityData login.IdentityData
	clientData   login.ClientData
}

func NewDialer(cfg config.ServerConfig, identityData login.IdentityData, clientData login.ClientData) *Dialer {
	return &Dialer{cfg: cfg, identityData: identityData, clientData: clientData}
}

func (d *Dialer) Dial() (*minecraft.Conn, error) {
	dialer := minecraft.Dialer{
		IdentityData: d.identityData,
		ClientData:   d.clientData,
	}

	conn, err := dialer.Dial("raknet", d.cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("dial server %s: %w", d.cfg.Address, err)
	}

	return conn, nil
}
