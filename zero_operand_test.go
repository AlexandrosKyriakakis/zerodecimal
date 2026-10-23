package zerodecimal

import (
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// zeroOperandValues returns nonzero values a canonical zero meets misaligned:
// every precision from 1 to MaxPrec, both signs, one- and two-limb
// coefficients, and the coefficient extremes. Precision 0 values are included
// too; they meet zero on the aligned path.
func zeroOperandValues() []Decimal {
	coefs := []u128{
		{lo: 1},
		{lo: 12345},
		{lo: maxUint64},
		{hi: 1},
		{hi: maxUint64, lo: maxUint64},
	}
	var xs []Decimal
	for prec := uint8(0); prec <= MaxPrec; prec++ {
		for _, c := range coefs {
			xs = append(xs, newDecimal(c, false, prec), newDecimal(c, true, prec))
		}
	}
	rng := rand.New(rand.NewPCG(0x2e60, 0x0e4a))
	for range 20_000 {
		xs = append(xs, randDecimal(rng))
	}
	return xs
}

// TestZeroOperandAddSub pins Add and Sub with a canonical zero operand to the
// other operand's exact representation. The contract (the exact result at
// max(d.prec, e.prec)) leaves no choice: 0 + x and x + 0 are x, 0 - x is -x
// and x - 0 is x, coefficient, sign and precision alike, and never an error.
func TestZeroOperandAddSub(t *testing.T) {
	var zero Decimal
	for _, x := range zeroOperandValues() {
		got, err := zero.Add(x)
		require.NoError(t, err, "0 + %v", x)
		require.Equal(t, x, got, "0 + %v", x)

		got, err = x.Add(zero)
		require.NoError(t, err, "%v + 0", x)
		require.Equal(t, x, got, "%v + 0", x)

		got, err = zero.Sub(x)
		require.NoError(t, err, "0 - %v", x)
		require.Equal(t, x.Neg(), got, "0 - %v", x)

		got, err = x.Sub(zero)
		require.NoError(t, err, "%v - 0", x)
		require.Equal(t, x, got, "%v - 0", x)

		require.Equal(t, x, zero.MustAdd(x), "MustAdd 0 + %v", x)
		require.Equal(t, x.Neg(), zero.MustSub(x), "MustSub 0 - %v", x)
	}
}

// TestZeroOperandCmp pins every comparison against a canonical zero to the
// sign of the other operand, whatever its precision.
func TestZeroOperandCmp(t *testing.T) {
	var zero Decimal
	for _, x := range zeroOperandValues() {
		s := x.Sign()
		require.Equal(t, s, x.Cmp(zero), "cmp(%v, 0)", x)
		require.Equal(t, -s, zero.Cmp(x), "cmp(0, %v)", x)
		require.Equal(t, s == 0, x.Equal(zero), "%v == 0", x)
		require.Equal(t, s > 0, x.GreaterThan(zero), "%v > 0", x)
		require.Equal(t, s < 0, x.LessThan(zero), "%v < 0", x)
		require.Equal(t, s >= 0, x.GreaterThanOrEqual(zero), "%v >= 0", x)
		require.Equal(t, s <= 0, x.LessThanOrEqual(zero), "%v <= 0", x)
		if s < 0 {
			require.Equal(t, x, Min(zero, x), "min(0, %v)", x)
			require.Equal(t, zero, Max(zero, x), "max(0, %v)", x)
		}
	}
}

// Package-level inputs and sinks for the zero-operand gates and benchmarks
// (see alloc_test.go for why neither may be local).
var (
	zeroOperandZero   Decimal
	zeroOperandFrac   = MustNew(12345, -2)
	zeroOperandSink   Decimal
	zeroOperandCmp    int
	zeroOperandErrOut error
)

// TestZeroOperandAllocs extends the zero-allocation gate to the zero-operand
// arms, which alloc_test.go's shape matrix does not cover.
func TestZeroOperandAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	ops := []struct {
		name string
		fn   func()
	}{
		{"add_zero_lhs", func() { zeroOperandSink, zeroOperandErrOut = zeroOperandZero.Add(zeroOperandFrac) }},
		{"add_zero_rhs", func() { zeroOperandSink, zeroOperandErrOut = zeroOperandFrac.Add(zeroOperandZero) }},
		{"sub_zero_lhs", func() { zeroOperandSink, zeroOperandErrOut = zeroOperandZero.Sub(zeroOperandFrac) }},
		{"sub_zero_rhs", func() { zeroOperandSink, zeroOperandErrOut = zeroOperandFrac.Sub(zeroOperandZero) }},
		{"cmp_zero_rhs", func() { zeroOperandCmp = zeroOperandFrac.Cmp(zeroOperandZero) }},
		{"cmp_zero_lhs", func() { zeroOperandCmp = zeroOperandZero.Cmp(zeroOperandFrac) }},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			requireAllocs(t, 0, op.fn)
		})
	}
}
