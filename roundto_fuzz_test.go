//go:build fuzz

package zerodecimal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzRoundTo checks RoundTo over arbitrary values, places and mode bytes. The
// mode is fuzzed over the whole uint8 range, so ErrInvalidRoundingMode is
// reached by mutation as well as by the seeds, and places likewise ranges past
// MaxPrec. Every call must obey the twin contract; a valid mode must equal the
// named method of that mode representation for representation, agree with the
// exact big.Rat value, and carry the shape every rounding shares; an invalid
// mode must fail with the zero Decimal, even where places ≥ d.Prec().
func FuzzRoundTo(f *testing.F) {
	seedPlaces := [...]uint8{0, 1, 2, 9, 18, 19, 20, 255}
	for i, s := range fuzzCorpus {
		for mode := range uint8(8) { // 0..5 are the six modes, 6 and 7 are invalid
			f.Add(s.neg, s.hi, s.lo, s.prec, seedPlaces[(i+int(mode))%len(seedPlaces)], mode)
		}
	}
	f.Add(false, uint64(0), uint64(5), uint8(1), uint8(0), uint8(255))
	f.Add(true, uint64(0), uint64(15), uint8(1), uint8(0), uint8(255))

	f.Fuzz(func(t *testing.T, neg bool, hi, lo uint64, prec, places, mc uint8) {
		d := fuzzDecimal(t, neg, hi, lo, prec)
		mode := RoundingMode(mc)

		got, err := d.RoundTo(places, mode)
		var must Decimal
		pv := fuzzPanicValue(func() { must = d.MustRoundTo(places, mode) })
		requireTwin(t, "round_to", err, pv, d, places, mode)

		if !mode.valid() {
			require.ErrorIsf(t, err, ErrInvalidRoundingMode, "invalid mode: d=%+v places=%d mode=%d", d, places, mode)
			require.Equalf(t, Decimal{}, got, "invalid mode must return the zero Decimal: d=%+v places=%d mode=%d", d, places, mode)
			return
		}
		require.NoErrorf(t, err, "valid mode must not fail: d=%+v places=%d mode=%d", d, places, mode)
		require.Equalf(t, got, must, "must twin result: d=%+v places=%d mode=%d", d, places, mode)

		for _, m := range roundToNamedMethods {
			if m.mode != mode {
				continue
			}
			require.Equalf(t, m.apply(d, places), got, "RoundTo must equal %s: d=%+v places=%d", m.method, d, places)
			want := roundModeOracle(d, places, m.oracle)
			require.Zerof(t, want.Cmp(decimalToRat(got)), "RoundTo vs exact %s value %s: d=%+v places=%d got=%+v", m.oracle, want, d, places, got)
		}
		checkRoundToShape(t, d, got, places, "RoundTo")
	})
}

// appendRoundToResults is FuzzInvariants' RoundTo leg: for every mode RoundTo
// must succeed and equal the named method of that mode, and each result joins
// results so the structural checks (canonical zero, precision bound, canonical
// string fixed point) cover RoundTo's output too.
func appendRoundToResults(t *testing.T, results []Decimal, d Decimal, places uint8) []Decimal {
	t.Helper()
	for _, m := range roundToNamedMethods {
		got, err := d.RoundTo(places, m.mode)
		require.NoErrorf(t, err, "RoundTo must accept %s's mode: d=%+v places=%d", m.method, d, places)
		require.Equalf(t, m.apply(d, places), got, "RoundTo must equal %s: d=%+v places=%d", m.method, d, places)
		results = append(results, got)
	}
	return results
}

// requireRoundToTwin is FuzzMustTwins' RoundTo leg: MustRoundTo must panic
// exactly when RoundTo errors, carry its sentinel, and otherwise return the
// identical value, for the six modes and two invalid ones.
func requireRoundToTwin(t *testing.T, d Decimal, places uint8) {
	t.Helper()
	for _, mode := range [...]RoundingMode{
		ToNearestAway, ToNearestEven, AwayFromZero, TowardZero, TowardPositive, TowardNegative,
		TowardNegative + 1, ^RoundingMode(0),
	} {
		want, wantErr := d.RoundTo(places, mode)
		var got Decimal
		pv := fuzzPanicValue(func() { got = d.MustRoundTo(places, mode) })
		requireTwin(t, "round_to", wantErr, pv, d, places, mode)
		if wantErr == nil {
			require.Equalf(t, want, got, "round_to: must twin result: d=%+v places=%d mode=%d", d, places, mode)
		}
	}
}
