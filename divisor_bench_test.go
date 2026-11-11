package zerodecimal

import "testing"

// divisorBenchDecimal builds a benchmark operand from raw parts, the way a
// database driver scans a NUMERIC column (a quantity at 9 places keeps all 9).
func divisorBenchDecimal(hi, lo uint64, prec uint8) Decimal {
	d, err := NewFromHiLo(false, hi, lo, prec)
	if err != nil {
		panic(err)
	}
	return d
}

// Package-level operands and sinks for the Divisor benchmarks and allocation
// gate (see alloc_test.go for why neither may be local).
var (
	// Pro-rata split: four amounts at 2 places times a part at 9 places
	// (products at 11 places), each divided by the whole at 9 places.
	divisorPart     = divisorBenchDecimal(0, 37_500_000_000, 9)  // 37.5 units
	divisorWhole    = divisorBenchDecimal(0, 100_250_000_000, 9) // 100.25 units
	divisorProducts = [4]Decimal{
		divisorBenchDecimal(0, 1_234_567, 2).MustMul(divisorPart), // cost
		divisorBenchDecimal(0, 495, 2).MustMul(divisorPart),       // fees
		divisorBenchDecimal(0, 1_321_040, 2).MustMul(divisorPart), // proceeds
		divisorBenchDecimal(0, 12_345, 2).MustMul(divisorPart),    // adjustment
	}
	divisorWholeDiv = MustNewDivisor(divisorWhole)

	// Σ price·qty at 18 places (a two-limb coefficient, 1234681.982…) over
	// Σ qty at 9 places: a VWAP.
	divisorNotional    = divisorBenchDecimal(0x1_0574, 0x3e8f_7a1b_2c3d_4e5f, 18)
	divisorTotalQty    = divisorBenchDecimal(0, 10_000_123_456_789, 9)
	divisorTotalQtyDiv = MustNewDivisor(divisorTotalQty)

	// A 19-place dividend over a 19-place divisor of at least 1.85, whose
	// coefficient (≥ 2^64) takes the two-limb reciprocal.
	divisorWideDividend   = RequireFromString("12.3456789012345678901")
	divisorWideDivisor    = RequireFromString("1.8500000000000000001")
	divisorWideDivisorDiv = MustNewDivisor(divisorWideDivisor)

	// A unit price: an amount at 2 places over a quantity at 9, rounded to 16
	// places.
	divisorCost = divisorBenchDecimal(0, 1_234_567, 2)

	divisorBenchSink Decimal
	divisorNewSink   Divisor
	errDivisorBench  error
)

// BenchmarkDivisor times each Divisor operation against the Decimal-argument
// operation it reproduces bit for bit, on identical inputs. Sub-benchmarks are
// named shape=…/impl=Decimal|Divisor, so benchstat -col /impl compares them;
// impl=Divisor excludes construction except in the shape that says so.
func BenchmarkDivisor(b *testing.B) {
	b.Run("shape=prorate_4x/impl=Decimal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for i := range divisorProducts {
				divisorBenchSink, errDivisorBench = divisorProducts[i].Div(divisorWhole)
			}
		}
	})
	b.Run("shape=prorate_4x/impl=Divisor", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for i := range divisorProducts {
				divisorBenchSink, errDivisorBench = divisorProducts[i].DivBy(&divisorWholeDiv)
			}
		}
	})
	// When the whole changes from one group of divisions to the next, the
	// realistic unit is one NewDivisor followed by the four prorates.
	b.Run("shape=prorate_4x_with_new/impl=Decimal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for i := range divisorProducts {
				divisorBenchSink, errDivisorBench = divisorProducts[i].Div(divisorWhole)
			}
		}
	})
	b.Run("shape=prorate_4x_with_new/impl=Divisor", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			v, err := NewDivisor(divisorWhole)
			if err != nil {
				b.Fatal(err)
			}
			for i := range divisorProducts {
				divisorBenchSink, errDivisorBench = divisorProducts[i].DivBy(&v)
			}
		}
	})
	b.Run("shape=ratio/impl=Decimal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorPart.Div(divisorWhole)
		}
	})
	b.Run("shape=ratio/impl=Divisor", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorPart.DivBy(&divisorWholeDiv)
		}
	})
	b.Run("shape=vwap/impl=Decimal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorNotional.Div(divisorTotalQty)
		}
	})
	b.Run("shape=vwap/impl=Divisor", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorNotional.DivBy(&divisorTotalQtyDiv)
		}
	})
	b.Run("shape=two_limb_divisor/impl=Decimal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorWideDividend.Div(divisorWideDivisor)
		}
	})
	b.Run("shape=two_limb_divisor/impl=Divisor", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorWideDividend.DivBy(&divisorWideDivisorDiv)
		}
	})
	b.Run("shape=div_round_cents/impl=Decimal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorProducts[0].DivRound(divisorWhole, 2, ToNearestEven)
		}
	})
	b.Run("shape=div_round_cents/impl=Divisor", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorProducts[0].DivRoundBy(&divisorWholeDiv, 2, ToNearestEven)
		}
	})
	b.Run("shape=div_round_16/impl=Decimal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorCost.DivRound(divisorWhole, 16, ToNearestAway)
		}
	})
	b.Run("shape=div_round_16/impl=Divisor", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorCost.DivRoundBy(&divisorWholeDiv, 16, ToNearestAway)
		}
	})
	// A two-limb divisor takes DivRoundBy's outlined arm (quoRem's 3-by-2
	// reciprocal) rather than the inline one-limb arm.
	b.Run("shape=div_round_two_limb/impl=Decimal", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorWideDividend.DivRound(divisorWideDivisor, 19, ToNearestEven)
		}
	})
	b.Run("shape=div_round_two_limb/impl=Divisor", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorBenchSink, errDivisorBench = divisorWideDividend.DivRoundBy(&divisorWideDivisorDiv, 19, ToNearestEven)
		}
	})
	b.Run("shape=new_divisor/impl=one_limb", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorNewSink, errDivisorBench = NewDivisor(divisorWhole)
		}
	})
	b.Run("shape=new_divisor/impl=two_limb", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			divisorNewSink, errDivisorBench = NewDivisor(divisorWideDivisor)
		}
	})
}
