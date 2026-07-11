package zerodecimal

import (
	"errors"
	"math/big"
	"math/bits"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

func aggregateOracleTotal(xs []Decimal) (*big.Int, uint8) {
	prec := uint8(0)
	for _, d := range xs {
		prec = max(prec, d.prec)
	}
	total := new(big.Int)
	for _, d := range xs {
		term := u128ToBig(d.coef)
		term.Mul(term, bp10(int(prec-d.prec)))
		if d.neg {
			term.Neg(term)
		}
		total.Add(total, term)
	}
	return total, prec
}

func aggregateOracleAt(total *big.Int, sourcePrec uint8, count uint64, places uint8) (q, rem, den *big.Int) {
	num := new(big.Int).Abs(new(big.Int).Set(total))
	num.Mul(num, bp10(int(places)))
	den = new(big.Int).Mul(new(big.Int).SetUint64(count), bp10(int(sourcePrec)))
	q, rem = new(big.Int).QuoRem(num, den, new(big.Int))
	return q, rem, den
}

func requireAggregateDecimal(t *testing.T, got Decimal, coef *big.Int, neg bool, prec uint8) {
	t.Helper()
	if coef.Sign() == 0 {
		require.Equal(t, Decimal{}, got, "zero must be canonical")
		return
	}
	require.Equal(t, neg, got.neg, "sign")
	require.Equal(t, prec, got.prec, "precision")
	require.Zero(t, coef.Cmp(u128ToBig(got.coef)), "coefficient: got=%+v want=%s", got, coef)
}

func requireAggregateSumOracle(t *testing.T, xs []Decimal) {
	t.Helper()
	total, prec := aggregateOracleTotal(xs)
	got, err := Sum(xs[0], xs[1:]...)
	if total.BitLen() > 128 {
		require.ErrorIs(t, err, ErrOverflow)
		require.Equal(t, Decimal{}, got)
		return
	}
	require.NoError(t, err)
	requireAggregateDecimal(t, got, new(big.Int).Abs(total), total.Sign() < 0, prec)
}

func requireAggregateAvgOracle(t *testing.T, xs []Decimal) {
	t.Helper()
	total, sourcePrec := aggregateOracleTotal(xs)
	count := uint64(len(xs))
	got, err := Avg(xs[0], xs[1:]...)
	require.NoError(t, err)
	for places := int(DefaultPrec); places >= 0; places-- {
		q, _, _ := aggregateOracleAt(total, sourcePrec, count, uint8(places))
		if q.BitLen() <= 128 {
			requireAggregateDecimal(t, got, q, total.Sign() < 0, uint8(places))
			return
		}
	}
	t.Fatal("mean integer coefficient must fit because a mean lies between its inputs")
}

func requireAggregateExactOracle(t *testing.T, xs []Decimal) {
	t.Helper()
	total, sourcePrec := aggregateOracleTotal(xs)
	count := uint64(len(xs))
	got, err := AvgExact(xs[0], xs[1:]...)
	for places := int(MaxPrec); places >= 0; places-- {
		q, rem, _ := aggregateOracleAt(total, sourcePrec, count, uint8(places))
		if q.BitLen() > 128 {
			continue
		}
		switch {
		case rem.Sign() == 0:
			require.NoError(t, err)
			requireAggregateDecimal(t, got, q, total.Sign() < 0, uint8(places))
		case q.Sign() == 0:
			require.ErrorIs(t, err, ErrUnderflow)
			require.ErrorIs(t, err, ErrInexact)
			require.Equal(t, Decimal{}, got)
		default:
			require.ErrorIs(t, err, ErrInexact)
			require.Equal(t, Decimal{}, got)
		}
		return
	}
	require.ErrorIs(t, err, ErrOverflow)
	require.Equal(t, Decimal{}, got)
}

func aggregateRoundUp(q, rem, den *big.Int, neg bool, mode RoundingMode) bool {
	if rem.Sign() == 0 {
		return false
	}
	halfCmp := new(big.Int).Lsh(new(big.Int).Set(rem), 1).Cmp(den)
	switch mode {
	case ToNearestAway:
		return halfCmp >= 0
	case ToNearestEven:
		return halfCmp > 0 || halfCmp == 0 && q.Bit(0) != 0
	case AwayFromZero:
		return true
	case TowardZero:
		return false
	case TowardPositive:
		return !neg
	case TowardNegative:
		return neg
	default:
		panic("invalid test rounding mode")
	}
}

func requireAggregateRoundOracle(t *testing.T, xs []Decimal, places uint8, mode RoundingMode) {
	t.Helper()
	total, sourcePrec := aggregateOracleTotal(xs)
	q, rem, den := aggregateOracleAt(total, sourcePrec, uint64(len(xs)), places)
	if aggregateRoundUp(q, rem, den, total.Sign() < 0, mode) {
		q.Add(q, big.NewInt(1))
	}
	got, err := AvgRound(xs[0], places, mode, xs[1:]...)
	if q.BitLen() > 128 {
		require.ErrorIs(t, err, ErrOverflow)
		require.Equal(t, Decimal{}, got)
		return
	}
	require.NoError(t, err)
	requireAggregateDecimal(t, got, q, total.Sign() < 0, places)
}

func aggregatePermutations(xs []Decimal, visit func([]Decimal)) {
	work := append([]Decimal(nil), xs...)
	var permute func(int)
	permute = func(i int) {
		if i == len(work) {
			visit(work)
			return
		}
		for j := i; j < len(work); j++ {
			work[i], work[j] = work[j], work[i]
			permute(i + 1)
			work[i], work[j] = work[j], work[i]
		}
	}
	permute(0)
}

func requireAdaptiveAggregateMatchesWide(t *testing.T, xs []Decimal, wantNarrow bool) {
	t.Helper()
	first, rest := xs[0], xs[1:]
	for first.coef.isZero() && len(rest) > 0 {
		first, rest = rest[0], rest[1:]
	}
	pos, neg, state := accumulateAggregateAdaptive(first, rest)
	narrow := state < 0
	require.Equal(t, wantNarrow, narrow)
	var gotWide aggregateAccum
	wantWide := accumulateAggregate(xs[0], xs[1:])
	if !narrow {
		aggregateFinishWide(&gotWide, pos, neg, first.prec, rest[state:])
		require.Equal(t, wantWide, gotWide, "promoted accumulator must equal a from-scratch wide accumulation")
		return
	}
	coef, wantNeg := wantWide.signedMagnitude()
	require.True(t, coef.isZeroUpper())
	require.Equal(t, newDecimal(coef.lo128(), wantNeg, wantWide.prec), newDecimal(pos, state == -2, first.prec))
}

func TestAggregatePermutationAndBoundaries(t *testing.T) {
	t.Parallel()
	maxValue, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 0)
	require.NoError(t, err)
	maxNegative := maxValue.Neg()
	minQuantum, err := NewFromHiLo(false, 0, 1, MaxPrec)
	require.NoError(t, err)
	onePoint20, err := NewFromHiLo(false, 0, 120, 2)
	require.NoError(t, err)
	minusOnePoint20 := onePoint20.Neg()
	onePoint2, err := NewFromHiLo(false, 0, 12, 1)
	require.NoError(t, err)

	cases := []struct {
		name string
		xs   []Decimal
	}{
		{name: "positive_partial_overflow_cancels", xs: []Decimal{maxValue, maxValue, maxNegative}},
		{name: "negative_partial_overflow_cancels", xs: []Decimal{maxNegative, maxNegative, maxValue}},
		{name: "192_bit_alignment_cancels", xs: []Decimal{maxValue, maxNegative, minQuantum}},
		{name: "cancellation_retains_max_input_precision", xs: []Decimal{onePoint20, minusOnePoint20, onePoint2}},
		{name: "same_precision_fast_mixed_sign", xs: []Decimal{MustNew(50025, -2), MustNew(-12525, -2), MustNew(25, -2), MustNew(-100, -2)}},
		{name: "final_overflow_is_order_independent", xs: []Decimal{maxValue, maxValue, maxNegative, One}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			permutations := 0
			aggregatePermutations(tc.xs, func(xs []Decimal) {
				permutations++
				requireAggregateSumOracle(t, xs)
				requireAggregateAvgOracle(t, xs)
				requireAggregateExactOracle(t, xs)
				requireAggregateRoundOracle(t, xs, 7, ToNearestEven)
			})
			want := 1
			for i := 2; i <= len(tc.xs); i++ {
				want *= i
			}
			require.Equal(t, want, permutations, "every index permutation must be exercised")
		})
	}
}

