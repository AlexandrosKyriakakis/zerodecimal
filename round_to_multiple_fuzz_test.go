//go:build fuzz

package zerodecimal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzRoundToMultiple checks RoundToMultiple against the exact big.Rat
// quotient d/|step| rounded once, in every mode, for arbitrary 128-bit
// coefficients, signs and precisions of both operands: success, the result's
// representation, ErrDivideByZero, and ErrOverflow exactly when the multiple
// does not fit at step's precision. The mode byte also reaches the invalid
// modes 6 and 7, which must return ErrInvalidRoundingMode.
func FuzzRoundToMultiple(f *testing.F) {
	seeds := []struct {
		dhi, dlo, shi, slo uint64
		dm, sm, mode       uint8
	}{
		{0, 10_137, 0, 25, 2, 2, 1},              // a 0.25 tick
		{0, 333_333_333, 0, 1, 7, 3, 3},          // a 0.001-share rule
		{0, 125, 0, 5, 1, 0, 1},                  // 12.5 to 5: a tie
		{0, 73, 0, 5, 0x80 | 1, 0x80, 4},         // -7.3 to -5 toward +inf
		{0, 1, 1 << 63, 0, 19, 0, 2},             // 10^-19 to 2^127
		{^uint64(0), ^uint64(0), 0, 1, 0, 19, 3}, // a quotient past 2^128
		{^uint64(0), ^uint64(0), 0, 2, 0, 0, 0},  // a tie rounding into overflow
		{^uint64(0), ^uint64(0), 1, 0, 0, 0, 1},  // a two-limb step
		{0, 1, 0, 0, 19, 0, 2},                   // a zero step
		{0, 0, 0, 25, 0, 2, 5},                   // a zero d
		{0, 15, 0, 25, 1, 2, 6},                  // an invalid mode
	}
	for _, s := range seeds {
		f.Add(s.dhi, s.dlo, s.shi, s.slo, s.dm, s.sm, s.mode)
	}
	f.Fuzz(func(t *testing.T, dhi, dlo, shi, slo uint64, dm, sm, mc uint8) {
		d := exactFuzzDecimal(dhi, dlo, dm)
		step := exactFuzzDecimal(shi, slo, sm)
		mode := RoundingMode(mc & 7)
		if !mode.valid() {
			got, err := d.RoundToMultiple(step, mode)
			require.Equal(t, ErrInvalidRoundingMode, err)
			require.Equal(t, Decimal{}, got)
			return
		}
		checkRoundToMultipleOracle(t, d, step, mode)
	})
}
