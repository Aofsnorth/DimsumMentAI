package config

import (
	"fmt"
	"os"
	"strings"

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
	applyDefaults(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Bot.Language == "" {
		cfg.Bot.Language = "Indonesian"
	}
	if cfg.Bot.LogLevel == "" {
		cfg.Bot.LogLevel = "info"
	}
	cfg.Bot.LogLevel = strings.ToLower(strings.TrimSpace(cfg.Bot.LogLevel))
	cfg.Bot.Language = strings.TrimSpace(cfg.Bot.Language)
	if cfg.Bot.StatePath == "" {
		cfg.Bot.StatePath = "data/bot_state.json"
	}
	if cfg.Chat.DuplicateWindowSec <= 0 {
		cfg.Chat.DuplicateWindowSec = 3
	}
	if cfg.Chat.RateLimitWindowSec <= 0 {
		cfg.Chat.RateLimitWindowSec = 10
	}
	if cfg.Chat.MaxMessagesPerWindow <= 0 {
		cfg.Chat.MaxMessagesPerWindow = 100
	}
}

func validate(cfg *Config) error {
	if cfg.Server.Host == "" {
		return fmt.Errorf("server.host is required")
	}
	if cfg.Server.Port <= 0 {
		return fmt.Errorf("server.port must be > 0")
	}
	if cfg.Bot.Name == "" {
		return fmt.Errorf("bot.name is required")
	}
	if cfg.Skin.ImagePath == "" {
		return fmt.Errorf("skin.image_path is required")
	}
	if cfg.Skin.ArmSize != "slim" && cfg.Skin.ArmSize != "wide" {
		return fmt.Errorf("skin.arm_size must be 'slim' or 'wide'")
	}
	switch cfg.Bot.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("bot.log_level must be one of: debug, info, warn, error")
	}
	switch cfg.AI.Provider {
	case "", "none":
		// AI disabled
	case "nvidia":
		if cfg.AI.ApiKey == "" && os.Getenv("NVIDIA_API_KEY") == "" {
			return fmt.Errorf("ai.api_key or NVIDIA_API_KEY environment variable is required when provider is 'nvidia'")
		}
		if cfg.AI.Model == "" {
			return fmt.Errorf("ai.model is required when provider is 'nvidia'")
		}
	case "minimax":
		if cfg.AI.ApiKey == "" && os.Getenv("MINIMAX_API_KEY") == "" {
			return fmt.Errorf("ai.api_key or MINIMAX_API_KEY environment variable is required when provider is 'minimax'")
		}
		if cfg.AI.Model == "" {
			return fmt.Errorf("ai.model is required when provider is 'minimax'")
		}
	case "opengateway", "openai_compatible":
		if cfg.AI.ApiKey == "" && os.Getenv("OPENAI_API_KEY") == "" {
			return fmt.Errorf("ai.api_key or OPENAI_API_KEY environment variable is required when provider is '%s'", cfg.AI.Provider)
		}
		if cfg.AI.BaseURL == "" {
			return fmt.Errorf("ai.base_url is required when provider is '%s'", cfg.AI.Provider)
		}
		if cfg.AI.Model == "" {
			return fmt.Errorf("ai.model is required when provider is '%s'", cfg.AI.Provider)
		}
	default:
		return fmt.Errorf("unknown ai.provider %q (expected: nvidia, minimax, opengateway, openai_compatible, none)", cfg.AI.Provider)
	}
	return nil
}
