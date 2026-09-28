// Package config loads and validates bot configuration.
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
	cfg.Server.CommandPrefix = strings.ToLower(strings.TrimSpace(cfg.Server.CommandPrefix))
	if cfg.Server.CommandPrefix == "" {
		cfg.Server.CommandPrefix = CommandPrefixAuto
	}
	cfg.Server.ResourcePacks = strings.ToLower(strings.TrimSpace(cfg.Server.ResourcePacks))
	if cfg.Server.ResourcePacks == "" {
		cfg.Server.ResourcePacks = ResourcePacksSkip
	}
	if cfg.Server.ResourcePackDir == "" {
		cfg.Server.ResourcePackDir = "data/resourcepacks"
	}
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
	// Join messages: default to a short settle delay and a one-second gap. Zero
	// would fire the first line during the world transfer, before the server has
	// finished handing over the command list.
	if cfg.Bot.JoinMessageDelaySec <= 0 {
		cfg.Bot.JoinMessageDelaySec = 2
	}
	if cfg.Bot.JoinMessageGapSec <= 0 {
		cfg.Bot.JoinMessageGapSec = 1
	}
	// Proactive conversation: disabled by default (interval=0). When
	// enabled, default chance is 0.3 (30% of ticks actually query the LLM).
	if cfg.AI.ProactiveChance <= 0 && cfg.AI.ProactiveIntervalSec > 0 {
		cfg.AI.ProactiveChance = 0.3
	}
	applyAGIDefaults(cfg)
}

// applyAGIDefaults fills the autonomy defaults. The thresholds are chosen so a
// full 20/20 bar never trips them: a reflex that fires on a healthy bot would
// make it eat and armour up constantly.
func applyAGIDefaults(cfg *Config) {
	agi := &cfg.AGI
	if agi.TickIntervalSec <= 0 {
		agi.TickIntervalSec = 30
	}
	if agi.LLMChance <= 0 {
		agi.LLMChance = 0.35
	}
	if agi.LLMChance > 1 {
		agi.LLMChance = 1
	}
	if agi.LowHPThreshold <= 0 || agi.LowHPThreshold > 20 {
		agi.LowHPThreshold = 8
	}
	if agi.LowHunger < 0 || agi.LowHunger > 20 {
		agi.LowHunger = 6
	}
	// Ten seconds is roughly the vanilla air supply's margin: long enough that
	// a bot crossing a flooded tunnel does not panic, short enough that it is
	// already climbing before the damage starts.
	if agi.LowAirSeconds <= 0 {
		agi.LowAirSeconds = 10
	}
	if agi.WanderDurationSec <= 0 {
		agi.WanderDurationSec = 20
	}
	if agi.SocialCooldownSec <= 0 {
		agi.SocialCooldownSec = 120
	}
	if agi.VisionRadius <= 0 {
		agi.VisionRadius = 10
	}
	if agi.VisionHoldSec <= 0 {
		agi.VisionHoldSec = 5
	}
	if agi.VisionCooldownSec <= 0 {
		agi.VisionCooldownSec = 15
	}
	if agi.Jev.DangerThreshold <= 0 || agi.Jev.DangerThreshold > 1 {
		agi.Jev.DangerThreshold = 0.6
	}
	if agi.Jev.SpeakThreshold <= 0 || agi.Jev.SpeakThreshold > 1 {
		agi.Jev.SpeakThreshold = 0.75
	}
	if agi.Jev.TimeoutSec <= 0 {
		agi.Jev.TimeoutSec = 5
	}
	if agi.NightStartTicks == 0 {
		agi.NightStartTicks = 12500
	}
	if agi.NightEndTicks == 0 {
		agi.NightEndTicks = 23500
	}
	if agi.EyeHeight <= 0 {
		agi.EyeHeight = 1.62
	}
	if agi.WanderRadius <= 0 {
		agi.WanderRadius = 12
	}

	perception := &agi.Perception
	if perception.MobScanDistance <= 0 {
		perception.MobScanDistance = 32
	}
	if perception.BlockScanDistance <= 0 {
		perception.BlockScanDistance = 12
	}
	if perception.BlockScanLimit <= 0 {
		perception.BlockScanLimit = 4
	}
	if perception.MobPromptLimit <= 0 {
		perception.MobPromptLimit = 6
	}
	if perception.NearbyRadius <= 0 {
		perception.NearbyRadius = 30
	}
	if len(perception.BedKeywords) == 0 {
		perception.BedKeywords = []string{"bed"}
	}
	if agi.IdleNudgeMinSec <= 0 {
		agi.IdleNudgeMinSec = 5
	}
	if agi.IdleNudgeMaxSec <= agi.IdleNudgeMinSec {
		agi.IdleNudgeMaxSec = agi.IdleNudgeMinSec + 4
	}
	if agi.IdleNudgeReach <= 0 {
		agi.IdleNudgeReach = 6
	}
	// The mode is normalised here rather than only where the runner reads it,
	// so that everything asking the config what mode the bot is in — a status
	// line, a test, a future CLI — gets the same answer the runner will. A
	// config that reports "plan" while the bot is in planning mode is a config
	// that lies about itself.
	agi.Mode = NormalizeMode(agi.Mode)
	if agi.PlanLifetimeMin <= 0 {
		agi.PlanLifetimeMin = 60
	}
	if agi.PlanReplanMin <= 0 {
		agi.PlanReplanMin = 10
	}
	if agi.PlanMaxSteps <= 0 {
		agi.PlanMaxSteps = 12
	}
}

