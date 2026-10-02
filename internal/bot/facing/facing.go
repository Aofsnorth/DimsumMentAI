// Package facing turns a desired orientation into the exact block-state
// properties Bedrock expects for each family of oriented block.
//
// # Why this is block state and not block-entity NBT
//
// The obvious assumption is that orientation is block-entity NBT, because that
// is where a chest's contents live and where a sign's text lives. It is not.
// On the protocol this bot speaks (gophertunnel v1.62.0, protocol 2193 =
// Bedrock 1.21.x) a chest's orientation is a property of the block state, and
// it is a STRING.
//
// That was verified rather than assumed. Every value in this package was
// printed from df-mc/dragonfly v0.10.13 — a Bedrock server implementation — by
// calling each block's own EncodeBlock method, and cross-checked against the
// Mojang-canonical block_states.nbt palette that dragonfly embeds. The printed
// ground truth is:
//
//	door            minecraft:cardinal_direction (string), door_hinge_bit,
//	                open_bit, upper_block_bit  (bool)
//	chest           minecraft:cardinal_direction (string)
//	furnace         minecraft:cardinal_direction (string)
//	bed             direction (int32), head_piece_bit (uint8), occupied_bit (uint8)
//	stairs          weirdo_direction (int32), upside_down_bit (bool)
//	wall sign       facing_direction (int32, 0-5)
//	standing sign   ground_sign_direction (int32, 0-15)
//
// Three things follow from that, and all three are load-bearing:
//
//   - The cardinal families take a lower-case string ("north"), not the int32
//     0-3 that Java Edition uses and not the int32 that legacy Bedrock NBT
//     used. Writing an int produces a chest the server cannot orient.
//   - The three int32 scales disagree with each other. A bed's direction is
//     0=south,1=west,2=north,3=east. A stair's weirdo_direction is
//     0=east,1=west,2=south,3=north. A wall sign's facing_direction is the
//     six-value cube.Face scale. They are transposed relative to each other and
//     cannot share a helper.
//   - A door's facing is rotated a quarter turn clockwise from the direction
//     it opens towards, and a chest's is not.
//
// # Which families are block entities
//
// Only some of these blocks have block-entity NBT at all. Chest, Furnace, Bed
// and Sign do, and carry an "id" the server matches on. A door and a stair do
// not: they have no block entity, so a BlockActorData naming one is an update
// addressed to a container that does not exist.
//
// # The transport, and what this package does not do
//
// The Writer here sends packet.BlockActorData, which is the only client-to-
// server block-entity channel gophertunnel v1.62.0 exposes. gophertunnel has no
// client-to-server block-state packet: UpdateBlock, UpdateSubChunkBlocks and
// BlockActorData are all server-to-client in Bedrock.
//
// That is a real limit, and it is why this package is split the way it is. The
// property tables — the part that had to be right, and the part that was wrong
// in every brief that guessed — are pure functions, exhaustively tested, and
// reusable by whatever writes block state properly. The Writer is the NBT
// path, which is correct for the block-entity families and the only thing
// available today; it cannot set a door's or a stair's orientation, and
// SetFacing refuses those rather than pretending.
//
// It also does not report a facing as applied. The packet went out; whether a
// host honoured it is not observable from here, and the writer does not claim
// otherwise.
package facing

import "fmt"

// Facing is one of the four horizontal directions a block can point.
//
// The zero value is North deliberately: it is a real direction, so a caller
// that forgets to set one gets a valid-but-wrong block rather than a panic.
type Facing int

// The four horizontal directions.
const (
	North Facing = iota
	East
	South
	West
)

// String returns the lower-case name Bedrock uses for this direction in a
// cardinal_direction state. It is the exact string, not a title-cased one:
// "north" is what the server compares against.
func (f Facing) String() string {
	switch f {
	case North:
		return "north"
	case East:
		return "east"
	case South:
		return "south"
	case West:
		return "west"
	default:
		return fmt.Sprintf("Facing(%d)", int(f))
	}
}

// IsCardinal reports whether f is one of the four real directions. It is the
// guard every builder runs before writing a direction onto the wire.
func (f Facing) IsCardinal() bool {
	return f == North || f == East || f == South || f == West
}

// AllFacings returns the four real directions in clockwise order, for
// exhaustive tests and for callers that iterate rather than choose.
func AllFacings() []Facing {
	return []Facing{North, East, South, West}
}

