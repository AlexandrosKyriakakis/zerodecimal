package zerodecimal

import "testing"

// Package-level operands and sinks for the MulAddRound allocation gate and
// benchmarks (see alloc_test.go for why neither may be local).
var (
	mulAddQty   = MustNew(1_500_000, -6) // 1.5 units at 6 places
	mulAddPrice = MustNew(5_012_345, -2) // $50,123.45
	mulAddFee   = MustNew(7_518, -2)     // $75.18
	mulAddLots  = NewFromInt(10)
	mulAddWideA = RequireFromString("12345678901234567890.123456789")
	mulAddWideB = RequireFromString("98765432109.8765432109")
	mulAddRate  = RequireFromString("1.0837000000000000001") // 19-place FX rate
	mulAddMax   = Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	mulAddSink  Decimal
	errMulAdd   error
)

// BenchmarkMulAddRound times MulAddRound against the chains it replaces. The
// legacy chain Mul, Add, RoundBank computes the same value on these shapes
// (the product needs at most DefaultPrec places); MulRound then Add rounds
// twice and is listed for its cost only.
func BenchmarkMulAddRound(b *testing.B) {
	cases := []struct {
		name      string
		q, p, fee Decimal
	}{
		// qty (6dp) × price (2dp) + fee (2dp) to cents: one-limb path.
		{"qty_x_price_plus_fee", mulAddQty, mulAddPrice, mulAddFee},
		// whole lots × price + fee: exact, the result only scales.
		{"lots_x_price_plus_fee", mulAddLots, mulAddPrice, mulAddFee},
		// a 19-place rate: the product needs 21 places, past DefaultPrec.
		{"rate_x_notional_plus_fee", mulAddRate, mulAddPrice, mulAddFee},
		// a product past 128 bits.
		{"wide_product", mulAddWideA, mulAddWideB, mulAddFee},
	}
	for _, c := range cases {
		q, p, fee := c.q, c.p, c.fee
		b.Run("MulAddRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mulAddSink, errMulAdd = q.MulAddRound(p, fee, 2, ToNearestEven)
			}
		})
	}
	b.Run("Mul+Add+RoundBank/qty_x_price_plus_fee", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			mulAddSink = mulAddQty.MustMul(mulAddPrice).MustAdd(mulAddFee).RoundBank(2)
		}
	})
	b.Run("Mul+Add+RoundBank/lots_x_price_plus_fee", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			mulAddSink = mulAddLots.MustMul(mulAddPrice).MustAdd(mulAddFee).RoundBank(2)
		}
	})
	b.Run("MulRound+Add/qty_x_price_plus_fee", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			r, err := mulAddQty.MulRound(mulAddPrice, 2, ToNearestEven)
			if err != nil {
				b.Fatal(err)
			}
			mulAddSink = r.MustAdd(mulAddFee)
		}
	})
}
