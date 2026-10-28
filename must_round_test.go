package zerodecimal

import (
	"math/rand/v2"
	"os/exec"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Must twins of the direct-round operations follow the FuzzMustTwins
// contract: a twin panics exactly when its erroring form returns an error, the
// panic value is that bare sentinel, and a success is representation-identical
// to the erroring form's result. These tests pin the contract on tables, on
// explicit values that do not trust the erroring form, and on random operands;
// FuzzMustRoundTwins (operand pairs) and FuzzMustRoundTwinsTriples (operand
// triples), in must_round_fuzz_test.go, replay it under the fuzzer.

var (
	// mustRoundMax is the largest magnitude, 2^128-1 at precision 0.
	mustRoundMax = Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	// mustRoundTiny is the smallest quantum, 10^-MaxPrec.
	mustRoundTiny = Decimal{coef: u128{lo: 1}, prec: MaxPrec}
)

// mustRoundOp binds one Must twin to its erroring form over the common operand
// tuple (a, b, c): the two-operand MulRound and DivRound ignore c, the
// three-operand MulAddRound and MulDivRound use all of them, and AvgRound
// averages a, b, c. operands is how many of the tuple an op consumes, so the
// fuzz targets can drive pairs for the former and triples for the latter.
type mustRoundOp struct {
	name     string
	operands int
	twin     func(a, b, c Decimal, places uint8, mode RoundingMode) (Decimal, error)
	must     func(a, b, c Decimal, places uint8, mode RoundingMode) Decimal
}

var mustRoundOps = []mustRoundOp{
	{
		name:     "MulRound",
		operands: 2,
		twin:     func(a, b, _ Decimal, p uint8, m RoundingMode) (Decimal, error) { return a.MulRound(b, p, m) },
		must:     func(a, b, _ Decimal, p uint8, m RoundingMode) Decimal { return a.MustMulRound(b, p, m) },
	},
	{
		name:     "DivRound",
		operands: 2,
		twin:     func(a, b, _ Decimal, p uint8, m RoundingMode) (Decimal, error) { return a.DivRound(b, p, m) },
		must:     func(a, b, _ Decimal, p uint8, m RoundingMode) Decimal { return a.MustDivRound(b, p, m) },
	},
	{
		name:     "MulAddRound",
		operands: 3,
		twin:     func(a, b, c Decimal, p uint8, m RoundingMode) (Decimal, error) { return a.MulAddRound(b, c, p, m) },
		must:     func(a, b, c Decimal, p uint8, m RoundingMode) Decimal { return a.MustMulAddRound(b, c, p, m) },
	},
	{
		name:     "MulDivRound",
		operands: 3,
		twin:     func(a, b, c Decimal, p uint8, m RoundingMode) (Decimal, error) { return a.MulDivRound(b, c, p, m) },
		must:     func(a, b, c Decimal, p uint8, m RoundingMode) Decimal { return a.MustMulDivRound(b, c, p, m) },
	},
	{
		name:     "AvgRound",
		operands: 3,
		twin:     func(a, b, c Decimal, p uint8, m RoundingMode) (Decimal, error) { return AvgRound(a, p, m, b, c) },
		must:     func(a, b, c Decimal, p uint8, m RoundingMode) Decimal { return MustAvgRound(a, p, m, b, c) },
	},
}

// mustRoundPanicValue runs fn and returns the recovered panic value, nil when
// fn returned normally. Library panics always carry a sentinel error, so nil is
// unambiguous.
func mustRoundPanicValue(fn func()) (pv any) {
	defer func() { pv = recover() }()
	fn()
	return nil
}

// requireMustRoundPanicsWith asserts fn panics with exactly the sentinel want, not
// a wrapped or recreated error: the library returns its sentinels bare.
func requireMustRoundPanicsWith(t *testing.T, want error, fn func(), ctx ...any) {
	t.Helper()
	pv := mustRoundPanicValue(fn)
	require.NotNilf(t, pv, "must twin must panic with %v: %v", want, ctx)
	err, ok := pv.(error)
	require.Truef(t, ok, "panic value must be an error, got %T (%v): %v", pv, pv, ctx)
	require.Samef(t, want, err, "panic value must be the bare sentinel %v, got %v: %v", want, err, ctx)
}

// requireMustRoundTwin holds op's Must form to the twin contract on one input
// and returns the erroring form's error (nil on success) so callers can count
// which outcomes they exercised.
func requireMustRoundTwin(t *testing.T, op mustRoundOp, a, b, c Decimal, places uint8, mode RoundingMode) error {
	t.Helper()
	want, wantErr := op.twin(a, b, c, places, mode)
	var got Decimal
	pv := mustRoundPanicValue(func() { got = op.must(a, b, c, places, mode) })
	if wantErr == nil {
		require.Nilf(t, pv, "%s: must twin panicked on success: pv=%v a=%+v b=%+v c=%+v places=%d mode=%d",
			op.name, pv, a, b, c, places, mode)
		require.Equalf(t, want, got, "%s: must twin result: a=%+v b=%+v c=%+v places=%d mode=%d",
			op.name, a, b, c, places, mode)
		return nil
	}
	require.NotNilf(t, pv, "%s: must twin must panic on %v: a=%+v b=%+v c=%+v places=%d mode=%d",
		op.name, wantErr, a, b, c, places, mode)
	perr, ok := pv.(error)
	require.Truef(t, ok, "%s: panic value must be an error, got %T (%v)", op.name, pv, pv)
	require.Samef(t, wantErr, perr, "%s: panic value must be the bare sentinel %v, got %v: a=%+v b=%+v c=%+v places=%d mode=%d",
		op.name, wantErr, perr, a, b, c, places, mode)
	return wantErr
}

// TestMustRoundTwinsResults pins each twin to explicit values worked out by
// hand, so a twin and its erroring form cannot be wrong together. The tie
// tables walk all six modes on both signs, which catches a twin that drops or
// hard-codes its mode; the asymmetric operands catch a swapped operand; the
// representations (precision places on a nonzero result, canonical zero on a
// zero) are compared exactly, not by value.
func TestMustRoundTwinsResults(t *testing.T) {
	t.Parallel()
	rs := RequireFromString
	price, qty := rs("19.99"), rs("1.5")

	// 1.5 × 19.99 = 29.985 and 1 / 8 = 0.125 are exact ties at two places. The
	// arrays follow the RoundingMode order: ToNearestAway, ToNearestEven,
	// AwayFromZero, TowardZero, TowardPositive, TowardNegative. 2998 and 12 are
	// even, so ToNearestEven keeps them.
	mulTiePos := [...]int64{2999, 2998, 2999, 2998, 2999, 2998}
	mulTieNeg := [...]int64{-2999, -2998, -2999, -2998, -2998, -2999}
	divTiePos := [...]int64{13, 12, 13, 12, 13, 12}
	divTieNeg := [...]int64{-13, -12, -13, -12, -12, -13}

	cases := []struct {
		name string
		got  func() Decimal
		want Decimal
	}{
		{"MustMulRound/scale_up_keeps_places", func() Decimal { return rs("3").MustMulRound(rs("0.5"), 3, TowardZero) }, MustNew(1500, -3)},
		{"MustMulRound/rounds_to_canonical_zero", func() Decimal { return rs("0.001").MustMulRound(rs("0.001"), 2, ToNearestEven) }, Decimal{}},
		{"MustMulRound/away_from_zero_rounds_up_to_one_unit", func() Decimal { return rs("0.001").MustMulRound(rs("0.001"), 2, AwayFromZero) }, MustNew(1, -2)},
		{"MustMulRound/zero_operand", func() Decimal { return Decimal{}.MustMulRound(price, 2, ToNearestEven) }, Decimal{}},
		{"MustMulRound/largest_magnitude", func() Decimal { return mustRoundMax.MustMulRound(NewFromInt(1), 0, TowardZero) }, mustRoundMax},
		{"MustMulRound/negative_ties_down_to_even", func() Decimal { return rs("-2.5").MustMulRound(NewFromInt(1), 0, ToNearestEven) }, NewFromInt(-2)},
		{"MustMulRound/negative_toward_negative", func() Decimal { return rs("-2.5").MustMulRound(NewFromInt(1), 0, TowardNegative) }, NewFromInt(-3)},

		{"MustDivRound/share_rounds_up", func() Decimal { return rs("10.01").MustDivRound(NewFromInt(3), 2, ToNearestEven) }, MustNew(334, -2)},
		{"MustDivRound/share_toward_zero", func() Decimal { return rs("10.01").MustDivRound(NewFromInt(3), 2, TowardZero) }, MustNew(333, -2)},
		{"MustDivRound/dividend_first", func() Decimal { return NewFromInt(1).MustDivRound(NewFromInt(8), 3, ToNearestEven) }, MustNew(125, -3)},
		{"MustDivRound/divisor_first", func() Decimal { return NewFromInt(8).MustDivRound(NewFromInt(1), 3, ToNearestEven) }, MustNew(8000, -3)},
		{"MustDivRound/zero_dividend", func() Decimal { return Decimal{}.MustDivRound(NewFromInt(3), 2, ToNearestEven) }, Decimal{}},
		{"MustDivRound/negative_divisor", func() Decimal { return NewFromInt(1).MustDivRound(NewFromInt(-3), 4, TowardZero) }, MustNew(-3333, -4)},

		// One rounding of the exact 29.989 gives 29.99; MulRound, Add and a
		// second rounding give 29.98.
		{"MustMulAddRound/single_rounding", func() Decimal { return qty.MustMulAddRound(price, rs("0.004"), 2, ToNearestEven) }, MustNew(2999, -2)},
		// 1.5 × 0.004 + 19.99 = 19.996: swapping e and f changes the value.
		{"MustMulAddRound/addend_last", func() Decimal { return qty.MustMulAddRound(rs("0.004"), price, 2, ToNearestEven) }, MustNew(2000, -2)},
		{"MustMulAddRound/cancels_to_canonical_zero", func() Decimal { return rs("1.25").MustMulAddRound(NewFromInt(4), NewFromInt(-5), 2, ToNearestEven) }, Decimal{}},
		{"MustMulAddRound/scale_up_keeps_places", func() Decimal { return NewFromInt(12).MustMulAddRound(NewFromInt(3), rs("1.5"), 4, TowardZero) }, MustNew(375000, -4)},

		// 10.01 × 1 / 3 = 3.33666...; swapping e and f gives 30.03.
		{"MustMulDivRound/pro_rata_share", func() Decimal { return rs("10.01").MustMulDivRound(NewFromInt(1), NewFromInt(3), 2, ToNearestEven) }, MustNew(334, -2)},
		{"MustMulDivRound/product_first", func() Decimal { return rs("10.01").MustMulDivRound(NewFromInt(3), NewFromInt(1), 2, ToNearestEven) }, MustNew(3003, -2)},
		// A 19-place rate times cents has no exact Decimal product (MulExact fails).
		{"MustMulDivRound/rate_without_exact_product", func() Decimal {
			return rs("10.01").MustMulDivRound(rs("1.0837000000000000001"), NewFromInt(1), 2, ToNearestEven)
		}, MustNew(1085, -2)},
		{"MustMulDivRound/keeps_places_on_a_whole_share", func() Decimal { return NewFromInt(10).MustMulDivRound(NewFromInt(3), NewFromInt(1), 2, ToNearestEven) }, MustNew(3000, -2)},
		{"MustMulDivRound/zero_numerator", func() Decimal { return Decimal{}.MustMulDivRound(NewFromInt(2), NewFromInt(3), 2, ToNearestEven) }, Decimal{}},

		{"MustAvgRound/single_value_ties_to_even", func() Decimal { return MustAvgRound(rs("1.005"), 2, ToNearestEven) }, MustNew(100, -2)},
		{"MustAvgRound/single_value_ties_away", func() Decimal { return MustAvgRound(rs("1.005"), 2, ToNearestAway) }, MustNew(101, -2)},
		{"MustAvgRound/single_value_scales_up", func() Decimal { return MustAvgRound(rs("2.5"), 3, ToNearestEven) }, MustNew(2500, -3)},
		{"MustAvgRound/two_values_tie_to_even", func() Decimal { return MustAvgRound(NewFromInt(2), 0, ToNearestEven, NewFromInt(3)) }, NewFromInt(2)},
		{"MustAvgRound/two_values_tie_up_to_even", func() Decimal { return MustAvgRound(NewFromInt(1), 0, ToNearestEven, NewFromInt(2)) }, NewFromInt(2)},
		{"MustAvgRound/three_values_one_rounding", func() Decimal { return MustAvgRound(NewFromInt(1), 2, ToNearestEven, NewFromInt(2), NewFromInt(4)) }, MustNew(233, -2)},
		{"MustAvgRound/cancels_to_canonical_zero", func() Decimal { return MustAvgRound(rs("1.5"), 2, ToNearestEven, rs("-1.5")) }, Decimal{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got Decimal
			require.NotPanics(t, func() { got = tc.got() })
			require.Equal(t, tc.want, got)
		})
	}

	for mode := ToNearestAway; mode <= TowardNegative; mode++ {
		t.Run("ties/"+mustRoundModeName(mode), func(t *testing.T) {
			var got [8]Decimal
			require.NotPanics(t, func() {
				got = [8]Decimal{
					price.MustMulRound(qty, 2, mode),
					price.Neg().MustMulRound(qty, 2, mode),
					NewFromInt(1).MustDivRound(NewFromInt(8), 2, mode),
					NewFromInt(-1).MustDivRound(NewFromInt(8), 2, mode),
					// The same ties through the fused forms: 29.985 + 0 and
					// 0.125 × 1 / 1.
					qty.MustMulAddRound(price, Decimal{}, 2, mode),
					rs("-0.125").MustMulDivRound(NewFromInt(1), NewFromInt(1), 2, mode),
					MustAvgRound(rs("29.98"), 2, mode, rs("29.99")), // 29.985
					MustAvgRound(rs("0.12"), 2, mode, rs("0.13")),   // 0.125
				}
			})
			require.Equal(t, MustNew(mulTiePos[mode], -2), got[0], "MustMulRound")
			require.Equal(t, MustNew(mulTieNeg[mode], -2), got[1], "MustMulRound, negative")
			require.Equal(t, MustNew(divTiePos[mode], -2), got[2], "MustDivRound")
			require.Equal(t, MustNew(divTieNeg[mode], -2), got[3], "MustDivRound, negative")
			require.Equal(t, MustNew(mulTiePos[mode], -2), got[4], "MustMulAddRound")
			require.Equal(t, MustNew(divTieNeg[mode], -2), got[5], "MustMulDivRound, negative")
			require.Equal(t, MustNew(mulTiePos[mode], -2), got[6], "MustAvgRound")
			require.Equal(t, MustNew(divTiePos[mode], -2), got[7], "MustAvgRound")
		})
	}
}

// mustRoundModeName names a rounding mode for subtest titles.
func mustRoundModeName(m RoundingMode) string {
	return [...]string{
		"ToNearestAway", "ToNearestEven", "AwayFromZero",
		"TowardZero", "TowardPositive", "TowardNegative",
	}[m]
}

// mustRoundPanicCase is one input that must make a twin panic with want.
type mustRoundPanicCase struct {
	name    string
	a, b, c Decimal
	places  uint8
	mode    RoundingMode
	want    error
}

// TestMustRoundTwinsPanicSentinels pins one panic per sentinel for every twin:
// ErrPrecOutOfRange for places past MaxPrec, ErrInvalidRoundingMode for an
// unknown mode, ErrDivideByZero for a zero divisor, and ErrOverflow for a
// result past 128 bits. Each panic value must be the bare sentinel the erroring
// form returns, and the validation order (places, then mode, then a zero
// divisor) must carry over unchanged.
func TestMustRoundTwinsPanicSentinels(t *testing.T) {
	t.Parallel()
	one, two, three := NewFromInt(1), NewFromInt(2), NewFromInt(3)
	const (
		badPlaces = MaxPrec + 1
		badMode   = TowardNegative + 1
	)

	// Shared by every twin: benign operands, one invalid argument at a time.
	common := []mustRoundPanicCase{
		{"places_out_of_range", one, two, three, badPlaces, ToNearestEven, ErrPrecOutOfRange},
		{"places_255", one, two, three, 255, ToNearestEven, ErrPrecOutOfRange},
		{"mode_out_of_range", one, two, three, 2, badMode, ErrInvalidRoundingMode},
		{"mode_255", one, two, three, 2, RoundingMode(255), ErrInvalidRoundingMode},
		{"places_checked_before_mode", one, two, three, badPlaces, badMode, ErrPrecOutOfRange},
	}
	extra := map[string][]mustRoundPanicCase{
		"MulRound": {
			{"overflow_max_times_max", mustRoundMax, mustRoundMax, Decimal{}, 0, ToNearestEven, ErrOverflow},
			{"overflow_rescaled_to_places", mustRoundMax, one, Decimal{}, 1, TowardZero, ErrOverflow},
		},
		"DivRound": {
			{"zero_divisor", one, Decimal{}, Decimal{}, 2, ToNearestEven, ErrDivideByZero},
			{"zero_over_zero", Decimal{}, Decimal{}, Decimal{}, 2, ToNearestEven, ErrDivideByZero},
			{"places_checked_before_zero_divisor", one, Decimal{}, Decimal{}, badPlaces, ToNearestEven, ErrPrecOutOfRange},
			{"mode_checked_before_zero_divisor", one, Decimal{}, Decimal{}, 2, badMode, ErrInvalidRoundingMode},
			{"overflow_tiny_divisor", mustRoundMax, mustRoundTiny, Decimal{}, 0, ToNearestEven, ErrOverflow},
			{"overflow_rescaled_to_places", mustRoundMax, one, Decimal{}, 1, TowardZero, ErrOverflow},
		},
		"MulAddRound": {
			{"overflow_product", mustRoundMax, mustRoundMax, Decimal{}, 0, ToNearestEven, ErrOverflow},
			{"overflow_sum", mustRoundMax, one, mustRoundMax, 0, ToNearestEven, ErrOverflow},
			{"overflow_rescaled_to_places", mustRoundMax, one, Decimal{}, 1, TowardZero, ErrOverflow},
		},
		"MulDivRound": {
			{"zero_divisor", one, two, Decimal{}, 2, ToNearestEven, ErrDivideByZero},
			{"zero_numerator_zero_divisor", Decimal{}, two, Decimal{}, 2, ToNearestEven, ErrDivideByZero},
			{"places_checked_before_zero_divisor", one, two, Decimal{}, badPlaces, ToNearestEven, ErrPrecOutOfRange},
			{"mode_checked_before_zero_divisor", one, two, Decimal{}, 2, badMode, ErrInvalidRoundingMode},
			{"overflow_product", mustRoundMax, mustRoundMax, one, 0, ToNearestEven, ErrOverflow},
			{"overflow_rescaled_to_places", mustRoundMax, one, one, 1, TowardZero, ErrOverflow},
		},
		"AvgRound": {
			{"overflow_rescaled_to_places", mustRoundMax, mustRoundMax, mustRoundMax, 1, TowardZero, ErrOverflow},
		},
	}

	// A misspelled key would silently drop its cases.
	opNames := make([]string, 0, len(mustRoundOps))
	for _, op := range mustRoundOps {
		opNames = append(opNames, op.name)
	}
	for name := range extra {
		require.Contains(t, opNames, name)
	}

	for _, op := range mustRoundOps {
		t.Run(op.name, func(t *testing.T) {
			for _, tc := range append(append([]mustRoundPanicCase(nil), common...), extra[op.name]...) {
				t.Run(tc.name, func(t *testing.T) {
					got, err := op.twin(tc.a, tc.b, tc.c, tc.places, tc.mode)
					require.ErrorIs(t, err, tc.want, "the erroring form must fail with the case's sentinel")
					require.Equal(t, Decimal{}, got)
					requireMustRoundPanicsWith(t, tc.want, func() { op.must(tc.a, tc.b, tc.c, tc.places, tc.mode) }, tc)
				})
			}
		})
	}
}

// TestMustRoundTwinsNoPanicAtBoundaries pins the successes right next to the
// panics above: the same operands one step inside the contract must not panic.
func TestMustRoundTwinsNoPanicAtBoundaries(t *testing.T) {
	t.Parallel()
	one := NewFromInt(1)
	tests := []struct {
		name string
		run  func() Decimal
		want Decimal
	}{
		{"places_at_max_prec", func() Decimal { return one.MustMulRound(one, MaxPrec, ToNearestEven) }, Decimal{coef: u128{lo: 10_000_000_000_000_000_000}, prec: MaxPrec}},
		{"last_mode", func() Decimal { return one.MustDivRound(NewFromInt(3), 2, TowardNegative) }, MustNew(33, -2)},
		{"max_times_one", func() Decimal { return mustRoundMax.MustMulRound(one, 0, TowardZero) }, mustRoundMax},
		{"max_divided_by_one", func() Decimal { return mustRoundMax.MustDivRound(one, 0, TowardZero) }, mustRoundMax},
		{"max_plus_zero", func() Decimal { return mustRoundMax.MustMulAddRound(one, Decimal{}, 0, TowardZero) }, mustRoundMax},
		{"max_times_one_over_one", func() Decimal { return mustRoundMax.MustMulDivRound(one, one, 0, TowardZero) }, mustRoundMax},
		{"average_of_max", func() Decimal { return MustAvgRound(mustRoundMax, 0, TowardZero, mustRoundMax, mustRoundMax) }, mustRoundMax},
		// (2^128-1 + 2^128-1 - (2^128-1)) / 3: the running sum passes 128 bits, and
		// 2^128-1 is divisible by 3, so the mean is exact.
		{"average_through_wide_intermediate", func() Decimal {
			return MustAvgRound(mustRoundMax, 0, TowardZero, mustRoundMax, mustRoundMax.Neg())
		}, RequireFromString("113427455640312821154458202477256070485")},
		{"average_cancels_wide_intermediate", func() Decimal {
			return MustAvgRound(mustRoundMax, 0, TowardZero, mustRoundMax, mustRoundMax.Neg(), mustRoundMax.Neg())
		}, Decimal{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got Decimal
			require.NotPanics(t, func() { got = tc.run() })
			require.Equal(t, tc.want, got)
		})
	}
}

// TestMustAvgRoundVariadic covers the variadic tail: no extra operand, one,
// several, and a long slice, each against AvgRound and, for the three-value
// mean, against the explicit representation of 7/3 rounded once.
func TestMustAvgRoundVariadic(t *testing.T) {
	t.Parallel()
	long := make([]Decimal, 1024)
	for i := range long {
		long[i] = MustNew(int64(i%17)-8, -2)
	}
	tests := []struct {
		name   string
		first  Decimal
		rest   []Decimal
		places uint8
		mode   RoundingMode
	}{
		{"no_rest", RequireFromString("1.005"), nil, 2, ToNearestAway},
		{"one_rest", NewFromInt(1), []Decimal{NewFromInt(2)}, 1, ToNearestEven},
		{"several_rest", NewFromInt(1), []Decimal{NewFromInt(2), NewFromInt(4)}, 2, ToNearestEven},
		{"long", long[0], long[1:], 4, ToNearestEven},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := AvgRound(tc.first, tc.places, tc.mode, tc.rest...)
			require.NoError(t, err)
			var got Decimal
			require.NotPanics(t, func() { got = MustAvgRound(tc.first, tc.places, tc.mode, tc.rest...) })
			require.Equal(t, want, got)
		})
	}
	require.Equal(t, MustNew(233, -2), MustAvgRound(NewFromInt(1), 2, ToNearestEven, NewFromInt(2), NewFromInt(4)))
}

