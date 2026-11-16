package zerodecimal

import "testing"

// Package-level inputs for the power-of-ten allocation gate (see alloc_test.go
// for why they may not be local). The success-path operands and the sinks are
// shared with pow10_operand_bench_test.go.
var (
	pow10AllocDiv1    = NewFromInt(1)
	pow10AllocDiv100  = NewFromInt(100)
	pow10AllocCent    = RequireFromString("0.01")
	pow10AllocQuantum = RequireFromString("0.0000000000000000001")
	pow10AllocMaxInt  = Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
)

// TestPow10OperandAllocs extends the zero-allocation gate to the arms this
// feature adds, which alloc_test.go's shape matrix does not reach: Div's
// power-of-ten rescale in both directions and its fall-through to the general
// path's degradation and overflow, and mulSlow's one-limb factor arms (no
// rescale, rescaled, and two two-limb factors that overflow outright).
func TestPow10OperandAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	tests := []struct {
		name string
		fn   func()
	}{
		{name: "div_rescale_up", fn: func() { pow10Sink, errPow10 = pow10OneLimb9.Div(pow10AllocDiv100) }},
		{name: "div_rescale_down", fn: func() { pow10Sink, errPow10 = pow10TwoLimb19.Div(pow10AllocDiv100) }},
		{name: "div_degrades", fn: func() { pow10Sink, errPow10 = pow10AllocMaxInt.Div(pow10AllocDiv1) }},
		{name: "div_overflow", fn: func() { pow10Sink, errPow10 = pow10AllocMaxInt.Div(pow10AllocCent) }},
		{name: "mul_one_limb_factor", fn: func() { pow10Sink, errPow10 = pow10TwoLimb19.Mul(pow10AllocDiv1) }},
		{name: "mul_one_limb_factor_rescaled", fn: func() { pow10Sink, errPow10 = pow10TwoLimb19.Mul(pow10AllocQuantum) }},
		{name: "mul_two_limb_pair_overflows", fn: func() { pow10Sink, errPow10 = pow10AllocMaxInt.Mul(pow10TwoLimb19) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if n := testing.AllocsPerRun(allocRuns, tc.fn); n != 0 {
				t.Fatalf("%s allocated %v times per run, want 0", tc.name, n)
			}
		})
	}
}