func validate(cfg *Config) error {
	if err := validateRequired(cfg); err != nil {
		return err
	}
	if err := validateAIProvider(cfg); err != nil {
		return err
	}
	return nil
}

func validateRequired(cfg *Config) error {
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
	switch cfg.Server.CommandPrefix {
	case CommandPrefixAuto, CommandPrefixSlash, CommandPrefixNone:
	default:
		return fmt.Errorf("server.command_prefix must be one of: %s, %s, %s",
			CommandPrefixAuto, CommandPrefixSlash, CommandPrefixNone)
	}
	switch cfg.Server.ResourcePacks {
	case ResourcePacksSkip, ResourcePacksDownload:
	default:
		return fmt.Errorf("server.resource_packs must be one of: %s, %s",
			ResourcePacksSkip, ResourcePacksDownload)
	}
	return nil
}

func validateAIProvider(cfg *Config) error {
	switch cfg.AI.Provider {
	case "", "none":
		return nil
	case "nvidia":
		return validateAIKeys(cfg, "NVIDIA_API_KEY")
	case "anthropic_compatible":
		return validateAIEndpoint(cfg, "ANTHROPIC_API_KEY")
	case "google_compatible":
		return validateAIEndpoint(cfg, "GOOGLE_API_KEY")
	// Legacy aliases, treated as OpenAI-compatible.
	case "minimax":
		return validateAIKeys(cfg, "MINIMAX_API_KEY")
	case "opengateway", "openai_compatible", "openai":
		// API key optional — local servers (vLLM, llama.cpp, etc.) often need none.
		if cfg.AI.Model == "" {
			return fmt.Errorf("ai.model is required when provider is '%s'", cfg.AI.Provider)
		}
		if cfg.AI.BaseURL == "" {
			return fmt.Errorf("ai.base_url is required when provider is '%s'", cfg.AI.Provider)
		}
		return nil
	default:
		return fmt.Errorf("unknown ai.provider %q (expected: openai_compatible, anthropic_compatible, google_compatible, nvidia, none)", cfg.AI.Provider)
	}
}

func validateAIKeys(cfg *Config, apiKeyEnv string) error {
	if os.Getenv(apiKeyEnv) == "" {
		return fmt.Errorf("%s environment variable is required when provider is '%s'", apiKeyEnv, cfg.AI.Provider)
	}
	if cfg.AI.Model == "" {
		return fmt.Errorf("ai.model is required when provider is '%s'", cfg.AI.Provider)
	}
	return nil
}

// validateAIEndpoint checks key + model + base_url for providers that need an
// explicit endpoint (every OpenAI/Anthropic/Google-compatible target except the
// built-in nvidia default).
func validateAIEndpoint(cfg *Config, apiKeyEnv string) error {
	if err := validateAIKeys(cfg, apiKeyEnv); err != nil {
		return err
	}
	if cfg.AI.BaseURL == "" {
		return fmt.Errorf("ai.base_url is required when provider is '%s'", cfg.AI.Provider)
	}
	return nil
}
