package zerodecimal

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"empty", "", ErrEmptyString},
		{"bare_minus", "-", ErrInvalidFormat},
		{"bare_plus", "+", ErrInvalidFormat},
		{"bare_dot", ".", ErrInvalidFormat},
		{"minus_dot", "-.", ErrInvalidFormat},
		{"double_dot", "1..2", ErrInvalidFormat},
		{"two_points", "1.2.3", ErrInvalidFormat},
		{"letter_inside", "12a.45", ErrInvalidFormat},
		{"leading_space", " 1", ErrInvalidFormat},
		{"trailing_space", "1 ", ErrInvalidFormat},
		{"comma_separator", "1,5", ErrInvalidFormat},
		{"underscore_separator", "1_000", ErrInvalidFormat},
		{"arabic_indic_digits", "١٢٣", ErrInvalidFormat},
		{"fullwidth_digits", "１２３", ErrInvalidFormat},
		{"nan_word", "NaN", ErrInvalidFormat},
		{"inf_word", "Inf", ErrInvalidFormat},
		{"negative_inf_word", "-Inf", ErrInvalidFormat},
		{"exponent_without_digits", "1e", ErrInvalidFormat},
		{"exponent_plus_only", "1e+", ErrInvalidFormat},
		{"exponent_minus_only", "1e-", ErrInvalidFormat},
		{"exponent_without_mantissa", "e5", ErrInvalidFormat},
		{"exponent_letter_tail", "1e5x", ErrInvalidFormat},
		{"trailing_dot", "1.", ErrInvalidFormat},
		{"leading_dot", ".1", ErrInvalidFormat},
		{"negative_leading_dot", "-.1", ErrInvalidFormat},
		{"dot_before_exponent", "1.e5", ErrInvalidFormat},
		{"double_sign", "+-1", ErrInvalidFormat},
		{"sign_then_dot_only", "+.", ErrInvalidFormat},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Grammar errors are mode-independent: all four entry points agree.
			_, err := NewFromString(tc.in)
			require.ErrorIs(t, err, tc.wantErr)
			_, err = NewFromStringTrunc(tc.in)
			require.ErrorIs(t, err, tc.wantErr)
			_, err = ParseBytes([]byte(tc.in))
			require.ErrorIs(t, err, tc.wantErr)
			_, err = ParseBytesTrunc([]byte(tc.in))
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestParseAccepts(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plus_one", "+1", "1"},
		{"zero", "0", "0"},
		{"negative_zero_is_canonical_zero", "-0", "0"},
		{"zero_fraction", "0.000", "0"},
		{"leading_and_trailing_zeros", "00123.4500", "123.45"},
		{"smallest_unit", "0.0000000000000000001", "0.0000000000000000001"},
		{"max_coefficient_39_digits", "340282366920938463463374607431768211455", "340282366920938463463374607431768211455"},
		{"max_coefficient_prec_19", "34028236692093846346.3374607431768211455", "34028236692093846346.3374607431768211455"},
		{"trailing_fraction_zeros_trim", "1.500", "1.5"},
		{"integer_point_zero", "1.0", "1"},
		{"negative_typical", "-1234.5678", "-1234.5678"},
		{"sci_positive_exponent", "1.23e4", "12300"},
		{"sci_negative_exponent", "1E-7", "0.0000001"},
		{"sci_explicit_plus", "12e+3", "12000"},
		{"sci_zero_exponent", "1e0", "1"},
		{"sci_fractional_result", "1.5e-3", "0.0015"},
		{"sci_exponent_leading_zeros", "1e+005", "100000"},
		{"sci_zero_mantissa", "0e25", "0"},
		{"large_exponent_offset_by_fraction", "0." + strings.Repeat("0", 49) + "1e60", "10000000000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewFromString(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, d.String())
			if tc.want == "0" {
				assert.Equal(t, Zero, d, "zero must collapse to the canonical Decimal{}")
			}

			db, err := ParseBytes([]byte(tc.in))
			require.NoError(t, err)
			assert.Equal(t, d, db, "string and byte parsers must agree")

			dt, err := NewFromStringTrunc(tc.in)
			require.NoError(t, err)
			assert.Equal(t, d, dt, "trunc mode must not alter exactly-representable input")
		})
	}
}

