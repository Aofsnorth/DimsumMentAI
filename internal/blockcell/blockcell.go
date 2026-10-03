// blockcell converts a world position into the block cell that contains it.
//
// The conversion looks like it should not need a package. It needs one because
// the obvious one-liner is wrong, and because the wrong version compiles.
//
// Go converts float to integer by truncating toward zero. A Bedrock entity
// stands at the centre of the cell it occupies, which is always `n + 0.5`. For
// a positive coordinate that lands on the right side of the truncation and the
// one-liner appears to work. For a negative coordinate it lands on the wrong
// side: the centre of cell -1 is -0.5, and `int32(-0.5)` is 0, which is the
// cell to the right of the one the entity is standing in.
//
// The bug is invisible in every test that uses positive coordinates and present
// in every test that uses negative ones, which is why it survived a codebase
// that already spelled the conversion correctly in 255 other places. Four call
// sites had it wrong, and each of them was wrong in a way that made the bot
// misread the world rather than crash: the chest it is standing next to is not
// the chest it sees, the sub-chunk row it asks the server for is the row above
// the one it is in, and the body clearance it publishes is written one cell
// east of the body it is describing.
//
// A named function makes the correct conversion the default rather than
// something each call site has to remember, and gives the convention one place
// to be tested.
package blockcell

import "math"

// Of returns the cell on one axis that contains v.
//
// Floor, not truncation. Cell n spans [n, n+1), and the entity coordinate that
// belongs to cell n is n + 0.5 — negative on the left half of the world. Floor
// is the operation that maps both halves of the axis onto the cell they are
// inside.
func Of(v float32) int32 {
	return int32(math.Floor(float64(v)))
}

// XYZ returns the cell that contains the point (x, y, z).
//
// Returned as three values rather than a slice so a caller cannot index one
// axis out of alignment with another: a three-element array invites
// `cell[0], cell[1], cell[2]` in the wrong order at least once.
func XYZ(x, y, z float32) (cx, cy, cz int32) {
	return Of(x), Of(y), Of(z)
}
