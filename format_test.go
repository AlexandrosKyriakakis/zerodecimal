package zerodecimal

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// canonicalOracle renders the value (neg ? -1 : +1) · coef / 10^prec
// canonically using only string surgery over the u128 limbs, independent of
// the library's formatter.
func canonicalOracle(neg bool, coef u128, prec uint8) string {
	digits := u128ToBig(coef).String()
	if pad := int(prec) - len(digits) + 1; pad > 0 {
		digits = strings.Repeat("0", pad) + digits
	}
	cut := len(digits) - int(prec)
	intPart, frac := digits[:cut], strings.TrimRight(digits[cut:], "0")
	s := intPart
	if frac != "" {
		s += "." + frac
	}
	if neg && !coef.isZero() {
		s = "-" + s
	}
	return s
}

func TestStringCanonical(t *testing.T) {
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	tests := []struct {
		name string
		d    Decimal
		want string
	}{
		{"zero", Zero, "0"},
		{"half_keeps_leading_zero", Decimal{coef: u128{lo: 5}, prec: 1}, "0.5"},
		{"negative_smallest_unit", Decimal{coef: u128{lo: 1}, neg: true, prec: 19}, "-0.0000000000000000001"},
		{"smallest_unit", Decimal{coef: u128{lo: 1}, prec: 19}, "0.0000000000000000001"},
		{"max_coef_prec_0", Decimal{coef: maxCoef}, "340282366920938463463374607431768211455"},
		{"max_coef_prec_19", Decimal{coef: maxCoef, prec: 19}, "34028236692093846346.3374607431768211455"},
		{"negative_max_coef_prec_19", Decimal{coef: maxCoef, neg: true, prec: 19}, "-34028236692093846346.3374607431768211455"},
		{"trailing_zero_trimmed", Decimal{coef: u128{lo: 150}, prec: 2}, "1.5"},
		{"all_fraction_zeros_no_trailing_dot", Decimal{coef: u128{lo: 100}, prec: 2}, "1"},
		{"pow10_19_two_chunks", Decimal{coef: u128{lo: 10_000_000_000_000_000_000}}, "10000000000000000000"},
		{"pow10_19_minus_1_single_chunk", Decimal{coef: u128{lo: 9_999_999_999_999_999_999}}, "9999999999999999999"},
		{"pow2_64", Decimal{coef: u128{hi: 1}}, "18446744073709551616"},
		{"pow2_64_prec_19", Decimal{coef: u128{hi: 1}, prec: 19}, "1.8446744073709551616"},
		{"negative_typical_price", Decimal{coef: u128{lo: 12345678}, neg: true, prec: 4}, "-1234.5678"},
		{"interior_zeros_preserved", Decimal{coef: u128{lo: 1002003}, prec: 3}, "1002.003"},
		{"fraction_with_leading_zeros", Decimal{coef: u128{lo: 5}, prec: 3}, "0.005"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.d.String(), "String")
			assert.Equal(t, tc.want, string(appendCanonical(nil, tc.d)), "appendCanonical")

			b, err := tc.d.AppendText(nil)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(b), "AppendText")
		})
	}
}

func TestStringRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x57F1, 0x0013))
	for range 20_000 {
		d := newDecimal(randShapedU128(rng), rng.Uint64()&1 == 1, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		neg, hi, lo, prec := d.ToHiLo()
		want := canonicalOracle(neg, u128{hi: hi, lo: lo}, prec)
		require.Equal(t, want, d.String(), "String of %+v", d)
		require.Equal(t, want, string(appendCanonical(nil, d)), "appendCanonical of %+v", d)
		// AppendText carries its own flattened copy of the canonical body on
		// the cache-miss path; cross-check it here so the duplication stays in
		// sync with the oracle.
		b, err := d.AppendText(nil)
		require.NoError(t, err)
		require.Equal(t, want, string(b), "AppendText of %+v", d)
	}
}

func TestAppendTextAppendsToExisting(t *testing.T) {
	buf := []byte("price=")
	buf, err := mustHiLo(t, true, 0, 1234567, 4).AppendText(buf)
	require.NoError(t, err)
	assert.Equal(t, "price=-123.4567", string(buf))

	// A second append must extend, never restart, the buffer.
	buf, err = One.AppendText(buf)
	require.NoError(t, err)
	assert.Equal(t, "price=-123.45671", string(buf))
}

