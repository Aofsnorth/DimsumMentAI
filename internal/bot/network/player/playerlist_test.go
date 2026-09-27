package player

import (
	"testing"

	"bedrock-ai/internal/bot"

	"github.com/google/uuid"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
)

// TestIsSelfEntryIgnoresZeroEntityUniqueID is the regression that matters most.
//
// Servers routinely send PlayerList entries with EntityUniqueID 0, and a freshly
// connected bot's own GameData().EntityUniqueID is also 0. Comparing the two
// directly made every such entry "match", so the bot adopted the first other
// player's name from the list — and was then kicked the instant it ran
// /register under a username that was already taken.
//
// This is the assertion that keeps that from coming back.
func TestIsSelfEntryIgnoresZeroEntityUniqueID(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{PlayerUUID: uuid.New()}

	other := protocol.PlayerListEntry{
		UUID:           uuid.New(),
		Username:       "ShigaLucifer",
		EntityUniqueID: 0, // what a real server sends for most entries
	}
	if isSelfEntry(b, other) {
		t.Error("an entry with EntityUniqueID 0 was treated as the bot; it identifies nobody")
	}
}

// TestIsSelfEntryMatchesOnUUID covers the authoritative path: our own UUID is
// enough on its own, regardless of what the entity ID says.
func TestIsSelfEntryMatchesOnUUID(t *testing.T) {
	t.Parallel()

	self := uuid.New()
	b := &bot.Bot{PlayerUUID: self}

	entry := protocol.PlayerListEntry{UUID: self, Username: "Luna", EntityUniqueID: 0}
	if !isSelfEntry(b, entry) {
		t.Error("the bot's own UUID was not recognised as itself")
	}
}

// TestIsSelfEntryRejectsAnotherPlayer guards the actual harm: a real, different
// player with a real entity ID must never be mistaken for the bot.
func TestIsSelfEntryRejectsAnotherPlayer(t *testing.T) {
	t.Parallel()

	b := &bot.Bot{PlayerUUID: uuid.New()}

	other := protocol.PlayerListEntry{
		UUID:           uuid.New(),
		Username:       "SomeoneElse",
		EntityUniqueID: 4242,
	}
	// b.Conn is nil here, so the entity-ID branch must short-circuit to false
	// rather than dereference it.
	if isSelfEntry(b, other) {
		t.Error("a different player was treated as the bot")
	}
}
