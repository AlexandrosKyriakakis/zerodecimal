package zerodecimal

import (
	"math"
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const maxUint64 = ^uint64(0)

// mustHiLo builds a Decimal from raw parts, failing the test on error.
func mustHiLo(t *testing.T, neg bool, hi, lo uint64, prec uint8) Decimal {
	t.Helper()
	d, err := NewFromHiLo(neg, hi, lo, prec)
	require.NoError(t, err)
	return d
}

// decimalToRat converts d to an exact big.Rat for oracle comparisons.
func decimalToRat(d Decimal) *big.Rat {
	neg, hi, lo, prec := d.ToHiLo()
	num := u128ToBig(u128{hi: hi, lo: lo})
	if neg {
		num.Neg(num)
	}
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(prec)), nil)
	return new(big.Rat).SetFrac(num, den)
}

func TestDecimalLayout(t *testing.T) {
	assert.Equal(t, uintptr(24), unsafe.Sizeof(Decimal{}), "Decimal must stay a 24-byte value")
}

func TestZeroOneConstants(t *testing.T) {
	assert.Equal(t, Decimal{}, Zero)
	assert.True(t, Zero.IsZero())
	assert.Equal(t, "0", Zero.String())
	assert.Equal(t, NewFromInt(1), One)
	assert.Equal(t, "1", One.String())
}

func TestNewFromInt(t *testing.T) {
	tests := []struct {
		name string
		v    int64
		want string
	}{
		{"zero", 0, "0"},
		{"one", 1, "1"},
		{"minus_one", -1, "-1"},
		{"max_int64", math.MaxInt64, "9223372036854775807"},
		{"min_int64", math.MinInt64, "-9223372036854775808"},
		{"typical_negative", -1234567, "-1234567"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewFromInt(tc.v)
			assert.Equal(t, tc.want, d.String())
			assert.Equal(t, uint8(0), d.Prec())
		})
	}

	t.Run("min_int64_magnitude_is_pow2_63", func(t *testing.T) {
		neg, hi, lo, prec := NewFromInt(math.MinInt64).ToHiLo()
		assert.True(t, neg)
		assert.Equal(t, uint64(0), hi)
		assert.Equal(t, uint64(1)<<63, lo)
		assert.Equal(t, uint8(0), prec)
	})
}

func TestNewFromInt32(t *testing.T) {
	tests := []struct {
		name string
		v    int32
		want string
	}{
		{"zero", 0, "0"},
		{"max_int32", math.MaxInt32, "2147483647"},
		{"min_int32", math.MinInt32, "-2147483648"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewFromInt32(tc.v)
			assert.Equal(t, tc.want, d.String())
			assert.Equal(t, uint8(0), d.Prec())
		})
	}
}

