package connection

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"bedrock-ai/internal/config"
	"bedrock-ai/internal/servercompat"

	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"golang.org/x/oauth2"
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
	dialer := d.newMinecraftDialer()

	if !d.cfg.Offline {
		tokenPath := "configs/token.json"
		token, err := loadToken(tokenPath)
		if err != nil {
			fmt.Println("No persistent Microsoft Live token found. Starting interactive Microsoft login flow...")
			token, err = auth.RequestLiveToken()
			if err != nil {
				return nil, fmt.Errorf("microsoft oauth login: %w", err)
			}
			if err := saveToken(tokenPath, token); err != nil {
				fmt.Printf("Warning: failed to save token: %v\n", err)
			} else {
				fmt.Printf("Successfully saved Microsoft Live token to %s\n", tokenPath)
			}
		} else {
			fmt.Printf("Loaded persistent Microsoft Live token from %s\n", tokenPath)
		}
		dialer.TokenSource = auth.RefreshTokenSource(token)
	}

	conn, err := dialer.Dial("raknet", d.cfg.Address())
	if err != nil {
		return nil, fmt.Errorf("dial server %s: %w", d.cfg.Address(), err)
	}

	return conn, nil
}

func (d *Dialer) newMinecraftDialer() minecraft.Dialer {
	profile := servercompat.Detect(d.cfg.Host)
	return minecraft.Dialer{
		IdentityData: d.identityData,
		ClientData:   mergeClientData(d.clientData, profile),
	}
}

func loadToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func saveToken(path string, token *oauth2.Token) error {
	data, err := json.Marshal(token)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
