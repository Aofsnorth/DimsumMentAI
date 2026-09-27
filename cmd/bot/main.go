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
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/action"
	"bedrock-ai/internal/bot/agi"
	"bedrock-ai/internal/bot/chat"
	"bedrock-ai/internal/bot/movement"
	"bedrock-ai/internal/bot/network"
	"bedrock-ai/internal/bot/network/world"
	"bedrock-ai/internal/bot/planner"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/connection"
	"bedrock-ai/internal/debuglog"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"
	"bedrock-ai/internal/memory"
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
	chatHandler := handler.NewChatHandler(logger, bus)
	registry.Register(reflect.TypeOf(&packet.Text{}), chatHandler)
	registry.Register(reflect.TypeOf(&packet.Disconnect{}), handler.NewDisconnectHandler(logger, bus))

	// --- Connection ---
	dialer := connection.NewDialer(cfg.Server, identityData, clientData)

	// --- AI Services ---
	var aiClient *ai.NvidiaClient
	var throttler *ai.MessageThrottler

	if cfg.AI.Provider != "" && cfg.AI.Provider != "none" {
		logger.Info("initializing LLM client",
			slog.String("provider", cfg.AI.Provider),
			slog.String("model", cfg.AI.Model),
			slog.String("base_url", cfg.AI.BaseURL),
		)
		aiClient = ai.NewLLMClient(cfg.AI.Provider, cfg.AI.Model, cfg.AI.BaseURL)
		aiClient.SetLanguage(cfg.Bot.Language)
		aiClient.SetContextWindow(cfg.AI.ContextWindow)
		logger.Info("context window budget",
			slog.Int("window_tokens", aiClient.ContextWindow()),
			slog.Int("budget_tokens", aiClient.ContextBudget()),
		)
		if cfg.AI.CustomPersonality != "" {
			aiClient.SetPersona(cfg.AI.CustomPersonality, cfg.Bot.Name)
		} else {
			// Even the built-in persona needs the configured name, and a gender
			// derived from it, so it is bound the same way.
			aiClient.SetBotName(cfg.Bot.Name)
		}
		throttler = ai.NewMessageThrottler(
			time.Duration(cfg.Chat.DuplicateWindowSec)*time.Second,
			time.Duration(cfg.Chat.RateLimitWindowSec)*time.Second,
			cfg.Chat.MaxMessagesPerWindow,
		)
	}

	// --- Dependency Injection Registration ---
	bot.SendInputLoopFunc = movement.SendInputLoop
	bot.PacketLoopFunc = network.PacketLoop
	bot.ChunkRequesterLoopFunc = network.ChunkRequesterLoop
	bot.VenityCompatLoopFunc = network.VenityCompatLoop
	bot.RequestChunkRadiusFunc = func(b *bot.Bot) {
		world.RequestChunkRadius(b, world.DefaultChunkRadius)
	}
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
	bot.StartProactiveLoopFunc = chat.StartProactiveLoop
	bot.StartAGILoopFunc = agi.StartLoop

	// Planner (agentic plan → execute → observe → decide loop)
	bot.NewPlannerFunc = func(b *bot.Bot, client *ai.NvidiaClient) bot.PlannerInterface {
		return planner.New(b, client)
	}

	// --- Long-term memory (MinePal-style curated facts + named places) ---
	memoryPath := cfg.Bot.MemoryPath
	if memoryPath == "" {
		memoryPath = "data/bot_memory.json"
	}
	memoryStore := memory.New(memoryPath)
	if err := memoryStore.Load(); err != nil && !os.IsNotExist(err) {
		logger.Warn("failed to load memory store, starting empty",
			slog.String("path", memoryPath),
			slog.String("error", err.Error()),
		)
	}

	// --- Bot ---
	b, err := bot.New(
		bot.WithLogger(logger),
		bot.WithDialer(dialer.Dial),
		bot.WithRegistry(registry),
		bot.WithEventBus(bus),
		bot.WithName(cfg.Bot.Name),
		bot.WithServerHost(cfg.Server.Host, cfg.Server.GeyserOverride()),
		bot.WithLanguage(cfg.Bot.Language),
		bot.WithStatePath(cfg.Bot.StatePath),
		bot.WithMemory(memoryStore),
		bot.WithDebug(cfg.Bot.Debug),
		bot.WithSkin(assets.ProtocolSkin, playerUUID),
		bot.WithAI(aiClient, throttler, cfg.AI),
		bot.WithCommandPrefix(cfg.Server.CommandPrefix),
		bot.WithIdleNudge(cfg.AGI.IdleNudgeMinSec, cfg.AGI.IdleNudgeMaxSec, cfg.AGI.IdleNudgeReach),
		bot.WithAGI(cfg.AGI),
		bot.WithJoinMessages(
			cfg.Bot.JoinMessages,
			time.Duration(cfg.Bot.JoinMessageDelaySec)*time.Second,
			time.Duration(cfg.Bot.JoinMessageGapSec)*time.Second,
		),
	)
	if err != nil {
		logger.Error("failed to create bot", slog.String("error", err.Error()))
		os.Exit(1)
	}

	// The chat handler is registered before the bot exists, so this is where it
	// learns where to report command replies.
	chatHandler.SetServerReplier(b)

	// Initialize the agentic planner now that the bot and AI client exist.
	if bot.NewPlannerFunc != nil {
		b.Planner = bot.NewPlannerFunc(b, aiClient)
	}

	// Let the bot move to a different server on request (the join action). It
	// works by ending the live session and re-dialing, so the hook is the
	// dialer's own target switch.
	b.SetJoinHook(dialer.SetTarget)

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
