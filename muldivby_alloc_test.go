package zerodecimal

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

// Allocation-gate fixtures and sinks (see alloc_test.go for the rules). The
// benchmark operands in muldivby_bench_test.go supply the success shapes.
var (
	mulDivByAllocResult Decimal
	errMulDivByAlloc    error

	mulDivByAllocNil   *Divisor
	mulDivByAllocZero  Divisor
	mulDivByAllocOne   = NewFromInt(1)
	mulDivByAllocThree = MustNewDivisor(NewFromInt(3))
	mulDivByAllocSeven = MustNewDivisor(NewFromInt(7))
	mulDivByAllocTiny  = MustNewDivisor(newDecimal(u128{lo: 1}, false, MaxPrec))
	mulDivByAllocMax   = newDecimal(u128{hi: maxUint64, lo: maxUint64}, false, 0)
	mulDivByAllocLimb  = newDecimal(u128{lo: maxUint64}, false, 0)
	mulDivByAllocMax19 = newDecimal(u128{hi: maxUint64, lo: maxUint64}, false, MaxPrec)
	// (7·2^128 - 2)/10: times 10^1 over 7 it is the quotient 2^128-1 with
	// remainder 5, which fits but overflows when rounded up.
	mulDivByAllocPastMax = func() Decimal {
		n := new(big.Int).Lsh(big.NewInt(7), 128)
		n.Sub(n, big.NewInt(2))
		n.Quo(n, big.NewInt(10))
		return newDecimal(exactU128FromBig(n), false, 0)
	}()
)

// TestMulDivRoundByAllocs gates MulDivRoundBy at exactly zero allocations on
// every arm (narrow, inline, both widths of the outlined arm, and a negative
// scale gap whose scaled divisor fits 128 bits or does not, dividing by the
// power of ten last or first), on each overflow (a scaled product past 256
// bits, a quotient past 128 bits from the inline bound or from a two-limb
// operand, and a quotient of 2^128-1 rounded up), on each validation error
// (places, mode, a nil and a zero Divisor) and on a zero operand.
func TestMulDivRoundByAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	tests := []struct {
		name string
		fn   func()
	}{
		{name: "narrow", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivAmount.MulDivRoundBy(mulDivPart, &mulDivByWholeDiv, 2, ToNearestEven)
		}},
		{name: "inline", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivAmount.MulDivRoundBy(mulDivPart, &mulDivByWholeDiv, 19, ToNearestEven)
		}},
		{name: "wide_one_limb", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivByAmount19.MulDivRoundBy(mulDivPart, &mulDivByWholeDiv, 19, ToNearestEven)
		}},
		{name: "wide_two_limb", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivAmount.MulDivRoundBy(mulDivByRate, &divisorWideDivisorDiv, 2, ToNearestEven)
		}},
		{name: "scaled_divisor_fits", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivWideA.MulDivRoundBy(mulDivWideB, &mulDivByWholeDiv, 2, ToNearestEven)
		}},
		{name: "scaled_divisor_wide", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivTiny.MulDivRoundBy(mulDivTiny, &mulDivByHugeDiv, 0, AwayFromZero)
		}},
		{name: "scaled_divisor_wide_pow10", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivByAllocMax19.MulDivRoundBy(mulDivByAllocMax19, &mulDivByAllocSeven, 0, ToNearestEven)
		}},
		{name: "overflow_scaled_product", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivByAllocMax.MulDivRoundBy(mulDivByAllocMax, &mulDivByAllocThree, MaxPrec, TowardZero)
		}},
		{name: "overflow_inline_bound", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivByAllocLimb.MulDivRoundBy(mulDivByAllocLimb, &mulDivByAllocThree, MaxPrec, TowardZero)
		}},
		{name: "overflow_quotient", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivByAllocMax.MulDivRoundBy(mulDivByAllocOne, &mulDivByAllocTiny, 0, TowardZero)
		}},
		{name: "overflow_round_up", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivByAllocPastMax.MulDivRoundBy(mulDivByAllocOne, &mulDivByAllocSeven, 1, AwayFromZero)
		}},
		{name: "places", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivAmount.MulDivRoundBy(mulDivPart, &mulDivByWholeDiv, MaxPrec+1, ToNearestEven)
		}},
		{name: "mode", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivAmount.MulDivRoundBy(mulDivPart, &mulDivByWholeDiv, 2, TowardNegative+1)
		}},
		{name: "nil_divisor", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivAmount.MulDivRoundBy(mulDivPart, mulDivByAllocNil, 2, ToNearestEven)
		}},
		{name: "zero_divisor", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivAmount.MulDivRoundBy(mulDivPart, &mulDivByAllocZero, 2, ToNearestEven)
		}},
		{name: "zero_operand", fn: func() {
			mulDivByAllocResult, errMulDivByAlloc = mulDivZero.MulDivRoundBy(mulDivPart, &mulDivByWholeDiv, 2, ToNearestEven)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, 0, tc.fn)
		})
	}
}

