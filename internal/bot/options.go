package bot

import (
	"log/slog"

	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"
)

type Option func(*Bot)

func WithLogger(logger *slog.Logger) Option {
	return func(b *Bot) {
		b.logger = logger
	}
}

func WithDialer(dialer DialerFunc) Option {
	return func(b *Bot) {
		b.dialer = dialer
	}
}

func WithRegistry(registry *handler.Registry) Option {
	return func(b *Bot) {
		b.registry = registry
	}
}

func WithEventBus(bus *event.Bus) Option {
	return func(b *Bot) {
		b.bus = bus
	}
}

func WithName(name string) Option {
	return func(b *Bot) {
		b.name = name
	}
}

func New(opts ...Option) (*Bot, error) {
	return newBot(opts...)
}