func TestParseStrictErrors(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr error
	}{
		{"exactly_two_pow_128", "340282366920938463463374607431768211456", ErrOverflow},
		{"forty_digits", "1" + strings.Repeat("0", 39), ErrOverflow},
		{"forty_significant_after_leading_zeros", "00" + strings.Repeat("9", 40), ErrOverflow},
		{"twenty_fraction_digits", "0.12345678901234567891", ErrPrecOutOfRange},
		{"twenty_fraction_zeros", "0.00000000000000000000", ErrPrecOutOfRange},
		{"exponent_below_range", "1e-25", ErrPrecOutOfRange},
		{"exponent_above_range", "1e39", ErrOverflow},
		{"fraction_offset_still_overflows", "1.5e39", ErrOverflow},
		{"saturated_positive_exponent", "1e+99999999999999999999", ErrOverflow},
		{"saturated_negative_exponent", "1e-99999999999999999999", ErrPrecOutOfRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewFromString(tc.in)
			require.ErrorIs(t, err, tc.wantErr)
			_, err = ParseBytes([]byte(tc.in))
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestParseTrunc(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"twenty_fraction_digits_truncated", "0.12345678901234567891", "0.1234567890123456789", nil},
		{"truncates_not_rounds", "0.99999999999999999999", "0.9999999999999999999", nil},
		{"truncates_to_zero", "0.00000000000000000000123", "0", nil},
		{"twenty_fraction_zeros_canonical_zero", "0.00000000000000000000", "0", nil},
		{"tiny_exponent_truncates_to_zero", "1e-25", "0", nil},
		{"huge_negative_exponent_truncates_to_zero", "123e-300", "0", nil},
		{"excess_over_nineteen_chains_two_divisions", "0.340282366920938463463374607431768211455", "0.3402823669209384634", nil},
		{"exponent_driven_excess_over_nineteen", "340282366920938463463374607431768211455e-39", "0.3402823669209384634", nil},
		{"integer_part_survives_truncation", "12345.678901234567890123456789", "12345.6789012345678901234", nil},
		{"overflow_still_errors", "1e39", "", ErrOverflow},
		{"coefficient_overflow_still_errors", "340282366920938463463374607431768211456", "", ErrOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewFromStringTrunc(tc.in)
			db, berr := ParseBytesTrunc([]byte(tc.in))
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.ErrorIs(t, berr, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.NoError(t, berr)
			assert.Equal(t, tc.want, d.String())
			assert.Equal(t, d, db, "string and byte parsers must agree")
			if tc.want == "0" {
				assert.Equal(t, Zero, d, "zero must collapse to the canonical Decimal{}")
			}
		})
	}
}

func TestParseRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"zero", "0"},
		{"one", "1"},
		{"minus_one", "-1"},
		{"half", "0.5"},
		{"minus_half", "-0.5"},
		{"typical_price", "1234.5678"},
		{"trimmed_fraction", "1.5"},
		{"smallest_unit", "0.0000000000000000001"},
		{"negative_smallest_unit", "-0.0000000000000000001"},
		{"max_coefficient", "340282366920938463463374607431768211455"},
		{"max_coefficient_prec_19", "34028236692093846346.3374607431768211455"},
		{"negative_max_coefficient_prec_19", "-34028236692093846346.3374607431768211455"},
		{"max_int64", "9223372036854775807"},
		{"min_int64", "-9223372036854775808"},
		{"beyond_uint64", "1000000000000000000000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewFromString(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.in, d.String())
		})
	}
}

func TestParseRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 2026))
	for range 2000 {
		d := newDecimal(
			u128{hi: rng.Uint64(), lo: rng.Uint64()},
			rng.IntN(2) == 1,
			uint8(rng.UintN(uint(MaxPrec)+1)),
		)
		s := d.String()
		p, err := NewFromString(s)
		require.NoError(t, err, "round-trip parse of %q", s)
		require.True(t, p.Equal(d), "parse(%q) = %q, want value-equal input", s, p.String())
		require.Equal(t, s, p.String(), "canonical strings must be parse-stable")
	}
}

func TestRequireFromString(t *testing.T) {
	assert.Equal(t, "1.5", RequireFromString("1.5").String())
	require.Panics(t, func() { RequireFromString("abc") })
	require.Panics(t, func() { RequireFromString("") })
	require.Panics(t, func() { RequireFromString("1e-25") })
}

func TestParseLengthLimit(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"exactly_200_bytes_accepted", strings.Repeat("0", 197) + "1.5", "1.5", nil},
		{"201_bytes_rejected", strings.Repeat("0", 198) + "1.5", "", ErrMaxStrLen},
		{"thousand_digit_string_rejected", strings.Repeat("9", 1000), "", ErrMaxStrLen},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewFromString(tc.in)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, d.String())
		})
	}
}

func TestNewFromFloat(t *testing.T) {
	tests := []struct {
		name    string
		f       float64
		want    string
		wantErr error
	}{
		{"nan", math.NaN(), "", ErrInvalidFloat},
		{"positive_infinity", math.Inf(1), "", ErrInvalidFloat},
		{"negative_infinity", math.Inf(-1), "", ErrInvalidFloat},
		{"positive_zero", 0.0, "0", nil},
		{"negative_zero", math.Copysign(0, -1), "0", nil},
		{"point_one", 0.1, "0.1", nil},
		{"one_point_five", 1.5, "1.5", nil},
		{"smallest_unit", 1e-19, "0.0000000000000000001", nil},
		{"below_smallest_unit", 1e-20, "", ErrPrecOutOfRange},
		{"shortest_form_needs_twenty_digits", 1.5e-19, "", ErrPrecOutOfRange},
		{"max_float64", math.MaxFloat64, "", ErrOverflow},
		{"two_pow_128_boundary", 0x1p128, "", ErrOverflow},
		{"just_below_two_pow_128", math.Nextafter(0x1p128, 0), "340282366920938430000000000000000000000", nil},
		{"seventeen_significant_digits", 123456789.123456789, "123456789.12345679", nil},
		{"float32_widened_keeps_double_noise", float64(float32(0.1)), "0.10000000149011612", nil},
		{"pi", math.Pi, "3.141592653589793", nil},
		{"negative_typical", -1234.5678, "-1234.5678", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewFromFloat(tc.f)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, d.String())
			if tc.want == "0" {
				assert.Equal(t, Zero, d, "zero must collapse to the canonical Decimal{}")
			}
		})
	}
}

func TestNewFromFloat32(t *testing.T) {
	tests := []struct {
		name    string
		f       float32
		want    string
		wantErr error
	}{
		{"nan", float32(math.NaN()), "", ErrInvalidFloat},
		{"positive_infinity", float32(math.Inf(1)), "", ErrInvalidFloat},
		{"point_one_shortest_form", 0.1, "0.1", nil},
		{"one_third", 1.0 / 3.0, "0.33333334", nil},
		{"negative_zero", float32(math.Copysign(0, -1)), "0", nil},
		{"max_float32_fits", math.MaxFloat32, "340282350000000000000000000000000000000", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, err := NewFromFloat32(tc.f)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, d.String())
		})
	}
}

func TestRequireFromFloat(t *testing.T) {
	assert.Equal(t, "1.5", RequireFromFloat(1.5).String())
	require.Panics(t, func() { RequireFromFloat(math.NaN()) })
	require.Panics(t, func() { RequireFromFloat(math.MaxFloat64) })
}
