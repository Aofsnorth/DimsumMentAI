package event

import (
	"github.com/sandertv/gophertunnel/minecraft"
)

type SpawnEvent struct {
	GameData minecraft.GameData
}

type ChatEvent struct {
	Message    string
	SourceName string
	TextType   byte
}

type DisconnectEvent struct {
	Reason string
}

// ActionStatus describes the outcome of an action the bot just performed. It is
// passed to the LLM so the bot can reply with a natural, context-aware status
// message instead of a hardcoded template.
type ActionStatus struct {
	Action  string // e.g. "craft", "gather", "breed", "farm", "fish"
	Item    string // raw item/block name, e.g. "oak_planks"
	Count   int    // quantity produced/collected/attempted
	Success bool
	Error   string // empty on success
}
