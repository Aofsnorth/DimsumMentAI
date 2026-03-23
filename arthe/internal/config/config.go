package config

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Config represents the top-level configuration for ElysiaBot
type Config struct {
	Bot     BotConfig     `yaml:"bot"`
	Server  ServerConfig  `yaml:"server"`
	Skin    SkinConfig    `yaml:"skin"`
	Chat    ChatConfig    `yaml:"chat"`
	LLM     LLMConfig     `yaml:"llm"`
	Camera  CameraConfig  `yaml:"camera"`
	Memory  MemoryConfig  `yaml:"memory"`
	Logging LoggingConfig `yaml:"logging"`
}

// BotConfig contains bot-related settings
type BotConfig struct {
	DisplayName string `yaml:"display_name"`
	MentionName string `yaml:"mention_name"`
	AuthMode    string `yaml:"auth_mode"`
}

// ServerConfig contains connection settings for the Minecraft server
type ServerConfig struct {
	Address string `yaml:"address"`
	UseTLS  bool   `yaml:"use_tls"`
}

// SkinConfig contains skin customization settings
type SkinConfig struct {
	Path            string `yaml:"path"`
	GeometryPath    string `yaml:"geometry_path"`
	GeometryName    string `yaml:"geometry_name"`
	ApplyOnFirstJoin bool  `yaml:"apply_on_first_join"`
}

// ChatConfig contains chat-related settings
type ChatConfig struct {
	Mode      string `yaml:"mode"`
	QueueMode string `yaml:"queue_mode"`
}

// LLMConfig contains Large Language Model settings
type LLMConfig struct {
	Provider        string `yaml:"provider"`
	APIKey          string `yaml:"api_key"`
	Model           string `yaml:"model"`
	BaseURL         string `yaml:"base_url"`
	MaxContextTokens int    `yaml:"max_context_tokens"`
}

// CameraConfig contains camera/rendering settings
type CameraConfig struct {
	Enabled         bool    `yaml:"enabled"`
	TickMin         int     `yaml:"tick_min"`
	TickMax         int     `yaml:"tick_max"`
	ViewRadius      float64 `yaml:"view_radius"`
	PlayerEyeHeight float64 `yaml:"player_eye_height"`
}

// MemoryConfig contains memory/rememberance settings
type MemoryConfig struct {
	Storage          string `yaml:"storage"`
	FilePath         string `yaml:"file_path"`
	AutoSaveInterval int    `yaml:"auto_save_interval"`
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	Level   string       `yaml:"level"`
	Console bool          `yaml:"console"`
	File    FileLogConfig `yaml:"file"`
	Discord DiscordConfig `yaml:"discord"`
}

// FileLogConfig contains file logging settings
type FileLogConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Path       string `yaml:"path"`
	MaxSizeMB  int    `yaml:"max_size_mb"`
	MaxBackups int    `yaml:"max_backups"`
}

// DiscordConfig contains Discord integration settings
type DiscordConfig struct {
	Enabled   bool   `yaml:"enabled"`
	WebhookURL string `yaml:"webhook_url"`
	Level     string `yaml:"level"`
}

var envVarPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// resolveEnvVars recursively resolves ${ENV_VAR} patterns in string fields
func resolveEnvVars(cfg *Config) {
	resolveString(&cfg.Bot.DisplayName)
	resolveString(&cfg.Bot.MentionName)
	resolveString(&cfg.Bot.AuthMode)

	resolveString(&cfg.Server.Address)

	resolveString(&cfg.Skin.Path)
	resolveString(&cfg.Skin.GeometryPath)
	resolveString(&cfg.Skin.GeometryName)

	resolveString(&cfg.Chat.Mode)
	resolveString(&cfg.Chat.QueueMode)

	resolveString(&cfg.LLM.Provider)
	resolveString(&cfg.LLM.APIKey)
	resolveString(&cfg.LLM.Model)
	resolveString(&cfg.LLM.BaseURL)

	resolveString(&cfg.Logging.Level)
	resolveString(&cfg.Logging.File.Path)
	resolveString(&cfg.Logging.Discord.WebhookURL)
	resolveString(&cfg.Logging.Discord.Level)
}

// resolveString replaces ${VAR} patterns in a string pointer with environment variable values
func resolveString(s *string) {
	if s == nil {
		return
	}
	*s = envVarPattern.ReplaceAllStringFunc(*s, func(match string) string {
		varName := match[2 : len(match)-1]
		return os.Getenv(varName)
	})
}

// Load reads and parses a YAML configuration file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	resolveEnvVars(&cfg)

	return &cfg, nil
}