func TestAggregateWideAverageAndDirectAPIs(t *testing.T) {
	t.Parallel()
	maxValue, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 0)
	require.NoError(t, err)

	t.Run("wide_sum_average_is_representable", func(t *testing.T) {
		got, err := Avg(maxValue, maxValue)
		require.NoError(t, err)
		require.Equal(t, maxValue, got)

		exact, err := AvgExact(maxValue, maxValue)
		require.NoError(t, err)
		require.Equal(t, maxValue, exact)
	})

	t.Run("exact_retains_greatest_precision", func(t *testing.T) {
		got, err := AvgExact(One, NewFromInt(2))
		require.NoError(t, err)
		require.Equal(t, MaxPrec, got.prec)
		require.Equal(t, "1.5000000000000000000", got.StringFixed(MaxPrec))
	})

	t.Run("exact_error_classification", func(t *testing.T) {
		_, err := AvgExact(One, NewFromInt(2), NewFromInt(4))
		require.ErrorIs(t, err, ErrInexact)

		minQuantum, newErr := NewFromHiLo(false, 0, 1, MaxPrec)
		require.NoError(t, newErr)
		_, err = AvgExact(minQuantum, Zero)
		require.ErrorIs(t, err, ErrUnderflow)
		require.True(t, errors.Is(err, ErrInexact))
	})

	t.Run("rounding_uses_full_aggregate_remainder", func(t *testing.T) {
		// (0.0100000001+0)/2 = 0.00500000005, just above the half-even
		// boundary at two places. AvgRound must retain that sticky remainder.
		xs := []Decimal{RequireFromString("0.0100000001"), Zero}
		requireAggregateRoundOracle(t, xs, 2, ToNearestEven)
		got, err := AvgRound(xs[0], 2, ToNearestEven, xs[1:]...)
		require.NoError(t, err)
		require.Equal(t, "0.01", got.String())
		if DefaultPrec == 9 {
			legacy, legacyErr := Avg(xs[0], xs[1:]...)
			require.NoError(t, legacyErr)
			require.Equal(t, "0.005", legacy.String())
			require.Equal(t, Decimal{}, legacy.RoundBank(2), "legacy composition loses the sticky remainder")
		}
	})

	t.Run("all_rounding_modes_and_signs", func(t *testing.T) {
		modes := []struct {
			mode                       RoundingMode
			positiveWant, negativeWant string
		}{
			{mode: ToNearestAway, positiveWant: "0.01", negativeWant: "-0.01"},
			{mode: ToNearestEven, positiveWant: "0", negativeWant: "0"},
			{mode: AwayFromZero, positiveWant: "0.01", negativeWant: "-0.01"},
			{mode: TowardZero, positiveWant: "0", negativeWant: "0"},
			{mode: TowardPositive, positiveWant: "0.01", negativeWant: "0"},
			{mode: TowardNegative, positiveWant: "0", negativeWant: "-0.01"},
		}
		for _, tc := range modes {
			positive, posErr := AvgRound(RequireFromString("0.01"), 2, tc.mode, Zero)
			require.NoError(t, posErr)
			require.Equal(t, tc.positiveWant, positive.String(), "positive mode %d", tc.mode)
			negative, negErr := AvgRound(RequireFromString("-0.01"), 2, tc.mode, Zero)
			require.NoError(t, negErr)
			require.Equal(t, tc.negativeWant, negative.String(), "negative mode %d", tc.mode)
		}
	})

	t.Run("requested_precision_overflow", func(t *testing.T) {
		got, err := AvgRound(maxValue, 1, TowardZero, maxValue)
		require.ErrorIs(t, err, ErrOverflow)
		require.Equal(t, Decimal{}, got)
	})

	t.Run("validation_precedence", func(t *testing.T) {
		_, err := AvgRound(One, MaxPrec+1, RoundingMode(255))
		require.ErrorIs(t, err, ErrPrecOutOfRange)
		_, err = AvgRound(One, 0, RoundingMode(255))
		require.ErrorIs(t, err, ErrInvalidRoundingMode)
	})
}

