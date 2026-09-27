package action

import (
	"context"
	"fmt"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/storage"
	"bedrock-ai/internal/event"
	"bedrock-ai/internal/safecast"

	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// storageSearcher builds a searcher bound to this bot. The opener is the same
// walk/look/click sequence the search layer drives, so a chest is only ever
// opened from where a player could open it.
func storageSearcher(b *bot.Bot) *storage.Searcher {
	return storage.NewSearcher(b.Storage(), b.OpenContainer)
}

// read_sign_words extracts the words a player said after "read sign", so the
// bot can look for the sign that carries them instead of reading whichever
// sign happens to be closest.
func readSignWords(param string) []string {
	fields := strings.Fields(strings.ToLower(param))
	words := make([]string, 0, len(fields))
	for _, field := range fields {
		clean := strings.Trim(field, "\"'.,!?")
		if clean == "" || clean == "yang" || clean == "diatas" || clean == "di" {
			continue
		}
		words = append(words, clean)
	}
	return words
}

// handleReadSign reads a sign the way a person does: walk up to it, look at
// it, and read what it says. The text is both reported and pushed into the AI
// context, so the conversation can carry on from what the sign actually said
// instead of the bot pretending to know.
func handleReadSign(b *bot.Bot, param, user string) {
	go func() {
		ctx := context.Background()
		signs := b.Storage().FindSigns()
		if len(signs) == 0 {
			b.ReportActionStatus(user, event.ActionStatus{
				Action:  "read_sign",
				Item:    "sign",
				Success: false,
				Error:   "tidak ada sign yang kelihatan di sini",
			})
			return
		}

		// With no words given, read the nearest sign: that is what "read the
		// sign" means to a player standing in front of one.
		target := signs[0]
		for _, sign := range signs {
			if signMatchesWords(sign.Text, param) {
				target = sign
				break
			}
		}

		text, err := b.Storage().ReadSign(ctx, target)
		if err != nil {
			b.Logger.Warn("read sign failed", "error", err.Error())
			b.ReportActionStatus(user, event.ActionStatus{
				Action:  "read_sign",
				Item:    "sign",
				Success: false,
				Error:   err.Error(),
			})
			return
		}

		b.Logger.Info("read sign", "text", text)
		// The sign's words go into the AI context: a player who reads a sign
		// then acts on it, and the model needs the text to choose what to do.
		b.InjectAIEvent(fmt.Sprintf(
			"[SIGN读到] Bot membaca sign di dekatnya. Isinya: %q. Loc: %d,%d,%d",
			text, target.Pos.X(), target.Pos.Y(), target.Pos.Z()))
		b.ReportActionStatus(user, event.ActionStatus{
			Action:  "read_sign",
			Item:    "sign",
			Success: true,
		})
	}()
}

func signMatchesWords(text, param string) bool {
	words := readSignWords(param)
	if len(words) == 0 {
		return false
	}
	lower := strings.ToLower(text)
	for _, word := range words {
		if len(word) >= 3 && strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// handleTakeFromChest searches visible chests one at a time for an item and
// brings it out. It replaces the old handler, which reached for a single
// arbitrary chest and reported success without the item ever moving.
func handleTakeFromChest(b *bot.Bot, param, user string) {
	go func() {
		if strings.TrimSpace(param) == "" {
			return
		}
		itemName, count := parseItemAndCount(param)
		result, err := storageSearcher(b).Find(context.Background(), itemName, count)
		b.Logger.Info("take from chest complete",
			"item", itemName,
			"found", result.Found,
			"taken", result.Taken,
			"opened", result.OpenedCount,
			"signs", result.SignsRead,
		)
		if !result.Found {
			b.ReportActionStatus(user, event.ActionStatus{
				Action:  "take",
				Item:    itemName,
				Success: false,
				Error:   result.Reason,
			})
			return
		}
		b.ReportActionStatus(user, event.ActionStatus{
			Action:  "take",
			Item:    itemName,
			Success: true,
		})
		_ = err
	}()
}

// handleStoreInChest puts an item away in the best visible chest.
func handleStoreInChest(b *bot.Bot, param, user string) {
	go func() {
		if strings.TrimSpace(param) == "" {
			return
		}
		itemName, count := parseItemAndCount(param)
		stored, err := storageSearcher(b).StoreItem(context.Background(), itemName, count)
		b.Logger.Info("store in chest complete", "item", itemName, "stored", stored)
		if err != nil {
			b.ReportActionStatus(user, event.ActionStatus{
				Action:  "store",
				Item:    itemName,
				Success: false,
				Error:   err.Error(),
			})
			return
		}
		b.ReportActionStatus(user, event.ActionStatus{
			Action:  "store",
			Item:    itemName,
			Success: true,
		})
	}()
}

// parseItemAndCount splits "oak_log,6" into the item name and a count. A
// missing or unparseable count means "one stack", which is what a player means
// by "take the wood".
func parseItemAndCount(param string) (string, int) {
	parts := strings.Split(param, ",")
	itemName := normalizeItemName(parts[0])
	count := 0
	if len(parts) >= 2 {
		var parsed int
		if _, err := fmt.Sscanf(parts[1], "%d", &parsed); err == nil {
			count = parsed
		}
	}
	return itemName, count
}

// handleScanChests reports what is in the visible chests without moving
// anything, so the LLM (and the player) can plan with real information.
func handleScanChests(b *bot.Bot, _, user string) {
	go func() {
		ctx := context.Background()
		chests := b.Storage().FindContainers()
		if len(chests) == 0 {
			b.ReportActionStatus(user, event.ActionStatus{
				Action:  "scan",
				Item:    "chest",
				Success: false,
				Error:   "tidak ada chest yang kelihatan",
			})
			return
		}
		labels := b.Storage().FindSigns()
		b.Storage().LabelChests(chests, labels)

		var lines []string
		for _, chest := range chests {
			container, err := b.OpenContainer(ctx, chest.Pos)
			if err != nil {
				continue
			}
			contents := storage.DescribeItems(container)
			container.Close()
			label := chest.Label
			if label == "" {
				label = "tanpa label"
			}
			lines = append(lines, fmt.Sprintf("%s [%s]: %s", blockKeyText(chest.Pos), label, contents))
			if len(lines) >= 6 {
				break
			}
		}
		b.Logger.Info("scanned chests", "count", len(lines), "detail", strings.Join(lines, " | "))
		b.ReportActionStatus(user, event.ActionStatus{
			Action:  "scan",
			Item:    "chest",
			Success: true,
		})
	}()
}

func blockKeyText(p protocol.BlockPos) string {
	return fmt.Sprintf("%d,%d,%d", safecast.To[int](p.X()), safecast.To[int](p.Y()), safecast.To[int](p.Z()))
}