func TestInexactFloat64(t *testing.T) {
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	tests := []struct {
		name string
		d    Decimal
	}{
		{"zero", Zero},
		{"half", Decimal{coef: u128{lo: 5}, prec: 1}},
		{"negative_smallest_unit", Decimal{coef: u128{lo: 1}, neg: true, prec: 19}},
		{"typical_price", MustNew(12345678, -4)},
		{"max_coef_prec_0", Decimal{coef: maxCoef}},
		{"max_coef_prec_19", Decimal{coef: maxCoef, prec: 19}},
		{"min_int64", NewFromInt(math.MinInt64)},
		{"one_third_at_prec_19", Decimal{coef: u128{lo: 3333333333333333333}, prec: 19}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want, err := strconv.ParseFloat(tc.d.String(), 64)
			require.NoError(t, err)
			// Exact bit equality is the contract, not approximate equality.
			assert.Equal(t, want, tc.d.InexactFloat64())
		})
	}
}

func TestInexactFloat64Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xF10A7, 0x0014))
	for range 20_000 {
		d := newDecimal(randShapedU128(rng), rng.Uint64()&1 == 1, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		want, err := strconv.ParseFloat(d.String(), 64)
		require.NoError(t, err)
		// Exact bit equality is the contract, not approximate equality.
		require.Equal(t, want, d.InexactFloat64(), "InexactFloat64 of %+v", d)
	}
}

// stringCacheEnabled reports whether the small-value cache is compiled in
// (it is not under -tags zerodecimal_nostrcache, where every probe misses).
func stringCacheEnabled() bool {
	_, ok := cachedString(Zero)
	return ok
}

func TestStringCacheMatchesComputed(t *testing.T) {
	if !stringCacheEnabled() {
		t.Skip("string cache compiled out by zerodecimal_nostrcache")
	}
	rng := rand.New(rand.NewPCG(0xCAC4E, 0x0015))
	for range 20_000 {
		cents := rng.Int64N(2*cacheSpan+1) - cacheSpan
		neg := cents < 0
		mag := uint64(cents)
		if neg {
			mag = uint64(-cents)
		}

		d := newDecimal(u128{lo: mag}, neg, 2)
		want := string(appendCanonical(nil, d))

		got, ok := cachedString(d)
		require.True(t, ok, "cents %d must hit the string cache", cents)
		require.Equal(t, want, got, "cached string for cents %d", cents)
		require.Equal(t, want, d.String(), "String for cents %d", cents)

		v, ok := cachedValue(d)
		require.True(t, ok, "cents %d must hit the value cache", cents)
		require.Equal(t, want, v, "cached value for cents %d", cents)

		// Lower-precision representations of the same value must land on
		// the same cached entry.
		if mag%10 == 0 {
			tenths, ok := cachedString(newDecimal(u128{lo: mag / 10}, neg, 1))
			require.True(t, ok)
			require.Equal(t, want, tenths, "prec-1 alias for cents %d", cents)
		}
		if mag%100 == 0 {
			units, ok := cachedString(newDecimal(u128{lo: mag / 100}, neg, 0))
			require.True(t, ok)
			require.Equal(t, want, units, "prec-0 alias for cents %d", cents)
		}
	}
}

func TestStringCacheHitMissBoundary(t *testing.T) {
	if !stringCacheEnabled() {
		t.Skip("string cache compiled out by zerodecimal_nostrcache")
	}
	tests := []struct {
		name    string
		d       Decimal
		wantHit bool
	}{
		{"zero_hits", Zero, true},
		{"plus_1000_00_prec_2_hits", Decimal{coef: u128{lo: 100000}, prec: 2}, true},
		{"minus_1000_00_prec_2_hits", Decimal{coef: u128{lo: 100000}, neg: true, prec: 2}, true},
		{"plus_1000_01_misses", Decimal{coef: u128{lo: 100001}, prec: 2}, false},
		{"minus_1000_01_misses", Decimal{coef: u128{lo: 100001}, neg: true, prec: 2}, false},
		{"plus_1000_prec_0_hits", Decimal{coef: u128{lo: 1000}}, true},
		{"plus_1001_prec_0_misses", Decimal{coef: u128{lo: 1001}}, false},
		{"plus_1000_0_prec_1_hits", Decimal{coef: u128{lo: 10000}, prec: 1}, true},
		{"plus_1000_1_prec_1_misses", Decimal{coef: u128{lo: 10001}, prec: 1}, false},
		{"prec_3_misses_even_in_range", Decimal{coef: u128{lo: 1500}, prec: 3}, false},
		{"hi_limb_misses", Decimal{coef: u128{hi: 1, lo: 5}, prec: 2}, false},
		{"scaling_wrap_guard_misses", Decimal{coef: u128{lo: 1<<63 + 100}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := string(appendCanonical(nil, tc.d))

			s, hit := cachedString(tc.d)
			assert.Equal(t, tc.wantHit, hit, "string cache hit")
			if hit {
				assert.Equal(t, want, s, "cached string")
			}

			v, hit := cachedValue(tc.d)
			assert.Equal(t, tc.wantHit, hit, "value cache hit")
			if hit {
				assert.Equal(t, want, v, "cached value")
			}

			// Hit or miss, String must produce the canonical form.
			assert.Equal(t, want, tc.d.String(), "String")
		})
	}
}

