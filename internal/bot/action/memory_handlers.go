// This file hosts the MinePal-parity handlers for the action dispatch in
// execute.go: curated long-term memory (remember/recall/forget), named home
// (sethome/home), surroundings analysis (analyze), and direct-command
// aliases (move/look) matching open-source MinePal forks.
package action

import (
	"fmt"
	"sort"
	"strings"

	"bedrock-ai/internal/bot"
	"bedrock-ai/internal/bot/entity"
	"bedrock-ai/internal/bot/perception"
	"bedrock-ai/internal/event"

	"github.com/go-gl/mathgl/mgl32"
)

func init() {
	// Storage and signage. take/store go through the labelled search so the
	// bot opens chests one at a time, in the order the signage implies, and
	// only from where it can actually see them.
	ActionHandlers["readsign"] = handleReadSign
	ActionHandlers["read_sign"] = handleReadSign
	ActionHandlers["take"] = handleTakeFromChest
	ActionHandlers["retrieve"] = handleTakeFromChest
	ActionHandlers["store"] = handleStoreInChest
	ActionHandlers["storeall"] = handleStoreInChest
	ActionHandlers["scan_chests"] = handleScanChests

	ActionHandlers["remember"] = handleRemember
	ActionHandlers["recall"] = handleRecall
	ActionHandlers["memories"] = handleRecall
	ActionHandlers["forget"] = handleForget
	ActionHandlers["sethome"] = handleSetHome
	ActionHandlers["home"] = handleHome
	ActionHandlers["analyze"] = handleAnalyze
	// Direct-command parity with open-source MinePal forks (!move/!look).
	ActionHandlers["move"] = ActionHandlers["goto"]
	ActionHandlers["look"] = ActionHandlers["lookat"]
	// Navigation family. Each asks for a different arrival spot, so they are
	// separate labels rather than one action with a mode parameter: the LLM
	// picks a word ("stand on that block") and gets the matching behaviour.
	ActionHandlers["gotoblock"] = func(b *bot.Bot, param, user string) { goToBlock(b, param, user) }
	ActionHandlers["walkto"] = ActionHandlers["gotoblock"]
	ActionHandlers["gotonearest"] = ActionHandlers["gotoblock"]
	ActionHandlers["standon"] = func(b *bot.Bot, param, user string) { standOnBlock(b, param, user) }
	ActionHandlers["ontop"] = ActionHandlers["standon"]
	ActionHandlers["standabove"] = ActionHandlers["standon"]
	ActionHandlers["enterportal"] = func(b *bot.Bot, param, user string) { enterPortal(b, param, user) }
	ActionHandlers["portal"] = ActionHandlers["enterportal"]
	ActionHandlers["usenetherportal"] = ActionHandlers["enterportal"]
}

// handleRemember stores player text as a curated long-term memory.
func handleRemember(b *bot.Bot, param, user string) {
	if b.Memory == nil {
		ReportStatus(b, user, event.ActionStatus{Action: "remember", Success: false, Error: "memori belum siap"})
		return
	}
	fact, err := b.Memory.Add(param, user)
	if err != nil {
		ReportStatus(b, user, event.ActionStatus{Action: "remember", Success: false, Error: err.Error()})
		return
	}
	ReportStatus(b, user, event.ActionStatus{Action: "remember", Item: fmt.Sprintf("(%d) %s", fact.ID, fact.Text), Success: true})
}

// handleRecall lists memories, optionally filtered by a substring query.
func handleRecall(b *bot.Bot, param, user string) {
	if b.Memory == nil {
		ReportStatus(b, user, event.ActionStatus{Action: "recall", Success: false, Error: "memori belum siap"})
		return
	}
	facts := b.Memory.Search(param)
	if len(facts) == 0 {
		ReportStatus(b, user, event.ActionStatus{Action: "recall", Item: "belum ada memori", Success: true})
		return
	}
	const maxShow = 10
	shown := facts
	if len(shown) > maxShow {
		shown = shown[len(shown)-maxShow:]
	}
	parts := make([]string, 0, len(shown))
	for _, f := range shown {
		parts = append(parts, fmt.Sprintf("(%d) %s", f.ID, f.Text))
	}
	item := strings.Join(parts, "; ")
	if hidden := len(facts) - len(shown); hidden > 0 {
		item += fmt.Sprintf(" (+%d lagi)", hidden)
	}
	ReportStatus(b, user, event.ActionStatus{Action: "recall", Item: item, Count: len(facts), Success: true})
}

