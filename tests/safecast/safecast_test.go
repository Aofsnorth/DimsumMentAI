// The package's own type constraint admits named types.
//
// Integer is written with tildes — `~int32`, not `int32` — which is a promise
// that `To[int32](MyInt32(7))` is a call this package handles. It did not, and
// the failure was the quietest kind available: a type switch on the dynamic type
// sees `safecast_test.MyInt32`, matches no case, falls out of the switch, and
// hands back the zero value. Seven became zero. A negative named source
// converted to an unsigned target became zero rather than clamping to zero by
// accident. An out-of-range named source skipped clamping entirely and wrapped,
// which is the single failure this package was written to make impossible.
//
// Nothing in the tree passed a named type, so no production behaviour was
// wrong. That is why it survived: the bug was one line of contract away from
// being live, and the only evidence it existed was reading the constraint and
// noticing it said something the code did not do.

package safecast_test

import (
	"math"
	"testing"

	"bedrock-ai/internal/safecast"
)

// Named types over each built-in width, and both signs. Signedness is the part
// that matters most: it is what decides whether a negative value clamps to zero
// or wraps to a huge one.
type (
	nInt   int
	nInt8  int8
	nInt16 int16
	nInt32 int32
	nInt64 int64
	nUint  uint
	nU8    uint8
	nU16   uint16
	nU32   uint32
	nU64   uint64
	nPtr   uintptr
)

// TestNamedTypesConvertRatherThanVanish is the regression. Each case is an
// in-range value that must survive the conversion unchanged.
func TestNamedTypesConvertRatherThanVanish(t *testing.T) {
	t.Parallel()

	if got := safecast.To[int32](nInt32(7)); got != 7 {
		t.Errorf("To[int32](nInt32(7)) = %d, want 7: a named type converted to zero", got)
	}
	if got := safecast.To[int](nInt(-3)); got != -3 {
		t.Errorf("To[int](nInt(-3)) = %d, want -3", got)
	}
	if got := safecast.To[int8](nInt8(-8)); got != -8 {
		t.Errorf("To[int8](nInt8(-8)) = %d, want -8", got)
	}
	if got := safecast.To[int16](nInt16(-16)); got != -16 {
		t.Errorf("To[int16](nInt16(-16)) = %d, want -16", got)
	}
	if got := safecast.To[int64](nInt64(-64)); got != -64 {
		t.Errorf("To[int64](nInt64(-64)) = %d, want -64", got)
	}
	if got := safecast.To[uint](nUint(1)); got != 1 {
		t.Errorf("To[uint](nUint(1)) = %d, want 1", got)
	}
	if got := safecast.To[uint8](nU8(255)); got != 255 {
		t.Errorf("To[uint8](nU8(255)) = %d, want 255", got)
	}
	if got := safecast.To[uint16](nU16(65535)); got != 65535 {
		t.Errorf("To[uint16](nU16(65535)) = %d, want 65535", got)
	}
	if got := safecast.To[uint32](nU32(1 << 20)); got != 1<<20 {
		t.Errorf("To[uint32](nU32(1<<20)) = %d, want %d", got, 1<<20)
	}
	if got := safecast.To[uint64](nU64(1 << 40)); got != 1<<40 {
		t.Errorf("To[uint64](nU64(1<<40)) = %d, want %d", got, 1<<40)
	}
	if got := safecast.To[uintptr](nPtr(99)); got != 99 {
		t.Errorf("To[uintptr](nPtr(99)) = %d, want 99", got)
	}
}