func TestValueCacheSharesStringBacking(t *testing.T) {
	if !stringCacheEnabled() {
		t.Skip("string cache compiled out by zerodecimal_nostrcache")
	}
	d := mustHiLo(t, false, 0, 12345, 2) // 123.45

	s, ok := cachedString(d)
	require.True(t, ok)
	v, ok := cachedValue(d)
	require.True(t, ok)

	vs, ok := v.(string)
	require.True(t, ok, "cached driver.Value must box a string")
	assert.Equal(t, s, vs)
	assert.Equal(t, unsafe.StringData(s), unsafe.StringData(vs),
		"value cache must box the same string backing as the string cache")
}

// fixedOracle renders d at exactly places fractional digits via string
// surgery over the already-rounded coefficient, independent of AppendFixed's
// scratch-buffer formatter. Rounding itself is covered by the round_test
// differential suite, so reusing Round here pins only the formatting.
func fixedOracle(d Decimal, places uint8) string {
	neg, hi, lo, prec := d.Round(places).ToHiLo()
	digits := u128ToBig(u128{hi: hi, lo: lo}).String()
	if pad := int(prec) - len(digits) + 1; pad > 0 {
		digits = strings.Repeat("0", pad) + digits
	}
	cut := len(digits) - int(prec)
	s := digits[:cut]
	if places > 0 {
		s += "." + digits[cut:] + strings.Repeat("0", int(places)-int(prec))
	}
	if neg {
		s = "-" + s
	}
	return s
}

func TestStringFixed(t *testing.T) {
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	// maxCoefD is the 2^128-1 coefficient at prec 0 (39 integer digits); its
	// padding at large places is the only path through AppendFixed's whole-block
	// zeroRun loop (format.go pad >= len(zeroRun)).
	maxCoefD := mustHiLo(t, false, maxUint64, maxUint64, 0)
	const maxCoefDigits = "340282366920938463463374607431768211455"
	tests := []struct {
		name   string
		d      Decimal
		places uint8
		want   string
	}{
		{name: "pads_trailing_zeros", d: RequireFromString("1.5"), places: 3, want: "1.500"},
		{name: "places_0_no_dot_rounds_half_away", d: RequireFromString("1.5"), places: 0, want: "2"},
		{name: "negative_tie_rounds_away", d: RequireFromString("-2.5"), places: 0, want: "-3"},
		{name: "negative_rounds_half_away", d: RequireFromString("-1.235"), places: 2, want: "-1.24"},
		{name: "carry_into_integer_part", d: RequireFromString("1.999"), places: 2, want: "2.00"},
		{name: "carry_widens_integer_part", d: RequireFromString("9.99"), places: 1, want: "10.0"},
		{name: "zero_places_0", d: Zero, places: 0, want: "0"},
		{name: "zero_pads", d: Zero, places: 3, want: "0.000"},
		{name: "negative_rounded_to_zero_loses_sign", d: RequireFromString("-0.004"), places: 2, want: "0.00"},
		{name: "truncating_to_integer", d: RequireFromString("123.456"), places: 0, want: "123"},
		{name: "fraction_padded_not_trimmed", d: RequireFromString("0.25"), places: 4, want: "0.2500"},
		{name: "max_coef_full_precision", d: Decimal{coef: maxCoef, prec: 19}, places: 19, want: "34028236692093846346.3374607431768211455"},
		{name: "two_chunk_integer_part", d: Decimal{coef: u128{hi: 10, lo: 5}, prec: 1}, places: 2, want: "18446744073709551616.50"},
		{name: "places_beyond_max_prec_pads", d: RequireFromString("1.5"), places: 25, want: "1.5000000000000000000000000"},
		// Large-places padding around the len(zeroRun)==32 whole-block boundary.
		// pad = places - prec after Round; the loop runs only for pad >= 32.
		{name: "prec0_places_31_below_block_boundary", d: maxCoefD, places: 31, want: maxCoefDigits + "." + strings.Repeat("0", 31)},                                     // pad 31: final mask append only
		{name: "prec0_places_32_first_whole_block", d: maxCoefD, places: 32, want: maxCoefDigits + "." + strings.Repeat("0", 32)},                                        // pad 32: one loop iteration, empty tail
		{name: "prec0_places_33_block_plus_remainder", d: maxCoefD, places: 33, want: maxCoefDigits + "." + strings.Repeat("0", 33)},                                     // pad 33: one block + 1
		{name: "prec1_positive_places_47", d: RequireFromString("1.5"), places: 47, want: "1.5" + strings.Repeat("0", 46)},                                               // pad 46: one block + 14
		{name: "prec1_negative_places_48", d: RequireFromString("-1.5"), places: 48, want: "-1.5" + strings.Repeat("0", 47)},                                             // pad 47: one block + 15
		{name: "prec2_positive_places_49", d: RequireFromString("2.75"), places: 49, want: "2.75" + strings.Repeat("0", 47)},                                             // pad 47: one block + 15
		{name: "zero_places_255", d: Zero, places: 255, want: "0." + strings.Repeat("0", 255)},                                                                           // pad 255: 7 blocks + 31
		{name: "prec0_max_coef_places_255", d: maxCoefD, places: 255, want: maxCoefDigits + "." + strings.Repeat("0", 255)},                                              // pad 255: 7 blocks + 31
		{name: "prec19_max_coef_places_49", d: Decimal{coef: maxCoef, prec: 19}, places: 49, want: "34028236692093846346.3374607431768211455" + strings.Repeat("0", 30)}, // pad 30: below boundary, prec>0 straddle check
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.d.StringFixed(tc.places), "StringFixed")
			assert.Equal(t, tc.want, string(tc.d.AppendFixed(nil, tc.places)), "AppendFixed")
		})
	}
}