func TestNewFromUint64(t *testing.T) {
	tests := []struct {
		name string
		v    uint64
		want string
	}{
		{"zero", 0, "0"},
		{"one", 1, "1"},
		{"max_uint64", maxUint64, "18446744073709551615"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewFromUint64(tc.v)
			assert.Equal(t, tc.want, d.String())
			assert.Equal(t, uint8(0), d.Prec())
		})
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name     string
		value    int64
		exp      int32
		want     string
		wantPrec uint8
		wantErr  error
	}{
		{name: "zero_value_any_positive_exp", value: 0, exp: 1000, want: "0"},
		{name: "zero_value_any_negative_exp", value: 0, exp: -1000, want: "0"},
		{name: "plain_integer", value: 12345, exp: 0, want: "12345"},
		{name: "positive_exp", value: 15, exp: 3, want: "15000"},
		{name: "exp_38_unit", value: 1, exp: 38, want: "1" + strings.Repeat("0", 38)},
		{name: "exp_38_value_3_fits", value: 3, exp: 38, want: "3" + strings.Repeat("0", 38)},
		{name: "exp_38_value_4_overflows", value: 4, exp: 38, wantErr: ErrOverflow},
		{name: "exp_39_overflows", value: 1, exp: 39, wantErr: ErrOverflow},
		{name: "exp_max_int32_overflows", value: 1, exp: math.MaxInt32, wantErr: ErrOverflow},
		{name: "negative_exp_simple", value: 12345, exp: -2, want: "123.45", wantPrec: 2},
		{name: "negative_exp_19_smallest_unit", value: 5, exp: -19, want: "0.0000000000000000005", wantPrec: 19},
		{name: "negative_exp_20_indivisible", value: 5, exp: -20, wantErr: ErrPrecOutOfRange},
		{name: "negative_exp_20_divisible", value: 50, exp: -20, want: "0.0000000000000000005", wantPrec: 19},
		{name: "negative_exp_25_trailing_zeros", value: 1_000_000, exp: -25, want: "0.0000000000000000001", wantPrec: 19},
		{name: "negative_exp_25_insufficient_zeros", value: 1_500_000, exp: -25, wantErr: ErrPrecOutOfRange},
		{name: "negative_exp_min_int32", value: 1, exp: math.MinInt32, wantErr: ErrPrecOutOfRange},
		{name: "min_int64_value", value: math.MinInt64, exp: 0, want: "-9223372036854775808"},
		{name: "min_int64_negative_exp", value: math.MinInt64, exp: -2, want: "-92233720368547758.08", wantPrec: 2},
		{name: "negative_value_positive_exp", value: -15, exp: 2, want: "-1500"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := New(tc.value, tc.exp)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Equal(t, Decimal{}, d, "error results must be the zero Decimal")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, d.String())
			assert.Equal(t, tc.wantPrec, d.Prec())
		})
	}
}

func TestMustNew(t *testing.T) {
	t.Run("returns_value", func(t *testing.T) {
		assert.Equal(t, "123.45", MustNew(12345, -2).String())
	})
	t.Run("panics_on_overflow", func(t *testing.T) {
		assert.PanicsWithError(t, ErrOverflow.Error(), func() { MustNew(1, 39) })
	})
	t.Run("panics_on_prec_out_of_range", func(t *testing.T) {
		assert.PanicsWithError(t, ErrPrecOutOfRange.Error(), func() { MustNew(7, -20) })
	})
}

func TestNewFromHiLo(t *testing.T) {
	tests := []struct {
		name     string
		neg      bool
		hi, lo   uint64
		prec     uint8
		want     string
		wantPrec uint8
		wantErr  error
	}{
		{name: "prec_20_out_of_range", lo: 1, prec: 20, wantErr: ErrPrecOutOfRange},
		{name: "prec_255_out_of_range", lo: 1, prec: 255, wantErr: ErrPrecOutOfRange},
		{name: "max_coef_prec_19", hi: maxUint64, lo: maxUint64, prec: 19, want: "34028236692093846346.3374607431768211455", wantPrec: 19},
		{name: "max_coef_prec_0", hi: maxUint64, lo: maxUint64, prec: 0, want: "340282366920938463463374607431768211455", wantPrec: 0},
		{name: "trailing_zeros_preserved", lo: 150, prec: 2, want: "1.5", wantPrec: 2},
		{name: "negative_zero_normalizes", neg: true, prec: 5, want: "0", wantPrec: 0},
		{name: "negative_value", neg: true, lo: 12345, prec: 3, want: "-12.345", wantPrec: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewFromHiLo(tc.neg, tc.hi, tc.lo, tc.prec)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Equal(t, Decimal{}, d, "error results must be the zero Decimal")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, d.String())
			assert.Equal(t, tc.wantPrec, d.Prec())
		})
	}
}

func TestToHiLoRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x41B0, 0x0011))
	for range 50_000 {
		coef := randShapedU128(rng)
		neg := rng.Uint64()&1 == 1
		prec := uint8(rng.Uint64N(uint64(MaxPrec) + 1))

		d, err := NewFromHiLo(neg, coef.hi, coef.lo, prec)
		require.NoError(t, err)

		gotNeg, gotHi, gotLo, gotPrec := d.ToHiLo()
		if coef.isZero() {
			// Canonical zero collapses sign and precision.
			require.Equal(t, Decimal{}, d)
			require.False(t, gotNeg)
			require.Zero(t, gotHi)
			require.Zero(t, gotLo)
			require.Zero(t, gotPrec)
			continue
		}
		require.Equal(t, neg, gotNeg)
		require.Equal(t, coef.hi, gotHi)
		require.Equal(t, coef.lo, gotLo)
		require.Equal(t, prec, gotPrec)

		back, err := NewFromHiLo(gotNeg, gotHi, gotLo, gotPrec)
		require.NoError(t, err)
		require.Equal(t, d, back)
	}
}

func TestNegativeZero(t *testing.T) {
	fromHiLo, err := NewFromHiLo(true, 0, 0, 5)
	require.NoError(t, err)
	fromNew, err := New(0, -5)
	require.NoError(t, err)

	tests := []struct {
		name string
		d    Decimal
	}{
		{"neg_of_zero", Zero.Neg()},
		{"abs_of_zero", Zero.Abs()},
		{"double_neg_of_zero", Zero.Neg().Neg()},
		{"new_from_hi_lo_neg_zero_prec_5", fromHiLo},
		{"new_zero_negative_exp", fromNew},
		{"new_from_int_zero", NewFromInt(0)},
		{"new_from_int32_zero", NewFromInt32(0)},
		{"new_from_uint64_zero", NewFromUint64(0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, Decimal{}, tc.d, "zero must be the canonical Decimal{}")
			assert.Zero(t, tc.d.Sign())
			assert.False(t, tc.d.IsNegative())
			assert.False(t, tc.d.IsPositive())
			assert.True(t, tc.d.IsZero())
			assert.Equal(t, "0", tc.d.String())
		})
	}
}

func TestSignPredicates(t *testing.T) {
	tests := []struct {
		name     string
		d        Decimal
		wantSign int
		wantZero bool
		wantPos  bool
		wantNeg  bool
	}{
		{"zero", Zero, 0, true, false, false},
		{"positive_int", NewFromInt(7), 1, false, true, false},
		{"negative_int", NewFromInt(-7), -1, false, false, true},
		{"positive_fraction", MustNew(5, -19), 1, false, true, false},
		{"negative_fraction", MustNew(-5, -19), -1, false, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantSign, tc.d.Sign())
			assert.Equal(t, tc.wantZero, tc.d.IsZero())
			assert.Equal(t, tc.wantPos, tc.d.IsPositive())
			assert.Equal(t, tc.wantNeg, tc.d.IsNegative())
		})
	}
}

func TestNegAbs(t *testing.T) {
	tests := []struct {
		name    string
		d       Decimal
		wantNeg string
		wantAbs string
	}{
		{"zero", Zero, "0", "0"},
		{"positive", MustNew(15, -1), "-1.5", "1.5"},
		{"negative", MustNew(-15, -1), "1.5", "1.5"},
		{"min_int64", NewFromInt(math.MinInt64), "9223372036854775808", "9223372036854775808"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantNeg, tc.d.Neg().String())
			assert.Equal(t, tc.wantAbs, tc.d.Abs().String())
			assert.Equal(t, tc.d, tc.d.Neg().Neg(), "double negation must round-trip")
		})
	}

	t.Run("precision_preserved", func(t *testing.T) {
		d := mustHiLo(t, false, 0, 150, 2) // 1.50 with the trailing zero kept
		_, _, lo, prec := d.Neg().ToHiLo()
		assert.Equal(t, uint64(150), lo)
		assert.Equal(t, uint8(2), prec)
	})
}

