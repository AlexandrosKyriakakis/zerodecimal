package zerodecimal

import "testing"

// Package-level operands and sinks for the MulDivRoundBy benchmarks and
// allocation gate (see alloc_test.go for why neither may be local). They
// extend the MulDivRound fixtures (muldiv_bench_test.go) and the pro-rata
// fixtures (divisor_bench_test.go).
var (
	// mulDivWhole, the fill's quantity at 6 places, and mulDivHuge, a
	// two-limb integer, precomputed.
	mulDivByWholeDiv = MustNewDivisor(mulDivWhole)
	mulDivByHugeDiv  = MustNewDivisor(mulDivHuge)

	// A 19-place amount whose coefficient is past one limb, such as an
	// amount left at full precision by an earlier division.
	mulDivByAmount19 = RequireFromString("12.3456789012345678901")
	// A cross rate: an amount times a 19-place rate over a 19-place rate of
	// at least 1.85, whose coefficient takes the two-limb reciprocal.
	mulDivByRate = RequireFromString("1.0837000000000000001")

	// Pro-rata split: four amounts at 2 places, each times the part and
	// over the whole, both at 9 places.
	mulDivByAmounts = [4]Decimal{
		divisorBenchDecimal(0, 1_234_567, 2), // cost
		divisorBenchDecimal(0, 495, 2),       // fees
		divisorBenchDecimal(0, 1_321_040, 2), // proceeds
		divisorBenchDecimal(0, 12_345, 2),    // adjustment
	}

	mulDivBySink Decimal
	errMulDivBy  error
)

// BenchmarkMulDivRoundBy times MulDivRoundBy against the MulDivRound call it
// reproduces bit for bit, on identical inputs. Sub-benchmarks are named
// shape=…/impl=MulDivRound|MulDivRoundBy, so benchstat -col /impl compares
// them; the Divisor is built outside the loop except in the shape that says
// so. Each shape notes the arm it takes.
func BenchmarkMulDivRoundBy(b *testing.B) {
	cases := []struct {
		name    string
		a, p, w Decimal
		v       *Divisor
		places  uint8
	}{
		// a fee split by quantity, cents × qty / qty to cents: one hardware
		// divide on both sides (narrow).
		{"share", mulDivAmount, mulDivPart, mulDivWhole, &mulDivByWholeDiv, 2},
		// the same share to 16 places: a one-digit quotient of a two-limb
		// scaled product (inline; MulDivRound's bits.Div64).
		{"share_16dp", mulDivAmount, mulDivPart, mulDivWhole, &mulDivByWholeDiv, 16},
		// to 19 places: a two-digit quotient (inline; MulDivRound's
		// div256by64).
		{"share_19dp", mulDivAmount, mulDivPart, mulDivWhole, &mulDivByWholeDiv, 19},
		// a two-limb 19-place amount's share to 19 places (wide_one_limb).
		{"amount19_19dp", mulDivByAmount19, mulDivPart, mulDivWhole, &mulDivByWholeDiv, 19},
		// cents × rate / two-limb rate to cents (wide_two_limb; MulDivRound's
		// div256by128).
		{"cross_rate", mulDivAmount, mulDivByRate, divisorWideDivisor, &divisorWideDivisorDiv, 2},
		// a product past 128 bits to cents: the gap is negative and the
		// scaled divisor, not v's, fits 128 bits (scaled_divisor_fits).
		{"wide_product", mulDivWideA, mulDivWideB, mulDivWhole, &mulDivByWholeDiv, 2},
		// a scaled divisor past 128 bits (scaled_divisor_wide).
		{"wide_denominator", mulDivTiny, mulDivTiny, mulDivHuge, &mulDivByHugeDiv, 0},
	}
	for _, c := range cases {
		x, y, z, v, p := c.a, c.p, c.w, c.v, c.places
		b.Run("shape="+c.name+"/impl=MulDivRound", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mulDivBySink, errMulDivBy = x.MulDivRound(y, z, p, ToNearestEven)
			}
		})
		b.Run("shape="+c.name+"/impl=MulDivRoundBy", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mulDivBySink, errMulDivBy = x.MulDivRoundBy(y, v, p, ToNearestEven)
			}
		})
	}
	// Four pro-rata shares of one whole, to cents (narrow) and to 19 places
	// (inline). When the whole changes per split, the realistic unit at 19
	// places includes one NewDivisor; its MulDivRound row repeats
	// prorate_4x_19dp's, for benchstat to pair.
	for _, c := range []struct {
		name   string
		places uint8
	}{{"prorate_4x", 2}, {"prorate_4x_19dp", 19}, {"prorate_4x_19dp_with_new", 19}} {
		p := c.places
		b.Run("shape="+c.name+"/impl=MulDivRound", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for i := range mulDivByAmounts {
					mulDivBySink, errMulDivBy = mulDivByAmounts[i].MulDivRound(divisorPart, divisorWhole, p, ToNearestEven)
				}
			}
		})
	}
	b.Run("shape=prorate_4x/impl=MulDivRoundBy", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for i := range mulDivByAmounts {
				mulDivBySink, errMulDivBy = mulDivByAmounts[i].MulDivRoundBy(divisorPart, &divisorWholeDiv, 2, ToNearestEven)
			}
		}
	})
	b.Run("shape=prorate_4x_19dp/impl=MulDivRoundBy", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for i := range mulDivByAmounts {
				mulDivBySink, errMulDivBy = mulDivByAmounts[i].MulDivRoundBy(divisorPart, &divisorWholeDiv, 19, ToNearestEven)
			}
		}
	})
	b.Run("shape=prorate_4x_19dp_with_new/impl=MulDivRoundBy", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			v, err := NewDivisor(divisorWhole)
			if err != nil {
				b.Fatal(err)
			}
			for i := range mulDivByAmounts {
				mulDivBySink, errMulDivBy = mulDivByAmounts[i].MulDivRoundBy(divisorPart, &v, 19, ToNearestEven)
			}
		}
	})
}