func TestAppendFixedAppendsToExisting(t *testing.T) {
	buf := []byte("price=")
	buf = mustHiLo(t, true, 0, 1234567, 4).AppendFixed(buf, 2)
	assert.Equal(t, "price=-123.46", string(buf))

	// A second append must extend, never restart, the buffer.
	buf = One.AppendFixed(buf, 1)
	assert.Equal(t, "price=-123.461.0", string(buf))
}

// TestAppendFixedLargePlacesPrefix exercises the whole-block zeroRun padding
// loop (format.go pad >= len(zeroRun)) while a non-empty prefix already
// occupies dst: the padding must extend that prefix, never restart it.
func TestAppendFixedLargePlacesPrefix(t *testing.T) {
	buf := []byte("amount=")
	buf = RequireFromString("1.5").AppendFixed(buf, 100) // pad 99: three whole blocks + 3
	assert.Equal(t, "amount=1.5"+strings.Repeat("0", 99), string(buf))
}

// TestStringFixedReparse pins the parse contract of fixed renderings flagged
// by the differential fuzz suite: padding zeros can push the digits as
// written past 39 significant figures, which strict parsing deliberately
// rejects (the written coefficient must fit 128 bits before trailing-zero
// trimming), while truncating parsing drops only the padding and recovers the
// exact rounded value.
func TestStringFixedReparse(t *testing.T) {
	tests := []struct {
		name      string
		d         Decimal
		places    uint8
		strictErr error
	}{
		{
			name:      "pow2_127_coef_padding_overflows_strict_parse",
			d:         Decimal{coef: u128{hi: 1 << 63}, prec: 18},
			places:    19,
			strictErr: ErrOverflow,
		},
		{
			name:      "negative_max_coef_padding_overflows_strict_parse",
			d:         Decimal{coef: u128{hi: maxUint64, lo: maxUint64}, neg: true, prec: 1},
			places:    11,
			strictErr: ErrOverflow,
		},
		{
			name:   "padding_within_coefficient_strict_reparses",
			d:      RequireFromString("1.5"),
			places: 3,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.d.StringFixed(tc.places)
			strict, err := NewFromString(s)
			if tc.strictErr != nil {
				require.ErrorIs(t, err, tc.strictErr, "strict parse of %q", s)
			} else {
				require.NoError(t, err, "strict parse of %q", s)
				require.Zero(t, tc.d.Round(tc.places).Cmp(strict), "strict reparse of %q", s)
			}
			trunc, err := NewFromStringTrunc(s)
			require.NoError(t, err, "trunc parse of %q", s)
			require.Zero(t, tc.d.Round(tc.places).Cmp(trunc), "trunc reparse must recover the rounded value: %q", s)
		})
	}
}

func TestStringFixedRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xF18ED, 0x0016))
	for range 20_000 {
		d := newDecimal(randShapedU128(rng), rng.Uint64()&1 == 1, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		places := uint8(rng.Uint64N(256)) // full uint8: past 32+prec drives the whole-block padding loop
		want := fixedOracle(d, places)
		require.Equal(t, want, d.StringFixed(places), "StringFixed of %+v at %d", d, places)
		require.Equal(t, want, string(d.AppendFixed(nil, places)), "AppendFixed of %+v at %d", d, places)
	}
}