func TestAggregateSamePrecisionFastPathDifferential(t *testing.T) {
	t.Parallel()
	maxScale2, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 2)
	require.NoError(t, err)
	maxScale2Neg := maxScale2.Neg()
	maxValue, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 0)
	require.NoError(t, err)
	minQuantum, err := NewFromHiLo(false, 0, 1, MaxPrec)
	require.NoError(t, err)

	cases := []struct {
		name   string
		xs     []Decimal
		narrow bool
	}{
		{
			name:   "positive_money",
			xs:     []Decimal{MustNew(123456, -2), MustNew(654321, -2)},
			narrow: true,
		},
		{
			name:   "mixed_sign_with_canonical_zero",
			xs:     []Decimal{Zero, MustNew(50025, -2), MustNew(-12525, -2), Zero},
			narrow: true,
		},
		{
			name:   "all_canonical_zero",
			xs:     []Decimal{Zero, Zero, Zero},
			narrow: true,
		},
		{
			name:   "negative_total",
			xs:     []Decimal{MustNew(-50025, -2), MustNew(12525, -2)},
			narrow: true,
		},
		{
			name:   "positive_subtotal_overflow_promotes",
			xs:     []Decimal{maxScale2, maxScale2, maxScale2Neg},
			narrow: false,
		},
		{
			name:   "negative_subtotal_overflow_promotes",
			xs:     []Decimal{maxScale2Neg, maxScale2Neg, maxScale2},
			narrow: false,
		},
		{
			name:   "zero_after_subtotal_overflow_handoff",
			xs:     []Decimal{maxScale2, maxScale2, Zero, maxScale2Neg},
			narrow: false,
		},
		{
			name:   "final_overflow_promotes",
			xs:     []Decimal{maxScale2, maxScale2},
			narrow: false,
		},
		{
			name:   "lower_precision_mismatch_promotes",
			xs:     []Decimal{MustNew(100, -2), MustNew(20, -1)},
			narrow: false,
		},
		{
			name:   "late_higher_precision_promotes",
			xs:     []Decimal{MustNew(100, -2), MustNew(-25, -2), MustNew(5000, -4)},
			narrow: false,
		},
		{
			name:   "multiple_precision_raises_after_promotion",
			xs:     []Decimal{MustNew(100, -1), MustNew(200, -2), MustNew(-300, -3), MustNew(4, -int32(MaxPrec))},
			narrow: false,
		},
		{
			name:   "zero_then_carry_precision_raise_and_cancellation",
			xs:     []Decimal{Zero, maxValue, maxValue, maxValue.Neg(), minQuantum, maxValue.Neg()},
			narrow: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requireAdaptiveAggregateMatchesWide(t, tc.xs, tc.narrow)
			requireAggregateSumOracle(t, tc.xs)
			requireAggregateAvgOracle(t, tc.xs)
			requireAggregateExactOracle(t, tc.xs)
			requireAggregateRoundOracle(t, tc.xs, 7, ToNearestEven)
		})
	}
}

