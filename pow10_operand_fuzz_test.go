//go:build fuzz

package zerodecimal

import "testing"

// FuzzPow10Operand steers the fuzzer onto the fast paths that mutated operand
// pairs almost never reach: the divisor is ±10^j at precision ePrec (Div's
// rescale for 1 and 100, the general path for every other j), and the
// multipliers are ±10^-ePrec and a fuzzed one-limb factor, both served by
// mulSlow's inline one-limb factor arms. Div must match pow10RefDiv field for
// field and the shopspring checkDiv oracle; Mul, in both operand orders, must
// match pow10RefMul and checkMul.
func FuzzPow10Operand(f *testing.F) {
	factors := []uint64{1, 100, 7, 1e19, 1<<64 - 1}
	for i, s := range fuzzCorpus {
		for j := range uint8(20) {
			f.Add(s.neg, s.hi, s.lo, s.prec, j, uint8(i+int(j))%20, (i+int(j))%2 == 1, factors[(i+int(j))%len(factors)])
		}
	}
	f.Fuzz(func(t *testing.T, neg bool, hi, lo uint64, prec, j, ePrec uint8, eNeg bool, factor uint64) {
		d := fuzzDecimal(t, neg, hi, lo, prec)
		e := newDecimal(u128{lo: pow10u64[j%20]}, eNeg, ePrec%20)
		cd := newCrossVal(d)
		_, _ = pow10CheckDiv(t, d, e)
		checkDiv(t, cd, newCrossVal(e))

		for _, u := range []Decimal{newDecimal(u128{lo: 1}, eNeg, ePrec%20), newDecimal(u128{lo: factor}, !eNeg, j%20)} {
			cu := newCrossVal(u)
			pow10CheckMul(t, d, u)
			pow10CheckMul(t, u, d)
			checkMul(t, cd, cu)
			checkMul(t, cu, cd)
		}
	})
}
