package zerodecimal

import "testing"

// Package-level operands and sinks for the power-of-ten operand benchmarks
// and allocation gate (see alloc_test.go for why neither may be local).
var (
	// A prec-19 dividend with a two-limb coefficient (≈1.2·10^21), the shape
	// of a price that came out of an earlier Div, and a prec-9 one-limb one.
	pow10TwoLimb19 = RequireFromString("123.4567890123456789012")
	pow10OneLimb9  = RequireFromString("1234.567890123")
	pow10OneLimb19 = RequireFromString("0.1234567890123456789")

	pow10Dividends = []struct {
		name string
		d    Decimal
	}{
		{"prec19_two_limb", pow10TwoLimb19},
		{"prec9_one_limb", pow10OneLimb9},
	}

	// Contract multipliers (1 for stocks, 100 for options, 0.01 for fixed
	// income) and a strike scale.
	pow10Divisors = []struct {
		name string
		e    Decimal
	}{
		{"1", NewFromInt(1)},
		{"100", NewFromInt(100)},
		{"0.01", RequireFromString("0.01")},
		{"1000", NewFromInt(1000)},
	}

	// Non-power-of-ten divisors guard the general path against the cost of
	// the detection: one-limb integers and fractions, a one-limb divisor whose
	// quotient needs the two-limb dividend, and a two-limb divisor.
	pow10GuardDivisors = []struct {
		name string
		e    Decimal
	}{
		{"7", NewFromInt(7)},
		{"99", NewFromInt(99)},
		{"3.7", RequireFromString("3.7")},
		{"1.000000001", RequireFromString("1.000000001")},
		{"0.9876543210987654321", RequireFromString("0.9876543210987654321")},
		{"two_limb", RequireFromString("98765432109.8765432109")},
	}

	pow10Sink Decimal
	errPow10  error
)

// BenchmarkPow10Operand times Div by power-of-ten divisors and Mul by
// one-limb factors (1, 100, …), next to rows that guard the general paths:
// non-power-of-ten divisors on the same dividends, and mulSlow arms without
// a one-limb factor.
func BenchmarkPow10Operand(b *testing.B) {
	for _, dv := range pow10Dividends {
		for _, dz := range pow10Divisors {
			d, e := dv.d, dz.e
			b.Run("Div/"+dv.name+"/by_"+dz.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					pow10Sink, errPow10 = d.Div(e)
				}
			})
		}
	}
	for _, dv := range pow10Dividends {
		for _, dz := range pow10GuardDivisors {
			d, e := dv.d, dz.e
			b.Run("DivGuard/"+dv.name+"/by_"+dz.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					pow10Sink, errPow10 = d.Div(e)
				}
			})
		}
	}

	mulCases := []struct {
		name string
		a, b Decimal
	}{
		// pSum = 19: no rescale; a one-limb factor times a two-limb one.
		{"Mul/prec19_two_limb/by_1", pow10TwoLimb19, NewFromInt(1)},
		{"Mul/prec19_two_limb/by_100", pow10TwoLimb19, NewFromInt(100)},
		{"Mul/prec19_two_limb/by_7", pow10TwoLimb19, NewFromInt(7)},
		// pSum = 21 and 28: the same products rescaled by 10^2 and 10^9.
		{"Mul/prec19_two_limb/by_0.01", pow10TwoLimb19, RequireFromString("0.01")},
		{"Mul/prec19_two_limb/by_1.000000001", pow10TwoLimb19, RequireFromString("1.000000001")},
		// Guards: mulSlow arms without a one-limb factor path — one-limb
		// pairs rescaled by 10^19, and a two-limb pair.
		{"MulGuard/prec19_one_limb/by_1e-19", pow10OneLimb19, RequireFromString("0.0000000000000000001")},
		{"MulGuard/prec19_one_limb/by_0.9876543210987654321", pow10OneLimb19, RequireFromString("0.9876543210987654321")},
		{"MulGuard/prec19_two_limb/by_prec19_two_limb", pow10TwoLimb19, pow10TwoLimb19},
	}
	for _, c := range mulCases {
		x, y := c.a, c.b
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				pow10Sink, errPow10 = x.Mul(y)
			}
		})
	}
}
