//go:build fuzz

package zerodecimal

import "testing"

// FuzzIntOperand holds MulInt and DivInt to Mul and Div with NewFromInt(n)
// for arbitrary 128-bit dividends, signs and precisions and every int64: the
// same representation and bare sentinel, DivInt failing only on n == 0, and
// MustMulInt and MustDivInt panicking exactly when their twin errors, with
// that sentinel. checkMul and checkDiv then hold the shared results to the
// big.Int and shopspring oracles. The seeds cross every corpus quadruple with
// every edge operand, so the zero divisor, MinInt64 and the max×max overflow
// replay from seeds alone.
func FuzzIntOperand(f *testing.F) {
	for _, s := range fuzzCorpus {
		for _, n := range intOperandEdges {
			f.Add(s.neg, s.hi, s.lo, s.prec, n)
		}
	}
	f.Fuzz(func(t *testing.T, neg bool, hi, lo uint64, prec uint8, n int64) {
		d := fuzzDecimal(t, neg, hi, lo, prec)
		requireIntOperandIdentity(t, d, n)
		dv, nv := newCrossVal(d), newCrossVal(NewFromInt(n))
		checkMul(t, dv, nv)
		checkDiv(t, dv, nv)
	})
}
