package zerodecimal

import "testing"

// Package-level operands and sinks for the MulDivRound allocation gate and
// benchmarks (see alloc_test.go for why neither may be local).
var (
	mulDivAmount = MustNew(12_665, -2)    // $126.65 of fees
	mulDivPart   = MustNew(763_190, -6)   // the lot's share of the fill
	mulDivWhole  = MustNew(2_544_930, -6) // the fill's quantity
	mulDivWideA  = RequireFromString("12345678901234567890.123456789")
	mulDivWideB  = RequireFromString("98765432109.8765432109")
	mulDivTiny   = Decimal{coef: u128{lo: 1}, prec: MaxPrec}
	mulDivHuge   = Decimal{coef: u128{hi: 1 << 40}}
	mulDivZero   Decimal
	mulDivSink   Decimal
	errMulDiv    error
)

// BenchmarkMulDivRound times MulDivRound against the chains it replaces: the
// legacy Mul, Div, RoundBank (which truncates the quotient at DefaultPrec
// before rounding) and MulExact then DivRound (one rounding, two calls).
func BenchmarkMulDivRound(b *testing.B) {
	cases := []struct {
		name    string
		a, p, w Decimal
		places  uint8
	}{
		// a fee split over lots: cents × qty / qty to cents, one-limb path.
		{"share", mulDivAmount, mulDivPart, mulDivWhole, 2},
		// a share at full precision: the scaled product passes one limb.
		{"share_19dp", mulDivAmount, mulDivPart, mulDivWhole, 19},
		// a product past 128 bits.
		{"wide_product", mulDivWideA, mulDivWideB, mulDivWhole, 2},
		// a scaled denominator past 128 bits.
		{"wide_denominator", mulDivTiny, mulDivTiny, mulDivHuge, 0},
	}
	for _, c := range cases {
		x, y, z, p := c.a, c.p, c.w, c.places
		b.Run("MulDivRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mulDivSink, errMulDiv = x.MulDivRound(y, z, p, ToNearestEven)
			}
		})
	}
	b.Run("Mul+Div+RoundBank/share", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			mulDivSink = mulDivAmount.MustMul(mulDivPart).MustDiv(mulDivWhole).RoundBank(2)
		}
	})
	b.Run("MulExact+DivRound/share", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			p, err := mulDivAmount.MulExact(mulDivPart)
			if err != nil {
				b.Fatal(err)
			}
			mulDivSink, errMulDiv = p.DivRound(mulDivWhole, 2, ToNearestEven)
		}
	})
}
