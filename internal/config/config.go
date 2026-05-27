package config

import "fmt"

type Config struct {
	Server ServerConfig `yaml:"server"`
	Bot    BotConfig    `yaml:"bot"`
	Skin   SkinConfig   `yaml:"skin"`
	AI     AIConfig     `yaml:"ai"`
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
}

type SkinConfig struct {
	ImagePath string `yaml:"image_path"`
	ArmSize   string `yaml:"arm_size"`
}

type AIConfig struct {
	Provider                  string `yaml:"provider"`
	Model                     string `yaml:"model"`
	ApiKey                    string `yaml:"api_key"`
	MainPlayer                string `yaml:"main_player"`
	RespondOnlyToLinkedPlayer bool   `yaml:"respond_only_to_linked_player"`
	RespondOnlyWhenTagged     bool   `yaml:"respond_only_when_tagged"`
	CustomPersonality         string `yaml:"custom_personality"`
}
