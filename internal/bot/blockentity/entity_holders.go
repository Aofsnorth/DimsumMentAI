package blockentity

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

const (
	// ItemFrameEntityID is the block-entity id of an item frame.
	ItemFrameEntityID = "ItemFrame"

	// ArmorStandEntityID is the block-entity id of an armour stand.
	ArmorStandEntityID = "ArmorStand"
)

// isItemFrameBlock reports whether a block name is an item frame.
//
// The names are matched on the bare form because the bot's block cache hands
// back both "minecraft:frame" and the block-entity name depending on where it
// was read from, and a frame that is not recognised is a frame the bot walks
// past.
func isItemFrameBlock(name string) bool {
	n := normaliseBlockName(name)
	return n == "frame" || n == "item_frame" || n == "glow_frame" || n == "glow_item_frame"
}

// isArmorStandBlock reports whether a block name is an armour stand.
func isArmorStandBlock(name string) bool {
	return normaliseBlockName(name) == "armor_stand"
}

// normaliseBlockName strips the namespace and lowercases, so the tables here are
// keyed on the bare name the rest of the bot uses.
func normaliseBlockName(name string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(name)), "minecraft:")
}

// InsertItemIntoFrame puts the held item into the item frame at pos and reports
// whether the interaction was sent.
//
// Putting an item in a frame is an entity interaction, not a block update: the
// frame is an entity in the world with its own runtime ID, and the click is a
// UseItemOnEntity inventory transaction aimed at that ID. There is no block-entity
// write for it — the frame's contents are not NBT the client sets, they are the
// result of a click the server resolves.
//
// Two things are checked before anything is sent. The cell has to actually hold
// a frame, because clicking empty space and reporting an insert is the failure
// this prevents. And the caller has to supply the frame's entity runtime ID,
// which is the only handle the protocol offers for addressing it: an ID the bot
// has never seen is not something it can invent, so a zero ID is refused rather
// than sent as a click aimed at nothing.
//
// The return says the interaction was sent. It does not claim the item is
// visible in the frame: that is the server's decision, and this package has no
// read channel for frame contents, so it is not asserted here.
func (w *Writer) InsertItemIntoFrame(ctx context.Context, pos protocol.BlockPos, frameRuntimeID uint64, item protocol.ItemStack) error {
	name, ok := w.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		return fmt.Errorf("no item frame at %s: the cell is not loaded", blockKey(pos))
	}
	if !isItemFrameBlock(name) {
		return fmt.Errorf("no item frame at %s: that cell holds %s", blockKey(pos), normaliseBlockName(name))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.interactWithEntity(ctx, pos, frameRuntimeID, item, "item frame")
}

// EquipArmorStand puts armour on the armour stand at pos and reports whether
// the interaction was sent.
//
// An armour stand is an entity like a frame, and equipping it is the same
// UseItemOnEntity click with a different target. There is no NBT path for it:
// the armour a stand wears is the result of the server resolving the click, not
// a field the client writes.
func (w *Writer) EquipArmorStand(ctx context.Context, pos protocol.BlockPos, standRuntimeID uint64, item protocol.ItemStack) error {
	name, ok := w.bot.GetBlockName(pos.X(), pos.Y(), pos.Z())
	if !ok {
		return fmt.Errorf("no armor stand at %s: the cell is not loaded", blockKey(pos))
	}
	if !isArmorStandBlock(name) {
		return fmt.Errorf("no armor stand at %s: that cell holds %s", blockKey(pos), normaliseBlockName(name))
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.interactWithEntity(ctx, pos, standRuntimeID, item, "armor stand")
}

// interactWithEntity sends the UseItemOnEntity transaction that both the frame
// and the stand need.
//
// The transaction carries the target's runtime ID, the interact action, the hot
// bar slot the item is in, and the held stack itself. The server checks that
// the held item is actually in that slot, so both are sent: sending the click
// without the stack is a click the server is free to treat as empty-handed.
func (w *Writer) interactWithEntity(ctx context.Context, pos protocol.BlockPos, targetRuntimeID uint64, item protocol.ItemStack, what string) error {
	if targetRuntimeID == 0 {
		return fmt.Errorf("cannot use the %s at %s: its entity runtime ID is unknown", what, blockKey(pos))
	}

	// Face the target first. The same reason a player turns to face a thing
	// before clicking it.
	w.bot.LookAt(entityAimPoint(pos))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(120 * time.Millisecond):
	}

	tx := &packet.InventoryTransaction{
		TransactionData: &protocol.UseItemOnEntityTransactionData{
			TargetEntityRuntimeID: targetRuntimeID,
			ActionType:            protocol.UseItemOnEntityActionInteract,
			HotBarSlot:            int32(w.bot.GetHeldItemSlot()),
			HeldItem:              protocol.ItemInstance{Stack: item},
			Position:              w.bot.GetCoords().Add(mgl32.Vec3{0, 1.62, 0}),
			ClickedPosition:       mgl32.Vec3{0, 0, 0},
		},
	}
	if err := w.bot.WritePacket(tx); err != nil {
		return fmt.Errorf("could not send the %s interaction: %w", what, err)
	}
	w.logger.Debug("entity interaction sent",
		"target", targetRuntimeID,
		"what", what,
		"pos", blockKey(pos),
	)
	return nil
}

// entityAimPoint is where the head goes to click an entity standing in a cell:
// the middle of the cell, at the height a player aims at.
func entityAimPoint(pos protocol.BlockPos) mgl32.Vec3 {
	return mgl32.Vec3{
		float32(pos.X()) + 0.5,
		float32(pos.Y()) + 1.0,
		float32(pos.Z()) + 0.5,
	}
}
