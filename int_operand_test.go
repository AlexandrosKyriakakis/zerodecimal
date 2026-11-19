package zerodecimal

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// intOperandEdges are the int64 operands every identity sweep crosses: zero,
// small magnitudes and powers of ten of both signs (3, 100 and 1000 are the
// benchmark divisors), the int32 extremes, and both int64 extremes. MinInt64's
// magnitude 2^63 has no int64 negation, so it pins the two's-complement
// magnitude.
var intOperandEdges = []int64{
	0, 1, -1, 2, -2, 3, -3, 10, -10, 100, -100, 1000, -1000,
	math.MaxInt32, -math.MaxInt32, math.MinInt32,
	math.MaxInt64, -math.MaxInt64, math.MinInt64,
}

// intOperandPanicValue runs fn and returns the recovered panic value, nil when
// fn returned normally. It mirrors fuzzPanicValue, which compiles only under
// the fuzz tag.
func intOperandPanicValue(fn func()) (pv any) {
	defer func() { pv = recover() }()
	fn()
	return nil
}

// requireIntOperandIdentity holds MulInt and DivInt to their contract, the
// representation and bare sentinel of Mul and Div with NewFromInt(n), and
// MustMulInt and MustDivInt to the Must-twin contract: a panic exactly when
// the erroring form fails, whose value is that sentinel, and otherwise the
// same result.
func requireIntOperandIdentity(t *testing.T, d Decimal, n int64) {
	t.Helper()
	e := NewFromInt(n)

	want, wantErr := d.Mul(e)
	got, err := d.MulInt(n)
	require.Equalf(t, wantErr, err, "MulInt error: d=%+v n=%d", d, n)
	require.Equalf(t, want, got, "MulInt result: d=%+v n=%d", d, n)
	var must Decimal
	pv := intOperandPanicValue(func() { must = d.MustMulInt(n) })
	requireIntOperandTwin(t, "MustMulInt", wantErr, pv, want, must, d, n)

	want, wantErr = d.Div(e)
	got, err = d.DivInt(n)
	require.Equalf(t, wantErr, err, "DivInt error: d=%+v n=%d", d, n)
	require.Equalf(t, want, got, "DivInt result: d=%+v n=%d", d, n)
	if n != 0 {
		require.NoErrorf(t, err, "DivInt fails only on a zero divisor: d=%+v n=%d", d, n)
	}
	pv = intOperandPanicValue(func() { must = d.MustDivInt(n) })
	requireIntOperandTwin(t, "MustDivInt", wantErr, pv, want, must, d, n)
}

// requireIntOperandTwin asserts the Must-twin contract for one call: no panic
// and the erroring form's result on success, else a panic with its sentinel.
func requireIntOperandTwin(t *testing.T, name string, wantErr error, pv any, want, got, d Decimal, n int64) {
	t.Helper()
	if wantErr == nil {
		require.Nilf(t, pv, "%s panicked on success: d=%+v n=%d", name, d, n)
		require.Equalf(t, want, got, "%s result: d=%+v n=%d", name, d, n)
		return
	}
	require.Equalf(t, wantErr, pv, "%s must panic with the bare sentinel: d=%+v n=%d", name, d, n)
}

// randIntOperand returns a nonzero int64 of either sign whose magnitude has a
// bit length uniform in 1..63.
func randIntOperand(rng *rand.Rand) int64 {
	n := int64(shapedCoef(rng, 63).lo)
	if rng.IntN(2) == 0 {
		n = -n
	}
	return n
}

// TestIntOperandIdentity sweeps every precision with zero, the 2^128-1
// ceiling of both signs and coefficients whose bit length is uniform in
// 1..128, crossed with the edge operands and random ones. Under the default
// tag this reaches every DivInt branch: one-limb and two-limb numerators, the
// 192-bit numerator whose quotient still fits, and the delegated degradation
// path. Under zerodecimal_prec9 and zerodecimal_prec12 it also covers the
// delegation for d.Prec() > DefaultPrec.
func TestIntOperandIdentity(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6d756c696e74, 0x646976696e74))
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	for prec := range MaxPrec + 1 {
		ds := []Decimal{{}, newDecimal(maxCoef, false, prec), newDecimal(maxCoef, true, prec)}
		for range 150 {
			ds = append(ds, shapedDecimal(rng, 128, prec))
		}
		for _, d := range ds {
			for _, n := range intOperandEdges {
				requireIntOperandIdentity(t, d, n)
			}
			for range 4 {
				requireIntOperandIdentity(t, d, randIntOperand(rng))
			}
		}
	}
}

