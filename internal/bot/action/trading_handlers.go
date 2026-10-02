// This file registers the villager trading action labels: trade (buy or sell
// with the nearest villager) and tradelist (open the trade UI and report what
// the villager actually offers).
//
// The handlers are thin on purpose. Every decision about what is affordable,
// which offer is worth taking and whether the trade happened belongs in
// internal/bot/inventory/trading, where it is testable without a connection and
// where a trade can only be reported as done when the server's result reached
// the bot's inventory. These handlers translate an action label into a call on
// that manager and report whatever it said — including the reason it failed.
package action

import (
	"context"
	"fmt"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/inventory/trading"
	"bedrock-ai/internal/event"
)

// tradingManager builds a trading manager bound to this bot.
//
// The observation seams are deliberately left unwired: nothing in the network
// layer records packet.UpdateTrade yet, and no attribute handler reads the
// player's experience level. With neither, the manager refuses to trade and
// says why, which is the honest outcome — the alternative is a bot that reports
// an emerald purchase it never made.
func tradingManager(b *bot.Bot) *trading.Manager {
	return trading.NewManager(b, b.Logger)
}

// handleTrade runs one trade with the nearest villager.
//
// param is the item wanted, optionally with a count ("bread" or "bread,8").
// An empty param takes the best affordable offer the villager has.
func handleTrade(b *bot.Bot, param, user string) {
	go func() {
		want := parseTradeParam(param)

		result, ok := tradingManager(b).Trade(context.Background(), want)
		if !ok {
			ReportStatus(b, user, event.ActionStatus{
				Action:  "trade",
				Item:    want,
				Success: false,
				Error:   result.Reason,
			})
			return
		}

		b.Logger.Info("traded with a villager",
			"want", want,
			"offer_index", result.Offer.Index,
			"gained", result.Gained.Name,
			"gained_count", result.Gained.Count,
			"xp_observed", result.XPObserved,
			"xp_delta", result.XPDelta,
		)
		ReportStatus(b, user, event.ActionStatus{
			Action:  "trade",
			Item:    result.Gained.Name,
			Count:   result.Gained.Count,
			Success: true,
		})
	}()
}

// handleTradeList opens the nearest villager's trade UI and reports what it
// offers, without buying anything. It is the way to see whether a villager has
// anything worth trading with before spending emeralds on a guess.
func handleTradeList(b *bot.Bot, _, user string) {
	go func() {
		window, _, err := tradingManager(b).OpenTradeWindow(context.Background())
		if err != nil {
			ReportStatus(b, user, event.ActionStatus{
				Action:  "trade_list",
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		ReportStatus(b, user, event.ActionStatus{
			Action:  "trade_list",
			Item:    describeOffers(window),
			Count:   len(window.Offers),
			Success: true,
		})
	}()
}

// describeOffers renders the villager's trade list for a status message. It
// says "nothing on offer" rather than staying silent when the list is empty,
// which is a real answer and not a failure.
func describeOffers(window trading.TradeWindow) string {
	if len(window.Offers) == 0 {
		return "villager offers nothing"
	}

	parts := make([]string, 0, len(window.Offers))
	for _, offer := range window.Offers {
		parts = append(parts, fmt.Sprintf("%s x%d for %s",
			offer.Output.Name, offer.Output.Count, describeInputs(offer)))
	}
	return strings.Join(parts, "; ")
}

func describeInputs(offer trading.Offer) string {
	parts := make([]string, 0, len(offer.Inputs))
	for _, input := range offer.Inputs {
		parts = append(parts, fmt.Sprintf("%s x%d", input.Name, input.Count))
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, " + ")
}

// parseTradeParam splits "bread,8" into the item wanted and a count. A missing
// or unparseable count means "one trade", which is what asking to trade for
// something means; the count is reported rather than used to inflate the ask,
// because a villager's offer fixes how many one trade yields.
func parseTradeParam(param string) string {
	parts := strings.Split(param, ",")
	return NormalizeItemName(parts[0])
}

func init() {
	ActionHandlers["trade"] = handleTrade
	ActionHandlers["trading"] = handleTrade
	ActionHandlers["tradelist"] = handleTradeList
	ActionHandlers["trade_list"] = handleTradeList
	ActionHandlers["villager"] = handleTradeList
}