// rotateRight returns the direction a quarter turn clockwise from f.
//
// It is what makes a door's facing differ from a chest's: a door's state is
// its opening direction turned one quarter clockwise, and a chest's is the
// direction itself.
func (f Facing) rotateRight() Facing {
	switch f {
	case North:
		return East
	case East:
		return South
	case South:
		return West
	case West:
		return North
	default:
		return f
	}
}

// Family is a kind of oriented block. It exists so a caller can say "this
// chest" rather than "this chest, which is a block entity, unlike that door".
type Family string

// The oriented block families this package encodes.
const (
	FamilyDoor    Family = "door"
	FamilyChest   Family = "chest"
	FamilyFurnace Family = "furnace"
	FamilyBed     Family = "bed"
	FamilyStairs  Family = "stairs"
	FamilySign    Family = "sign"
)

// AllFamilies returns every family, in a stable order.
func AllFamilies() []Family {
	return []Family{
		FamilyDoor,
		FamilyChest,
		FamilyFurnace,
		FamilyBed,
		FamilyStairs,
		FamilySign,
	}
}

// entityIDs are the block-entity ids Bedrock matches on, from the "id" field
// of each block's NBT.
//
// A door and a stair are absent because they have no block entity at all. This
// map is the single source of truth for that distinction: EntityID and
// IsBlockEntity both read it, so the two cannot disagree.
var entityIDs = map[Family]string{
	FamilyChest:   "Chest",
	FamilyFurnace: "Furnace",
	FamilyBed:     "Bed",
	FamilySign:    "Sign",
}

// EntityID returns the block-entity id Bedrock uses for this family, or the
// empty string for a family that is not a block entity.
func (f Family) EntityID() string {
	return entityIDs[f]
}

// IsBlockEntity reports whether this family has block-entity NBT. Only these
// can be written through packet.BlockActorData.
func (f Family) IsBlockEntity() bool {
	return f.EntityID() != ""
}

// The block-state property keys, verified against the Mojang-canonical palette
// (see the package comment). They are exported as constants so a test can pin
// a literal name and a caller can read one without a stringly-typed guess.
const (
	// KeyCardinalDirection is the modern cardinal property, a lower-case
	// direction string. Used by doors, chests and furnaces.
	KeyCardinalDirection = "minecraft:cardinal_direction"

	// KeyDoorHinge, KeyDoorOpen and KeyDoorUpper are the door's remaining
	// state bits, all bool.
	KeyDoorHinge = "door_hinge_bit"
	KeyDoorOpen  = "open_bit"
	KeyDoorUpper = "upper_block_bit"

	// KeyBedDirection is the bed's int32 direction, on the 0=south,
	// 1=west, 2=north, 3=east scale.
	KeyBedDirection = "direction"

	// KeyBedHeadPiece and KeyBedOccupied are the bed's uint8 bits.
	KeyBedHeadPiece = "head_piece_bit"
	KeyBedOccupied  = "occupied_bit"

	// KeyStairsDirection is the stairs' int32 direction, on the
	// 0=east, 1=west, 2=south, 3=north scale.
	KeyStairsDirection = "weirdo_direction"

	// KeyStairsUpsideDown is the stairs' bool, true when the full side is on
	// top.
	KeyStairsUpsideDown = "upside_down_bit"

	// KeySignFacingDirection is a wall sign's int32, on the six-value
	// cube.Face scale (0=down, 1=up, 2=north, 3=south, 4=west, 5=east).
	KeySignFacingDirection = "facing_direction"

	// KeySignGroundDirection is a standing sign's int32 rotation, 0-15.
	KeySignGroundDirection = "ground_sign_direction"
)

// Cardinal-only options, kept as named types so a caller cannot pass a bed's
// head flag where a door's hinge belongs.

// Hinge is which side of the opening a door's hinge is on.
type Hinge int

// The two door hinges.
const (
	HingeLeft Hinge = iota
	HingeRight
)

// DoorState is whether a door is open.
type DoorState bool

// The two door open states.
const (
	DoorClosed DoorState = false
	DoorOpen   DoorState = true
)

// DoorHalf is which half of the two-block door cell this is.
type DoorHalf int

// The two door halves. A door occupies two blocks and upper_block_bit
// distinguishes them.
const (
	DoorLower DoorHalf = iota
	DoorUpper
)

// BedPart is which half of a bed this is. A bed is two blocks, and
// head_piece_bit is what tells them apart.
type BedPart int

// The two bed halves.
const (
	BedFoot BedPart = iota
	BedHead
)

// StairsFlip is whether a stair is upside down, i.e. the full side is on top.
type StairsFlip int

// The two stair orientations.
const (
	StairsUpright StairsFlip = iota
	StairsUpsideDown
)
