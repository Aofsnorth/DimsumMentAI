// Package safecast provides bounded integer conversions that prevent gosec G115
// (integer overflow) warnings. It intentionally clamps values to the target
// type's representable range, which is the safe behavior for protocol sizes
// and indices in this project.
//
//nolint:gosec
package safecast

import (
	"math"
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
func To[T, S Integer](v S) T {
	n := normalise(v)
	switch any(*new(T)).(type) {
	case int:
		return T(toInt(n))
	case int8:
		return T(toInt8(n))
	case int16:
		return T(toInt16(n))
	case int32:
		return T(toInt32(n))
	case int64:
		return T(toInt64(n))
	case uint:
		return T(toUint(n))
	case uint8:
		return T(toUint8(n))
	case uint16:
		return T(toUint16(n))
	case uint32:
		return T(toUint32(n))
	case uint64:
		return T(toUint64(n))
	case uintptr:
		return T(toUintptr(n))
	}
	return T(v)
}

func normalise[S Integer](v S) normalised {
	switch x := any(v).(type) {
	case int:
		return normalised{i: int64(x), signed: true}
	case int8:
		return normalised{i: int64(x), signed: true}
	case int16:
		return normalised{i: int64(x), signed: true}
	case int32:
		return normalised{i: int64(x), signed: true}
	case int64:
		return normalised{i: x, signed: true}
	case uint:
		return normalised{u: uint64(x)}
	case uint8:
		return normalised{u: uint64(x)}
	case uint16:
		return normalised{u: uint64(x)}
	case uint32:
		return normalised{u: uint64(x)}
	case uint64:
		return normalised{u: x}
	case uintptr:
		return normalised{u: uint64(x)}
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
