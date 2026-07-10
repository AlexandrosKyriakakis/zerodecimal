package zerodecimal

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

const maxCoefficientText = "340282366920938463463374607431768211455"

func TestParseCanonicalRescueCases(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "raw_coefficient_overflow_fraction_zero",
			in:   maxCoefficientText + ".0",
			want: maxCoefficientText,
		},
		{
			name: "raw_coefficient_overflow_exponent",
			in:   maxCoefficientText + "0e-1",
			want: maxCoefficientText,
		},
		{
			name: "raw_coefficient_overflow_multiple_exponent_zeros",
			in:   maxCoefficientText + "000e-3",
			want: maxCoefficientText,
		},
		{
			name: "raw_coefficient_overflow_fraction_and_exponent",
			in:   maxCoefficientText + "0.0e-2",
			want: "34028236692093846346337460743176821145.5",
		},
		{
			name: "max_coefficient_related_point_fifty",
			in:   "34028236692093846346337460743176821145.50",
			want: "34028236692093846346337460743176821145.5",
		},
		{
			name: "max_coefficient_at_max_precision_padded",
			in:   "34028236692093846346.337460743176821145500000000000",
			want: "34028236692093846346.3374607431768211455",
		},
		{
			name: "lexical_precision_twenty_trims_to_integer",
			in:   "1." + strings.Repeat("0", 20),
			want: "1",
		},
		{
			name: "lexical_precision_twenty_trims_to_zero",
			in:   "0." + strings.Repeat("0", 20),
			want: "0",
		},
		{
			name: "trailing_integer_zero_rescues_scale_twenty",
			in:   "10e-20",
			want: "0.0000000000000000001",
		},
		{
			name: "negative_raw_overflow_rescue",
			in:   "-" + maxCoefficientText + "00e-2",
			want: "-" + maxCoefficientText,
		},
		{
			name: "explicit_plus_raw_overflow_rescue",
			in:   "+" + maxCoefficientText + ".000",
			want: maxCoefficientText,
		},
		{
			name: "exactly_maximum_input_length",
			in:   strings.Repeat("0", 159) + maxCoefficientText + ".0",
			want: maxCoefficientText,
		},
		{
			name: "zero_with_saturated_positive_exponent",
			in:   "0.00000000000000000000e99999999999999999999",
			want: "0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewFromString(tc.in)
			if err != nil {
				t.Fatalf("NewFromString(%q): %v", tc.in, err)
			}
			if got.String() != tc.want {
				t.Fatalf("NewFromString(%q) = %q, want %q", tc.in, got, tc.want)
			}

			gotBytes, err := ParseBytes([]byte(tc.in))
			if err != nil {
				t.Fatalf("ParseBytes(%q): %v", tc.in, err)
			}
			if gotBytes != got {
				t.Fatalf("ParseBytes(%q) = %+v, want representation %+v", tc.in, gotBytes, got)
			}

			gotTrunc, err := NewFromStringTrunc(tc.in)
			if err != nil {
				t.Fatalf("NewFromStringTrunc(%q): %v", tc.in, err)
			}
			if gotTrunc != got {
				t.Fatalf("NewFromStringTrunc(%q) = %+v, want representation %+v", tc.in, gotTrunc, got)
			}
		})
	}
}

func TestParseCanonicalRescueBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr error
	}{
		{
			name:    "canonical_coefficient_two_to_128_fraction_zero",
			in:      "340282366920938463463374607431768211456.0",
			wantErr: ErrOverflow,
		},
		{
			name:    "canonical_coefficient_two_to_128_exponent_rescue_attempt",
			in:      "3402823669209384634633746074317682114560e-1",
			wantErr: ErrOverflow,
		},
		{
			name:    "nonzero_fraction_above_max",
			in:      maxCoefficientText + ".50",
			wantErr: ErrOverflow,
		},
		{
			name:    "uncompensated_trailing_integer_zero",
			in:      maxCoefficientText + "0e0",
			wantErr: ErrOverflow,
		},
		{
			name:    "canonical_precision_twenty_after_trim",
			in:      "1." + strings.Repeat("0", 19) + "10",
			wantErr: ErrPrecOutOfRange,
		},
		{
			name:    "canonical_precision_twenty_exponent",
			in:      "10e-21",
			wantErr: ErrPrecOutOfRange,
		},
		{
			name:    "raw_overflow_canonical_precision_twenty",
			in:      "100000000000000000000.00000000000000000000e-40",
			wantErr: ErrPrecOutOfRange,
		},
		{
			name:    "invalid_tail_after_rescue_shape",
			in:      maxCoefficientText + ".0x",
			wantErr: ErrInvalidFormat,
		},
		{
			name:    "over_maximum_input_length",
			in:      strings.Repeat("0", 160) + maxCoefficientText + ".0",
			wantErr: ErrMaxStrLen,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewFromString(tc.in); !errors.Is(err, tc.wantErr) {
				t.Fatalf("NewFromString(%q) error = %v, want %v", tc.in, err, tc.wantErr)
			}
			if _, err := ParseBytes([]byte(tc.in)); !errors.Is(err, tc.wantErr) {
				t.Fatalf("ParseBytes(%q) error = %v, want %v", tc.in, err, tc.wantErr)
			}
		})
	}
}

