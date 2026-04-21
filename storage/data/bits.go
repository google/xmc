// Package data provides data manipulation functions.
package data

import "math/bits"

// ReverseBits reverses the bits of a 64-bit integer.
func ReverseBits(x int64) int64 {
	return int64(bits.Reverse64(uint64(x)))
}
