// Package agi bootstraps the autonomy brain. It lives beside the brain itself
// so the rest of the app only has to know the package exists.
package agi

import (
	"context"
	"log/slog"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/config"
)

// StartLoop is wired into bot.StartAGILoopFunc. It is a no-op when AGI is
// disabled, so the session can start autonomy unconditionally.
func StartLoop(ctx context.Context, b *bot.Bot) {
	runner := New(b, ConfigFrom(b.Agicfg))
	if runner == nil {
		b.Logger.Debug("AGI disabled")
		return
	}
	if b.AiClient == nil {
		b.Logger.Warn("AGI enabled but no AI client is configured; autonomy will stay off")
		return
	}
	b.Logger.Info("AGI enabled",
		slog.Duration("tick", runner.DecisionInterval()),
		slog.Float64("llm_chance", b.Agicfg.LLMChance),
		slog.Bool("social", b.Agicfg.Social),
		slog.Bool("wander", b.Agicfg.Wander),
		slog.Bool("vision", b.Agicfg.Vision),
	)
	// Take ownership of unprompted speech. The proactive chat loop asks the LLM
	// directly on a timer, with none of the guards this brain has: a danger
	// check, a goal, a social cooldown. Running both means the bot answers to
	// two disagreeing opinions about whether it wants to talk, which is how a
	// companion turns into a spammer.
	if !b.ClaimUnpromptedSpeech(bot.SpeechOwnerAGI) {
		b.Logger.Debug("another loop already owns unprompted speech; AGI will not speak unprompted")
	} else {
		b.Logger.Info("unprompted speech is now owned by the AGI brain")
	}
	go runner.Run(ctx)
}

// From builds a runner directly, for callers that want to drive a tick
// themselves rather than run the loop.
func From(b *bot.Bot, cfg config.AGIConfig) *Runner {
	return New(b, ConfigFrom(cfg))
}
