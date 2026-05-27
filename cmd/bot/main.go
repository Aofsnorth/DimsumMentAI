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

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/bot/chat"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/network"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/connection"
	"bedrock-ai/internal/debuglog"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"
	"bedrock-ai/internal/skin"

	"github.com/google/uuid"
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
	logger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLogLevel(cfg.Bot.LogLevel),
	}))
	slog.SetDefault(logger)
	debuglog.SetEnabled(cfg.Bot.LogLevel == "debug")
	if debuglog.Enabled() {
		logger.Info("debug session logging enabled", slog.String("file", "logs/debug-090ce4.log"))
	}

	// --- Skin ---
	logger.Debug("loading skin",
		slog.String("image", cfg.Skin.ImagePath),
		slog.String("arm_size", cfg.Skin.ArmSize),
	)

	skinProvider := skin.NewProvider(cfg.Skin)
	assets, err := skinProvider.Provide()
	if err != nil {
		logger.Error("failed to load skin", slog.String("error", err.Error()))
		os.Exit(1)
	}

	clientData := assets.ClientData
	skinBytes, _ := base64.StdEncoding.DecodeString(clientData.SkinData)
	patchBytes, _ := base64.StdEncoding.DecodeString(clientData.SkinResourcePatch)
	logger.Debug("skin data prepared",
		slog.Int("rgba_bytes", len(skinBytes)),
		slog.Int("width", clientData.SkinImageWidth),
		slog.Int("height", clientData.SkinImageHeight),
		slog.Bool("size_match", len(skinBytes) == clientData.SkinImageWidth*clientData.SkinImageHeight*4),
		slog.String("arm_size", clientData.ArmSize),
		slog.String("resource_patch", string(patchBytes)),
		slog.Int("geometry_json_len", len(assets.ProtocolSkin.SkinGeometry)),
	)

	// --- Identity (fixed UUID so PlayerSkin can reference it) ---
	playerUUID := uuid.New()
	identityData := login.IdentityData{
		Identity:    playerUUID.String(),
		DisplayName: cfg.Bot.Name,
	}
	logger.Debug("identity set",
		slog.String("display_name", identityData.DisplayName),
		slog.String("uuid", identityData.Identity),
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

	// --- AI Services ---
	var aiClient *ai.NvidiaClient
	var throttler *ai.MessageThrottler

	if cfg.AI.Provider == "nvidia" {
		logger.Info("initializing Nvidia NIM LLM client",
			slog.String("model", cfg.AI.Model),
		)
		aiClient = ai.NewNvidiaClient(cfg.AI.ApiKey, cfg.AI.Model)
		aiClient.SetLanguage(cfg.Bot.Language)
		if cfg.AI.CustomPersonality != "" {
			aiClient.SetPersona(cfg.AI.CustomPersonality)
		}
		throttler = ai.DefaultThrottler()
	}

	// --- Dependency Injection Registration ---
	bot.SendInputLoopFunc = movement.SendInputLoop
	bot.PacketLoopFunc = network.PacketLoop
	bot.ChunkRequesterLoopFunc = network.ChunkRequesterLoop
	bot.VenityCompatLoopFunc = network.VenityCompatLoop
	bot.SendPlayerSkinFunc = network.SendPlayerSkin
	bot.SendLoadingScreenDoneFunc = network.SendLoadingScreenDone
	bot.RecalculatePathFunc = movement.RecalculatePath
	bot.NavigateToFunc = movement.NavigateTo
	bot.StopMovementFunc = movement.StopMovement
	bot.NavigateToBlockFunc = movement.NavigateToBlock
	bot.LookAtFunc = movement.LookAt

	// Chat listener and action hooks
	bot.InitChatListenerFunc = chat.Init
	bot.ExecuteActionFunc = action.Execute

	// --- Bot ---
	b, err := bot.New(
		bot.WithLogger(logger),
		bot.WithDialer(dialer.Dial),
		bot.WithRegistry(registry),
		bot.WithEventBus(bus),
		bot.WithName(cfg.Bot.Name),
		bot.WithServerHost(cfg.Server.Host),
		bot.WithLanguage(cfg.Bot.Language),
		bot.WithStatePath(cfg.Bot.StatePath),
		bot.WithSkin(assets.ProtocolSkin, playerUUID),
		bot.WithAI(aiClient, throttler, cfg.AI),
	)
	if err != nil {
		logger.Error("failed to create bot", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("starting bot",
		slog.String("name", cfg.Bot.Name),
		slog.String("address", cfg.Server.Address()),
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := b.Run(ctx); err != nil {
		logger.Error("bot exited with error", slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Info("bot shut down gracefully")
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
