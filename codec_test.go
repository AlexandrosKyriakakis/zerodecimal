package zerodecimal

import (
	"encoding/json"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// codecBoundaryCases is the shared round-trip table: domain corners (zero,
// max coefficient at prec 0 and 19, the smallest representable unit),
// negatives, both string-cache-window and out-of-window values, and the
// limb/chunk boundaries of the formatter.
var codecBoundaryCases = []struct {
	name string
	str  string
}{
	{name: "zero", str: "0"},
	{name: "one", str: "1"},
	{name: "negative_one", str: "-1"},
	{name: "smallest_unit", str: "0.0000000000000000001"},
	{name: "negative_smallest_unit", str: "-0.0000000000000000001"},
	{name: "max_coef_prec_0", str: "340282366920938463463374607431768211455"},
	{name: "max_coef_prec_19", str: "34028236692093846346.3374607431768211455"},
	{name: "negative_max_coef_prec_19", str: "-34028236692093846346.3374607431768211455"},
	{name: "cache_range_price", str: "1.5"},
	{name: "cache_range_negative", str: "-999.99"},
	{name: "cache_edge", str: "1000"},
	{name: "typical_price", str: "1234.5678"},
	{name: "pow2_64", str: "18446744073709551616"},
	{name: "pow10_19_minus_1", str: "9999999999999999999"},
}

func TestMarshalTextRoundTrip(t *testing.T) {
	for _, tc := range codecBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString(tc.str)
			b, err := d.MarshalText()
			require.NoError(t, err)
			assert.Equal(t, tc.str, string(b))

			var got Decimal
			require.NoError(t, got.UnmarshalText(b))
			assert.Equal(t, d, got)
		})
	}
}

func TestUnmarshalTextErrors(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		{name: "empty", input: "", wantErr: ErrEmptyString},
		{name: "garbage", input: "abc", wantErr: ErrInvalidFormat},
		{name: "bare_sign", input: "-", wantErr: ErrInvalidFormat},
		{name: "too_much_precision", input: "0.00000000000000000001", wantErr: ErrPrecOutOfRange},
		{name: "overflow", input: "340282366920938463463374607431768211456", wantErr: ErrOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString("42.5")
			err := d.UnmarshalText([]byte(tc.input))
			require.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, RequireFromString("42.5"), d, "failed unmarshal must not modify the receiver")
		})
	}
}

func TestMarshalJSONRoundTrip(t *testing.T) {
	for _, tc := range codecBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString(tc.str)
			b, err := json.Marshal(d)
			require.NoError(t, err)
			assert.Equal(t, `"`+tc.str+`"`, string(b), "JSON form must always be quoted canonical")

			var got Decimal
			require.NoError(t, json.Unmarshal(b, &got))
			assert.Equal(t, d, got)
		})
	}
}

// TestMarshalJSONResultIsPrivate pins the no-cache-aliasing contract:
// MarshalJSON callers own the returned slice, so mutating it must not poison
// the small-value string cache or any later marshal.
func TestMarshalJSONResultIsPrivate(t *testing.T) {
	d := RequireFromString("1.5") // inside the string-cache window
	b1, err := d.MarshalJSON()
	require.NoError(t, err)
	for i := range b1 {
		b1[i] = 'X'
	}
	b2, err := d.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, `"1.5"`, string(b2))
	assert.Equal(t, "1.5", d.String())
}

func TestUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    string
		wantErr error
	}{
		{name: "quoted", data: `"1.23"`, want: "1.23"},
		{name: "unquoted", data: `1.23`, want: "1.23"},
		{name: "quoted_negative", data: `"-0.5"`, want: "-0.5"},
		{name: "unquoted_integer", data: `42`, want: "42"},
		{name: "scientific_unquoted", data: `1.23e2`, want: "123"},
		{name: "scientific_negative_exp", data: `1e-7`, want: "0.0000001"},
		{name: "quoted_scientific", data: `"1.5E3"`, want: "1500"},
		{name: "quoted_max_coef", data: `"340282366920938463463374607431768211455"`, want: "340282366920938463463374607431768211455"},
		{name: "garbage", data: `abc`, wantErr: ErrInvalidFormat},
		{name: "quoted_garbage", data: `"abc"`, wantErr: ErrInvalidFormat},
		{name: "empty", data: ``, wantErr: ErrEmptyString},
		{name: "quoted_empty", data: `""`, wantErr: ErrEmptyString},
		{name: "lone_quote", data: `"`, wantErr: ErrInvalidFormat},
		{name: "unbalanced_open_quote", data: `"1.5`, wantErr: ErrInvalidFormat},
		{name: "unbalanced_close_quote", data: `1.5"`, wantErr: ErrInvalidFormat},
		{name: "json_object", data: `{}`, wantErr: ErrInvalidFormat},
		{name: "json_array", data: `[1]`, wantErr: ErrInvalidFormat},
		{name: "json_true", data: `true`, wantErr: ErrInvalidFormat},
		{name: "quoted_null_is_not_null", data: `"null"`, wantErr: ErrInvalidFormat},
		{name: "too_much_precision", data: `"0.00000000000000000001"`, wantErr: ErrPrecOutOfRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString("42.5")
			err := d.UnmarshalJSON([]byte(tc.data))
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Equal(t, RequireFromString("42.5"), d, "failed unmarshal must not modify the receiver")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, d.String())
		})
	}
}

