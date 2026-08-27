package zerodecimal

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// requireQuoRemBigOracle checks the complete public QuoRem/Mod contract
// against an unbounded integer model and shopspring's independent decimal
// implementation. In particular, a common-scale divisor wider than 128 bits
// is legal: it proves a zero quotient whenever the dividend coefficient is
// the already-aligned operand.
func requireQuoRemBigOracle(t *testing.T, d, e Decimal) {
	t.Helper()
	require.False(t, e.IsZero(), "oracle requires a nonzero divisor")

	f := max(d.prec, e.prec)
	num := new(big.Int).Mul(u128ToBig(d.coef), pow10Big(int(f-d.prec)))
	den := new(big.Int).Mul(u128ToBig(e.coef), pow10Big(int(f-e.prec)))
	wantQCoef, wantRCoef := new(big.Int).QuoRem(num, den, new(big.Int))

	gotQ, gotR, err := d.QuoRem(e)
	gotM, modErr := d.Mod(e)
	if wantQCoef.BitLen() > 128 {
		require.ErrorIs(t, err, ErrOverflow, "QuoRem quotient overflow: d=%+v e=%+v", d, e)
		require.ErrorIs(t, modErr, ErrOverflow, "Mod quotient overflow: d=%+v e=%+v", d, e)
		require.Equal(t, Decimal{}, gotQ)
		require.Equal(t, Decimal{}, gotR)
		require.Equal(t, Decimal{}, gotM)
		return
	}

	require.NoError(t, err, "QuoRem: d=%+v e=%+v", d, e)
	require.NoError(t, modErr, "Mod: d=%+v e=%+v", d, e)
	wantQ := newDecimal(bigToU128(t, wantQCoef), d.neg != e.neg, 0)
	wantR := newDecimal(bigToU128(t, wantRCoef), d.neg, f)
	require.Equal(t, wantQ, gotQ, "quotient representation: d=%+v e=%+v", d, e)
	require.Equal(t, wantR, gotR, "remainder representation: d=%+v e=%+v", d, e)
	require.Equal(t, gotR, gotM, "Mod must equal QuoRem remainder: d=%+v e=%+v", d, e)

	ssD, ssE := ssOf(d), ssOf(e)
	ssQ, ssR := ssD.QuoRem(ssE, 0)
	requireSameValue(t, ssQ, gotQ, "shopspring quotient", d, e)
	requireSameValue(t, ssR, gotR, "shopspring remainder", d, e)
	require.True(t, ssD.Equal(ssOf(gotQ).Mul(ssE).Add(ssOf(gotR))),
		"identity d = q*e + r: d=%+v e=%+v q=%+v r=%+v", d, e, gotQ, gotR)
	require.True(t, ssOf(gotR).Abs().LessThan(ssE.Abs()),
		"remainder bound |r| < |e|: d=%+v e=%+v r=%+v", d, e, gotR)
}

