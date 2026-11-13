//go:build fuzz

package zerodecimal

import (
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzDivisor holds the Divisor API to the Decimal-argument operations it
// reproduces: d.DivBy(&v) against d.Div(e) and d.DivRoundBy(&v, places, mode)
// against d.DivRound(e, places, mode), bit for bit, plus NewDivisor's
// zero-divisor contract, the nil Divisor, and the Must-twin contract for
// MustNewDivisor and MustDivBy. places reduces into 0..20 (20 is out of
// range) and mode into 0..7 (6 and 7 are invalid), so the validation order is
// fuzzed with the arithmetic. Div's own oracle (FuzzDiv) and DivRound's
// (FuzzDirectRoundedArithmetic) prove the reference results.
func FuzzDivisor(f *testing.F) {
	rng := rand.New(rand.NewPCG(0xd1715012, 0x5eed))
	for _, a := range fuzzCorpus {
		for _, b := range fuzzCorpus {
			f.Add(a.neg, a.hi, a.lo, a.prec, b.neg, b.hi, b.lo, b.prec, uint8(rng.Uint64N(21)), uint8(rng.Uint64N(8)))
		}
	}
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8, pc, mc uint8) {
		d, e := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		places := pc % (MaxPrec + 2)
		mode := RoundingMode(mc % 8)

		v, err := NewDivisor(e)
		var mv Divisor
		pv := fuzzPanicValue(func() { mv = MustNewDivisor(e) })
		requireTwin(t, "must_new_divisor", err, pv, e)
		if e.IsZero() {
			require.ErrorIsf(t, err, ErrDivideByZero, "e=%+v", e)
			require.Equal(t, Divisor{}, v)
		} else {
			require.NoErrorf(t, err, "e=%+v", e)
			require.Equal(t, v, mv)
			require.Equal(t, e, v.Decimal())
		}

		want, wantErr := d.Div(e)
		got, err := d.DivBy(&v)
		require.Truef(t, errors.Is(err, wantErr), "DivBy error: d=%+v e=%+v got %v want %v", d, e, err, wantErr)
		require.Equalf(t, want, got, "DivBy: d=%+v e=%+v", d, e)
		var mgot Decimal
		pv = fuzzPanicValue(func() { mgot = d.MustDivBy(&v) })
		requireTwin(t, "must_div_by", wantErr, pv, d, e)
		if wantErr == nil {
			require.Equalf(t, want, mgot, "MustDivBy: d=%+v e=%+v", d, e)
		}

		want, wantErr = d.DivRound(e, places, mode)
		got, err = d.DivRoundBy(&v, places, mode)
		require.Truef(t, errors.Is(err, wantErr), "DivRoundBy error: d=%+v e=%+v places=%d mode=%d got %v want %v", d, e, places, mode, err, wantErr)
		require.Equalf(t, want, got, "DivRoundBy: d=%+v e=%+v places=%d mode=%d", d, e, places, mode)

		// A nil Divisor is invalid like the zero one, behind the same
		// validation order.
		got, err = d.DivBy(nil)
		require.ErrorIs(t, err, ErrDivideByZero)
		require.Equal(t, Decimal{}, got)
		want, wantErr = d.DivRound(Decimal{}, places, mode)
		got, err = d.DivRoundBy(nil, places, mode)
		require.ErrorIs(t, err, wantErr)
		require.Equal(t, want, got)
	})
}