// TestMustRoundTwinsContractRandom replays the contract on random operands of
// every coefficient width, with places and mode drawn one step past their valid
// ranges so every sentinel is reachable, and requires each outcome to occur so
// the property cannot pass vacuously.
func TestMustRoundTwinsContractRandom(t *testing.T) {
	t.Parallel()
	type outcome struct {
		op  string
		err error
	}
	seen := map[outcome]int{}
	rng := rand.New(rand.NewPCG(0x6d757374, 0x726f756e64))
	widths := [...]int{8, 24, 40, 64, 96, 128}
	randPrec := func() uint8 { return uint8(rng.Uint64N(uint64(MaxPrec) + 1)) }
	for i := range 20_000 {
		a := shapedDecimal(rng, widths[i%6], randPrec())
		b := shapedDecimal(rng, widths[(i/6)%6], randPrec())
		c := shapedDecimal(rng, widths[(i/36)%6], randPrec())
		// Inject zeros so the zero-divisor panics and the zero-operand successes are
		// both common: b is DivRound's divisor, c is MulDivRound's divisor, and a is
		// every op's first operand (DivRound's dividend).
		if i%11 == 0 {
			b = Decimal{}
		}
		if i%13 == 0 {
			c = Decimal{}
		}
		if i%17 == 0 {
			a = Decimal{}
		}
		places := uint8(rng.Uint64N(uint64(MaxPrec) + 2))
		mode := RoundingMode(rng.Uint64N(uint64(TowardNegative) + 2))
		for _, op := range mustRoundOps {
			err := requireMustRoundTwin(t, op, a, b, c, places, mode)
			seen[outcome{op.name, err}]++
		}
	}
	for _, op := range mustRoundOps {
		require.Greater(t, seen[outcome{op.name, nil}], 1_000, "%s: too few successes", op.name)
		for _, sentinel := range []error{ErrPrecOutOfRange, ErrInvalidRoundingMode, ErrOverflow} {
			require.Positive(t, seen[outcome{op.name, sentinel}], "%s: %v never exercised", op.name, sentinel)
		}
	}
	for _, name := range []string{"DivRound", "MulDivRound"} {
		require.Positive(t, seen[outcome{name, ErrDivideByZero}], "%s: ErrDivideByZero never exercised", name)
	}
}

