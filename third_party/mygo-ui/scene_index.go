package ui

import "math"

// Scene buffers use signed 32-bit offsets. Reject oversized frames before narrowing an index.
func sceneIndex(n int) int32 {
	if n < 0 || n > math.MaxInt32 {
		panic("mygo: native scene exceeds its index range")
	}
	return int32(n)
}
