package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"

	"github.com/sandertv/gophertunnel/minecraft"
)

type Bot struct {
	logger   *slog.Logger
	conn     *minecraft.Conn
	dialer   DialerFunc
	registry *handler.Registry
	bus      *event.Bus
	name     string
}

type DialerFunc func() (*minecraft.Conn, error)

func newBot(opts ...Option) (*Bot, error) {
	b := &Bot{}

	for _, opt := range opts {
		opt(b)
	}

	if err := b.validate(); err != nil {
		return nil, fmt.Errorf("validate bot options: %w", err)
	}

	return b, nil
}

func (b *Bot) validate() error {
	if b.logger == nil {
		return fmt.Errorf("logger is required")
	}
	if b.dialer == nil {
		return fmt.Errorf("dialer is required")
	}
	if b.registry == nil {
		return fmt.Errorf("registry is required")
	}
	if b.bus == nil {
		return fmt.Errorf("event bus is required")
	}
	return nil
}

func (b *Bot) Run(ctx context.Context) error {
	conn, err := b.dialer()
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	b.conn = conn
	defer b.conn.Close()

	b.logger.Info("connected to server",
		slog.String("address", conn.RemoteAddr().String()),
	)

	if err := b.conn.DoSpawn(); err != nil {
		return fmt.Errorf("spawn: %w", err)
	}

	b.logger.Info("spawned in world",
		slog.String("name", b.name),
	)

	b.bus.Publish(event.SpawnEvent{
		GameData: b.conn.GameData(),
	})

	return b.packetLoop(ctx)
}

func (b *Bot) packetLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			b.logger.Info("shutting down", slog.String("reason", ctx.Err().Error()))
			return nil
		default:
		}

		pk, err := b.conn.ReadPacket()
		if err != nil {
			var disc minecraft.DisconnectError
			if errors.As(err, &disc) {
				b.logger.Info("disconnected by server",
					slog.String("reason", disc.Error()),
				)
				b.bus.Publish(event.DisconnectEvent{Reason: disc.Error()})
				return nil
			}
			return fmt.Errorf("read packet: %w", err)
		}

		if handleErr := b.registry.Handle(ctx, pk); handleErr != nil {
			b.logger.Error("handle packet",
				slog.String("error", handleErr.Error()),
			)
		}
	}
}

func (b *Bot) Close() error {
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}
