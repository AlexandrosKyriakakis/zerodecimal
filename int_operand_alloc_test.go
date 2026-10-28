package zerodecimal

import (
	"math"
	"testing"
)

// Package-level operands and sinks for the MulInt/DivInt allocation gate (see
// alloc_test.go for why neither may be local).
var (
	intOperandAllocResult Decimal
	errIntOperandAlloc    error

	// intOperandAllocShapes reach every MulInt and DivInt branch that the
	// default build compiles: one- and two-limb numerators, a 192-bit numerator
	// whose quotient fits, Div's precision-lowering path, and MinInt64.
	intOperandAllocShapes = []struct {
		name string
		d    Decimal
		n    int64
	}{
		{name: "one_limb_cents", d: MustNew(12_665, -2), n: 3},
		{name: "two_limb_max_prec", d: RequireFromString("12.3456789012345678901"), n: -1000},
		{name: "wide_numerator", d: Decimal{coef: u128{hi: 1 << 40}}, n: math.MaxInt64},
		{name: "lowers_precision", d: Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}, n: 1},
		{name: "min_int64", d: NewFromInt(-1), n: math.MinInt64},
	}
	intOperandAllocOverflow = Decimal{coef: u128{hi: 1 << 63}} // × intOperandAllocTwo overflows
	intOperandAllocTwo      = int64(2)
	intOperandAllocZero     int64
)

// TestIntOperandAllocs gates MulInt, DivInt and their Must twins at exactly
// zero allocations on every success path, and the erroring forms on both of
// their error paths: MulInt's ErrOverflow and DivInt's ErrDivideByZero.
func TestIntOperandAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, s := range intOperandAllocShapes {
		d, n := s.d, s.n
		tests := []struct {
			name string
			fn   func()
		}{
			{name: "div_int", fn: func() { intOperandAllocResult, errIntOperandAlloc = d.DivInt(n) }},
			{name: "must_div_int", fn: func() { intOperandAllocResult = d.MustDivInt(n) }},
			{name: "mul_int", fn: func() { intOperandAllocResult, errIntOperandAlloc = d.MulInt(n) }},
		}
		if _, err := d.MulInt(n); err == nil {
			tests = append(tests, struct {
				name string
				fn   func()
			}{name: "must_mul_int", fn: func() { intOperandAllocResult = d.MustMulInt(n) }})
		}
		for _, tc := range tests {
			t.Run(s.name+"/"+tc.name, func(t *testing.T) {
				requireAllocs(t, 0, tc.fn)
			})
		}
	}
	t.Run("mul_int_overflow", func(t *testing.T) {
		requireAllocs(t, 0, func() {
			intOperandAllocResult, errIntOperandAlloc = intOperandAllocOverflow.MulInt(intOperandAllocTwo)
		})
	})
	t.Run("div_int_by_zero", func(t *testing.T) {
		requireAllocs(t, 0, func() {
			intOperandAllocResult, errIntOperandAlloc = intOperandAllocOverflow.DivInt(intOperandAllocZero)
		})
	})
}
