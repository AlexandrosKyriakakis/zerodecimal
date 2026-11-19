//go:build 386 || arm || mips || mipsle

package zerodecimal

import "unsafe"

// Compile-time guards for the size Accumulator's doc comment states on gc's
// 32-bit, max-align-4 ports: both differences must be valid array lengths,
// which proves equality even when tests are only cross-compiled.
var (
	_ [unsafe.Sizeof(Accumulator{}) - 68]byte
	_ [68 - unsafe.Sizeof(Accumulator{})]byte
)
