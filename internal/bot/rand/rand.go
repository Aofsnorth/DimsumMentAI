// Package rand provides non-cryptographic randomness for gameplay.
//
//nolint:gosec // G404: math/rand is intentional for non-cryptographic gameplay jitter.
package rand

import (
	"math/rand"
)

// Float64 returns a float64 in [0.0, 1.0).
func Float64() float64 { return rand.Float64() }

// Intn returns a non-negative int in [0, n).
func Intn(n int) int { return rand.Intn(n) }

// Int returns a non-negative int.
func Int() int { return rand.Int() }

// Int63 returns a non-negative int64.
func Int63() int64 { return rand.Int63() }
