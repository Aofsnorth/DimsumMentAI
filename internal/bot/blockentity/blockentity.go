// Package blockentity writes the text and contents of the blocks that are more
// than a block: signs, item frames, and armour stands.
//
// Reading these already worked. Sign text arrives as block-entity NBT appended
// to chunk data, and internal/bot/world parses it, so a bot can find the sign
// over the chest and say what is written on it. Nothing could put text there.
// The bot could label a room it could build, and it could never write a word.
//
// Bedrock carries all of this as block-entity NBT, in one packet:
// packet.BlockActorData, which holds a protocol.BlockPos and a
// map[string]any that is encoded as little-endian NBT. Sign text, sign colour,
// and the item inside a frame are all NBT keys inside that map. gophertunnel
// v1.62.0 carries an NBT encoder (nbt.NewEncoderWithEncoding, used by
// protocol.Writer.NBT), so the payload can be built here and is not something
// this bot has to hand-roll.
//
// The part worth being careful about is the claim, not the packet. A
// BlockActorData write is a request. Whether the host accepted it, and what the
// block then actually says, is only knowable from what comes back: a server
// that re-broadcasts the block entity echoes the text, and one that does not,
// does not. So every write here returns a result that distinguishes "the server
// showed me the new text" from "the packet was sent and nothing came back",
// and never reports the second as the first.
//
// The other discipline is that text is validated before it is written rather
// than discovered to be bad afterwards. A sign that overflows, carries its own
// line break, or ends in a colour code with no colour is written, rendered for
// every other player, and wrong — and by the time a human sees it, the bot has
// already moved on and reported success. Validation is a pure function
// precisely so it can be tested exhaustively without a server.
package blockentity

import (
	"log/slog"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot is the slice of the bot that writing block-entity state needs.
//
// It is narrow on purpose. This package is reached from the storage-room
// labelling path and from the building path, and neither should have to drag the
// other in. Everything here is read back through SignText and GetBlockName
// rather than through a private world model, because the confirmation has to
// come from the same source a human's view comes from.
type Bot interface {
	// GetCoords is where the bot's body is, for the aim point and the
	// interaction's reported player position.
	GetCoords() mgl32.Vec3
	// GetBlockName reports what block is in a cell, and whether that cell is
	// known at all. A cell that is not loaded is unknown, not empty.
	GetBlockName(x, y, z int32) (string, bool)
	// SignText returns the text of the sign at a position. This is the
	// confirmation channel: a write that is not visible here was not observed.
	SignText(x, y, z int32) (string, bool)
	// GetHeldItemSlot is the hot bar slot the placed or inserted item came from.
	GetHeldItemSlot() uint32
	// GetEntityRuntimeID is the bot's own entity runtime ID, required by the
	// server to attribute an inventory transaction to a player.
	GetEntityRuntimeID() uint64
	// WritePacket sends a packet to the server.
	WritePacket(pk packet.Packet) error
	// LookAt turns the head. A sign written without turning to face it is
	// written by something that is not looking at what it is labelling.
	LookAt(pos mgl32.Vec3)
	// HeldItem is the stack in the selected slot, which is what actually goes
	// into a frame or onto a stand.
	HeldItem() protocol.ItemStack
}

// Writer writes block-entity state and reports what the server did with it.
type Writer struct {
	bot    Bot
	logger *slog.Logger
	// confirmTimeout bounds how long a write waits for the server to show its
	// work before reporting the write as unobserved.
	confirmTimeout time.Duration
}

// defaultConfirmTimeout is how long a write waits for the server to echo the
// change back.
//
// It is long enough to cover a round trip plus a chunk or block-actor rebroadcast
// on a busy host, and short enough that a caller doing several signs in a row is
// not sitting on each one. A wait that ended sooner would report "unconfirmed"
// on hosts that simply answer slowly, which trains the caller to ignore the
// distinction between confirmed and unobserved.
const defaultConfirmTimeout = 2 * time.Second

// confirmPollInterval is how often the confirmation re-reads the sign. It is
// the same 50ms the rest of the bot uses when waiting on a block update, which
// is frequent enough to be quick and rare enough not to spin.
const confirmPollInterval = 50 * time.Millisecond

// Option configures a Writer.
type Option func(*Writer)

// WithConfirmTimeout overrides how long a write waits for the server to echo
// the change. Zero or negative is ignored.
func WithConfirmTimeout(d time.Duration) Option {
	return func(w *Writer) {
		if d > 0 {
			w.confirmTimeout = d
		}
	}
}

// NewWriter returns a Writer that sends block-entity writes through bot and
// reports what came back.
func NewWriter(bot Bot, logger *slog.Logger, opts ...Option) *Writer {
	if logger == nil {
		logger = slog.Default()
	}
	w := &Writer{
		bot:            bot,
		logger:         logger,
		confirmTimeout: defaultConfirmTimeout,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}
