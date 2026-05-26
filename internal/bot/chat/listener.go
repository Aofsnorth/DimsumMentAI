package chat

import (
	"context"
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
	b.Logger.Info("Chat listener successfully registered on event bus")
}

// Simple internal math helper for floats
func mathSqrt(v float64) float64 {
	return basicSqrt(v)
}

func basicSqrt(v float64) float64 {
	if v == 0 {
		return 0
	}
	z := 1.0
	for i := 0; i < 10; i++ {
		z -= (z*z - v) / (2 * z)
	}
	return z
}
