package facing_test

import (
	"bytes"
	"testing"

	"bedrock-ai/internal/bot/facing"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
)

// The expected values in this file are not guesses. Every one of them was
// printed from a real Bedrock server implementation (df-mc/dragonfly v0.10.13)
// calling the block's own EncodeBlock method, and cross-checked against the
// Mojang-canonical block_states.nbt palette that dragonfly embeds. The probe
// output that produced them is quoted next to each table below, so a future
// change to this table can be re-verified the same way instead of by memory.

// --- Cardinal families: chests, furnaces, doors ---

// TestChestFacingUsesCardinalDirectionString pins the modern Bedrock encoding.
//
// A chest is a block entity, so the payload legitimately travels as
// packet.BlockActorData NBT, but its orientation is NOT a block-entity field.
// The printed ground truth (dragonfly Chest.EncodeBlock) is:
//
//	chest north  minecraft:chest minecraft:cardinal_direction=north(string)
//	chest east   minecraft:chest minecraft:cardinal_direction=east(string)
//	chest south  minecraft:chest minecraft:cardinal_direction=south(string)
//	chest west   minecraft:chest minecraft:cardinal_direction=west(string)
//
// The value is a lower-case string, not the int32 0-3 that Java Edition uses
// and not the int32 that legacy Bedrock NBT used. Writing an int here produces
// a chest the server cannot orient.
func TestChestFacingUsesCardinalDirectionString(t *testing.T) {
	t.Parallel()

	want := map[facing.Facing]string{
		facing.North: "north",
		facing.East:  "east",
		facing.South: "south",
		facing.West:  "west",
	}
	for f, wantDir := range want {
		props := facing.ChestProperties(f)
		got, ok := props[facing.KeyCardinalDirection].(string)
		if !ok {
			t.Errorf("chest %s: %s is %T, want a string", f, facing.KeyCardinalDirection, props[facing.KeyCardinalDirection])
			continue
		}
		if got != wantDir {
			t.Errorf("chest %s: %s = %q, want %q", f, facing.KeyCardinalDirection, got, wantDir)
		}
	}
}

// TestFurnaceFacingUsesCardinalDirectionString is the same encoding for a
// furnace, whose printed state is minecraft:cardinal_direction as a string.
func TestFurnaceFacingUsesCardinalDirectionString(t *testing.T) {
	t.Parallel()

	for f, wantDir := range map[facing.Facing]string{
		facing.North: "north",
		facing.East:  "east",
		facing.South: "south",
		facing.West:  "west",
	} {
		props := facing.FurnaceProperties(f)
		if got, _ := props[facing.KeyCardinalDirection].(string); got != wantDir {
			t.Errorf("furnace %s: %s = %v, want %q", f, facing.KeyCardinalDirection, props[facing.KeyCardinalDirection], wantDir)
		}
	}
}

// TestDoorFacingIsRotatedRightFromTheOpeningDirection is the one genuinely
// surprising mapping in this package, and the reason it gets its own test.
//
// A door's direction is the way the door OPENS. What goes on the wire is that
// direction rotated a quarter turn clockwise. The printed ground truth
// (dragonfly WoodDoor.EncodeBlock, which writes d.Facing.RotateRight()) is:
//
//	door opens north -> minecraft:cardinal_direction=east
//	door opens east  -> minecraft:cardinal_direction=south
//	door opens south -> minecraft:cardinal_direction=west
//	door opens west  -> minecraft:cardinal_direction=north
//
// A door written with the un-rotated direction faces 90 degrees off. A chest
// is not rotated, so the difference between the two families is the kind of
// thing that has to be a tested fact rather than a convention.
func TestDoorFacingIsRotatedRightFromTheOpeningDirection(t *testing.T) {
	t.Parallel()

	want := map[facing.Facing]string{
		// opening      -> on the wire
		facing.North: "east",
		facing.East:  "south",
		facing.South: "west",
		facing.West:  "north",
	}
	for opening, wantDir := range want {
		props := facing.DoorProperties(opening, facing.HingeLeft, facing.DoorClosed, facing.DoorLower)
		if got, _ := props[facing.KeyCardinalDirection].(string); got != wantDir {
			t.Errorf("door opening %s: %s = %v, want %q (opening rotated right)",
				opening, facing.KeyCardinalDirection, props[facing.KeyCardinalDirection], wantDir)
		}
	}
}

