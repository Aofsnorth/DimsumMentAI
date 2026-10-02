package facing

import "fmt"

// The three int32 direction scales, as tables rather than arithmetic, because
// the scales disagree with each other and a formula hides which one is in play.
//
// Every entry here was printed from dragonfly's EncodeBlock methods and the
// Mojang block_states.nbt palette. See the package comment for the transcript.

// bedDirection maps a world direction to the bed's int32 direction.
//
// The scale is 0=south, 1=west, 2=north, 3=east. It is the legacy Bedrock
// scale, and it is the reason a bed does not share a helper with a chest: the
// same north that is 2 on a bed is 3 on a stair.
var bedDirection = map[Facing]int32{
	South: 0,
	West:  1,
	North: 2,
	East:  3,
}

// stairsDirection maps a world direction to the stairs' int32 weirdo_direction.
//
// The scale is 0=east, 1=west, 2=south, 3=north — a rotation of the bed's.
var stairsDirection = map[Facing]int32{
	East:  0,
	West:  1,
	South: 2,
	North: 3,
}

// wallSignDirection maps a world direction to a wall sign's int32
// facing_direction.
//
// This is the six-value cube.Face scale, not a four-value cardinal one, so a
// wall sign can also point up or down. The four values here are the horizontal
// subset.
var wallSignDirection = map[Facing]int32{
	North: 2,
	South: 3,
	West:  4,
	East:  5,
}

// signRotationSteps is how many rotations a standing sign has. The printed
// palette gives it 0-15, which is a finer scale than any other family here.
const signRotationSteps = 16

// checkFacing is the one validation every cardinal builder runs. A direction
// outside the four is refused before it can be written, because an out-of-range
// value does not fail loudly on the wire — it produces a block the server
// either ignores or misplaces.
func checkFacing(f Facing) error {
	if !f.IsCardinal() {
		return fmt.Errorf("facing: %d is not one of the four cardinal directions", int(f))
	}
	return nil
}

// ChestProperties returns the block-state properties for a chest pointing in f.
//
// The orientation is minecraft:cardinal_direction, a lower-case string, and a
// chest is not rotated: a chest facing north says "north".
//
// The returned map is freshly allocated on every call, so a caller may modify
// it without corrupting anyone else's.
func ChestProperties(f Facing) map[string]any {
	props, err := ChestPropertiesE(f)
	if err != nil {
		// Unreachable for a valid f; the invalid case still returns a
		// well-formed map so the non-erroring wrapper is total.
		return map[string]any{}
	}
	return props
}

// ChestPropertiesE is ChestProperties with the facing validated.
func ChestPropertiesE(f Facing) (map[string]any, error) {
	if err := checkFacing(f); err != nil {
		return nil, err
	}
	return map[string]any{
		KeyCardinalDirection: f.String(),
	}, nil
}

// FurnaceProperties returns the block-state properties for a furnace pointing
// in f. It takes the same string-valued cardinal direction as a chest, from the
// same printed palette.
func FurnaceProperties(f Facing) map[string]any {
	props, err := FurnacePropertiesE(f)
	if err != nil {
		return map[string]any{}
	}
	return props
}

// FurnacePropertiesE is FurnaceProperties with the facing validated.
func FurnacePropertiesE(f Facing) (map[string]any, error) {
	if err := checkFacing(f); err != nil {
		return nil, err
	}
	return map[string]any{
		KeyCardinalDirection: f.String(),
	}, nil
}

// DoorProperties returns the block-state properties for a door that opens
// towards opening.
//
// The rotation is the part that matters: the direction on the wire is
// opening turned a quarter turn clockwise, so a door that opens north carries
// "east". A chest facing north carries "north". Both are printed ground truth
// and they genuinely differ.
//
// hinge, open and half describe the rest of a door's state. All four
// properties are always written: a door sent with only a facing is a door the
// server has to guess the rest of.
func DoorProperties(opening Facing, hinge Hinge, open DoorState, half DoorHalf) map[string]any {
	props, err := DoorPropertiesE(opening, hinge, open, half)
	if err != nil {
		return map[string]any{}
	}
	return props
}

