package bot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The in-game player whitelist.
//
// The chat gate only lets linked main players talk to the bot; everyone else
// is logged and dropped. On a public server the operator's friends are not all
// in bot.yaml, and editing the config mid-session is not a thing — so a main
// player grants access from inside the game ("Luna, whitelist nama Budi") and
// the bot answers them from then on. Only main players can change the list,
// enforced at the command site: a gate anyone in the world can widen is no
// gate at all.
//
// The list persists to whitelistPath so a restart does not silently revoke
// everybody. An empty path keeps the list in memory only — what the tests
// want, and what a bot running somewhere it cannot write gets.

// DefaultWhitelistPath is where newBot persists the in-game whitelist, next to
// the bot state and memory files.
const DefaultWhitelistPath = "data/player_whitelist.json"

// whitelistFile is the on-disk shape.
type whitelistFile struct {
	Players []string `json:"players"`
}

// canonicalWhitelistName normalises a chat source or a command argument into
// the name the list stores. Chat rank prefixes ("[VIP] Budi") are
// canonicalised by the chat layer before this runs; here it is trimming and
// the leading server-name dot, the same normalisation IsMainPlayer applies.
func canonicalWhitelistName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, ".")
	return strings.TrimSpace(name)
}

// loadWhitelistLocked reads the persisted list once. Caller holds whitelistMu.
func (b *Bot) loadWhitelistLocked() {
	if b.whitelistLoaded {
		return
	}
	b.whitelistLoaded = true
	if b.whitelistPath == "" {
		return
	}
	data, err := os.ReadFile(b.whitelistPath)
	if err != nil {
		if !os.IsNotExist(err) && b.Logger != nil {
			b.Logger.Warn("whitelist: could not read file, starting empty",
				"path", b.whitelistPath, "error", err.Error())
		}
		return
	}
	var wf whitelistFile
	if err := json.Unmarshal(data, &wf); err != nil {
		if b.Logger != nil {
			b.Logger.Warn("whitelist: could not parse file, starting empty",
				"path", b.whitelistPath, "error", err.Error())
		}
		return
	}
	b.whitelist = make(map[string]string, len(wf.Players))
	for _, p := range wf.Players {
		if name := canonicalWhitelistName(p); name != "" {
			b.whitelist[strings.ToLower(name)] = name
		}
	}
}

// saveWhitelistLocked writes the list back. Caller holds whitelistMu. A bot
// with no path (or an empty list) skips the write; the file returning on the
// next load is the caller's problem to have made, not this one's.
func (b *Bot) saveWhitelistLocked() {
	if b.whitelistPath == "" || b.whitelist == nil {
		return
	}
	names := make([]string, 0, len(b.whitelist))
	for _, display := range b.whitelist {
		names = append(names, display)
	}
	sort.Strings(names)
	data, err := json.MarshalIndent(whitelistFile{Players: names}, "", "  ")
	if err != nil && b.Logger != nil {
		b.Logger.Warn("whitelist: could not encode file", "path", b.whitelistPath, "error", err.Error())
		return
	}
	if dir := filepath.Dir(b.whitelistPath); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(b.whitelistPath, data, 0o600); err != nil && b.Logger != nil {
		b.Logger.Warn("whitelist: could not save file", "path", b.whitelistPath, "error", err.Error())
	}
}

// IsWhitelistedPlayer reports whether the player was granted access in-game.
func (b *Bot) IsWhitelistedPlayer(name string) bool {
	name = canonicalWhitelistName(name)
	if name == "" {
		return false
	}
	b.whitelistMu.Lock()
	defer b.whitelistMu.Unlock()
	b.loadWhitelistLocked()
	_, ok := b.whitelist[strings.ToLower(name)]
	return ok
}

// WhitelistPlayer grants a player access to the bot and persists the list. It
// reports whether the list changed, so the caller can tell a fresh grant from
// somebody already on it.
func (b *Bot) WhitelistPlayer(name string) bool {
	name = canonicalWhitelistName(name)
	if name == "" {
		return false
	}
	b.whitelistMu.Lock()
	defer b.whitelistMu.Unlock()
	b.loadWhitelistLocked()
	if b.whitelist == nil {
		b.whitelist = make(map[string]string)
	}
	lower := strings.ToLower(name)
	if _, exists := b.whitelist[lower]; exists {
		return false
	}
	b.whitelist[lower] = name
	b.saveWhitelistLocked()
	if b.Logger != nil {
		b.Logger.Info("whitelist: player added", "player", name, "path", b.whitelistPath)
	}
	return true
}

// UnwhitelistPlayer revokes a player's access and persists the list. It
// reports whether the list changed — revoking a name that was never on it
// changes nothing.
func (b *Bot) UnwhitelistPlayer(name string) bool {
	name = canonicalWhitelistName(name)
	if name == "" {
		return false
	}
	b.whitelistMu.Lock()
	defer b.whitelistMu.Unlock()
	b.loadWhitelistLocked()
	lower := strings.ToLower(name)
	if _, exists := b.whitelist[lower]; !exists {
		return false
	}
	delete(b.whitelist, lower)
	b.saveWhitelistLocked()
	if b.Logger != nil {
		b.Logger.Info("whitelist: player removed", "player", name, "path", b.whitelistPath)
	}
	return true
}

// WhitelistedPlayers returns the granted names, for the chat context and
// diagnostics.
func (b *Bot) WhitelistedPlayers() []string {
	b.whitelistMu.Lock()
	defer b.whitelistMu.Unlock()
	b.loadWhitelistLocked()
	out := make([]string, 0, len(b.whitelist))
	for _, display := range b.whitelist {
		out = append(out, display)
	}
	sort.Strings(out)
	return out
}