// TestMustRoundTwinsMoneyShapes replays the contract on the shapes the twins
// exist for: quantities, prices, fees and shares at money precisions, rounded to
// 0..4 places, where the one-limb fast paths and exact ties dominate. The third
// operand is nonzero because it is MulDivRound's divisor.
func TestMustRoundTwinsMoneyShapes(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6d6f6e6579, 0x74776e73))
	for range 20_000 {
		qty := newDecimal(u128{lo: 1 + rng.Uint64N(1<<uint(1+rng.IntN(40)))}, rng.IntN(2) == 0, uint8(rng.IntN(10)))
		price := newDecimal(u128{lo: 1 + rng.Uint64N(1<<uint(1+rng.IntN(30)))}, false, uint8(rng.IntN(5)))
		fee := newDecimal(u128{lo: 1 + rng.Uint64N(1<<uint(1+rng.IntN(20)))}, rng.IntN(4) == 0, uint8(rng.IntN(4)))
		places := uint8(rng.IntN(5))
		mode := RoundingMode(rng.Uint64N(uint64(TowardNegative) + 1))
		for _, op := range mustRoundOps {
			require.NoError(t, requireMustRoundTwin(t, op, qty, price, fee, places, mode),
				"%s: money shapes must never fail", op.name)
		}
	}
}