func TestAggregateSamePrecisionRandomDifferential(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x8f28_6d3c_01c7_ada9, 0x7e1a_b905_322f_a441))
	for iter := 0; iter < 1_000; iter++ {
		prec := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		n := 1 + int(rng.Uint64N(32))
		xs := make([]Decimal, n)
		for i := range xs {
			if rng.Uint64N(16) == 0 {
				xs[i] = Zero
				continue
			}
			d, err := NewFromHiLo(rng.Uint64()&1 != 0, rng.Uint64N(1<<40), rng.Uint64(), prec)
			require.NoError(t, err)
			xs[i] = d
		}
		requireAdaptiveAggregateMatchesWide(t, xs, true)
		requireAggregateSumOracle(t, xs)
		requireAggregateAvgOracle(t, xs)
		requireAggregateExactOracle(t, xs)
		places := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		mode := RoundingMode(rng.Uint64N(uint64(TowardNegative) + 1))
		requireAggregateRoundOracle(t, xs, places, mode)
	}
}

func TestAggregateLateMismatchRandomDifferential(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x0db8_dbd3_9b6b_5401, 0x431d_beef_19ac_8072))
	for iter := 0; iter < 1_000; iter++ {
		prec := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		n := 3 + int(rng.Uint64N(30))
		xs := make([]Decimal, n)
		for i := range xs[:n-1] {
			d, err := NewFromHiLo(rng.Uint64()&1 != 0, rng.Uint64N(1<<40), rng.Uint64()|1, prec)
			require.NoError(t, err)
			xs[i] = d
		}
		mismatchPrec := prec + 1
		if prec == MaxPrec {
			mismatchPrec = prec - 1
		}
		last, err := NewFromHiLo(rng.Uint64()&1 != 0, rng.Uint64(), rng.Uint64()|1, mismatchPrec)
		require.NoError(t, err)
		xs[n-1] = last

		requireAdaptiveAggregateMatchesWide(t, xs, false)
		requireAggregateSumOracle(t, xs)
		requireAggregateAvgOracle(t, xs)
	}
}

