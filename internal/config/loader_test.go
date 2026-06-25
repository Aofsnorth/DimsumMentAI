package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoad_ValidMinimalConfig(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  host: "localhost"
  port: 19132
bot:
  name: "TestBot"
skin:
  image_path: "skins/test.png"
  arm_size: "wide"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Server.Host != "localhost" {
		t.Errorf("Server.Host = %q", cfg.Server.Host)
	}
	if cfg.Server.Port != 19132 {
		t.Errorf("Server.Port = %d", cfg.Server.Port)
	}
	if cfg.Bot.Name != "TestBot" {
		t.Errorf("Bot.Name = %q", cfg.Bot.Name)
	}
}

func TestLoad_AppliesDefaults(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  host: "localhost"
  port: 19132
bot:
  name: "TestBot"
skin:
  image_path: "skins/test.png"
  arm_size: "wide"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Bot.Language != "Indonesian" {
		t.Errorf("default Bot.Language = %q, want %q", cfg.Bot.Language, "Indonesian")
	}
	if cfg.Bot.LogLevel != "info" {
		t.Errorf("default Bot.LogLevel = %q, want %q", cfg.Bot.LogLevel, "info")
	}
	if cfg.Bot.StatePath != "data/bot_state.json" {
		t.Errorf("default Bot.StatePath = %q", cfg.Bot.StatePath)
	}
	if cfg.Chat.DuplicateWindowSec != 3 {
		t.Errorf("default Chat.DuplicateWindowSec = %d, want 3", cfg.Chat.DuplicateWindowSec)
	}
	if cfg.Chat.RateLimitWindowSec != 10 {
		t.Errorf("default Chat.RateLimitWindowSec = %d, want 10", cfg.Chat.RateLimitWindowSec)
	}
	if cfg.Chat.MaxMessagesPerWindow != 100 {
		t.Errorf("default Chat.MaxMessagesPerWindow = %d, want 100", cfg.Chat.MaxMessagesPerWindow)
	}
}

func TestLoad_MissingServerHost(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  port: 19132
bot:
  name: "TestBot"
skin:
  image_path: "skins/test.png"
  arm_size: "wide"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load should error when server.host is missing")
	}
}

func TestLoad_MissingBotName(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  host: "localhost"
  port: 19132
skin:
  image_path: "skins/test.png"
  arm_size: "wide"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load should error when bot.name is missing")
	}
}

func TestLoad_InvalidArmSize(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  host: "localhost"
  port: 19132
bot:
  name: "TestBot"
skin:
  image_path: "skins/test.png"
  arm_size: "medium"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load should error for invalid arm_size")
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  host: "localhost"
  port: 19132
bot:
  name: "TestBot"
  log_level: "verbose"
skin:
  image_path: "skins/test.png"
  arm_size: "wide"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load should error for invalid log_level")
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  host: "localhost"
  port: 0
bot:
  name: "TestBot"
skin:
  image_path: "skins/test.png"
  arm_size: "wide"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load should error for port <= 0")
	}
}

func TestLoad_MissingSkinImage(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  host: "localhost"
  port: 19132
bot:
  name: "TestBot"
skin:
  arm_size: "wide"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load should error when skin.image_path is missing")
	}
}

func TestLoad_AIProviderNone(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  host: "localhost"
  port: 19132
bot:
  name: "TestBot"
skin:
  image_path: "skins/test.png"
  arm_size: "wide"
ai:
  provider: "none"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load with provider=none should succeed: %v", err)
	}
	if cfg.AI.Provider != "none" {
		t.Errorf("AI.Provider = %q, want %q", cfg.AI.Provider, "none")
	}
}

func TestLoad_AIProviderUnknown(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  host: "localhost"
  port: 19132
bot:
  name: "TestBot"
skin:
  image_path: "skins/test.png"
  arm_size: "wide"
ai:
  provider: "unknown_provider"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load should error for unknown AI provider")
	}
}

func TestServerConfig_Address(t *testing.T) {
	t.Parallel()
	s := ServerConfig{Host: "example.com", Port: 19132}
	if got := s.Address(); got != "example.com:19132" {
		t.Errorf("Address() = %q, want %q", got, "example.com:19132")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	t.Parallel()
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("Load should error for non-existent file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	t.Parallel()
	path := writeTempConfig(t, `
server:
  host: "localhost"
  port: [invalid
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load should error for invalid YAML")
	}
}
