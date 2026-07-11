package zerodecimal

import "testing"

func BenchmarkExactArithmetic(b *testing.B) {
	mulA := RequireFromString("123456789.123456789")
	mulB := RequireFromString("7.000000001")
	divA := RequireFromString("5000000000000000001")
	divB := RequireFromString("1000000000000000000000")
	fixedOne := newDecimal(u128{lo: pow10u64[MaxPrec]}, false, MaxPrec)

	b.Run("Mul/legacy_then_bank", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			d, err := mulA.Mul(mulB)
			if err != nil {
				b.Fatal(err)
			}
			exactResultSink = d.RoundBank(8)
		}
	})
	b.Run("Mul/direct_bank", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			exactResultSink, errExactSink = mulA.MulRound(mulB, 8, ToNearestEven)
		}
	})
	b.Run("Mul/exact", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			exactResultSink, errExactSink = mulA.MulExact(One)
		}
	})
	b.Run("Mul/exact_wide_rescale", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			exactResultSink, errExactSink = fixedOne.MulExact(fixedOne)
		}
	})
	b.Run("Div/legacy_then_bank", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			d, err := divA.Div(divB)
			if err != nil {
				b.Fatal(err)
			}
			exactResultSink = d.RoundBank(2)
		}
	})
	b.Run("Div/direct_bank", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			exactResultSink, errExactSink = divA.DivRound(divB, 2, ToNearestEven)
		}
	})
	b.Run("Div/exact", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			exactResultSink, errExactSink = One.DivExact(NewFromInt(8))
		}
	})
	b.Run("Div/exact_large_integer", func(b *testing.B) {
		maxDecimal := RequireFromString(exactMaxString)
		b.ReportAllocs()
		for b.Loop() {
			exactResultSink, errExactSink = maxDecimal.DivExact(One)
		}
	})
}
