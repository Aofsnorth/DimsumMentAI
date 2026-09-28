package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	Bot    BotConfig    `yaml:"bot"`
	Skin   SkinConfig   `yaml:"skin"`
	AI     AIConfig     `yaml:"ai"`
	Chat   ChatConfig   `yaml:"chat"`
	AGI    AGIConfig    `yaml:"agi"`
}

// AGIConfig turns the bot from a tool you command into a companion with its own
// agenda. Everything here is off by default: an autonomous bot that moves and
// speaks on its own is a large behavioural change, so it has to be asked for.
//
// The loop has two layers. A reflex layer runs first and never calls the LLM —
// it reacts to danger, hunger and people walking up, and it is what keeps the
// bot alive between decisions. A decision layer asks the model what to do with
// itself, gated by a probability so it neither burns tokens nor chatters.
type AGIConfig struct {
	// Enabled turns the whole subsystem on. Off means the bot only ever acts on
	// a player's instruction, exactly as before.
	Enabled bool `yaml:"enabled"`

	// TickIntervalSec is how often the brain wakes up.
	TickIntervalSec int `yaml:"tick_interval_sec"`
	// LLMChance is the probability (0..1) that a tick consults the model. The
	// reflex layer still runs on every tick regardless.
	LLMChance float64 `yaml:"llm_chance"`

	// SelfPreservation is the reflex layer: eat when hungry, armour up, run
	// when badly hurt. These must not wait for a decision — a bot that thinks
	// about whether to eat while starving is not much use.
	SelfPreservation bool `yaml:"self_preservation"`
	LowHPThreshold   int  `yaml:"low_hp_threshold"`
	LowHunger        int  `yaml:"low_hunger_threshold"`

	// LowAirSeconds is how long the bot's head can be under water before it
	// stops what it is doing and goes up.
	//
	// It is a count of seconds rather than an air bar because the bot has no air
	// bar to read: Bedrock's air supply is not something this client has been
	// shown receiving, so the honest version of "am I drowning" is "how long has
	// my head been in water". Health only starts falling once the air is nearly
	// gone, which is far too late to react to.
	LowAirSeconds int `yaml:"low_air_seconds"`

	// Wander makes the bot go somewhere when it has nothing to do, the way a
	// player idles around a world. Exploration is local and cheap; it never
	// leaves loaded chunks.
	Wander bool `yaml:"wander"`
	// WanderDurationSec is how long a single wander lasts before the brain
	// re-evaluates.
	WanderDurationSec int `yaml:"wander_duration_sec"`

	// Social lets the bot start a conversation nobody asked for.
	Social bool `yaml:"social"`
	// SocialCooldownSec is the minimum gap between two unprompted messages. This
	// is the single most important number here: without it the bot looks like a
	// spammer rather than a companion.
	SocialCooldownSec int `yaml:"social_cooldown_sec"`

	// Vision makes the bot turn towards a player who walks into view, the way
	// people acknowledge someone arriving. Purely reflex-driven and local — no
	// LLM call, so reacting stays instant.
	Vision bool `yaml:"vision"`
	// VisionRadius is how close a player must be to be looked at.
	VisionRadius float32 `yaml:"vision_radius"`
	// VisionHoldSec is how long the gaze lingers before the bot looks away.
	VisionHoldSec int `yaml:"vision_hold_sec"`
	// VisionCooldownSec is the gap between two gaze turns at the same player,
	// so a player pacing around does not make the bot swing its head back and
	// forth like a metronome.
	VisionCooldownSec int `yaml:"vision_cooldown_sec"`

	// Jev enables TypeSafe AI's System One model as the reflex layer's brain.
	//
	// Jev cannot write text. It answers typed questions with calibrated
	// probabilities — that is all it does. The split is deliberate: Jev decides
	// ("should I run?", "is this worth speaking?"), the LLM writes ("what do I
	// say?"). Without a key the bot keeps its hardcoded thresholds and everything
	// still works, so this is strictly an upgrade.
	Jev JevConfig `yaml:"jev"`

	// Perception holds the numbers that decide what the bot can notice. They are
	// config rather than constants because they are taste: too short a mob range
	// and the bot walks past a creeper, too long and the prompt fills with noise
	// the model then reasons about wrongly.
	Perception PerceptionConfig `yaml:"perception"`

	// IdleNudgeMinSec and IdleNudgeMaxSec bound the gap between the small steps
	// a standing bot takes; IdleNudgeReach is how far one may go. This is not
	// cosmetic: a server-side AFK kicker watches for POSITION change, so a bot
	// that stands perfectly still is removed from the world no matter how alive
	// its head movement looks.
	IdleNudgeMinSec int     `yaml:"idle_nudge_min_sec"`
	IdleNudgeMaxSec int     `yaml:"idle_nudge_max_sec"`
	IdleNudgeReach  float32 `yaml:"idle_nudge_reach"`

	// NightStartTicks and NightEndTicks bound nightfall in Bedrock world ticks
	// (0-24000). These match the boundaries the survival manager uses; both
	// sides have to agree or the bot decides to sleep at noon.
	NightStartTicks int64 `yaml:"night_start_ticks"`
	NightEndTicks   int64 `yaml:"night_end_ticks"`

	// EyeHeight is where the bot's eyes sit above its feet, in blocks. Every
	// line-of-sight ray uses it, so a wrong value makes the bot see through
	// floors or fail to see over them.
	EyeHeight float32 `yaml:"eye_height"`

	// WanderRadius is how far from its current spot one wander step may land.
	WanderRadius float32 `yaml:"wander_radius"`

	// Mode selects which autonomy the brain runs.
	Mode string `yaml:"mode"`

	// PlanLifetimeMin is how long one plan is pursued before the planner is
	// consulted from scratch. A plan needs room to actually finish — an
	// expedition that gets replanned every minute never leaves the door.
	PlanLifetimeMin int `yaml:"plan_lifetime_min"`
	// PlanReplanMin is how often the planner gets a look in while a plan is
	// alive. Replanning is advisory: the current plan keeps executing while a
	// replan is in flight, which is what keeps a slow planner from stalling
	// the bot.
	PlanReplanMin int `yaml:"plan_replan_min"`
	// PlanMaxSteps bounds a generated plan. An unbounded plan is a wishlist,
	// and a wishlist cannot be finished — there has to be a last step.
	PlanMaxSteps int `yaml:"plan_max_steps"`
}

