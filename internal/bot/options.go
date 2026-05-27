package bot

import (
	"log/slog"
	"strings"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

type Option func(*Bot)

func WithLogger(logger *slog.Logger) Option {
	return func(b *Bot) {
		b.Logger = logger
	}
}

func WithDialer(dialer DialerFunc) Option {
	return func(b *Bot) {
		b.Dialer = dialer
	}
}

func WithRegistry(registry *handler.Registry) Option {
	return func(b *Bot) {
		b.Registry = registry
	}
}

func WithEventBus(bus *event.Bus) Option {
	return func(b *Bot) {
		b.Bus = bus
	}
}

func WithName(name string) Option {
	return func(b *Bot) {
		b.Name = name
	}
}

func WithServerHost(host string) Option {
	return func(b *Bot) {
		b.ServerHost = host
		h := strings.ToLower(host)
		b.VenityCompat = strings.Contains(h, "venity.net") || strings.Contains(h, "venity")
	}
}

func WithLanguage(language string) Option {
	return func(b *Bot) {
		if language != "" {
			b.Language = language
		}
	}
}

func WithStatePath(path string) Option {
	return func(b *Bot) {
		if path != "" {
			b.StatePath = path
		}
	}
}

func WithSkin(skin protocol.Skin, playerUUID uuid.UUID) Option {
	return func(b *Bot) {
		b.ProtoSkin = skin
		b.PlayerUUID = playerUUID
	}
}

func WithAI(client *ai.NvidiaClient, throttler *ai.MessageThrottler, cfg config.AIConfig) Option {
	return func(b *Bot) {
		b.AiClient = client
		b.Throttler = throttler
		b.AiCfg = cfg
	}
}

func New(opts ...Option) (*Bot, error) {
	return newBot(opts...)
}
