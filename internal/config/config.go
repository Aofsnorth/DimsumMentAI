package config

import "fmt"

type Config struct {
	Server ServerConfig `yaml:"server"`
	Bot    BotConfig    `yaml:"bot"`
	Skin   SkinConfig   `yaml:"skin"`
	AI     AIConfig     `yaml:"ai"`
	Chat   ChatConfig   `yaml:"chat"`
}

type ServerConfig struct {
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	Offline bool   `yaml:"offline"`
}

func (s ServerConfig) Address() string {
	return s.Host + ":" + fmt.Sprint(s.Port)
}

type BotConfig struct {
	Name      string `yaml:"name"`
	Language  string `yaml:"language"`
	LogLevel  string `yaml:"log_level"`
	StatePath string `yaml:"state_path"`
	Debug     bool   `yaml:"debug"`
}

type SkinConfig struct {
	ImagePath string `yaml:"image_path"`
	ArmSize   string `yaml:"arm_size"`
}

type AIConfig struct {
	Provider                  string  `yaml:"provider"` // nvidia, minimax, opengateway, openai_compatible
	Model                     string  `yaml:"model"`
	BaseURL                   string  `yaml:"base_url"` // override default endpoint; required for opengateway/openai_compatible
	MainPlayer                string  `yaml:"main_player"`
	RespondOnlyToLinkedPlayer bool    `yaml:"respond_only_to_linked_player"`
	RespondOnlyWhenTagged     bool    `yaml:"respond_only_when_tagged"`
	CustomPersonality         string  `yaml:"custom_personality"`
	ProactiveIntervalSec      int     `yaml:"proactive_interval_sec"` // 0 = disabled. Periodic autonomous conversation tick.
	ProactiveChance           float64 `yaml:"proactive_chance"`       // 0.0-1.0, probability of actually querying LLM each tick.
}

type ChatConfig struct {
	DuplicateWindowSec   int `yaml:"duplicate_window_sec"`
	RateLimitWindowSec   int `yaml:"rate_limit_window_sec"`
	MaxMessagesPerWindow int `yaml:"max_messages_per_window"`
}
