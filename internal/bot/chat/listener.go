// Package chat handles player chat messages and AI-driven bot responses.
package chat

import (
	"context"
	"math"
	"reflect"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/event"
)

// Init registers the bot's listener on the event bus for ChatEvents
func Init(ctx context.Context, b *bot.Bot) {
	b.Bus.Subscribe(reflect.TypeOf(event.ChatEvent{}), func(evt interface{}) {
		chatEvt, ok := evt.(event.ChatEvent)
		if !ok {
			return
		}
		go HandleIncomingChat(ctx, b, chatEvt)
	})
	b.Logger.Debug("Chat listener registered on event bus")
}

// Simple internal math helper for floats
func mathSqrt(v float64) float64 {
	return math.Sqrt(v)
}

// The hand-rolled Newton iteration this replaced started at z = 1.0 and ran a
// fixed ten steps, which is not a square root — it is a guess whose accuracy
// depends on the input being small. At 100 it returned 100.0000002, at 1000 it
// returned 1296, and at 5000 it returned 24754. The caller compares the result
// against a 10-block limit, so a player standing exactly 10 blocks away was
// computed as 10.0000002, failed `dist > 10.0`, and was logged as "too far" while
// standing right there.
//
// A function that decides whether to talk to someone should not be the one
// place in the codebase that approximates the standard library.
