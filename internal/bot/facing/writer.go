package facing

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// Bot is the slice of the bot that writing a facing needs.
//
// It is narrow on purpose, and it deliberately does not include a way to read
// a facing back: gophertunnel v1.62.0 has no client-to-server block-state
// packet, so there is nothing here that would let a caller confirm an
// orientation it just set. The writer does not claim one.
type Bot interface {
	// GetCoords is where the bot's body is, for the aim point.
	GetCoords() mgl32.Vec3
	// GetEntityRuntimeID is the bot's own runtime ID, required for the
	// server to attribute an update to a player.
	GetEntityRuntimeID() uint64
	// GetHeldItemSlot is the hot bar slot the block came from.
	GetHeldItemSlot() uint32
	// WritePacket sends a packet to the server.
	WritePacket(pk packet.Packet) error
	// LookAt turns the head. A block oriented without turning toward it is
	// oriented by something that is not looking at what it built.
	LookAt(pos mgl32.Vec3)
}

// aimDelay is how long the writer waits after turning toward a block before
// sending the update, so the head has arrived and the server sees a coherent
// player.
const aimDelay = 180 * time.Millisecond

// Writer sends orientation updates to the server.
type Writer struct {
	bot    Bot
	logger *slog.Logger
}

// NewWriter returns a Writer that sends orientation updates through bot.
func NewWriter(bot Bot, logger *slog.Logger) *Writer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Writer{bot: bot, logger: logger}
}

// SetFacing writes the given block-state properties for the family at pos.
//
// It is the NBT path, and its reach is narrower than the package's tables.
// packet.BlockActorData carries block-ENTITY data, so it can only address a
// family that is a block entity: chest, furnace, bed, sign. A door and a stair
// have no block entity in Bedrock, and this returns an error for them rather
// than sending an update addressed to a container that does not exist.
//
// For those two families the properties are still available from
// DoorProperties and StairsProperties; what is missing is a transport, not a
// table. See the package comment for why gophertunnel v1.62.0 has none.
//
// The write is sent, not applied. Nothing here observes the result, and the
// return value is deliberately an error rather than a success flag so that no
// caller can read "it worked" out of it.
func (w *Writer) SetFacing(pos protocol.BlockPos, family Family, props map[string]any) error {
	if !family.IsBlockEntity() {
		return fmt.Errorf("facing: %q is not a block entity; its orientation is block state, "+
			"and this transport carries block-entity NBT only", family)
	}
	if len(props) == 0 {
		return fmt.Errorf("facing: refusing to send an empty orientation update for %q at %d,%d,%d",
			family, pos.X(), pos.Y(), pos.Z())
	}

	// Turn toward the block first. The update is attributed to the player's
	// position, and an update from a bot looking the other way is one a
	// server is entitled to be suspicious of.
	w.bot.LookAt(blockCentre(pos))

	data := make(map[string]any, len(props)+4)
	for k, v := range props {
		data[k] = v
	}
	// The id and the coordinates are stamped on rather than left to the
	// caller. A BlockActorData whose NBT names no position is an update the
	// server attributes to no block at all, and it is discarded; and one
	// whose id is missing is not recognised as the family it claims to be.
	data["id"] = family.EntityID()
	data["x"] = pos.X()
	data["y"] = pos.Y()
	data["z"] = pos.Z()

	if err := w.bot.WritePacket(&packet.BlockActorData{
		Position: pos,
		NBTData:  data,
	}); err != nil {
		return fmt.Errorf("could not send the %s orientation at %d,%d,%d: %w",
			family, pos.X(), pos.Y(), pos.Z(), err)
	}

	w.logger.Debug("facing write sent",
		"family", string(family),
		"pos", fmt.Sprintf("%d,%d,%d", pos.X(), pos.Y(), pos.Z()),
		"keys", len(props),
	)
	return nil
}

// blockCentre is the middle of a block, which is where the head goes when
// looking at it.
func blockCentre(pos protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(pos.X()) + 0.5,
		float32(pos.Y()) + 0.5,
		float32(pos.Z()) + 0.5,
	}
}