func TestIntPart(t *testing.T) {
	coef9e37, overflow := mul128by64(u128{lo: 9_000_000_000_000_000_000}, 10_000_000_000_000_000_000)
	require.Zero(t, overflow)

	tests := []struct {
		name    string
		d       Decimal
		want    int64
		wantErr error
	}{
		{name: "zero", d: Zero, want: 0},
		{name: "simple_truncation", d: MustNew(12345, -2), want: 123},
		{name: "truncate_toward_zero_positive", d: MustNew(19, -1), want: 1},
		{name: "truncate_toward_zero_negative", d: MustNew(-19, -1), want: -1},
		{name: "negative_fraction_only", d: MustNew(-99, -2), want: 0},
		{name: "max_int64", d: NewFromInt(math.MaxInt64), want: math.MaxInt64},
		{name: "max_int64_plus_one_overflows", d: NewFromUint64(1 << 63), wantErr: ErrIntPartOverflow},
		{name: "min_int64_round_trips", d: NewFromInt(math.MinInt64), want: math.MinInt64},
		{name: "min_int64_negative_exp_truncates", d: MustNew(math.MinInt64, -2), want: -92233720368547758},
		{name: "hi_limb_overflows", d: Decimal{coef: u128{hi: 1}}, wantErr: ErrIntPartOverflow},
		{name: "hi_limb_with_prec_19_fits", d: Decimal{coef: coef9e37, prec: 19}, want: 9_000_000_000_000_000_000},
		{name: "max_coef_prec_19_overflows", d: Decimal{coef: u128{hi: maxUint64, lo: maxUint64}, prec: 19}, wantErr: ErrIntPartOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.d.IntPart()
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("min_int64_minus_one_unit_overflows", func(t *testing.T) {
		d := mustHiLo(t, true, 0, 1<<63+1, 0) // -(2^63 + 1)
		_, err := d.IntPart()
		require.ErrorIs(t, err, ErrIntPartOverflow)
	})
}

func TestCmp(t *testing.T) {
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	tests := []struct {
		name string
		d, e Decimal
		want int
	}{
		{"zero_vs_zero", Zero, Zero, 0},
		{"zero_vs_positive", Zero, One, -1},
		{"zero_vs_negative", Zero, NewFromInt(-1), 1},
		{"negative_vs_positive", NewFromInt(-1), One, -1},
		{"equal_same_prec", MustNew(150, -2), MustNew(150, -2), 0},
		{"less_same_prec", MustNew(149, -2), MustNew(150, -2), -1},
		{"greater_same_prec", MustNew(150, -2), MustNew(149, -2), 1},
		{"negative_ordering_flips", MustNew(-150, -2), MustNew(-149, -2), -1},
		{"equal_1_5_vs_1_50", MustNew(15, -1), Decimal{coef: u128{lo: 150}, prec: 2}, 0},
		{"equal_1_50_vs_1_5", Decimal{coef: u128{lo: 150}, prec: 2}, MustNew(15, -1), 0},
		{"unaligned_greater", MustNew(15, -1), MustNew(149, -2), 1},
		{"unaligned_less", MustNew(149, -2), MustNew(15, -1), -1},
		{"unaligned_negative_flips", MustNew(-15, -1), MustNew(-149, -2), -1},
		{"one_vs_pow10_19_at_prec_19", One, Decimal{coef: u128{lo: 10_000_000_000_000_000_000}, prec: 19}, 0},
		{"max_coef_prec_0_vs_small_prec_19", Decimal{coef: maxCoef}, Decimal{coef: u128{lo: 5}, prec: 19}, 1},
		{"small_prec_19_vs_max_coef_prec_0", Decimal{coef: u128{lo: 5}, prec: 19}, Decimal{coef: maxCoef}, -1},
		{"max_coef_prec_0_vs_max_coef_prec_19", Decimal{coef: maxCoef}, Decimal{coef: maxCoef, prec: 19}, 1},
		{"max_coef_prec_19_vs_max_coef_prec_0", Decimal{coef: maxCoef, prec: 19}, Decimal{coef: maxCoef}, -1},
		{"negative_max_coef_prec_0_vs_negative_small_prec_19", Decimal{coef: maxCoef, neg: true}, Decimal{coef: u128{lo: 5}, neg: true, prec: 19}, -1},
		{"pow2_64_minus_1_prec_0_vs_max_coef_prec_19", Decimal{coef: u128{lo: maxUint64}}, Decimal{coef: maxCoef, prec: 19}, -1},
		{"max_coef_same_prec_equal", Decimal{coef: maxCoef, prec: 19}, Decimal{coef: maxCoef, prec: 19}, 0},
		// Canonical zero (prec 0) versus a nonzero value at differing prec now
		// routes through the unaligned arm instead of a Sign short-circuit.
		{"zero_vs_positive_prec_2", Zero, MustNew(5, -2), -1},
		{"zero_vs_negative_prec_2", Zero, MustNew(-5, -2), 1},
		{"positive_prec_2_vs_zero", MustNew(5, -2), Zero, 1},
		// Both negative, differing prec: cmpSlow must negate cmpUnaligned's
		// magnitude result so the larger magnitude orders smaller.
		{"both_negative_unaligned_greater_mag_less", MustNew(-15, -1), MustNew(-149, -2), -1},
		{"both_negative_unaligned_less_mag_greater", MustNew(-149, -2), MustNew(-15, -1), 1},
		{"both_negative_unaligned_equal", MustNew(-15, -1), Decimal{coef: u128{lo: 150}, neg: true, prec: 2}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.d.Cmp(tc.e), "Cmp")
			assert.Equal(t, -tc.want, tc.e.Cmp(tc.d), "antisymmetry")
			assert.Equal(t, tc.want == 0, tc.d.Equal(tc.e), "Equal")
			assert.Equal(t, tc.want > 0, tc.d.GreaterThan(tc.e), "GreaterThan")
			assert.Equal(t, tc.want >= 0, tc.d.GreaterThanOrEqual(tc.e), "GreaterThanOrEqual")
			assert.Equal(t, tc.want < 0, tc.d.LessThan(tc.e), "LessThan")
			assert.Equal(t, tc.want <= 0, tc.d.LessThanOrEqual(tc.e), "LessThanOrEqual")
		})
	}
}

func TestCmpRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xC4B, 0x0012))
	for range 20_000 {
		d := newDecimal(randShapedU128(rng), rng.Uint64()&1 == 1, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		e := newDecimal(randShapedU128(rng), rng.Uint64()&1 == 1, uint8(rng.Uint64N(uint64(MaxPrec)+1)))

		want := decimalToRat(d).Cmp(decimalToRat(e))
		require.Equal(t, want, d.Cmp(e), "Cmp(%+v, %+v)", d, e)
		require.Equal(t, -want, e.Cmp(d), "antisymmetry of Cmp(%+v, %+v)", d, e)
		require.Equal(t, want == 0, d.Equal(e), "Equal(%+v, %+v)", d, e)
		self := d // a copy, so the self-comparison cannot be optimized into identity
		require.True(t, d.Equal(self), "reflexivity of %+v", d)
		require.Equal(t, 0, d.Cmp(self), "self-comparison of %+v", d)
	}
}

func TestEqual(t *testing.T) {
	tests := []struct {
		name string
		d, e Decimal
		want bool
	}{
		{"identical_representation", MustNew(15, -1), MustNew(15, -1), true},
		{"equal_across_precision", MustNew(15, -1), Decimal{coef: u128{lo: 1500}, prec: 3}, true},
		{"not_equal_close_values", MustNew(15, -1), MustNew(151, -2), false},
		{"sign_differs", MustNew(15, -1), MustNew(-15, -1), false},
		{"zero_vs_zero", Zero, Zero, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.d.Equal(tc.e))
			assert.Equal(t, tc.want, tc.e.Equal(tc.d))
		})
	}
}