func TestUnmarshalJSONNullIsNoOp(t *testing.T) {
	d := RequireFromString("7.25")
	require.NoError(t, d.UnmarshalJSON([]byte("null")))
	assert.Equal(t, RequireFromString("7.25"), d)
}

// codecFixture exercises the codecs the way applications use them: as struct
// fields driven by encoding/json.
type codecFixture struct {
	Price Decimal     `json:"price"`
	Tax   NullDecimal `json:"tax"`
}

func TestJSONStructRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		fixture  codecFixture
		wantJSON string
	}{
		{
			name: "value_tax",
			fixture: codecFixture{
				Price: RequireFromString("1234.5678"),
				Tax:   NewNullDecimal(RequireFromString("0.07")),
			},
			wantJSON: `{"price":"1234.5678","tax":"0.07"}`,
		},
		{
			name:     "null_tax",
			fixture:  codecFixture{Price: RequireFromString("-0.0000000000000000001")},
			wantJSON: `{"price":"-0.0000000000000000001","tax":null}`,
		},
		{
			name:     "zero_values",
			fixture:  codecFixture{Price: Zero, Tax: NewNullDecimal(Zero)},
			wantJSON: `{"price":"0","tax":"0"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.fixture)
			require.NoError(t, err)
			assert.Equal(t, tc.wantJSON, string(b))

			var got codecFixture
			require.NoError(t, json.Unmarshal(b, &got))
			assert.Equal(t, tc.fixture, got)
		})
	}
}

// TestJSONStructUnquotedNumbers decodes fields produced by float-emitting
// JSON encoders: bare numbers, scientific notation included, must land
// exactly in both Decimal and NullDecimal fields.
func TestJSONStructUnquotedNumbers(t *testing.T) {
	var got codecFixture
	require.NoError(t, json.Unmarshal([]byte(`{"price":12.5,"tax":7e-2}`), &got))
	assert.Equal(t, RequireFromString("12.5"), got.Price)
	assert.True(t, got.Tax.Valid)
	assert.Equal(t, RequireFromString("0.07"), got.Tax.Decimal)
}

// TestMarshalBinaryGolden pins the wire bytes of one one-limb and one
// two-limb value. These bytes are a published format: if this test breaks,
// the change is wire-incompatible and needs a new format version in the
// reserved flag bits, not a test update.
func TestMarshalBinaryGolden(t *testing.T) {
	t.Run("one_limb", func(t *testing.T) {
		b, err := RequireFromString("-1.5").MarshalBinary()
		require.NoError(t, err)
		want := []byte{
			0x01,                                           // flags: neg, no high limb
			0x01,                                           // prec 1
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0F, // coef.lo = 15
		}
		assert.Equal(t, want, b)
	})
	t.Run("two_limb", func(t *testing.T) {
		// 2^64 / 10^19: coefficient hi=1, lo=0, prec 19.
		b, err := RequireFromString("1.8446744073709551616").MarshalBinary()
		require.NoError(t, err)
		want := []byte{
			0x02,                                           // flags: positive, high limb present
			0x13,                                           // prec 19
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, // coef.lo = 0
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, // coef.hi = 1
		}
		assert.Equal(t, want, b)
	})
}

func TestBinaryRoundTrip(t *testing.T) {
	for _, tc := range codecBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString(tc.str)
			b, err := d.MarshalBinary()
			require.NoError(t, err)

			wantLen := binSizeLo
			if _, hi, _, _ := d.ToHiLo(); hi != 0 {
				wantLen = binSizeHi
			}
			assert.Len(t, b, wantLen, "encoding must use the short form whenever the high limb is zero")

			var got Decimal
			require.NoError(t, got.UnmarshalBinary(b))
			assert.Equal(t, d, got)
		})
	}
}

// TestUnmarshalBinaryTruncated feeds every strict prefix of valid one-limb
// and two-limb encodings to the decoder: each must error, never panic, never
// read past its input.
func TestUnmarshalBinaryTruncated(t *testing.T) {
	for _, str := range []string{"-1.5", "1.8446744073709551616"} {
		full, err := RequireFromString(str).MarshalBinary()
		require.NoError(t, err)
		for i := range full {
			var d Decimal
			err := d.UnmarshalBinary(full[:i])
			require.ErrorIs(t, err, ErrInvalidBinaryData, "prefix of length %d of %q must be rejected", i, str)
		}
	}
}

func TestUnmarshalBinaryMalformed(t *testing.T) {
	lo15 := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0F}
	zero8 := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	concat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "reserved_bit_2_set", data: concat([]byte{0x04, 0x01}, lo15)},
		{name: "reserved_bit_7_set", data: concat([]byte{0x80, 0x01}, lo15)},
		{name: "all_flags_set", data: concat([]byte{0xFF, 0x01}, lo15)},
		{name: "prec_20", data: concat([]byte{0x00, 0x14}, lo15)},
		{name: "prec_255", data: concat([]byte{0x00, 0xFF}, lo15)},
		{name: "hi_present_with_zero_hi", data: concat([]byte{0x02, 0x01}, lo15, zero8)},
		{name: "hi_present_short_length", data: concat([]byte{0x02, 0x01}, lo15)},
		{name: "hi_absent_long_length", data: concat([]byte{0x00, 0x01}, lo15, zero8)},
		{name: "length_11", data: concat([]byte{0x00, 0x01}, lo15, []byte{0x00})},
		{name: "length_17", data: concat([]byte{0x02, 0x01}, lo15, zero8[:7])},
		{name: "length_19", data: concat([]byte{0x02, 0x01}, lo15, zero8, []byte{0x01})},
		{name: "empty", data: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString("42.5")
			err := d.UnmarshalBinary(tc.data)
			require.ErrorIs(t, err, ErrInvalidBinaryData)
			assert.Equal(t, RequireFromString("42.5"), d, "failed unmarshal must not modify the receiver")
		})
	}
}

// TestUnmarshalBinaryForeignZeroNormalizes accepts a non-canonical zero from
// a foreign encoder — sign and precision set on a zero coefficient — and
// verifies it collapses to the canonical Decimal{}, preserving the package
// invariant that zero is always the zero value.
func TestUnmarshalBinaryForeignZeroNormalizes(t *testing.T) {
	data := []byte{0x01, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00} // "-0.00000"
	var d Decimal
	require.NoError(t, d.UnmarshalBinary(data))
	assert.Equal(t, Decimal{}, d)
	assert.Equal(t, 0, d.Sign())
}

func TestAppendBinary(t *testing.T) {
	for _, tc := range codecBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString(tc.str)
			prefix := []byte{0xDE, 0xAD}
			out, err := d.AppendBinary(append([]byte(nil), prefix...))
			require.NoError(t, err)

			mb, err := d.MarshalBinary()
			require.NoError(t, err)
			assert.Equal(t, prefix, out[:len(prefix)], "AppendBinary must preserve the existing buffer")
			assert.Equal(t, mb, out[len(prefix):], "AppendBinary must append exactly the MarshalBinary bytes")
		})
	}
}

// TestCodecRandomRoundTrip sweeps fixed-seed random raw representations
// through every codec. Binary preserves the representation exactly (no
// trailing-zero trimming); text and JSON re-parse to the canonical form, so
// they are checked for numeric equality and canonical-string stability.
func TestCodecRandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 4242))
	for i := 0; i < 500; i++ {
		hi := rng.Uint64()
		if rng.Uint64()&3 == 0 {
			hi = 0 // a quarter of the sweep exercises the 10-byte form
		}
		neg := rng.Uint64()&1 == 1
		prec := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		d, err := NewFromHiLo(neg, hi, rng.Uint64(), prec)
		require.NoError(t, err)

		bin, err := d.MarshalBinary()
		require.NoError(t, err)
		var fromBin Decimal
		require.NoError(t, fromBin.UnmarshalBinary(bin))
		require.Equal(t, d, fromBin, "binary round-trip of %s (iteration %d)", d, i)

		txt, err := d.MarshalText()
		require.NoError(t, err)
		var fromText Decimal
		require.NoError(t, fromText.UnmarshalText(txt))
		require.True(t, d.Equal(fromText), "text round-trip of %s (iteration %d)", d, i)
		require.Equal(t, d.String(), fromText.String(), "iteration %d", i)

		js, err := json.Marshal(d)
		require.NoError(t, err)
		var fromJSON Decimal
		require.NoError(t, json.Unmarshal(js, &fromJSON))
		require.True(t, d.Equal(fromJSON), "JSON round-trip of %s (iteration %d)", d, i)
		require.Equal(t, d.String(), fromJSON.String(), "iteration %d", i)
	}
}
