//go:build fuzz

package zerodecimal

import (
	"math/big"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func quoremFuzzShopspring(d Decimal) decimal.Decimal {
	coef := u128ToBig(d.coef)
	if d.neg {
		coef.Neg(coef)
	}
	return decimal.NewFromBigInt(coef, -int32(d.prec))
}

// FuzzQuoRemAlignedDivisorOracle proves QuoRem and Mod against two unbounded
// models. math/big decides the exact representability boundary; shopspring
// independently checks T-division, the reconstruction identity, and the
// signed remainder. A divisor that becomes wider than 128 bits after common-
// scale alignment is explicitly a success case with q=0 and r=d.
func FuzzQuoRemAlignedDivisorOracle(f *testing.F) {
	seeds := []struct {
		dNeg     bool
		dHi, dLo uint64
		dPrec    uint8
		eNeg     bool
		eHi, eLo uint64
		ePrec    uint8
	}{
		{dLo: 1, dPrec: 19, eHi: ^uint64(0), eLo: ^uint64(0)},
		{dNeg: true, dHi: ^uint64(0), dLo: ^uint64(0), dPrec: 19, eHi: 1 << 63},
		{dHi: 1, dLo: 7, dPrec: 7, eNeg: true, eHi: 1 << 63, ePrec: 6},
		{dHi: ^uint64(0), dLo: ^uint64(0), eLo: 1, ePrec: 19}, // quotient overflow
		{dLo: 7, eLo: 2}, // same-precision fast path
		{dLo: 7},         // divide by zero
		{},               // canonical zero pair
	}
	for _, seed := range seeds {
		f.Add(seed.dNeg, seed.dHi, seed.dLo, seed.dPrec,
			seed.eNeg, seed.eHi, seed.eLo, seed.ePrec)
	}

	f.Fuzz(func(t *testing.T,
		dNeg bool, dHi, dLo uint64, dPrecRaw uint8,
		eNeg bool, eHi, eLo uint64, ePrecRaw uint8,
	) {
		dPrec := dPrecRaw % (MaxPrec + 1)
		ePrec := ePrecRaw % (MaxPrec + 1)
		d := newDecimal(u128{hi: dHi, lo: dLo}, dNeg, dPrec)
		e := newDecimal(u128{hi: eHi, lo: eLo}, eNeg, ePrec)

		gotQ, gotR, err := d.QuoRem(e)
		gotM, modErr := d.Mod(e)
		if e.IsZero() {
			require.ErrorIs(t, err, ErrDivideByZero)
			require.ErrorIs(t, modErr, ErrDivideByZero)
			require.Equal(t, Decimal{}, gotQ)
			require.Equal(t, Decimal{}, gotR)
			require.Equal(t, Decimal{}, gotM)
			return
		}

		commonPrec := max(d.prec, e.prec)
		num := new(big.Int).Mul(u128ToBig(d.coef), pow10Big(int(commonPrec-d.prec)))
		den := new(big.Int).Mul(u128ToBig(e.coef), pow10Big(int(commonPrec-e.prec)))
		wantQCoef, wantRCoef := new(big.Int).QuoRem(num, den, new(big.Int))
		if wantQCoef.BitLen() > 128 {
			require.ErrorIs(t, err, ErrOverflow)
			require.ErrorIs(t, modErr, ErrOverflow)
			require.Equal(t, Decimal{}, gotQ)
			require.Equal(t, Decimal{}, gotR)
			require.Equal(t, Decimal{}, gotM)
			return
		}

		require.NoError(t, err)
		require.NoError(t, modErr)
		wantQ := newDecimal(bigToU128(t, wantQCoef), d.neg != e.neg, 0)
		wantR := newDecimal(bigToU128(t, wantRCoef), d.neg, commonPrec)
		require.Equal(t, wantQ, gotQ)
		require.Equal(t, wantR, gotR)
		require.Equal(t, gotR, gotM)

		ssD, ssE := quoremFuzzShopspring(d), quoremFuzzShopspring(e)
		ssQ, ssR := ssD.QuoRem(ssE, 0)
		require.True(t, ssQ.Equal(quoremFuzzShopspring(gotQ)), "shopspring quotient")
		require.True(t, ssR.Equal(quoremFuzzShopspring(gotR)), "shopspring remainder")
		require.True(t, ssD.Equal(quoremFuzzShopspring(gotQ).Mul(ssE).Add(quoremFuzzShopspring(gotR))),
			"identity d = q*e + r")
		require.True(t, quoremFuzzShopspring(gotR).Abs().LessThan(ssE.Abs()), "|r| < |e|")

		if den.BitLen() > 128 {
			require.Equal(t, Decimal{}, gotQ, "wide aligned divisor proves a zero quotient")
			require.Equal(t, d, gotR, "wide aligned divisor leaves the dividend as remainder")
		}
	})
}