func TestAggregateEarlyHandoffRandomDifferential(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x2575_52da_7b2c_a9d1, 0xdd1a_4795_2f61_b8e3))
	for iter := 0; iter < 1_000; iter++ {
		prec := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		n := 4 + int(rng.Uint64N(29))
		xs := make([]Decimal, n)
		start := 0
		if iter&1 == 0 {
			// The second equal-sign maximum carries immediately, so the wide
			// suffix begins at rest[0].
			maxValue, err := NewFromHiLo(rng.Uint64()&1 != 0, ^uint64(0), ^uint64(0), prec)
			require.NoError(t, err)
			xs[0], xs[1], start = maxValue, maxValue, 2
		} else {
			// Alternate mismatches at rest[0] and rest[1]. The bounded prefix
			// stays narrow while the arbitrary suffix exercises precision raises,
			// lower-precision alignment, and both signs after handoff.
			prefixLen := 1 + ((iter >> 1) & 1)
			for i := 0; i < prefixLen; i++ {
				d, err := NewFromHiLo(rng.Uint64()&1 != 0, rng.Uint64N(1<<32), rng.Uint64()|1, prec)
				require.NoError(t, err)
				xs[i] = d
			}
			mismatchPrec := (prec + 1) % (MaxPrec + 1)
			d, err := NewFromHiLo(rng.Uint64()&1 != 0, rng.Uint64(), rng.Uint64()|1, mismatchPrec)
			require.NoError(t, err)
			xs[prefixLen], start = d, prefixLen+1
		}
		for i := start; i < n; i++ {
			d, err := NewFromHiLo(
				rng.Uint64()&1 != 0,
				rng.Uint64(),
				rng.Uint64(),
				uint8(rng.Uint64N(uint64(MaxPrec)+1)),
			)
			require.NoError(t, err)
			xs[i] = d
		}

		requireAdaptiveAggregateMatchesWide(t, xs, false)
		requireAggregateSumOracle(t, xs)
		requireAggregateAvgOracle(t, xs)
	}
}