func TestQuoRemWideAlignedDivisorAllSignsAndPrecisions(t *testing.T) {
	maxCoef := u128{hi: ^uint64(0), lo: ^uint64(0)}
	tests := []struct {
		name  string
		dCoef u128
		dPrec uint8
		eCoef u128
		ePrec uint8
	}{
		{
			name:  "one_digit_alignment",
			dCoef: maxCoef, dPrec: 7,
			eCoef: u128{hi: 1 << 63}, ePrec: 6,
		},
		{
			name:  "middle_precision_alignment",
			dCoef: u128{hi: 1, lo: 7}, dPrec: 14,
			eCoef: maxCoef, ePrec: 5,
		},
		{
			name:  "full_precision_alignment",
			dCoef: u128{lo: 1}, dPrec: MaxPrec,
			eCoef: maxCoef, ePrec: 0,
		},
		{
			name:  "nonzero_divisor_precision",
			dCoef: u128{hi: 1 << 63, lo: 17}, dPrec: MaxPrec,
			eCoef: u128{hi: 1 << 63}, ePrec: MaxPrec - 1,
		},
	}

	for _, tc := range tests {
		for _, dNeg := range []bool{false, true} {
			for _, eNeg := range []bool{false, true} {
				name := fmt.Sprintf("%s/dneg=%t/eneg=%t", tc.name, dNeg, eNeg)
				t.Run(name, func(t *testing.T) {
					d := newDecimal(tc.dCoef, dNeg, tc.dPrec)
					e := newDecimal(tc.eCoef, eNeg, tc.ePrec)
					requireQuoRemBigOracle(t, d, e)

					q, r, err := d.QuoRem(e)
					require.NoError(t, err)
					require.Equal(t, Decimal{}, q, "zero quotient must be canonical")
					require.Equal(t, d, r, "remainder must preserve the dividend representation")
					m, err := d.Mod(e)
					require.NoError(t, err)
					require.Equal(t, d, m)

					mustQ, mustR := d.MustQuoRem(e)
					require.Equal(t, q, mustQ)
					require.Equal(t, r, mustR)
					require.Equal(t, m, d.MustMod(e))
				})
			}
		}
	}
}

func TestQuoRemAlignedDivisorU128Boundary(t *testing.T) {
	maxCoef := u128{hi: ^uint64(0), lo: ^uint64(0)}
	for _, scaleDigits := range []uint8{1, 2, 9, 18, 19} {
		t.Run(fmt.Sprintf("scale_1e%d", scaleDigits), func(t *testing.T) {
			scale := pow10Big(int(scaleDigits))
			largestFitting := new(big.Int).Quo(new(big.Int).Set(mask128big), scale)
			firstWide := new(big.Int).Add(new(big.Int).Set(largestFitting), big.NewInt(1))

			d := newDecimal(maxCoef, false, scaleDigits)
			fitE := newDecimal(bigToU128(t, largestFitting), false, 0)
			wideE := newDecimal(bigToU128(t, firstWide), false, 0)

			fitAligned := new(big.Int).Mul(new(big.Int).Set(largestFitting), scale)
			wideAligned := new(big.Int).Mul(new(big.Int).Set(firstWide), scale)
			require.LessOrEqual(t, fitAligned.Cmp(mask128big), 0)
			require.Greater(t, wideAligned.Cmp(mask128big), 0)

			requireQuoRemBigOracle(t, d, fitE)
			fitQ, _, err := d.QuoRem(fitE)
			require.NoError(t, err)
			require.False(t, fitQ.IsZero(), "largest fitting aligned divisor must take the division path")

			requireQuoRemBigOracle(t, d, wideE)
			wideQ, wideR, err := d.QuoRem(wideE)
			require.NoError(t, err)
			require.Equal(t, Decimal{}, wideQ)
			require.Equal(t, d, wideR)
		})
	}
}

func TestQuoRemWideAlignedDivisorRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xA11C_0DED, 0xD1A1_50A5))
	for i := range 25_000 {
		scaleDigits := uint8(1 + rng.Uint64N(uint64(MaxPrec)))
		ePrec := uint8(rng.Uint64N(uint64(MaxPrec-scaleDigits) + 1))
		dPrec := ePrec + scaleDigits

		// eCoef >= 2^127, so multiplying it by even 10^1 necessarily
		// exceeds the 128-bit coefficient range.
		eCoef := u128{hi: rng.Uint64() | 1<<63, lo: rng.Uint64()}
		dCoef := randShapedU128(rng)
		if dCoef.isZero() {
			dCoef.lo = 1
		}
		d := newDecimal(dCoef, rng.Uint64()&1 != 0, dPrec)
		e := newDecimal(eCoef, rng.Uint64()&1 != 0, ePrec)

		requireQuoRemBigOracle(t, d, e)
		q, r, err := d.QuoRem(e)
		require.NoError(t, err, "iteration %d", i)
		require.Equal(t, Decimal{}, q, "iteration %d", i)
		require.Equal(t, d, r, "iteration %d", i)
	}
}
