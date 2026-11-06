//go:build fuzz

package zerodecimal

import "testing"

// FuzzMulAddRound checks MulAddRound against the exact big.Rat value of
// a*b + c rounded once, in every mode, for arbitrary 128-bit coefficients,
// signs and precisions: success, the result's representation, and
// ErrOverflow exactly when the rounded coefficient exceeds 128 bits.
func FuzzMulAddRound(f *testing.F) {
	seeds := []struct {
		ahi, alo, bhi, blo, chi, clo uint64
		am, bm, cm                   uint8
		places, mode                 uint8
	}{
		{0, 1_500_000, 0, 5_012_345, 0, 7_518, 6, 2, 2, 2, 1},
		{0, 15, 0, 1, 0, 5, 1, 1, 0x80 | 3, 2, 0},
		{0, 125, 0, 4, 0, 5, 2, 0, 0x80, 2, 1},
		{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0), 0, 1, 0, 0, 19, 0, 3},
		{^uint64(0), ^uint64(0), 0, 2, ^uint64(0), ^uint64(0), 0, 0, 0x80, 0, 5},
		{1 << 63, 0, 0, 0, 0, 1, 19, 19, 19, 19, 4},
	}
	for _, s := range seeds {
		f.Add(s.ahi, s.alo, s.bhi, s.blo, s.chi, s.clo, s.am, s.bm, s.cm, s.places, s.mode)
	}
	f.Fuzz(func(t *testing.T, ahi, alo, bhi, blo, chi, clo uint64, am, bm, cm, pc, mc uint8) {
		a := exactFuzzDecimal(ahi, alo, am)
		b := exactFuzzDecimal(bhi, blo, bm)
		c := exactFuzzDecimal(chi, clo, cm)
		places := pc % (MaxPrec + 1)
		mode := RoundingMode(mc % uint8(TowardNegative+1))
		checkMulAddOracle(t, a, b, c, places, mode)
	})
}