func TestSumPairMatchesAdd(t *testing.T) {
	t.Parallel()
	maxValue, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 0)
	require.NoError(t, err)
	cases := [][2]Decimal{
		{MustNew(123456, -2), MustNew(654321, -2)},
		{MustNew(123456, -2), MustNew(-123456, -2)},
		{MustNew(100, -2), MustNew(20, -1)},
		{maxValue, maxValue},
		{maxValue, maxValue.Neg()},
		{Decimal{}, MustNew(-1, -int32(MaxPrec))},
	}
	for _, pair := range cases {
		want, wantErr := pair[0].Add(pair[1])
		got, gotErr := Sum(pair[0], pair[1])
		require.Equal(t, wantErr, gotErr)
		require.Equal(t, want, got)
		requireAggregateSumOracle(t, pair[:])
		requireAggregateAvgOracle(t, pair[:])
	}

	rng := rand.New(rand.NewPCG(0xe89f_b24a_3724_f80d, 0x9a26_1de7_c041_55b3))
	for iter := 0; iter < 10_000; iter++ {
		a, newErr := NewFromHiLo(rng.Uint64()&1 != 0, rng.Uint64(), rng.Uint64(), uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		require.NoError(t, newErr)
		b, newErr := NewFromHiLo(rng.Uint64()&1 != 0, rng.Uint64(), rng.Uint64(), uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		require.NoError(t, newErr)
		want, wantErr := a.Add(b)
		got, gotErr := Sum(a, b)
		require.Equal(t, wantErr, gotErr)
		require.Equal(t, want, got)
	}
}

func TestAggregateCountUsesUnsignedFullRange(t *testing.T) {
	t.Parallel()
	// A variadic rest can theoretically have MaxInt elements, so its count is
	// MaxInt+1 == 2^63 on 64-bit systems. Exercise that denominator directly:
	// total=(2^63)*7, hence the unscaled average is exactly seven.
	const count = uint64(1) << 63
	hi, lo := bits.Mul64(count, 7)
	a := aggregateAccum{pos: u256{d0: lo, d1: hi}}
	base, rem, neg := aggregateBase(a, count)
	require.False(t, neg)
	require.Zero(t, rem)
	require.Equal(t, u256{d0: 7}, base)
	q, frac, den := aggregateAverageAt(base, rem, count, 0, MaxPrec)
	require.True(t, q.isZeroUpper())
	require.True(t, frac.isZero())
	require.Equal(t, u128{lo: count}, den)
	require.Equal(t, new(big.Int).Mul(big.NewInt(7), bp10(int(MaxPrec))), u128ToBig(q.lo128()))
}

func TestAggregateRandomDifferential(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x98f3_821b_71c4_4db2, 0x26aa_c901_b7e5_1337))
	for iter := 0; iter < 3_000; iter++ {
		n := 1 + int(rng.Uint64N(24))
		xs := make([]Decimal, n)
		for i := range xs {
			prec := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
			d, err := NewFromHiLo(rng.Uint64()&1 != 0, rng.Uint64(), rng.Uint64(), prec)
			require.NoError(t, err)
			xs[i] = d
		}
		requireAggregateSumOracle(t, xs)
		requireAggregateAvgOracle(t, xs)
		requireAggregateExactOracle(t, xs)
		places := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		mode := RoundingMode(rng.Uint64N(uint64(TowardNegative) + 1))
		requireAggregateRoundOracle(t, xs, places, mode)

		// Compatibility oracle: whenever the old range-limited left fold was
		// successful, the corrected wide Avg must retain its exact Div result
		// representation. Inputs whose fold overflows are the newly supported
		// cases and are covered by the arbitrary-width oracle above.
		legacySum := xs[0]
		var legacyErr error
		for _, d := range xs[1:] {
			legacySum, legacyErr = legacySum.Add(d)
			if legacyErr != nil {
				break
			}
		}
		if legacyErr == nil {
			legacyAvg, err := legacySum.Div(NewFromInt(int64(len(xs))))
			require.NoError(t, err)
			got, err := Avg(xs[0], xs[1:]...)
			require.NoError(t, err)
			require.Equal(t, legacyAvg, got, "legacy successful Avg representation: xs=%+v", xs)
		}
	}
}