// TestDoorPropertiesCarryTheFullStateSet keeps a door from being written as a
// facing alone. The printed door state has four properties, and a door missing
// the half/hinge/open bits is a door the server has to guess the rest of.
func TestDoorPropertiesCarryTheFullStateSet(t *testing.T) {
	t.Parallel()

	props := facing.DoorProperties(facing.North, facing.HingeRight, facing.DoorOpen, facing.DoorUpper)
	for _, key := range []string{
		facing.KeyCardinalDirection,
		facing.KeyDoorHinge,
		facing.KeyDoorOpen,
		facing.KeyDoorUpper,
	} {
		if _, ok := props[key]; !ok {
			t.Errorf("door state has no %q key: %#v", key, props)
		}
	}
	if got, _ := props[facing.KeyDoorHinge].(bool); !got {
		t.Error("a right-hinged door did not set door_hinge_bit")
	}
	if got, _ := props[facing.KeyDoorOpen].(bool); !got {
		t.Error("an open door did not set open_bit")
	}
	if got, _ := props[facing.KeyDoorUpper].(bool); !got {
		t.Error("an upper door half did not set upper_block_bit")
	}
}

// --- Beds ---

// TestBedDirectionIsTheLegacyInt32Scale pins the bed's own numbering, which is
// neither the cardinal order nor the Java Edition order.
//
// The printed ground truth (dragonfly Bed.EncodeBlock, which writes
// horizontalDirection(b.Facing)) is:
//
//	bed north -> direction=2
//	bed east  -> direction=3
//	bed south -> direction=0
//	bed west  -> direction=1
//
// This is the 0=south, 1=west, 2=north, 3=east scale. Guessing the obvious
// north=0 gives a bed pointing at the wall.
func TestBedDirectionIsTheLegacyInt32Scale(t *testing.T) {
	t.Parallel()

	want := map[facing.Facing]int32{
		facing.North: 2,
		facing.East:  3,
		facing.South: 0,
		facing.West:  1,
	}
	for f, wantDir := range want {
		props := facing.BedProperties(f, facing.BedFoot)
		if got, _ := props[facing.KeyBedDirection].(int32); got != wantDir {
			t.Errorf("bed %s: %s = %v, want int32 %d", f, facing.KeyBedDirection, props[facing.KeyBedDirection], wantDir)
		}
	}
}

// TestBedHeadPieceBitIsWhatDistinguishesTheTwoHalves is the other half of a
// bed. The direction alone does not say which block is the head; the printed
// state carries head_piece_bit as a uint8.
func TestBedHeadPieceBitIsWhatDistinguishesTheTwoHalves(t *testing.T) {
	t.Parallel()

	foot := facing.BedProperties(facing.East, facing.BedFoot)
	head := facing.BedProperties(facing.East, facing.BedHead)

	if got, _ := foot[facing.KeyBedHeadPiece].(uint8); got != 0 {
		t.Errorf("foot half: %s = %d, want 0", facing.KeyBedHeadPiece, got)
	}
	if got, _ := head[facing.KeyBedHeadPiece].(uint8); got != 1 {
		t.Errorf("head half: %s = %d, want 1", facing.KeyBedHeadPiece, got)
	}
	if foot[facing.KeyBedDirection] != head[facing.KeyBedDirection] {
		t.Error("the two halves of a bed disagree about which way it points")
	}
}

// --- Stairs ---

