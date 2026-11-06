//go:build fuzz

package zerodecimal

import "testing"

// FuzzMulDivRound checks MulDivRound against the exact big.Rat value of a*b/c
// rounded once, in every mode, for arbitrary 128-bit coefficients, signs and
// precisions: success, the result's representation, ErrDivideByZero, and
// ErrOverflow exactly when the rounded coefficient exceeds 128 bits.
func FuzzMulDivRound(f *testing.F) {
	seeds := []struct {
		ahi, alo, bhi, blo, chi, clo uint64
		am, bm, cm                   uint8
		places, mode                 uint8
	}{
		{0, 12_665, 0, 763_190, 0, 2_544_930, 2, 6, 6, 2, 1},
		{0, 9, 0, 1, 0, 6, 2, 0, 0, 2, 1},
		{0, 1, 0, 1, 1 << 40, 0, 19, 19, 0, 0, 2},
		{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0), 0, 0, 0, 19, 3},
		{0, 7, 0, 3, 0, 0, 0x80 | 5, 1, 0, 2, 0},
		{1 << 63, 0, 0, 3, 0, 5, 19, 19, 0x80, 0, 5},
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
		checkMulDivOracle(t, a, b, c, places, mode)
	})
}