// DoorPropertiesE is DoorProperties with the opening direction validated.
func DoorPropertiesE(opening Facing, hinge Hinge, open DoorState, half DoorHalf) (map[string]any, error) {
	if err := checkFacing(opening); err != nil {
		return nil, err
	}
	return map[string]any{
		KeyCardinalDirection: opening.rotateRight().String(),
		KeyDoorHinge:         hinge == HingeRight,
		KeyDoorOpen:          bool(open),
		KeyDoorUpper:         half == DoorUpper,
	}, nil
}

// BedProperties returns the block-state properties for one half of a bed.
//
// The direction is the bed's own int32 scale (0=south, 1=west, 2=north,
// 3=east), which is not the cardinal order and not the stairs' order. part
// sets head_piece_bit, which is what distinguishes the two blocks of a bed —
// the direction alone does not.
func BedProperties(f Facing, part BedPart) map[string]any {
	props, err := BedPropertiesE(f, part)
	if err != nil {
		return map[string]any{}
	}
	return props
}

// BedPropertiesE is BedProperties with the facing validated.
func BedPropertiesE(f Facing, part BedPart) (map[string]any, error) {
	if err := checkFacing(f); err != nil {
		return nil, err
	}
	var head uint8
	if part == BedHead {
		head = 1
	}
	return map[string]any{
		KeyBedDirection: bedDirection[f],
		KeyBedHeadPiece: head,
		KeyBedOccupied:  uint8(0),
	}, nil
}

// StairsProperties returns the block-state properties for a stair pointing in
// f, upright or upside down.
//
// The direction is weirdo_direction on the 0=east, 1=west, 2=south, 3=north
// scale, which is a rotation of the bed's. The key name is Mojang's, not a
// typo.
func StairsProperties(f Facing, flip StairsFlip) map[string]any {
	props, err := StairsPropertiesE(f, flip)
	if err != nil {
		return map[string]any{}
	}
	return props
}

// StairsPropertiesE is StairsProperties with the facing validated.
func StairsPropertiesE(f Facing, flip StairsFlip) (map[string]any, error) {
	if err := checkFacing(f); err != nil {
		return nil, err
	}
	return map[string]any{
		KeyStairsDirection:  stairsDirection[f],
		KeyStairsUpsideDown: flip == StairsUpsideDown,
	}, nil
}

// WallSignProperties returns the block-state properties for a sign hung on the
// wall of the block in f.
//
// A wall sign uses the six-value cube.Face scale rather than a four-value
// cardinal one, so it can also point up or down. north is 2 here — not 4,
// which is what adding a two-block offset would give.
func WallSignProperties(f Facing) map[string]any {
	props, err := WallSignPropertiesE(f)
	if err != nil {
		return map[string]any{}
	}
	return props
}

// WallSignPropertiesE is WallSignProperties with the facing validated.
func WallSignPropertiesE(f Facing) (map[string]any, error) {
	if err := checkFacing(f); err != nil {
		return nil, err
	}
	return map[string]any{
		KeySignFacingDirection: wallSignDirection[f],
	}, nil
}

// StandingSignProperties returns the block-state properties for a sign planted
// in the ground, rotated by step.
//
// A standing sign has no facing at all: it has a rotation, and it has sixteen
// of them where a wall sign has six. A step outside 0-15 is refused rather than
// written as an impossible sign.
func StandingSignProperties(step int) (map[string]any, error) {
	if step < 0 || step >= signRotationSteps {
		return nil, fmt.Errorf("facing: standing sign rotation %d is outside 0-%d",
			step, signRotationSteps-1)
	}
	return map[string]any{
		KeySignGroundDirection: int32(step),
	}, nil
}