// TestIntOperandOracle ties the int-operand calls to the cross-check oracles
// rather than to Mul and Div alone: checkMul and checkDiv hold the Decimal
// forms to big.Int and shopspring on the same operands, and the identity makes
// that hold for MulInt and DivInt too.
func TestIntOperandOracle(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6f7261636c65, 0x696e74))
	for i := range 4_000 {
		d := shapedDecimal(rng, [...]int{20, 64, 96, 128}[i%4], uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		n := randIntOperand(rng)
		if i%5 == 0 {
			n = intOperandEdges[rng.IntN(len(intOperandEdges))]
		}
		requireIntOperandIdentity(t, d, n)
		dv, nv := newCrossVal(d), newCrossVal(NewFromInt(n))
		checkMul(t, dv, nv)
		checkDiv(t, dv, nv)
	}
}

// TestMulIntBoundaries pins MulInt results that hold under every build tag
// (each d has at most 2 places, below every DefaultPrec): canonical zero from
// either zero operand, MinInt64's magnitude, the 2^128 overflow edge from
// both sides, and that d's precision is kept rather than trimmed.
func TestMulIntBoundaries(t *testing.T) {
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	// 3 divides 2^128-1 = (2^64-1)(2^64+1) because 2^64 ≡ 1 (mod 3).
	third, _ := quoRem64(maxCoef, 3)
	thirdUp := inc128(third)
	tests := []struct {
		name    string
		d       Decimal
		n       int64
		want    Decimal
		wantErr error
	}{
		{name: "zero_times_negative_is_canonical", d: Decimal{}, n: -7, want: Decimal{}},
		{name: "negative_times_zero_is_canonical", d: MustNew(-125, -2), n: 0, want: Decimal{}},
		{name: "keeps_precision", d: MustNew(123, -2), n: 100, want: Decimal{coef: u128{lo: 12_300}, prec: 2}},
		{name: "negative_operand_flips_sign", d: MustNew(-125, -2), n: -4, want: Decimal{coef: u128{lo: 500}, prec: 2}},
		{name: "min_int64", d: NewFromInt(1), n: math.MinInt64, want: Decimal{coef: u128{lo: 1 << 63}, neg: true}},
		// 15·2^63 = 7·2^64 + 2^63: MinInt64's magnitude carries into the hi limb.
		{name: "negative_times_min_int64", d: MustNew(-15, -1), n: math.MinInt64, want: Decimal{coef: u128{hi: 7, lo: 1 << 63}, prec: 1}},
		{name: "two_limb_times_max_int64", d: Decimal{coef: u128{hi: 1}}, n: math.MaxInt64, want: Decimal{coef: u128{hi: math.MaxInt64}}},
		{name: "product_fills_coefficient", d: Decimal{coef: third}, n: 3, want: Decimal{coef: maxCoef}},
		{name: "negative_product_fills_coefficient", d: Decimal{coef: third, prec: 2}, n: -3, want: Decimal{coef: maxCoef, neg: true, prec: 2}},
		{name: "product_one_past_coefficient_overflows", d: Decimal{coef: thirdUp}, n: 3, wantErr: ErrOverflow},
		{name: "two_pow_127_times_two_overflows", d: Decimal{coef: u128{hi: 1 << 63}, neg: true}, n: 2, wantErr: ErrOverflow},
		{name: "two_pow_64_times_min_int64_fits", d: Decimal{coef: u128{hi: 1}}, n: math.MinInt64, want: Decimal{coef: u128{hi: 1 << 63}, neg: true}},
		{name: "two_pow_65_times_min_int64_overflows", d: Decimal{coef: u128{hi: 2}}, n: math.MinInt64, wantErr: ErrOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.d.MulInt(tc.n)
			require.Equal(t, tc.wantErr, err)
			require.Equal(t, tc.want, got)
			requireIntOperandIdentity(t, tc.d, tc.n)
			checkMul(t, newCrossVal(tc.d), newCrossVal(NewFromInt(tc.n)))
		})
	}
}