// TestParseCanonicalRescueMatrix exhaustively crosses representative u128
// coefficient shapes with both signs, every supported precision, and forty
// redundant raw mantissa zeros. Every generated literal has a raw coefficient
// wider than its value's coefficient; max-coefficient cases overflow u128 by
// at least one decimal digit before the exponent restores the exact value.
func TestParseCanonicalRescueMatrix(t *testing.T) {
	maxWord := ^uint64(0)
	coefficients := []u128{
		{lo: 1},
		{lo: 10},
		{lo: maxWord},
		{hi: 1},
		{hi: 1 << 63},
		{hi: maxWord, lo: maxWord - 1},
		{hi: maxWord, lo: maxWord},
	}

	for coefficientIndex, coef := range coefficients {
		coefText := newDecimal(coef, false, 0).String()
		for _, neg := range []bool{false, true} {
			sign := ""
			if neg {
				sign = "-"
			}
			for prec := uint8(0); prec <= MaxPrec; prec++ {
				want := newDecimal(coef, neg, prec)
				for redundant := 1; redundant <= 40; redundant++ {
					literal := sign + coefText + strings.Repeat("0", redundant) +
						"e-" + strconv.Itoa(int(prec)+redundant)
					got, err := NewFromString(literal)
					if err != nil {
						t.Fatalf("coefficient[%d] neg=%v prec=%d redundant=%d input=%q: %v",
							coefficientIndex, neg, prec, redundant, literal, err)
					}
					if !got.Equal(want) {
						t.Fatalf("coefficient[%d] neg=%v prec=%d redundant=%d: got %s, want value %s",
							coefficientIndex, neg, prec, redundant, got, want)
					}

					gotBytes, err := ParseBytes([]byte(literal))
					if err != nil || gotBytes != got {
						t.Fatalf("byte parser disagreement for %q: got=%+v err=%v, string=%+v",
							literal, gotBytes, err, got)
					}

					gotTrunc, err := NewFromStringTrunc(literal)
					if err != nil || gotTrunc != got {
						t.Fatalf("trunc parser disagreement for %q: got=%+v err=%v, strict=%+v",
							literal, gotTrunc, err, got)
					}
					gotTruncBytes, err := ParseBytesTrunc([]byte(literal))
					if err != nil || gotTruncBytes != got {
						t.Fatalf("byte trunc parser disagreement for %q: got=%+v err=%v, strict=%+v",
							literal, gotTruncBytes, err, got)
					}
				}
			}
		}
	}
}

var (
	canonicalParseSink    Decimal
	errCanonicalParseSink error
)

func TestParseCanonicalRescueAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	rescue := maxCoefficientText + "0e-1"
	rescueBytes := []byte(rescue)
	overflow := "3402823669209384634633746074317682114560e-1"
	overflowBytes := []byte(overflow)
	precision := "10e-21"
	precisionBytes := []byte(precision)
	invalid := maxCoefficientText + ".0x"
	invalidBytes := []byte(invalid)
	tooLong := strings.Repeat("0", maxParseLen+1)
	tooLongBytes := []byte(tooLong)

	tests := []struct {
		name string
		fn   func()
	}{
		{"string_success", func() { canonicalParseSink, errCanonicalParseSink = NewFromString(rescue) }},
		{"bytes_success", func() { canonicalParseSink, errCanonicalParseSink = ParseBytes(rescueBytes) }},
		{"string_overflow", func() { canonicalParseSink, errCanonicalParseSink = NewFromString(overflow) }},
		{"bytes_overflow", func() { canonicalParseSink, errCanonicalParseSink = ParseBytes(overflowBytes) }},
		{"string_precision", func() { canonicalParseSink, errCanonicalParseSink = NewFromString(precision) }},
		{"bytes_precision", func() { canonicalParseSink, errCanonicalParseSink = ParseBytes(precisionBytes) }},
		{"string_invalid", func() { canonicalParseSink, errCanonicalParseSink = NewFromString(invalid) }},
		{"bytes_invalid", func() { canonicalParseSink, errCanonicalParseSink = ParseBytes(invalidBytes) }},
		{"string_too_long", func() { canonicalParseSink, errCanonicalParseSink = NewFromString(tooLong) }},
		{"bytes_too_long", func() { canonicalParseSink, errCanonicalParseSink = ParseBytes(tooLongBytes) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := testing.AllocsPerRun(1000, tc.fn); got != 0 {
				t.Fatalf("allocations per parse = %v, want exactly 0", got)
			}
		})
	}
}
