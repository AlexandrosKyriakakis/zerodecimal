//go:build fuzz

package zerodecimal

import (
	"math/big"
	"testing"

	shopspring "github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// FuzzDirectRoundedArithmetic checks every rounding mode against an exact
// big.Rat oracle. ToNearestAway successes are additionally checked against
// shopspring's independently implemented Mul+Round and DivRound operations.
func FuzzDirectRoundedArithmetic(f *testing.F) {
	seeds := []struct {
		ahi, alo, bhi, blo uint64
		am, bm             uint8
		places, mode       uint8
	}{
		{0, 1, 0, 2, 0, 0, 0, 0},
		{0, 25, 0, 10, 0, 0, 0, 1},
		{0, 5_000_000_000_000_000_001, 0, 1_000, 0, 18, 2, 1},
		{^uint64(0), ^uint64(0), 0, 1, 0, 0, 19, 3},
		{1 << 63, 0, 1 << 62, 1, 0x80 | 19, 17, 19, 5},
	}
	for _, s := range seeds {
		f.Add(s.ahi, s.alo, s.bhi, s.blo, s.am, s.bm, s.places, s.mode)
	}

	f.Fuzz(func(t *testing.T, ahi, alo, bhi, blo uint64, am, bm, pc, mc uint8) {
		a := exactFuzzDecimal(ahi, alo, am)
		b := exactFuzzDecimal(bhi, blo, bm)
		places := pc % (MaxPrec + 1)
		mode := RoundingMode(mc % uint8(TowardNegative+1))

		checkDirectRoundOracle(t, "mul", a, b, places, mode, a.MulRound)
		checkDirectRoundOracle(t, "div", a, b, places, mode, a.DivRound)

		if mode != ToNearestAway {
			return
		}
		mul, mulErr := a.MulRound(b, places, mode)
		if mulErr == nil {
			want := exactShopspring(a).Mul(exactShopspring(b)).Round(int32(places))
			require.Zero(t, exactShopspring(mul).Cmp(want), "shopspring mul a=%+v b=%+v", a, b)
		}
		if b.IsZero() {
			return
		}
		div, divErr := a.DivRound(b, places, mode)
		if divErr == nil {
			want := exactShopspring(a).DivRound(exactShopspring(b), int32(places))
			require.Zero(t, exactShopspring(div).Cmp(want), "shopspring div a=%+v b=%+v", a, b)
		}
	})
}

// FuzzExactArithmetic proves that every exact-operation success equals the
// mathematical rational result and every failure has the precise range,
// precision, underflow, or divide-by-zero classification.
func FuzzExactArithmetic(f *testing.F) {
	seeds := []struct {
		ahi, alo, bhi, blo uint64
		am, bm             uint8
	}{
		{0, 1, 0, 3, 0, 0},
		{0, 1, 0, 10, 19, 1},
		{^uint64(0), ^uint64(0), 0, 2, 0, 0},
		{0, 1, 0, 0, 19, 0},
		{1 << 63, 0, 0, 10, 0x80 | 19, 1},
	}
	for _, s := range seeds {
		f.Add(s.ahi, s.alo, s.bhi, s.blo, s.am, s.bm)
	}

	f.Fuzz(func(t *testing.T, ahi, alo, bhi, blo uint64, am, bm uint8) {
		a := exactFuzzDecimal(ahi, alo, am)
		b := exactFuzzDecimal(bhi, blo, bm)

		mulWant := new(big.Rat).Mul(exactDecimalRat(a), exactDecimalRat(b))
		mul, mulErr := a.MulExact(b)
		checkExactOracle(t, "mul", a, b, mulWant, mul, mulErr)
		if mulErr == nil {
			require.Zero(t, exactShopspring(mul).Cmp(exactShopspring(a).Mul(exactShopspring(b))))
		}

		div, divErr := a.DivExact(b)
		if b.IsZero() {
			require.ErrorIs(t, divErr, ErrDivideByZero)
			return
		}
		divWant := new(big.Rat).Quo(exactDecimalRat(a), exactDecimalRat(b))
		checkExactOracle(t, "div", a, b, divWant, div, divErr)
		if divErr == nil {
			require.Zero(t, exactShopspring(div).Mul(exactShopspring(b)).Cmp(exactShopspring(a)))
		}
	})
}

func exactFuzzDecimal(hi, lo uint64, meta uint8) Decimal {
	prec := meta % (MaxPrec + 1)
	neg := meta&0x80 != 0
	d, err := NewFromHiLo(neg, hi, lo, prec)
	if err != nil {
		panic(err)
	}
	return d
}

func exactShopspring(d Decimal) shopspring.Decimal {
	coef := exactBigFromU128(d.coef)
	if d.neg {
		coef.Neg(coef)
	}
	return shopspring.NewFromBigInt(coef, -int32(d.prec))
}
