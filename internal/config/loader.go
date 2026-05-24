package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

func validate(cfg *Config) error {
	if cfg.Server.Address == "" {
		return fmt.Errorf("server.address is required")
	}
	if cfg.Bot.Name == "" {
		return fmt.Errorf("bot.name is required")
	}
	if cfg.Skin.ImagePath == "" {
		return fmt.Errorf("skin.image_path is required")
	}
	if cfg.Skin.GeometryPath == "" {
		return fmt.Errorf("skin.geometry_path is required")
	}
	if cfg.Skin.GeometryName == "" {
		return fmt.Errorf("skin.geometry_name is required")
	}
	if cfg.Skin.ArmSize != "slim" && cfg.Skin.ArmSize != "wide" {
		return fmt.Errorf("skin.arm_size must be 'slim' or 'wide'")
	}
	return nil
}