// TestMustRoundInlineBudgets pins the thinness of the twins: each fits the
// default inline budget of 80, so a call costs its erroring form plus a branch
// and no frame of its own (BenchmarkMustRound pairs each twin with the call it
// replaces). A wrapper's cost is the 57-point call into the outlined operation
// plus its node count: 78 for MustMulRound and MustDivRound, 79 for the other
// three, the same on go1.26.6 and go1.27.3. Growth of more than a point or two,
// from an extra parameter pair or a compiler cost-model change, trips this pin.
// It asks only that the wrapper still inlines, like the Rescale entry (78) of
// TestArithRoundingInlineBudgets.
func TestMustRoundInlineBudgets(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("inline budgets are calibrated for 64-bit compiler targets")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; cannot query inlining diagnostics")
	}
	report := buildInlineReport(t)
	for _, fn := range []string{
		"Decimal.MustMulRound", "Decimal.MustDivRound", "Decimal.MustMulAddRound",
		"Decimal.MustMulDivRound", "MustAvgRound",
	} {
		t.Run(fn, func(t *testing.T) {
			entry, ok := report[fn]
			require.True(t, ok, "%s missing from -m=2 inline report", fn)
			require.Truef(t, entry.canInline, "%s must fit the default inline budget, got cost %d", fn, entry.cost)
		})
	}
}