// TestStairsWeirdoDirectionIsThreeMinusTheFacing pins the stairs scale, whose
// name in the palette is weirdo_direction.
//
// Printed ground truth (dragonfly Stairs.EncodeBlock writes 3 - int(facing)):
//
//	stairs north -> weirdo_direction=3
//	stairs east  -> weirdo_direction=0
//	stairs south -> weirdo_direction=2
//	stairs west  -> weirdo_direction=1
//
// So north is 3 here and 2 on a bed. The two scales are transposed relative to
// each other and a shared helper would silently misplace both.
func TestStairsWeirdoDirectionIsThreeMinusTheFacing(t *testing.T) {
	t.Parallel()

	want := map[facing.Facing]int32{
		facing.North: 3,
		facing.East:  0,
		facing.South: 2,
		facing.West:  1,
	}
	for f, wantDir := range want {
		props := facing.StairsProperties(f, facing.StairsUpright)
		if got, _ := props[facing.KeyStairsDirection].(int32); got != wantDir {
			t.Errorf("stairs %s: %s = %v, want int32 %d", f, facing.KeyStairsDirection, props[facing.KeyStairsDirection], wantDir)
		}
	}
}

// TestStairsUpsideDownBitFlipsTheFullSide guards the other stairs property.
func TestStairsUpsideDownBitFlipsTheFullSide(t *testing.T) {
	t.Parallel()

	up := facing.StairsProperties(facing.North, facing.StairsUpright)
	down := facing.StairsProperties(facing.North, facing.StairsUpsideDown)
	if got, _ := up[facing.KeyStairsUpsideDown].(bool); got {
		t.Error("upright stairs set upside_down_bit")
	}
	if got, _ := down[facing.KeyStairsUpsideDown].(bool); !got {
		t.Error("upside-down stairs did not set upside_down_bit")
	}
	if up[facing.KeyStairsDirection] != down[facing.KeyStairsDirection] {
		t.Error("flipping a stair changed the direction it points")
	}
}

// --- Signs ---

// TestWallSignFacingDirectionUsesTheFaceScale pins the wall sign, which is the
// only family here that uses a six-value face scale rather than a four-value
// cardinal one.
//
// Printed ground truth (dragonfly Sign.EncodeBlock for a wall attachment) is:
//
//	wall sign north -> facing_direction=2
//	wall sign east  -> facing_direction=5
//	wall sign south -> facing_direction=3
//	wall sign west  -> facing_direction=4
//
// Note this is NOT face+2: north is 2, not 4. It is the cube.Face value itself.
func TestWallSignFacingDirectionUsesTheFaceScale(t *testing.T) {
	t.Parallel()

	want := map[facing.Facing]int32{
		facing.North: 2,
		facing.East:  5,
		facing.South: 3,
		facing.West:  4,
	}
	for f, wantDir := range want {
		props := facing.WallSignProperties(f)
		if got, _ := props[facing.KeySignFacingDirection].(int32); got != wantDir {
			t.Errorf("wall sign %s: %s = %v, want int32 %d",
				f, facing.KeySignFacingDirection, props[facing.KeySignFacingDirection], wantDir)
		}
	}
}

// TestStandingSignTakesASixteenStepRotation keeps a standing sign off the wall
// sign's scale. It has no facing at all; it has a rotation, and the printed
// palette gives it sixteen steps rather than six.
func TestStandingSignTakesASixteenStepRotation(t *testing.T) {
	t.Parallel()

	for step := 0; step < 16; step++ {
		props := mustStandingSign(t, step)
		if got, _ := props[facing.KeySignGroundDirection].(int32); got != int32(step) {
			t.Errorf("standing sign step %d: %s = %v, want int32 %d",
				step, facing.KeySignGroundDirection, props[facing.KeySignGroundDirection], step)
		}
		if _, wrong := props[facing.KeySignFacingDirection]; wrong {
			t.Errorf("standing sign step %d carries a wall sign's %s", step, facing.KeySignFacingDirection)
		}
	}
}

// TestStandingSignRotationIsRangeChecked is the guard that stops an
// out-of-range rotation from being written as an impossible sign.
func TestStandingSignRotationIsRangeChecked(t *testing.T) {
	t.Parallel()

	if _, err := facing.StandingSignProperties(16); err == nil {
		t.Error("rotation 16 was accepted; the scale stops at 15")
	}
	if _, err := facing.StandingSignProperties(-1); err == nil {
		t.Error("a negative rotation was accepted")
	}
}

// --- Block entity identity ---

