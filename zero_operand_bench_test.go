package zerodecimal

import "testing"

// zeroOperandShapes are values with fractional digits, so each meets the
// canonical zero (precision 0) misaligned.
var zeroOperandShapes = []struct {
	name string
	x    Decimal
}{
	{"cents", MustNew(12345, -2)},
	{"negative_cents", MustNew(-12345, -2)},
	{"max_prec", RequireFromString("0.1234567890123456789")},
	{"two_limb", RequireFromString("12345678901234567890.12345")},
}

// BenchmarkZeroOperand times Add, Sub and Cmp against Decimal{}: the first add
// into an accumulator that starts from zero, a zero term in a sum, and the
// x.Cmp(Decimal{}) / x.GreaterThan(Decimal{}) sign tests.
func BenchmarkZeroOperand(b *testing.B) {
	for _, s := range zeroOperandShapes {
		x := s.x
		b.Run("Add/zero_lhs/"+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				zeroOperandSink, zeroOperandErrOut = zeroOperandZero.Add(x)
			}
		})
		b.Run("Add/zero_rhs/"+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				zeroOperandSink, zeroOperandErrOut = x.Add(zeroOperandZero)
			}
		})
		b.Run("Sub/zero_lhs/"+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				zeroOperandSink, zeroOperandErrOut = zeroOperandZero.Sub(x)
			}
		})
		b.Run("Cmp/zero_rhs/"+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				zeroOperandCmp = x.Cmp(zeroOperandZero)
			}
		})
	}
	// An accumulator that starts from Decimal{}: only its first add meets zero.
	terms := []Decimal{MustNew(1999, -2), MustNew(-250, -2), MustNew(12345, -2), MustNew(1, -2)}
	b.Run("Add/accumulate_4_from_zero", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			sum := zeroOperandZero
			for _, v := range terms {
				sum = sum.MustAdd(v)
			}
			zeroOperandSink = sum
		}
	})
}