// handleForget removes one memory by ID or substring match.
func handleForget(b *bot.Bot, param, user string) {
	if b.Memory == nil {
		ReportStatus(b, user, event.ActionStatus{Action: "forget", Success: false, Error: "memori belum siap"})
		return
	}
	removed, ok := b.Memory.Forget(param)
	if !ok {
		ReportStatus(b, user, event.ActionStatus{Action: "forget", Success: false, Error: "memori tidak ketemu"})
		return
	}
	ReportStatus(b, user, event.ActionStatus{Action: "forget", Item: fmt.Sprintf("(%d) %s", removed.ID, removed.Text), Success: true})
}

// handleSetHome remembers the bot's current position as "home".
func handleSetHome(b *bot.Bot, _, user string) {
	if b.Memory == nil {
		ReportStatus(b, user, event.ActionStatus{Action: "sethome", Success: false, Error: "memori belum siap"})
		return
	}
	pos := b.GetCoords()
	place, err := b.Memory.RememberPlace("home", pos.X(), pos.Y(), pos.Z())
	if err != nil {
		ReportStatus(b, user, event.ActionStatus{Action: "sethome", Success: false, Error: err.Error()})
		return
	}
	ReportStatus(b, user, event.ActionStatus{
		Action:  "sethome",
		Item:    fmt.Sprintf("home di X:%.0f Y:%.0f Z:%.0f", place.X, place.Y, place.Z),
		Success: true,
	})
}

// handleHome walks back to the remembered "home" position.
func handleHome(b *bot.Bot, _, user string) {
	if b.Memory == nil {
		ReportStatus(b, user, event.ActionStatus{Action: "home", Success: false, Error: "memori belum siap"})
		return
	}
	place, ok := b.Memory.Place("home")
	if !ok {
		ReportStatus(b, user, event.ActionStatus{Action: "home", Success: false, Error: "belum ada home, pakai sethome dulu"})
		return
	}
	b.WalkTo(mgl32.Vec3{place.X, place.Y, place.Z})
}

// handleAnalyze reports the bot's surroundings: status, held item,
// inventory, nearby players, nearby mobs, and ground drops.
func handleAnalyze(b *bot.Bot, _, user string) {
	hp, hunger, coords := b.GetStatusDetails()
	held := b.GetHeldItem()
	inv := b.GetInventorySummary()

	type nearby struct {
		name string
		dist float32
	}
	b.Mu.Lock()
	origin := b.Pos
	players := make([]nearby, 0, 8)
	for name, id := range b.PlayerEntityIDs {
		if strings.EqualFold(name, b.Name) {
			continue
		}
		pos, ok := b.PlayerPositions[id]
		if !ok {
			continue
		}
		if d := pos.Sub(origin).Len(); d <= 30 {
			players = append(players, nearby{name: name, dist: d})
		}
	}
	mobs := make([]nearby, 0, 16)
	drops := 0
	for _, info := range b.Actors {
		if info == nil {
			continue
		}
		if entity.IsItemActor(info) {
			drops++
			continue
		}
		name := entity.NormalizeName(info.Name)
		if name == "" {
			name = entity.NormalizeName(info.Type)
		}
		if name == "" {
			continue
		}
		if d := info.Position.Sub(origin).Len(); d <= 20 {
			mobs = append(mobs, nearby{name: name, dist: d})
		}
	}
	b.Mu.Unlock()

	sort.Slice(players, func(i, j int) bool { return players[i].dist < players[j].dist })
	sort.Slice(mobs, func(i, j int) bool { return mobs[i].dist < mobs[j].dist })

	playerStr := "tidak ada"
	if len(players) > 0 {
		parts := make([]string, 0, len(players))
		for _, p := range players {
			parts = append(parts, fmt.Sprintf("%s (%.0fm)", p.name, p.dist))
		}
		playerStr = strings.Join(parts, ", ")
	}
	mobStr := "tidak ada"
	if len(mobs) > 0 {
		limit := len(mobs)
		if limit > 5 {
			limit = 5
		}
		parts := make([]string, 0, limit)
		for _, m := range mobs[:limit] {
			parts = append(parts, fmt.Sprintf("%s (%.0fm)", m.name, m.dist))
		}
		mobStr = strings.Join(parts, ", ")
		if len(mobs) > limit {
			mobStr += fmt.Sprintf(" (+%d lagi)", len(mobs)-limit)
		}
	}
	summary := fmt.Sprintf("HP %d/20, lapar %d/20, pos %s, pegang %s | Inventory: %s | Pemain dekat: %s | Mob dekat: %s | Drop: %d | Blok terlihat: %s",
		hp, hunger, coords, held, inv, playerStr, mobStr, drops, perception.BlocksSummary(b, 12.0, 6))
	ReportStatus(b, user, event.ActionStatus{Action: "analyze", Item: summary, Success: true})
}