// TestBlockEntityIDsAreTheOnesBedrockUses pins the ids that go in the NBT.
//
// Only families that really are block entities get one. Printed from
// dragonfly's EncodeNBT methods: Chest, Furnace, Bed, Sign. A door and a stair
// have no block entity at all in Bedrock, so writing one is inventing a
// container the server does not have.
func TestBlockEntityIDsAreTheOnesBedrockUses(t *testing.T) {
	t.Parallel()

	for got, want := range map[facing.Family]string{
		facing.FamilyChest:   "Chest",
		facing.FamilyFurnace: "Furnace",
		facing.FamilyBed:     "Bed",
		facing.FamilySign:    "Sign",
	} {
		if got.EntityID() != want {
			t.Errorf("family %q entity id = %q, want %q", got, got.EntityID(), want)
		}
	}
	for _, fam := range []facing.Family{facing.FamilyDoor, facing.FamilyStairs} {
		if fam.EntityID() != "" {
			t.Errorf("family %q reports entity id %q; it is not a Bedrock block entity",
				fam, fam.EntityID())
		}
	}
}

// TestIsBlockEntityAgreesWithEntityID keeps the two answers from drifting apart,
// because the writer branches on one of them.
func TestIsBlockEntityAgreesWithEntityID(t *testing.T) {
	t.Parallel()

	for _, fam := range facing.AllFamilies() {
		if fam.IsBlockEntity() != (fam.EntityID() != "") {
			t.Errorf("family %q: IsBlockEntity()=%v but EntityID()=%q",
				fam, fam.IsBlockEntity(), fam.EntityID())
		}
	}
}

// --- Property maps are pure ---

// TestPropertyBuildersDoNotShareMutableState is the aliasing guard. Each call
// must hand back a fresh map, because a caller that mutates the result of one
// call would otherwise corrupt every later call through the same backing array.
func TestPropertyBuildersDoNotShareMutableState(t *testing.T) {
	t.Parallel()

	first := facing.ChestProperties(facing.North)
	first[facing.KeyCardinalDirection] = "tampered"
	first["injected"] = true

	second := facing.ChestProperties(facing.North)
	if got, _ := second[facing.KeyCardinalDirection].(string); got != "north" {
		t.Errorf("a later chest read %q after an earlier map was mutated; the maps are shared", got)
	}
	if _, leaked := second["injected"]; leaked {
		t.Error("a key added to one chest map appeared in the next one")
	}
}

// TestEveryCardinalFamilyRejectsAnInvalidFacing keeps a zero-value or
// out-of-range direction from being written as if it were a real one.
func TestEveryCardinalFamilyRejectsAnInvalidFacing(t *testing.T) {
	t.Parallel()

	bad := facing.Facing(99)
	if _, err := facing.ChestPropertiesE(bad); err == nil {
		t.Error("chest accepted facing 99")
	}
	if _, err := facing.FurnacePropertiesE(bad); err == nil {
		t.Error("furnace accepted facing 99")
	}
	if _, err := facing.DoorPropertiesE(bad, facing.HingeLeft, facing.DoorClosed, facing.DoorLower); err == nil {
		t.Error("door accepted facing 99")
	}
	if _, err := facing.BedPropertiesE(bad, facing.BedFoot); err == nil {
		t.Error("bed accepted facing 99")
	}
	if _, err := facing.StairsPropertiesE(bad, facing.StairsUpright); err == nil {
		t.Error("stairs accepted facing 99")
	}
	if _, err := facing.WallSignPropertiesE(bad); err == nil {
		t.Error("wall sign accepted facing 99")
	}
}

// TestValidFacingIsTheComplement is the other half: every real cardinal value
// must be accepted, so the range check above is not just rejecting everything.
func TestValidFacingIsTheComplement(t *testing.T) {
	t.Parallel()

	for _, f := range facing.AllFacings() {
		if !f.IsCardinal() {
			t.Errorf("facing %q is not cardinal but is listed by AllFacings", f)
		}
		if _, err := facing.ChestPropertiesE(f); err != nil {
			t.Errorf("chest rejected the valid facing %q: %v", f, err)
		}
	}
}

// --- NBT round trip ---

