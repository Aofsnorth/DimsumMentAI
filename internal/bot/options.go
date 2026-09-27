package bot

import (
	"log/slog"
	"time"

	"bedrock-ai/internal/ai"
	"bedrock-ai/internal/config"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/handler"
	"bedrock-ai/internal/memory"
	"bedrock-ai/internal/servercompat"

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

// WithServerHost sets the host and its detected compat profile.
//
// explicitGeyser is the operator's override from `is_geyser_server`. A pointer
// is used rather than a bool so "not configured" is distinguishable from
// "configured false": with a plain bool, a config that simply omits the key
// would look identical to a deliberate false, and there is no way to tell a
// default from a decision. nil means "detect from the hostname".
func WithServerHost(host string, explicitGeyser *bool) Option {
	return func(b *Bot) {
		b.ServerHost = host
		b.GeyserOverride = explicitGeyser
		profile := servercompat.Detect(host)
		if explicitGeyser != nil {
			profile.Geyser = *explicitGeyser
			profile.IdleNudge = *explicitGeyser
			profile.NoSubChunks = *explicitGeyser
			profile.NoHeldItemEcho = *explicitGeyser
		}
		b.VenityCompat = profile.Venity
		b.NetherGamesCompat = profile.NetherGames
		// Derived from the profile, not from config directly: this is a fix
		// for servers that drop a silent client, and applying it everywhere
		// would change behaviour on servers that already work.
		b.IdleNudge = profile.IdleNudge
		b.GeyserNoSubChunks = profile.NoSubChunks
		b.GeyserNoHeldItemEcho = profile.NoHeldItemEcho
		if profile.SlashCommandFirst {
			b.CommandPrefixOrder = CommandPrefixSlashFirst
		} else {
			b.CommandPrefixOrder = CommandPrefixNoneFirst
		}
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

// WithMemory attaches the curated long-term memory store. A nil store is
// allowed: memory actions then report "not ready" instead of panicking.
func WithMemory(store *memory.Store) Option {
	return func(b *Bot) {
		b.Memory = store
	}
}

func WithDebug(debug bool) Option {
	return func(b *Bot) {
		b.Debug = debug
	}
}

// WithJoinMessages sets the lines replayed after every successful join. A line
// starting with "/" is sent as a server command, anything else as chat.
func WithJoinMessages(lines []string, delay, interval time.Duration) Option {
	return func(b *Bot) {
		b.JoinMessages = lines
		b.JoinMessageDelay = delay
		b.JoinMessageInterval = interval
	}
}

// WithCommandPrefix selects the wire form of server commands.
func WithCommandPrefix(style string) Option {
	return func(b *Bot) {
		b.CommandPrefix = style
	}
}

func WithSkin(skin protocol.Skin, playerUUID uuid.UUID) Option {
	return func(b *Bot) {
		b.ProtoSkin = skin
		b.PlayerUUID = playerUUID
	}
}

// WithAGI attaches the autonomy settings. The brain is started by the session
// through a function pointer, so this only carries the configuration.
func WithAGI(cfg config.AGIConfig) Option {
	return func(b *Bot) {
		b.Agicfg = cfg
	}
}

// WithIdleNudge tunes the small steps a standing bot takes. A server-side AFK
// kicker watches for position change, so a bot that never moves while idle gets
// removed; these bound how often and how far it shifts.
func WithIdleNudge(minSec, maxSec int, reach float32) Option {
	return func(b *Bot) {
		b.IdleNudgeMinSec = minSec
		b.IdleNudgeMaxSec = maxSec
		b.IdleNudgeReach = reach
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