// TestMulDivRoundByAllocShapes pins each allocation-gate fixture (and with it
// each BenchmarkMulDivRoundBy shape) to the arm or error its name claims, so
// a fixture change cannot silently retarget a gate.
func TestMulDivRoundByAllocShapes(t *testing.T) {
	t.Parallel()
	arms := []struct {
		name   string
		d, e   Decimal
		v      *Divisor
		places uint8
	}{
		{"narrow", mulDivAmount, mulDivPart, &mulDivByWholeDiv, 2},
		{"inline", mulDivAmount, mulDivPart, &mulDivByWholeDiv, 16},
		{"inline", mulDivAmount, mulDivPart, &mulDivByWholeDiv, 19},
		{"wide_one_limb", mulDivByAmount19, mulDivPart, &mulDivByWholeDiv, 19},
		{"wide_two_limb", mulDivAmount, mulDivByRate, &divisorWideDivisorDiv, 2},
		{"scaled_divisor_fits", mulDivWideA, mulDivWideB, &mulDivByWholeDiv, 2},
		{"scaled_divisor_wide", mulDivTiny, mulDivTiny, &mulDivByHugeDiv, 0},
		{"scaled_divisor_wide_pow10", mulDivByAllocMax19, mulDivByAllocMax19, &mulDivByAllocSeven, 0},
		{"narrow", mulDivByAmounts[0], divisorPart, &divisorWholeDiv, 2},
		{"inline", mulDivByAmounts[0], divisorPart, &divisorWholeDiv, 19},
	}
	for _, a := range arms {
		require.Equalf(t, a.name, mulDivByPath(a.d, a.e, a.v, a.places), "d=%v e=%v f=%v places=%d", a.d, a.e, a.v.Decimal(), a.places)
		_, err := a.d.MulDivRoundBy(a.e, a.v, a.places, ToNearestEven)
		require.NoError(t, err)
	}
	errs := []struct {
		name   string
		d, e   Decimal
		v      *Divisor
		places uint8
		mode   RoundingMode
		want   error
	}{
		{"overflow_scaled_product", mulDivByAllocMax, mulDivByAllocMax, &mulDivByAllocThree, MaxPrec, TowardZero, ErrOverflow},
		{"overflow_inline_bound", mulDivByAllocLimb, mulDivByAllocLimb, &mulDivByAllocThree, MaxPrec, TowardZero, ErrOverflow},
		{"overflow_quotient", mulDivByAllocMax, mulDivByAllocOne, &mulDivByAllocTiny, 0, TowardZero, ErrOverflow},
		{"overflow_round_up", mulDivByAllocPastMax, mulDivByAllocOne, &mulDivByAllocSeven, 1, AwayFromZero, ErrOverflow},
		{"places", mulDivAmount, mulDivPart, &mulDivByWholeDiv, MaxPrec + 1, ToNearestEven, ErrPrecOutOfRange},
		{"mode", mulDivAmount, mulDivPart, &mulDivByWholeDiv, 2, TowardNegative + 1, ErrInvalidRoundingMode},
		{"nil_divisor", mulDivAmount, mulDivPart, mulDivByAllocNil, 2, ToNearestEven, ErrDivideByZero},
		{"zero_divisor", mulDivAmount, mulDivPart, &mulDivByAllocZero, 2, ToNearestEven, ErrDivideByZero},
	}
	for _, c := range errs {
		_, err := c.d.MulDivRoundBy(c.e, c.v, c.places, c.mode)
		require.ErrorIsf(t, err, c.want, "fixture %s", c.name)
	}
	// The scaled product of overflow_inline_bound has n2 ≥ c, so it reaches
	// the outlined arm's fits test; overflow_scaled_product passes 2^256
	// before dividing; overflow_round_up fits but rounds 2^128-1 up.
	require.Equal(t, "wide_one_limb", mulDivByPath(mulDivByAllocLimb, mulDivByAllocLimb, &mulDivByAllocThree, MaxPrec))
	require.Equal(t, "wide_one_limb", mulDivByPath(mulDivByAllocPastMax, mulDivByAllocOne, &mulDivByAllocSeven, 1))
	got, err := mulDivByAllocPastMax.MulDivRoundBy(mulDivByAllocOne, &mulDivByAllocSeven, 1, TowardZero)
	require.NoError(t, err)
	require.Equal(t, u128{hi: maxUint64, lo: maxUint64}, got.coef, "overflow_round_up must truncate to 2^128-1")
	_, err = mulDivZero.MulDivRoundBy(mulDivPart, &mulDivByWholeDiv, 2, ToNearestEven)
	require.NoError(t, err)
}