// Autonomy modes.
const (
	// ModeDefault is the reactive brain: reflexes, a short goal, and activities
	// chosen from what is in front of the bot. It is a companion, and it is the
	// right default because it needs no objective to be useful.
	ModeDefault = "default"

	// ModePlanning adds a long-horizon plan on top: a structured objective with
	// steps and waypoints that survives across ticks, executed one bounded
	// action at a time.
	//
	// It is additive, not a replacement. The reflexes still run in planning mode
	// — a bot pursuing a plan that stops to eat and runs from a creeper is
	// correct, not distracted. What planning changes is the horizon, not the
	// safety.
	//
	// The mode is deliberately general. It is not a "dragon mode": the plan's
	// objective comes from the player or the model, and the machinery underneath
	// is the same whatever the objective happens to be. Hardcoding a target into
	// the brain is what turns an agent back into a script.
	ModePlanning = "planning"
)

// NormalizeMode maps a configured mode to a known one, falling back to default.
//
// An unknown mode is a typo, and silently guessing at behaviour would be worse
// than the safe default: a user who writes "planing" should get a working bot
// and a default brain, not a bot stuck in a half-configured planning mode.
func NormalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModePlanning, "plan", "long_horizon", "long-horizon":
		return ModePlanning
	default:
		return ModeDefault
	}
}

// PerceptionConfig bounds what the bot notices, in world units.
type PerceptionConfig struct {
	// MobScanDistance is how far away a mob is still worth listing.
	MobScanDistance float32 `yaml:"mob_scan_distance"`
	// BlockScanDistance is how far away a block is still worth listing.
	BlockScanDistance float32 `yaml:"block_scan_distance"`
	// BlockScanLimit caps how many clickable blocks reach the prompt.
	BlockScanLimit int `yaml:"block_scan_limit"`
	// MobPromptLimit caps how many mobs reach the prompt.
	MobPromptLimit int `yaml:"mob_prompt_limit"`
	// NearbyRadius is who counts as nearby when vision is off. Vision has its
	// own radius; this is the fallback, so the decision layer still knows who
	// is around even when the bot never turns its head.
	NearbyRadius float32 `yaml:"nearby_radius"`
	// BedKeywords match inventory items the bot could sleep in. Case and
	// namespace are ignored, so "minecraft:red_bed" matches on "bed".
	BedKeywords []string `yaml:"bed_keywords"`
}

