package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// LoadEnv attempts to load a .env file from the current working directory.
// A missing .env file is not an error — environment variables may already be
// set in the shell. Loaded variables do NOT overwrite existing env vars
// (godotenv default), so explicit shell exports always win.
func LoadEnv() error {
	if err := godotenv.Load(); err != nil {
		// Only treat read errors (e.g. malformed file) as fatal; a missing
		// .env is fine when the vars are already in the environment.
		if _, ok := err.(*os.PathError); !ok {
			return fmt.Errorf("load .env: %w", err)
		}
	}
	return nil
}

func Load(path string) (*Config, error) {
	// Load .env from the working directory so API keys and other secrets can
	// live outside the YAML. Existing env vars take precedence over .env.
	_ = LoadEnv()

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
	// Proactive conversation: disabled by default (interval=0). When
	// enabled, default chance is 0.3 (30% of ticks actually query the LLM).
	if cfg.AI.ProactiveChance <= 0 && cfg.AI.ProactiveIntervalSec > 0 {
		cfg.AI.ProactiveChance = 0.3
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
		if os.Getenv("NVIDIA_API_KEY") == "" {
			return fmt.Errorf("NVIDIA_API_KEY environment variable is required when provider is 'nvidia'")
		}
		if cfg.AI.Model == "" {
			return fmt.Errorf("ai.model is required when provider is 'nvidia'")
		}
	case "minimax":
		if os.Getenv("MINIMAX_API_KEY") == "" {
			return fmt.Errorf("MINIMAX_API_KEY environment variable is required when provider is 'minimax'")
		}
		if cfg.AI.Model == "" {
			return fmt.Errorf("ai.model is required when provider is 'minimax'")
		}
	case "opengateway", "openai_compatible":
		if os.Getenv("OPENAI_API_KEY") == "" {
			return fmt.Errorf("OPENAI_API_KEY environment variable is required when provider is '%s'", cfg.AI.Provider)
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