// TestNamedTypesClampLikeBuiltIns is the other half. The package's reason for
// existing is the clamp, so a named source has to get one.
func TestNamedTypesClampLikeBuiltIns(t *testing.T) {
	t.Parallel()

	// Overflow wraps if it is not clamped, which is the failure being guarded.
	if got := safecast.To[uint8](nUint(300)); got != 255 {
		t.Errorf("To[uint8](nUint(300)) = %d, want 255: an unsigned overflow wrapped "+
			"instead of saturating", got)
	}
	if got := safecast.To[int8](nInt32(70000)); got != math.MaxInt8 {
		t.Errorf("To[int8](nInt32(70000)) = %d, want %d: an unsigned overflow wrapped "+
			"instead of saturating", got, int8(math.MaxInt8))
	}
	if got := safecast.To[int16](nInt32(70000)); got != math.MaxInt16 {
		t.Errorf("To[int16](nInt32(70000)) = %d, want %d", got, int16(math.MaxInt16))
	}

	// A negative source on an unsigned target clamps to zero rather than
	// wrapping to a value near the top of the range — which is the difference
	// between "no offset" and "far outside the world".
	if got := safecast.To[uint32](nInt32(-5)); got != 0 {
		t.Errorf("To[uint32](nInt32(-5)) = %d, want 0: a negative offset became a huge "+
			"coordinate rather than clamping to zero", got)
	}
	if got := safecast.To[uint8](nInt32(-1)); got != 0 {
		t.Errorf("To[uint8](nInt32(-1)) = %d, want 0", got)
	}
	if got := safecast.To[uint](nInt(-1)); got != 0 {
		t.Errorf("To[uint](nInt(-1)) = %d, want 0", got)
	}
}

// TestNamedAndBuiltInSourcesAgree is the property the fix rests on. A named type
// over int32 and int32 itself are the same width with the same range, so they
// must produce the same answer for every value — which is what "the constraint
// admits named types" means in practice.
func TestNamedAndBuiltInSourcesAgree(t *testing.T) {
	t.Parallel()

	values := []int32{0, 1, -1, 127, -128, 32767, -32768, 1 << 30, -(1 << 30)}
	targets := []struct {
		name string
		got  func(nInt32) int64
		want func(int32) int64
	}{
		{"int", func(v nInt32) int64 { return int64(safecast.To[int](v)) }, func(v int32) int64 { return int64(safecast.To[int](v)) }},
		{"int8", func(v nInt32) int64 { return int64(safecast.To[int8](v)) }, func(v int32) int64 { return int64(safecast.To[int8](v)) }},
		{"int16", func(v nInt32) int64 { return int64(safecast.To[int16](v)) }, func(v int32) int64 { return int64(safecast.To[int16](v)) }},
		{"int32", func(v nInt32) int64 { return int64(safecast.To[int32](v)) }, func(v int32) int64 { return int64(safecast.To[int32](v)) }},
		{"int64", func(v nInt32) int64 { return int64(safecast.To[int64](v)) }, func(v int32) int64 { return int64(safecast.To[int64](v)) }},
		{"uint", func(v nInt32) int64 { return int64(safecast.To[uint](v)) }, func(v int32) int64 { return int64(safecast.To[uint](v)) }},
		{"uint8", func(v nInt32) int64 { return int64(safecast.To[uint8](v)) }, func(v int32) int64 { return int64(safecast.To[uint8](v)) }},
		{"uint16", func(v nInt32) int64 { return int64(safecast.To[uint16](v)) }, func(v int32) int64 { return int64(safecast.To[uint16](v)) }},
		{"uint32", func(v nInt32) int64 { return int64(safecast.To[uint32](v)) }, func(v int32) int64 { return int64(safecast.To[uint32](v)) }},
		{"uint64", func(v nInt32) int64 { return int64(safecast.To[uint64](v)) }, func(v int32) int64 { return int64(safecast.To[uint64](v)) }},
	}

	for _, tgt := range targets {
		for _, v := range values {
			if got, want := tgt.got(nInt32(v)), tgt.want(v); got != want {
				t.Errorf("To[%s](nInt32(%d)) = %d but To[%s](int32(%d)) = %d: a named "+
					"type must convert exactly as the built-in it is defined over",
					tgt.name, v, got, tgt.name, v, want)
			}
		}
	}
}
