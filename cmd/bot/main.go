package main

import (
	"context"
	"encoding/base64"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"reflect"
	"syscall"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/connection"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"
	"bedrock-ai/internal/skin"

	"github.com/sandertv/gophertunnel/minecraft/protocol/login"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

func main() {
	configPath := flag.String("config", "configs/bot.yaml", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("failed to load config", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// --- Skin ---
	logger.Info("loading skin",
		slog.String("image", cfg.Skin.ImagePath),
		slog.String("geometry", cfg.Skin.GeometryName),
		slog.String("arm_size", cfg.Skin.ArmSize),
	)

	skinProvider := skin.NewProvider(cfg.Skin)
	clientData, err := skinProvider.Provide()
	if err != nil {
		logger.Error("failed to load skin", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Debug: verify skin data before sending
	skinBytes, _ := base64.StdEncoding.DecodeString(clientData.SkinData)
	patchBytes, _ := base64.StdEncoding.DecodeString(clientData.SkinResourcePatch)
	logger.Info("skin data prepared",
		slog.Int("rgba_bytes", len(skinBytes)),
		slog.Int("width", clientData.SkinImageWidth),
		slog.Int("height", clientData.SkinImageHeight),
		slog.Int("expected_rgba", clientData.SkinImageWidth*clientData.SkinImageHeight*4),
		slog.Bool("size_match", len(skinBytes) == clientData.SkinImageWidth*clientData.SkinImageHeight*4),
		slog.String("skin_id", clientData.SkinID),
		slog.String("arm_size", clientData.ArmSize),
		slog.String("resource_patch", string(patchBytes)),
		slog.Int("geometry_len", len(clientData.SkinGeometry)),
		slog.Int("geometry_version_len", len(clientData.SkinGeometryVersion)),
	)

	// --- Identity ---
	identityData := login.IdentityData{
		DisplayName: cfg.Bot.Name,
	}
	logger.Info("identity set",
		slog.String("display_name", identityData.DisplayName),
		slog.String("identity", identityData.Identity),
	)

	// --- Events ---
	bus := event.NewBus()
	bus.Subscribe(reflect.TypeOf(event.DisconnectEvent{}), func(evt interface{}) {
		e := evt.(event.DisconnectEvent)
		logger.Info("disconnected", slog.String("reason", e.Reason))
	})

	// --- Handlers ---
	registry := handler.NewRegistry()
	registry.Register(reflect.TypeOf(&packet.Text{}), handler.NewChatHandler(logger, bus))
	registry.Register(reflect.TypeOf(&packet.Disconnect{}), handler.NewDisconnectHandler(logger, bus))

	// --- Connection ---
	dialer := connection.NewDialer(cfg.Server, identityData, clientData)

	// --- Bot ---
	b, err := bot.New(
		bot.WithLogger(logger),
		bot.WithDialer(dialer.Dial),
		bot.WithRegistry(registry),
		bot.WithEventBus(bus),
		bot.WithName(cfg.Bot.Name),
	)
	if err != nil {
		logger.Error("failed to create bot", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("starting bot",
		slog.String("name", cfg.Bot.Name),
		slog.String("address", cfg.Server.Address),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := b.Run(ctx); err != nil {
		logger.Error("bot exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("bot shut down gracefully")
}
