//go:build fuzz

package zerodecimal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzIsRoundedFracDigits holds IsRounded and FracDigits to the identities
// they document,
//
//	d.IsRounded(p) == d.Equal(d.Truncate(p))   for every p in 0..255
//	d.FracDigits() == d.Trim().Prec()
//
// to each other (d fits p places exactly when it needs at most p), and to
// being properties of the number: widening d with trailing zeros must change
// neither answer. Raw random coefficients almost never end in a decimal zero,
// so every operand is also widened to a fuzzed precision, which is what makes
// the trailing-zero ladder and the divisibility core meet exact multiples.
func FuzzIsRoundedFracDigits(f *testing.F) {
	fuzzPairsPlaces(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8, pc uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		for _, d := range []Decimal{a, b, fuzzProduct(t, a, b)} {
			// Rescale only keeps the value when it raises the precision (a lower
			// one rounds), and raising can overflow; d stays in the list either way.
			forms := []Decimal{d}
			if prec := pc % (MaxPrec + 1); prec >= d.Prec() {
				if wide, err := d.Rescale(prec); err == nil {
					forms = append(forms, wide)
				}
			}
			for _, e := range forms {
				fd := e.FracDigits()
				require.Equalf(t, e.Trim().Prec(), fd, "FracDigits is the trimmed precision: e=%+v", e)
				require.Equalf(t, d.FracDigits(), fd, "FracDigits is a property of the value: d=%+v e=%+v", d, e)
				for _, places := range []uint8{pc, d.Prec(), e.Prec(), fd, 0, MaxPrec, 255} {
					got := e.IsRounded(places)
					require.Equalf(t, e.Equal(e.Truncate(places)), got, "IsRounded matches Truncate: e=%+v places=%d", e, places)
					require.Equalf(t, fd <= places, got, "IsRounded is FracDigits <= places: e=%+v places=%d", e, places)
					require.Equalf(t, d.IsRounded(places), got, "IsRounded is a property of the value: d=%+v e=%+v places=%d", d, e, places)
				}
				if fd > 0 {
					require.Falsef(t, e.IsRounded(fd-1), "one place short of FracDigits is never enough: e=%+v", e)
				}
			}
		}
	})
}
