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
	// Whisper is true when this line reached the bot as a private message.
	//
	// It is set from the relayed text form rather than from TextType: the server
	// forwards a whisper to a Bedrock client as an ordinary system message with
	// the participants in the body ("MSG (Synterix ➺ Bot) halo") and an empty
	// source, so the packet carries no whisper flag to read. Replies have to go
	// back the same way they came, which this flag is what makes possible.
	Whisper bool
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

	// Terminal marks the report that closes out a job the player asked for.
	//
	// Long jobs report more than once: one line per tree, one per vein. Those are
	// routine progress and stay unsaid — announcing every tree turns the bot into
	// a build server log. The one line the player is actually waiting for is the
	// final one, and without this flag nothing distinguished it from the rest, so
	// the bot finished ten logs of wood and said nothing at all. A terminal
	// success is worth saying out loud even when its action label is otherwise
	// quiet.
	Terminal bool
}