// JevConfig configures the System One layer.
//
// Deliberately holds no model name and no key. Both are environment concerns:
// the key is a secret that must never reach a committed file, and the model
// changes per gateway (Vercel AI Gateway wants "typesafe-ai/jev", OpenRouter
// wants "typesafe/jev-1.13"). They are read from the environment — see
// jev.EnvAPIKey and jev.EnvModel — so switching gateway is a change to .env,
// not to the config.
//
// What stays here is policy, which is a decision rather than a secret: which
// endpoint to trust, and where to cut the probabilities the model returns.
type JevConfig struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"`
	// DangerThreshold is the probability above which the bot treats a situation
	// as dangerous. It stays in your hands on purpose — Jev returns a
	// probability, and where to cut is a policy decision, not a model output.
	DangerThreshold float64 `yaml:"danger_threshold"`
	// SpeakThreshold is the same for unprompted speech. Kept high because a
	// companion that talks too easily is worse than one that stays quiet.
	SpeakThreshold float64 `yaml:"speak_threshold"`
	// EngageThreshold is where Jev's "should the bot be doing something" answer
	// is read as engaged. It sits low by default: a bot that only bothers when
	// it is certain to be busy ends up standing still, and stillness on its own
	// is as robotic as constant motion.
	EngageThreshold float64 `yaml:"engage_threshold"`
	// TimeoutSec bounds one evaluate call. Short on purpose — Jev answers in tens
	// to hundreds of milliseconds, so anything beyond a few seconds means
	// something is wrong and the reflex layer should carry on without it.
	TimeoutSec int `yaml:"timeout_sec"`
}

// Command wire-form styles. The protocol does not settle whether a
// CommandRequest line carries a leading slash, and servers disagree, so the
// style is configurable and can be auto-detected.
const (
	CommandPrefixAuto  = "auto"
	CommandPrefixSlash = "slash"
	CommandPrefixNone  = "none"

	// ResourcePacksSkip refuses packs; ResourcePacksDownload fetches and caches
	// them. See ServerConfig.ResourcePacks.
	ResourcePacksSkip     = "skip"
	ResourcePacksDownload = "download"
)

type ServerConfig struct {
	Host         string `yaml:"host"`
	Port         int    `yaml:"port"`
	Offline      bool   `yaml:"offline"`
	LANDiscovery *bool  `yaml:"lan_discovery"`
	LANWorld     string `yaml:"lan_world"`

	// CommandPrefix selects how a server command is written on the wire.
	//
	// The protocol does not settle this and server software disagrees: Geyser
	// strips a leading "/" before translating the command to Java, so sending
	// "/register" arrives as an unknown command, while dragonfly refuses
	// anything without the slash. Rather than make the user guess per server,
	// "auto" tries the bare form first and retries with the slash when the
	// server answers nothing.
	//
	//   "auto"  (default) try "register", retry as "/register" if silent
	//   "slash" always send "/register"
	//   "none"  always send "register"
	CommandPrefix string `yaml:"command_prefix"`

	// IsGeyserServer marks this server as fronted by Geyser (the Bedrock↔Java
	// bridge, usually behind Floodgate). Setting it true enables the behaviour
	// that server software needs — today the idle nudge, because a Geyser
	// front-end drops a client that goes quiet.
	//
	// It is explicit rather than auto-detected on purpose. A fix that applies
	// only to Geyser must not be applied to a server that already works, and a
	// hostname guess cannot tell the two apart reliably: a Geyser front-end is
	// just as often reached by a custom domain with no "geyser" in it. Leaving
	// it false keeps the bot standing still exactly as before, which is the safe
	// default for every server not observed to time out.
	IsGeyserServer bool `yaml:"is_geyser_server"`
	// IsGeyserSet records whether `is_geyser_server` appeared in the file, so an
	// omitted key stays distinguishable from an explicit false.
	IsGeyserSet bool `yaml:"-"`

	// ResourcePacks selects how texture/behaviour packs are handled.
	//
	//   "skip"     refuse every pack (the bot is headless and never renders).
	//              gophertunnel records the pack as ignored, which is what
	//              lets the ResourcePackStack check pass.
	//   "download" fetch each pack and cache it on disk, so the bot behaves
	//              like a real client on servers that require the pack.
	//
	// "skip" is the default because a pack is pure download cost for a bot
	// that never draws. Servers differ in how they react, so "download" is
	// there for the ones that insist.
	ResourcePacks string `yaml:"resource_packs"`

	// ResourcePackDir is where downloaded packs are cached between logins.
	ResourcePackDir string `yaml:"resource_pack_dir"`
}

