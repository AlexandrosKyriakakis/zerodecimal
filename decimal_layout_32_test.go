//go:build 386 || arm || mips || mipsle

package zerodecimal

import "unsafe"

// Compile-time layout guards for gc's supported 32-bit, max-align-4 ports.
// Both differences must be valid array lengths, which proves equality rather
// than merely an upper or lower bound even when tests are only cross-compiled.
var (
	_ [unsafe.Sizeof(Decimal{}) - 20]byte
	_ [20 - unsafe.Sizeof(Decimal{})]byte
	_ [unsafe.Alignof(Decimal{}) - 4]byte
	_ [4 - unsafe.Alignof(Decimal{})]byte
)
