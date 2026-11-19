package zerodecimal

import "testing"

// Package-level operands and sinks for the Must-twin allocation gate and
// benchmarks (see alloc_test.go for why neither may be local).
var (
	mustRoundQty      = MustNew(1_500_000, -6) // 1.5 units at 6 places
	mustRoundPrice    = MustNew(5_012_345, -2) // $50,123.45
	mustRoundFee      = MustNew(7_518, -2)     // $75.18
	mustRoundNotional = MustNew(18_517, -2)    // $185.17 to spread over the fill
	mustRoundPart     = MustNew(763_190, -6)   // one part's share of the fill
	mustRoundWhole    = MustNew(2_544_930, -6) // the fill's quantity
	mustRoundWideA    = RequireFromString("12345678901234567890.123456789")
	mustRoundWideB    = RequireFromString("98765432109.8765432109")
	mustRoundPrices   = []Decimal{
		MustNew(1999, -2), MustNew(2005, -2), MustNew(1995, -2), MustNew(2010, -2),
		MustNew(1998, -2), MustNew(2002, -2), MustNew(2000, -2), MustNew(1997, -2),
	}
	mustRoundLong = func() []Decimal {
		xs := make([]Decimal, 1024)
		for i := range xs {
			xs[i] = NewFromInt(int64(i%17) - 8)
		}
		return xs
	}()
	mustRoundSink Decimal
)

// BenchmarkMustRound times each Must twin ("Must/…") next to the call a
// caller writes without it ("Checked/…": the erroring form and a panic on its
// error), on the operand widths the twins' operations dispatch on. The twin
// is a thin wrapper, so the pair shows its whole cost: nothing when it
// inlines into the caller, one call frame when it does not. Both forms call
// the method directly: a function value in the loop would stop it inlining.
func BenchmarkMustRound(b *testing.B) {
	type pair struct {
		name    string
		a, b, c Decimal
		places  uint8
	}
	mulCases := []pair{
		// qty (6dp) × price (2dp) to cents: a one-limb product.
		{"qty_x_price", mustRoundQty, mustRoundPrice, Decimal{}, 2},
		// a product past 128 bits.
		{"wide_product", mustRoundWideA, mustRoundWideB, Decimal{}, 2},
	}
	for _, c := range mulCases {
		x, y, p := c.a, c.b, c.places
		b.Run("Must/MulRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mustRoundSink = x.MustMulRound(y, p, ToNearestEven)
			}
		})
		b.Run("Checked/MulRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r, err := x.MulRound(y, p, ToNearestEven)
				if err != nil {
					panic(err)
				}
				mustRoundSink = r
			}
		})
	}

	divCases := []pair{
		// a notional over a quantity to cents: a one-limb dividend.
		{"share", mustRoundNotional, mustRoundQty, Decimal{}, 2},
		// a two-limb divisor at full precision.
		{"wide_divisor", mustRoundWideA, mustRoundWideB, Decimal{}, 19},
	}
	for _, c := range divCases {
		x, y, p := c.a, c.b, c.places
		b.Run("Must/DivRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mustRoundSink = x.MustDivRound(y, p, ToNearestEven)
			}
		})
		b.Run("Checked/DivRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r, err := x.DivRound(y, p, ToNearestEven)
				if err != nil {
					panic(err)
				}
				mustRoundSink = r
			}
		})
	}

	addCases := []pair{
		{"qty_x_price_plus_fee", mustRoundQty, mustRoundPrice, mustRoundFee, 2},
		{"wide_product", mustRoundWideA, mustRoundWideB, mustRoundFee, 2},
	}
	for _, c := range addCases {
		x, y, z, p := c.a, c.b, c.c, c.places
		b.Run("Must/MulAddRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mustRoundSink = x.MustMulAddRound(y, z, p, ToNearestEven)
			}
		})
		b.Run("Checked/MulAddRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r, err := x.MulAddRound(y, z, p, ToNearestEven)
				if err != nil {
					panic(err)
				}
				mustRoundSink = r
			}
		})
	}

	divRoundCases := []pair{
		// a pro-rata share: cents × part / whole to cents, one-limb.
		{"share", mustRoundNotional, mustRoundPart, mustRoundWhole, 2},
		{"wide_product", mustRoundWideA, mustRoundWideB, mustRoundWhole, 2},
	}
	for _, c := range divRoundCases {
		x, y, z, p := c.a, c.b, c.c, c.places
		b.Run("Must/MulDivRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mustRoundSink = x.MustMulDivRound(y, z, p, ToNearestEven)
			}
		})
		b.Run("Checked/MulDivRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r, err := x.MulDivRound(y, z, p, ToNearestEven)
				if err != nil {
					panic(err)
				}
				mustRoundSink = r
			}
		})
	}

	avgCases := []struct {
		name string
		xs   []Decimal
	}{
		{"prices_2", mustRoundPrices[:2]},
		{"prices_8", mustRoundPrices},
	}
	for _, c := range avgCases {
		xs := c.xs
		b.Run("Must/AvgRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mustRoundSink = MustAvgRound(xs[0], 2, ToNearestEven, xs[1:]...)
			}
		})
		b.Run("Checked/AvgRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r, err := AvgRound(xs[0], 2, ToNearestEven, xs[1:]...)
				if err != nil {
					panic(err)
				}
				mustRoundSink = r
			}
		})
	}
}