func (s ServerConfig) Address() string {
	return s.Host + ":" + fmt.Sprint(s.Port)
}

// GeyserOverride returns the operator's explicit `is_geyser_server` value, or
// nil when the key was absent from the config and detection should decide.
//
// A pointer is required: an omitted key and an explicit `false` both decode to
// false, but they mean different things. Omitted means "use hostname
// detection"; explicit false means "definitely not Geyser, stop guessing".
// Collapsing them would silently disable the Geyser behaviour on a server that
// needs it, which is exactly the bug this indirection exists to avoid.
func (s ServerConfig) GeyserOverride() *bool {
	if s.IsGeyserSet {
		v := s.IsGeyserServer
		return &v
	}
	return nil
}

// UnmarshalYAML records whether `is_geyser_server` was present while decoding
// the rest of the server block normally.
func (s *ServerConfig) UnmarshalYAML(node *yaml.Node) error {
	type plain ServerConfig
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*s = ServerConfig(decoded)
	s.IsGeyserSet = nodeHasKey(node, "is_geyser_server")
	return nil
}

// nodeHasKey reports whether a YAML mapping node declares the given key.
func nodeHasKey(node *yaml.Node, key string) bool {
	if node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return true
		}
	}
	return false
}

// LANDiscoveryEnabled reports whether local world discovery is requested.
// Port 7551 is the Bedrock LAN discovery port; an explicit false disables it.
func (s ServerConfig) LANDiscoveryEnabled() bool {
	if s.LANDiscovery != nil {
		return *s.LANDiscovery
	}
	return s.Port == 7551
}

type BotConfig struct {
	Name       string `yaml:"name"`
	Language   string `yaml:"language"`
	LogLevel   string `yaml:"log_level"`
	StatePath  string `yaml:"state_path"`
	MemoryPath string `yaml:"memory_path"`
	Debug      bool   `yaml:"debug"`

	// JoinMessages are sent once after every successful join, and again after
	// every rejoin or server switch. A line starting with "/" is sent as a real
	// server command (CommandRequest packet); anything else is plain chat. That
	// split is what a human player does too: typing "/register pass pass" in the
	// Minecraft chat box runs the command, it does not post the words.
	JoinMessages []string `yaml:"join_messages"`
	// JoinMessageDelaySec is how long to wait after spawning before the first
	// line goes out. Servers hand out the command list and finish the world
	// transfer right around spawn, so firing immediately is how the first
	// command ends up rejected as "unknown command".
	JoinMessageDelaySec int `yaml:"join_message_delay_sec"`
	// JoinMessageGapSec is the pause between consecutive lines, so a burst of
	// commands is not sent as a single flood the server rate-limits or kicks.
	JoinMessageGapSec int `yaml:"join_message_gap_sec"`
}

type SkinConfig struct {
	ImagePath string `yaml:"image_path"`
	ArmSize   string `yaml:"arm_size"`
}

type AIConfig struct {
	Provider                  string  `yaml:"provider"` // openai_compatible, anthropic_compatible, google_compatible, nvidia, none
	Model                     string  `yaml:"model"`
	BaseURL                   string  `yaml:"base_url"` // endpoint URL; empty uses provider default (nvidia only). Required for the *_compatible providers.
	MainPlayer                string  `yaml:"main_player"`
	RespondOnlyToLinkedPlayer bool    `yaml:"respond_only_to_linked_player"`
	RespondOnlyWhenTagged     bool    `yaml:"respond_only_when_tagged"`
	CustomPersonality         string  `yaml:"custom_personality"`
	ContextWindow             int     `yaml:"context_window"`         // override model context window in tokens; 0 = auto-detect. Bot uses 25% of this as its request budget.
	ProactiveIntervalSec      int     `yaml:"proactive_interval_sec"` // 0 = disabled. Periodic autonomous conversation tick.
	ProactiveChance           float64 `yaml:"proactive_chance"`       // 0.0-1.0, probability of actually querying LLM each tick.
}

type ChatConfig struct {
	DuplicateWindowSec   int `yaml:"duplicate_window_sec"`
	RateLimitWindowSec   int `yaml:"rate_limit_window_sec"`
	MaxMessagesPerWindow int `yaml:"max_messages_per_window"`
}
