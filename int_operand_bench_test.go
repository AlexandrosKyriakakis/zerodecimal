package zerodecimal

import (
	"strconv"
	"testing"
)

// Package-level operands and sinks for the MulInt/DivInt benchmarks (see
// alloc_test.go for why neither may be local).
var (
	intOperandCents    = MustNew(12_665, -2)                         // $126.65: one limb at 2 places
	intOperandRate     = RequireFromString("12.3456789012345678901") // two limbs at 19 places
	intOperandDivisors = [...]int64{3, 100, 1000}
	intOperandSink     Decimal
	errIntOperand      error
)

// BenchmarkIntOperand times MulInt and DivInt against the calls they replace,
// d.Mul(NewFromInt(n)) and d.Div(NewFromInt(n)), for a one-limb dividend at 2
// places and a two-limb one at 19 places. The names carry key=value parts, so
// "benchstat -col /operand" puts the two forms side by side.
func BenchmarkIntOperand(b *testing.B) {
	shapes := []struct {
		name string
		d    Decimal
	}{
		{"cents", intOperandCents},
		{"prec19", intOperandRate},
	}
	for _, s := range shapes {
		for _, n := range intOperandDivisors {
			d := s.d
			suffix := "/d=" + s.name + "/n=" + strconv.FormatInt(n, 10)
			b.Run("op=Mul"+suffix+"/operand=decimal", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					intOperandSink, errIntOperand = d.Mul(NewFromInt(n))
				}
			})
			b.Run("op=Mul"+suffix+"/operand=int", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					intOperandSink, errIntOperand = d.MulInt(n)
				}
			})
			b.Run("op=Div"+suffix+"/operand=decimal", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					intOperandSink, errIntOperand = d.Div(NewFromInt(n))
				}
			})
			b.Run("op=Div"+suffix+"/operand=int", func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					intOperandSink, errIntOperand = d.DivInt(n)
				}
			})
		}
	}
}
