// Package safecast provides bounded integer conversions that prevent gosec G115
// (integer overflow) warnings. It intentionally clamps values to the target
// type's representable range, which is the safe behavior for protocol sizes
// and indices in this project.
//
//nolint:gosec
package safecast

import (
	"math"
	"reflect"
)

// Integer is a constraint that matches all built-in integer types.
type Integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

type normalised struct {
	i      int64
	u      uint64
	signed bool
}

// To converts the integer value v from type S to type T, clamping the result
// to the range that T can represent. Negative source values are clamped to
// zero when the target type is unsigned, and values larger than the target
// maximum are clamped to the target maximum.
//
// The target is dispatched on reflect.Kind rather than on the dynamic type. The
// Integer constraint admits named types — it is written with tildes — so
// `To[int32](MyInt32(7))` is a call this package is required to handle. A
// type switch matches the exact dynamic type, so it saw `main.MyInt32`, matched
// no case, and returned the zero value: seven became zero. Every named source
// was silently converted to 0, and every out-of-range named source skipped the
// clamping that is the entire reason this package exists. Kind is what both a
// built-in type and a named type over it report, so it is the question that
// distinguishes "a 32-bit integer" from "the 32-bit integer type called
// int32".
func To[T, S Integer](v S) T {
	n := normalise(v)
	switch reflect.TypeOf(*new(T)).Kind() {
	case reflect.Int:
		return T(toInt(n))
	case reflect.Int8:
		return T(toInt8(n))
	case reflect.Int16:
		return T(toInt16(n))
	case reflect.Int32:
		return T(toInt32(n))
	case reflect.Int64:
		return T(toInt64(n))
	case reflect.Uint:
		return T(toUint(n))
	case reflect.Uint8:
		return T(toUint8(n))
	case reflect.Uint16:
		return T(toUint16(n))
	case reflect.Uint32:
		return T(toUint32(n))
	case reflect.Uint64:
		return T(toUint64(n))
	case reflect.Uintptr:
		return T(toUintptr(n))
	}
	// Unreachable while T satisfies Integer, which the constraint enforces. The
	// fallback is the raw conversion, so if the constraint ever widens, the
	// result is unchecked rather than silently zero.
	return T(v)
}

// normalise widens any integer to a form the clamping helpers can work on.
//
// It dispatches on reflect.Kind for the same reason To does. A type switch on
// the dynamic type sees `main.MyInt32` rather than `int32`, matches nothing, and
// returns the zero normalised value — which To then reports as a legitimate 0.
func normalise[S Integer](v S) normalised {
	switch x := reflect.ValueOf(v); x.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return normalised{i: x.Int(), signed: true}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return normalised{u: x.Uint()}
	}
	return normalised{}
}

func clampInt64(i int64, min, max int64) int64 {
	if i < min {
		return min
	}
	if i > max {
		return max
	}
	return i
}

func clampUint64(u uint64, max uint64) uint64 {
	if u > max {
		return max
	}
	return u
}

func toInt(n normalised) int {
	if n.signed {
		return int(clampInt64(n.i, math.MinInt, math.MaxInt))
	}
	return int(clampUint64(n.u, math.MaxUint))
}

func toInt8(n normalised) int8 {
	if n.signed {
		return int8(clampInt64(n.i, math.MinInt8, math.MaxInt8))
	}
	return int8(clampUint64(n.u, math.MaxInt8))
}

func toInt16(n normalised) int16 {
	if n.signed {
		return int16(clampInt64(n.i, math.MinInt16, math.MaxInt16))
	}
	return int16(clampUint64(n.u, math.MaxInt16))
}

func toInt32(n normalised) int32 {
	if n.signed {
		return int32(clampInt64(n.i, math.MinInt32, math.MaxInt32))
	}
	return int32(clampUint64(n.u, math.MaxInt32))
}

func toInt64(n normalised) int64 {
	if n.signed {
		return n.i
	}
	if n.u > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(n.u)
}

func toUint(n normalised) uint {
	var c uint64
	if n.signed {
		if n.i < 0 {
			return 0
		}
		c = uint64(n.i)
	} else {
		c = n.u
	}
	if c > uint64(math.MaxUint) {
		return uint(math.MaxUint)
	}
	return uint(c)
}

func toUint8(n normalised) uint8 {
	if n.signed {
		if n.i < 0 {
			return 0
		}
		return uint8(clampInt64(n.i, 0, math.MaxUint8))
	}
	return uint8(clampUint64(n.u, math.MaxUint8))
}

func toUint16(n normalised) uint16 {
	if n.signed {
		if n.i < 0 {
			return 0
		}
		return uint16(clampInt64(n.i, 0, math.MaxUint16))
	}
	return uint16(clampUint64(n.u, math.MaxUint16))
}

func toUint32(n normalised) uint32 {
	if n.signed {
		if n.i < 0 {
			return 0
		}
		return uint32(clampInt64(n.i, 0, math.MaxUint32))
	}
	return uint32(clampUint64(n.u, math.MaxUint32))
}

func toUint64(n normalised) uint64 {
	if n.signed {
		if n.i < 0 {
			return 0
		}
		return uint64(n.i)
	}
	return n.u
}

func toUintptr(n normalised) uintptr {
	var c uint64
	if n.signed {
		if n.i < 0 {
			return 0
		}
		c = uint64(n.i)
	} else {
		c = n.u
	}
	if c > uint64(math.MaxUint) {
		return uintptr(math.MaxUint)
	}
	return uintptr(c)
}
