package zerodecimal

import "testing"

// Package-level inputs and sinks for the PostgreSQL NUMERIC allocation gates
// (see alloc_test.go for why neither may be local).
var (
	pgAllocResult Decimal
	errPgAlloc    error
	pgAllocBuf    = make([]byte, 0, PgNumericMaxLen)
	pgAllocOut    []byte

	pgAllocMaxCoef = newDecimal(u128{hi: maxUint64, lo: maxUint64}, true, MaxPrec)

	// pgAllocWire covers every ParsePgNumeric success shape and every error
	// path, each error from each place the decoder can detect it.
	pgAllocWire = []struct {
		name string
		src  []byte
	}{
		{name: "zero", src: pgWire(0, 0, 0, 2)},
		{name: "integer", src: pgDigitsWire(0, 0, 0, 42)},
		{name: "integer_scaled", src: pgDigitsWire(2, 0, 0, 1)},
		{name: "price_9dp", src: pgDigitsWire(0, 0, 9, 12, 3456, 7890)},
		{name: "two_chunks", src: pgDigitsWire(1, 0x4000, 18, 1234, 5678, 9012, 3456, 7890, 1234)},
		{name: "max_coefficient", src: pgAllocMaxCoef.AppendPgNumeric(nil)},
		{name: "zero_padded", src: pgDigitsWire(2, 0, 4, 0, 0, 1, 2345, 0)},
		{name: "truncated", src: pgDigitsWire(0, 0, 6, 1, 2345, 6789)},
		{name: "truncated_cleared_digit", src: pgDigitsWire(0, 0, 9, 1, 0, 0, 5)},
		{name: "truncated_to_zero", src: pgDigitsWire(-1, 0x4000, 2, 99)},
		{name: "short", src: []byte{0, 1, 0, 0}},
		{name: "length_mismatch", src: pgWire(2, 0, 0, 0, 1)},
		{name: "ndigits_0xFFFF_short", src: pgWire(0xFFFF, 0, 0, 0)},
		{name: "ndigits_0x8000_zero_padded", src: pgWire(0x8000, 0x7FFF, 0, 0, append(make([]uint16, 0x7FFF), 7)...)},
		{name: "dscale", src: pgDigitsWire(0, 0, 0x4000, 1)},
		{name: "unknown_sign", src: pgDigitsWire(0, 0x1234, 0, 1)},
		{name: "nan", src: pgWire(0, 0, 0xC000, 0)},
		{name: "pos_inf", src: pgWire(0, 0, 0xD000, 0x20)},
		{name: "neg_inf", src: pgWire(0, 0, 0xF000, 0x20)},
		{name: "nan_bad_digit", src: pgDigitsWire(0, 0xC000, 0, 10000)},
		{name: "bad_last_digit", src: pgDigitsWire(0, 0, 0, 1, 10000)},
		{name: "bad_first_chunk", src: pgDigitsWire(1, 0, 0, 10000, 1)},
		{name: "bad_second_chunk", src: pgDigitsWire(6, 0, 0, 1, 2, 3, 4, 5, 10000, 7)},
		{name: "bad_third_chunk", src: pgDigitsWire(9, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 10000, 9)},
		{name: "bad_digit_past_limit", src: pgDigitsWire(12, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 10000)},
		{name: "bad_digit_past_dscale", src: pgDigitsWire(0, 0, 0, 1, 10000, 1)},
		{name: "bad_digit_all_past_dscale", src: pgDigitsWire(-1, 0, 0, 10000, 1)},
		{name: "bad_digit_last_kept", src: pgDigitsWire(1, 0, 0, 1, 10000, 1, 1)},
		{name: "bad_digit_exposed_by_cut", src: pgDigitsWire(0, 0, 5, 10000, 0, 1)},
		{name: "overflow_past_limit", src: pgDigitsWire(12, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1)},
		{name: "overflow_third_chunk", src: pgDigitsWire(10, 0, 0, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999)},
		{name: "overflow_last_digit", src: pgDigitsWire(9, 0, 0, 35, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1000)},
		{name: "overflow_scale_up", src: pgDigitsWire(10, 0, 0, 1)},
		{name: "prec", src: pgDigitsWire(-5, 0, 20, 1)},
		{name: "prec_after_cut", src: pgDigitsWire(-5, 0, 20, 1, 1)},
		{name: "overflow_after_cut", src: pgDigitsWire(0, 0, 44, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1)},
	}

	// pgAllocValues are AppendPgNumeric inputs spanning every encoder path:
	// zero, one-limb and two-limb integer parts, fractions with and without
	// leading zero digits, and the widest encoding.
	pgAllocValues = []struct {
		name string
		d    Decimal
	}{
		{name: "zero", d: Decimal{}},
		{name: "integer", d: NewFromInt(100_000_000)},
		{name: "price_9dp", d: RequireFromString("12.3456789").MustRescale(9)},
		{name: "small_fraction", d: RequireFromString("-0.000000001")},
		{name: "crypto_18dp", d: RequireFromString("1234.567890123456789012")},
		{name: "max_coefficient_19dp", d: pgAllocMaxCoef},
		{name: "max_coefficient_0dp", d: newDecimal(u128{hi: maxUint64, lo: maxUint64}, false, 0)},
		{name: "widest", d: newDecimal(inc128(pow10u128[37]), false, 1)},
	}
)

// TestParsePgNumericAllocs gates ParsePgNumeric at zero allocations on every
// success shape and every error path.
func TestParsePgNumericAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, tc := range pgAllocWire {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src
			requireAllocs(t, 0, func() { pgAllocResult, errPgAlloc = ParsePgNumeric(src) })
		})
	}
}

// TestAppendPgNumericAllocs gates AppendPgNumeric at zero allocations into a
// buffer with PgNumericMaxLen capacity, and at exactly one, the result, into
// a nil buffer.
func TestAppendPgNumericAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, tc := range pgAllocValues {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.d
			requireAllocs(t, 0, func() { pgAllocOut = d.AppendPgNumeric(pgAllocBuf[:0]) })
			requireAllocs(t, 1, func() { pgAllocOut = d.AppendPgNumeric(nil) })
		})
	}
}