// TestFacingPropertiesSurviveAWireRoundTrip is the test that says the payload
// is real rather than merely well-formed Go.
//
// The properties are put into a packet.BlockActorData and marshalled through
// protocol.NewWriter exactly as a connection would send them, read back through
// protocol.NewReader, and checked. An int32 that arrives as an int, or a bool
// that arrives as a string, is a payload the server cannot read back, and a Go
// map that merely looks right does not catch it.
func TestFacingPropertiesSurviveAWireRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		props map[string]any
		key   string
		want  any
	}{
		{"chest", facing.ChestProperties(facing.West), facing.KeyCardinalDirection, "west"},
		{"door", facing.DoorProperties(facing.South, facing.HingeLeft, facing.DoorClosed, facing.DoorLower), facing.KeyCardinalDirection, "west"},
		{"bed", facing.BedProperties(facing.North, facing.BedHead), facing.KeyBedDirection, int32(2)},
		{"stairs", facing.StairsProperties(facing.East, facing.StairsUpright), facing.KeyStairsDirection, int32(0)},
		{"wall sign", facing.WallSignProperties(facing.East), facing.KeySignFacingDirection, int32(5)},
		{"standing sign", mustStandingSign(t, 9), facing.KeySignGroundDirection, int32(9)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pos := protocol.BlockPos{7, 64, -3}
			pk := &packet.BlockActorData{Position: pos, NBTData: tc.props}

			var buf bytes.Buffer
			pk.Marshal(protocol.NewWriter(&buf, 0))
			if buf.Len() == 0 {
				t.Fatal("marshalling produced no bytes")
			}

			var got packet.BlockActorData
			got.Marshal(protocol.NewReader(bytes.NewReader(buf.Bytes()), 0, false))

			if got.Position != pos {
				t.Errorf("position survived as %v, want %v", got.Position, pos)
			}
			if got.NBTData[tc.key] != tc.want {
				t.Errorf("%s: %s survived as %v (%T), want %v (%T)",
					tc.name, tc.key, got.NBTData[tc.key], got.NBTData[tc.key], tc.want, tc.want)
			}
		})
	}
}

// TestFacingPropertiesAreIndependentlyNBTDecodable checks the payload with the
// NBT decoder directly, so the wire test above is not the only thing standing
// between the map and the bytes.
func TestFacingPropertiesAreIndependentlyNBTDecodable(t *testing.T) {
	t.Parallel()

	props := facing.DoorProperties(facing.West, facing.HingeRight, facing.DoorOpen, facing.DoorLower)

	encoded, err := nbt.MarshalEncoding(props, nbt.NetworkLittleEndian)
	if err != nil {
		t.Fatalf("the facing NBT could not be encoded: %v", err)
	}
	var out map[string]any
	dec := nbt.NewDecoderWithEncoding(bytes.NewReader(encoded), nbt.NetworkLittleEndian)
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("the facing NBT could not be decoded: %v", err)
	}

	if got, _ := out[facing.KeyCardinalDirection].(string); got != "north" {
		t.Errorf("%s decoded as %v, want %q", facing.KeyCardinalDirection, out[facing.KeyCardinalDirection], "north")
	}

	// The bit properties are written as Go bools, and NBT decodes them back
	// as the byte the wire carries. That is not a loss: dragonfly's
	// block-state hash treats bool and uint8 identically, writing a single
	// 0/1 byte for either, so bool and uint8 are the same value on this wire.
	// Asserting the decoded form here keeps the round trip honest without
	// pinning a Go type the protocol does not actually distinguish.
	if got := out[facing.KeyDoorOpen]; got != uint8(1) {
		t.Errorf("%s decoded as %v (%T), want the byte 1", facing.KeyDoorOpen, got, got)
	}
	if got := out[facing.KeyDoorHinge]; got != uint8(1) {
		t.Errorf("%s decoded as %v (%T), want the byte 1", facing.KeyDoorHinge, got, got)
	}
}

// --- The writer ---