// TestDivIntBoundaries pins DivInt results, stated in terms of DefaultPrec so
// they hold under every build tag (each d has at most 1 place). The two
// "2^128" rows straddle the DefaultPrec fit edge exactly: d = 2^(128-P) at
// P = DefaultPrec divided by 5^P has the quotient 2^128 at P places, one past
// the largest coefficient, so Div lowers the precision; one unit less
// quotes 2^128-2^P through the 192-bit numerator path.
func TestDivIntBoundaries(t *testing.T) {
	pow5 := int64(1)
	for range DefaultPrec {
		pow5 *= 5
	}
	edge := u128{hi: 1 << (64 - DefaultPrec)} // 2^(128-DefaultPrec)
	belowEdge, _ := sub128(edge, u128{lo: 1})
	justFits, _ := sub128(u128{}, u128{lo: 1 << DefaultPrec}) // 2^128 - 2^DefaultPrec
	// At DefaultPrec-1 places the quotient is 2^127·5^(P-1)/5^P = 2^127/5.
	degraded, _ := quoRem64(u128{hi: 1 << 63}, 5)
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	atDefault := func(s string) Decimal { return RequireFromString(s).MustRescale(DefaultPrec) }
	tests := []struct {
		name    string
		d       Decimal
		n       int64
		want    Decimal
		wantErr error
	}{
		{name: "by_zero", d: NewFromInt(1), n: 0, wantErr: ErrDivideByZero},
		{name: "zero_by_zero", d: Decimal{}, n: 0, wantErr: ErrDivideByZero},
		{name: "zero_dividend_is_canonical", d: Decimal{}, n: -7, want: Decimal{}},
		{name: "even_split", d: MustNew(126, -1), n: 3, want: atDefault("4.2")},
		{name: "negative_divisor", d: MustNew(126, -1), n: -3, want: atDefault("-4.2")},
		{name: "quotient_truncates_to_canonical_zero", d: MustNew(5, -1), n: math.MaxInt64, want: Decimal{}},
		{name: "min_int64_divisor", d: Decimal{coef: u128{lo: 1 << 63}}, n: math.MinInt64, want: atDefault("-1")},
		{name: "min_int64_divides_negative", d: Decimal{coef: u128{lo: 1 << 63}, neg: true}, n: math.MinInt64, want: atDefault("1")},
		{name: "wide_numerator_quotient_fits", d: Decimal{coef: belowEdge}, n: pow5, want: Decimal{coef: justFits, prec: DefaultPrec}},
		{name: "quotient_2_128_lowers_precision", d: Decimal{coef: edge}, n: pow5, want: Decimal{coef: degraded, prec: DefaultPrec - 1}},
		{name: "max_coefficient_by_one_keeps_integer", d: Decimal{coef: maxCoef}, n: 1, want: Decimal{coef: maxCoef}},
		{name: "max_coefficient_by_minus_one", d: Decimal{coef: maxCoef}, n: -1, want: Decimal{coef: maxCoef, neg: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.d.DivInt(tc.n)
			require.Equal(t, tc.wantErr, err)
			require.Equal(t, tc.want, got)
			requireIntOperandIdentity(t, tc.d, tc.n)
			checkDiv(t, newCrossVal(tc.d), newCrossVal(NewFromInt(tc.n)))
		})
	}
}

// TestDivIntNeverOverflows checks the documented error contract on its worst
// case: a nonzero int64 never enlarges a magnitude, so even the 2^128-1
// ceiling at precision 0, divided by every edge operand and by random ones,
// yields a quotient (here a nonzero one) instead of ErrOverflow.
func TestDivIntNeverOverflows(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x6e6f6f76, 0x6466))
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	ns := append([]int64(nil), intOperandEdges...)
	for range 1_000 {
		ns = append(ns, randIntOperand(rng))
	}
	for _, d := range []Decimal{{coef: maxCoef}, {coef: maxCoef, neg: true}, {coef: maxCoef, prec: MaxPrec}} {
		for _, n := range ns {
			if n == 0 {
				continue
			}
			q, err := d.DivInt(n)
			require.NoErrorf(t, err, "d=%+v n=%d", d, n)
			require.Falsef(t, q.IsZero(), "d=%+v n=%d", d, n)
		}
	}
}
