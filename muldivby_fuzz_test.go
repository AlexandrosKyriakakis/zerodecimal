//go:build fuzz

package zerodecimal

import (
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzMulDivRoundBy holds d.MulDivRoundBy(e, &v, places, mode) to
// d.MulDivRound(e, f, places, mode) bit for bit, for v = NewDivisor(f): the
// same representation and the same sentinel, including the zero Divisor that
// NewDivisor returns for a zero f and a nil *Divisor, which must fail like a
// zero f behind the same validation order. places reduces into 0..20 (20 is
// out of range) and mode into 0..7 (6 and 7 are invalid), so the validation
// order is fuzzed with the arithmetic. FuzzMulDivRound's big.Rat oracle
// proves the reference results. Seeds are the corpus pairs with a rotating
// third operand (fuzzTriples' scheme) and a fixed-seed scale and mode.
func FuzzMulDivRoundBy(f *testing.F) {
	rng := rand.New(rand.NewPCG(0x6d756c64, 0x5eed))
	for i, a := range fuzzCorpus {
		for j, b := range fuzzCorpus {
			c := fuzzCorpus[(i+j)%len(fuzzCorpus)]
			f.Add(a.neg, a.hi, a.lo, a.prec, b.neg, b.hi, b.lo, b.prec, c.neg, c.hi, c.lo, c.prec,
				uint8(rng.Uint64N(uint64(MaxPrec)+2)), uint8(rng.Uint64N(8)))
		}
	}
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8,
		cneg bool, chi, clo uint64, cprec uint8, pc, mc uint8,
	) {
		d, e := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		c := fuzzDecimal(t, cneg, chi, clo, cprec)
		places := pc % (MaxPrec + 2)
		mode := RoundingMode(mc % 8)

		v, err := NewDivisor(c)
		if c.IsZero() {
			require.ErrorIsf(t, err, ErrDivideByZero, "c=%+v", c)
		} else {
			require.NoErrorf(t, err, "c=%+v", c)
		}
		want, wantErr := d.MulDivRound(e, c, places, mode)
		got, err := d.MulDivRoundBy(e, &v, places, mode)
		require.Truef(t, errors.Is(err, wantErr), "MulDivRoundBy error: d=%+v e=%+v c=%+v places=%d mode=%d got %v want %v",
			d, e, c, places, mode, err, wantErr)
		require.Equalf(t, want, got, "MulDivRoundBy: d=%+v e=%+v c=%+v places=%d mode=%d", d, e, c, places, mode)

		want, wantErr = d.MulDivRound(e, Decimal{}, places, mode)
		got, err = d.MulDivRoundBy(e, nil, places, mode)
		require.Truef(t, errors.Is(err, wantErr), "nil Divisor: d=%+v e=%+v places=%d mode=%d got %v want %v",
			d, e, places, mode, err, wantErr)
		require.Equal(t, want, got)
	})
}