// TestWriteSendsExactlyOneBlockActorData is the basic contract of the writer:
// a facing write is one BlockActorData at the right position carrying the
// right properties.
func TestWriteSendsExactlyOneBlockActorData(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := facing.NewWriter(bot, discardLogger())

	pos := protocol.BlockPos{10, 65, -4}
	if err := w.SetFacing(pos, facing.FamilyChest, facing.ChestProperties(facing.North)); err != nil {
		t.Fatalf("SetFacing: %v", err)
	}

	writes := bot.blockActorWrites()
	if len(writes) != 1 {
		t.Fatalf("got %d block actor writes, want exactly 1", len(writes))
	}
	if writes[0].Position != pos {
		t.Errorf("write went to %v, want %v", writes[0].Position, pos)
	}
	if got, _ := writes[0].NBTData[facing.KeyCardinalDirection].(string); got != "north" {
		t.Errorf("write carried %s = %v, want %q", facing.KeyCardinalDirection, writes[0].NBTData[facing.KeyCardinalDirection], "north")
	}
}

// TestWriteStampsTheBlockEntityIDAndCoordinates is what makes the update
// attributable to a block at all. A BlockActorData whose NBT names no position
// is an update the server attributes to nothing, and it is discarded.
func TestWriteStampsTheBlockEntityIDAndCoordinates(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := facing.NewWriter(bot, discardLogger())

	pos := protocol.BlockPos{5, 70, 9}
	if err := w.SetFacing(pos, facing.FamilyChest, facing.ChestProperties(facing.South)); err != nil {
		t.Fatalf("SetFacing: %v", err)
	}

	data := bot.blockActorWrites()[0].NBTData
	if got, _ := data["id"].(string); got != "Chest" {
		t.Errorf("id = %q, want %q", got, "Chest")
	}
	for field, want := range map[string]int32{"x": 5, "y": 70, "z": 9} {
		if v, ok := data[field].(int32); !ok || v != want {
			t.Errorf("%s = %v (%T), want int32 %d; the server cannot read a non-int32 coordinate",
				field, data[field], data[field], want)
		}
	}
}

// TestWriteRefusesABlockEntityUpdateForANonBlockEntity is the door and stairs
// guard. They have no block entity, so a BlockActorData naming one is a
// fiction: the server has no container at that position to apply it to.
func TestWriteRefusesABlockEntityUpdateForANonBlockEntity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fam   facing.Family
		props map[string]any
	}{
		{facing.FamilyDoor, facing.DoorProperties(facing.North, facing.HingeLeft, facing.DoorClosed, facing.DoorLower)},
		{facing.FamilyStairs, facing.StairsProperties(facing.North, facing.StairsUpright)},
	} {
		bot := newFakeBot()
		w := facing.NewWriter(bot, discardLogger())
		if err := w.SetFacing(protocol.BlockPos{1, 1, 1}, tc.fam, tc.props); err == nil {
			t.Errorf("family %q accepted a block-entity write; it is not a block entity", tc.fam)
		}
		if len(bot.blockActorWrites()) != 0 {
			t.Errorf("family %q still reached the wire", tc.fam)
		}
	}
}

// TestWriteIsRejectedBeforeTheWireWhenPropertiesAreEmpty is the same
// discipline the sign writer follows: refuse the write, do not send a blank
// update over a real chest.
func TestWriteIsRejectedBeforeTheWireWhenPropertiesAreEmpty(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	w := facing.NewWriter(bot, discardLogger())

	if err := w.SetFacing(protocol.BlockPos{1, 1, 1}, facing.FamilyChest, nil); err == nil {
		t.Error("an empty property map was accepted")
	}
	if len(bot.blockActorWrites()) != 0 {
		t.Error("an empty facing update reached the wire")
	}
}

// TestWriteReportsATransportFailureAsAnError keeps a failed send distinct from
// a successful one. A packet that never left the bot has not been sent to
// anybody.
func TestWriteReportsATransportFailureAsAnError(t *testing.T) {
	t.Parallel()

	bot := newFakeBot()
	bot.writeErr = errBoom{}
	w := facing.NewWriter(bot, discardLogger())

	if err := w.SetFacing(protocol.BlockPos{1, 1, 1}, facing.FamilyChest, facing.ChestProperties(facing.North)); err == nil {
		t.Error("a failed write reported success")
	}
}

func mustStandingSign(t *testing.T, step int) map[string]any {
	t.Helper()
	props, err := facing.StandingSignProperties(step)
	if err != nil {
		t.Fatalf("StandingSignProperties(%d): %v", step, err)
	}
	return props
}
