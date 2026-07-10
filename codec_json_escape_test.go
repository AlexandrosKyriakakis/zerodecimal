package zerodecimal

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalJSONEscapedDecimalStrings(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "escaped_point", data: `"1\u002e5"`, want: "1.5"},
		{name: "all_digits_escaped", data: `"\u0031\u0032\u0033\u002E\u0034\u0035"`, want: "123.45"},
		{name: "escaped_sign", data: `"\u002D0\u002e5"`, want: "-0.5"},
		{name: "escaped_exponent", data: `"1\u0045\u002B2"`, want: "100"},
		{name: "lowercase_exponent", data: `"5\u0065\u002d2"`, want: "0.05"},
		{name: "mixed_plain_and_escaped", data: `"12\u0033.4\u0035e-1"`, want: "12.345"},
		{name: "escaped_leading_plus", data: `"\u002b42"`, want: "42"},
		{name: "escaped_zero_is_canonical", data: `"\u002d0\u002e00"`, want: "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			marker := RequireFromString("987.65")
			d := marker
			require.NoError(t, d.UnmarshalJSON([]byte(tc.data)))
			assert.Equal(t, RequireFromString(tc.want), d)

			n := NewNullDecimal(marker)
			require.NoError(t, n.UnmarshalJSON([]byte(tc.data)))
			assert.Equal(t, NewNullDecimal(RequireFromString(tc.want)), n)
		})
	}
}

func TestUnmarshalJSONEscapesThroughEncodingJSON(t *testing.T) {
	var got codecFixture
	require.NoError(t, json.Unmarshal(
		[]byte(`{"price":"1\u002e5","tax":"\u002d2\u0045\u002d1"}`),
		&got,
	))
	assert.Equal(t, RequireFromString("1.5"), got.Price)
	assert.Equal(t, NewNullDecimal(RequireFromString("-0.2")), got.Tax)
}

func TestUnmarshalJSONEscapeErrorsAreAtomic(t *testing.T) {
	invalidUTF8 := string([]byte{'"', '1', '\\', 'u', '0', '0', '3', '2', 0xFF, '"'})
	tests := []struct {
		name string
		data string
	}{
		{name: "unknown_escape", data: `"1\x32"`},
		{name: "truncated_unicode", data: `"1\u123"`},
		{name: "non_hex_unicode", data: `"1\u12x4"`},
		{name: "lone_high_surrogate", data: `"1\uD800"`},
		{name: "high_surrogate_without_escape", data: `"1\uD800x"`},
		{name: "high_surrogate_with_basic_rune", data: `"1\uD800\u0032"`},
		{name: "high_surrogate_with_high_surrogate", data: `"1\uD800\uDBFF"`},
		{name: "lone_low_surrogate", data: `"1\uDC00"`},
		{name: "low_then_high_surrogate", data: `"1\uDC00\uD800"`},
		// Include balanced outer quotes and, where needed, an earlier valid
		// escape so these cases enter unescapeJSONString and exercise the branch
		// named by the test rather than failing in the outer/zero-copy dispatch.
		{name: "trailing_backslash", data: "\"1\\\""},
		{name: "unescaped_inner_quote", data: `"\u0031"2"`},
		{name: "unescaped_control", data: "\"\\u0031\n2\""},
		{name: "invalid_utf8", data: invalidUTF8},
	}

	marker := RequireFromString("987.65")
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := marker
			require.ErrorIs(t, d.UnmarshalJSON([]byte(tc.data)), ErrInvalidFormat)
			assert.Equal(t, marker, d)

			n := NewNullDecimal(marker)
			require.ErrorIs(t, n.UnmarshalJSON([]byte(tc.data)), ErrInvalidFormat)
			assert.Equal(t, NewNullDecimal(marker), n)
		})
	}
}

func TestUnmarshalJSONEscapedParseErrorsAreAtomic(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr error
	}{
		{name: "invalid_decimal", data: `"1\u002e"`, wantErr: ErrInvalidFormat},
		{name: "precision_out_of_range", data: `"0\u002e00000000000000000001"`, wantErr: ErrPrecOutOfRange},
		{
			name:    "coefficient_overflow",
			data:    `"34028236692093846346337460743176821145\u0036"`,
			wantErr: ErrOverflow,
		},
	}

	marker := RequireFromString("987.65")
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := marker
			require.ErrorIs(t, d.UnmarshalJSON([]byte(tc.data)), tc.wantErr)
			assert.Equal(t, marker, d)

			n := NewNullDecimal(marker)
			require.ErrorIs(t, n.UnmarshalJSON([]byte(tc.data)), tc.wantErr)
			assert.Equal(t, NewNullDecimal(marker), n)
		})
	}
}

func TestUnescapeJSONStringUnicodeValidation(t *testing.T) {
	var dst [maxParseLen]byte
	n, err := unescapeJSONString([]byte(`\u0031\u00e9\uD83D\uDE00`), &dst)
	require.NoError(t, err)
	assert.Equal(t, "1é😀", string(dst[:n]))
}

func TestUnmarshalJSONValidSurrogatePairThroughPublicDecoder(t *testing.T) {
	marker := RequireFromString("987.65")

	// A valid pair decodes to one four-byte scalar. It is valid JSON string
	// content but not decimal grammar, so the public decoder must reject it
	// atomically at the decimal layer rather than as a malformed surrogate.
	d := marker
	require.ErrorIs(t, d.UnmarshalJSON([]byte(`"1\uD83D\uDE00"`)), ErrInvalidFormat)
	assert.Equal(t, marker, d)

	// Leave only three bytes in the semantic-input buffer before the pair. This
	// proves the decoder accepts the surrogate pair and then rejects it at the
	// capacity boundary; the exact-fit test below separately proves its decoded
	// width and bytes.
	tooLong := []byte(`"` + strings.Repeat("0", maxParseLen-3) + `\uD83D\uDE00"`)
	d = marker
	require.ErrorIs(t, d.UnmarshalJSON(tooLong), ErrMaxStrLen)
	assert.Equal(t, marker, d)
}

func TestUnmarshalJSONSurrogatePairExactDecodedFit(t *testing.T) {
	// Leave exactly UTFMax bytes in the semantic-input buffer. This catches an
	// off-by-one capacity rejection at the boundary and, unlike an error-only
	// public assertion, verifies that the surrogate pair becomes the exact
	// four-byte UTF-8 encoding rather than a shorter replacement or truncation.
	body := []byte(strings.Repeat("0", maxParseLen-utf8.UTFMax) + `\uD83D\uDE00`)
	var decoded [maxParseLen]byte
	n, err := unescapeJSONString(body, &decoded)
	require.NoError(t, err)
	require.Equal(t, maxParseLen, n)
	assert.Equal(t, []byte{0xf0, 0x9f, 0x98, 0x80}, decoded[n-utf8.UTFMax:n])

	// The exact-fit decode must reach decimal grammar validation. Returning
	// ErrMaxStrLen here would mean the valid four-byte scalar was rejected at
	// the buffer boundary instead.
	data := make([]byte, 0, len(body)+2)
	data = append(data, '"')
	data = append(data, body...)
	data = append(data, '"')
	marker := RequireFromString("987.65")
	d := marker
	require.ErrorIs(t, d.UnmarshalJSON(data), ErrInvalidFormat)
	assert.Equal(t, marker, d)
}

func TestUnescapeJSONStringShortEscapes(t *testing.T) {
	var dst [maxParseLen]byte
	n, err := unescapeJSONString([]byte(`\"\\\/\b\f\n\r\t`), &dst)
	require.NoError(t, err)
	assert.Equal(t, []byte{'"', '\\', '/', '\b', '\f', '\n', '\r', '\t'}, dst[:n])
}

func TestUnmarshalJSONEscapedDecodedLengthBoundary(t *testing.T) {
	// The 200-byte case is also the maximum raw representation: every byte
	// uses a six-byte Unicode escape, for 1,202 bytes including the quotes.
	exact := []byte(`"` + strings.Repeat(`\u0030`, maxParseLen-1) + `\u0031"`)
	require.Len(t, exact, maxQuotedJSONLen)
	var d Decimal
	require.NoError(t, d.UnmarshalJSON(exact))
	assert.Equal(t, One, d)

	marker := RequireFromString("987.65")
	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "escaped_over_maximum_raw_length",
			data: []byte(`"` + strings.Repeat(`\u0030`, maxParseLen) + `\u0031"`),
		},
		{
			name: "mixed_encoding_over_decoded_limit",
			data: []byte(`"\u0030` + strings.Repeat("0", maxParseLen) + `"`),
		},
		{
			name: "plain_quoted_over_decoded_limit",
			data: []byte(`"` + strings.Repeat("0", maxParseLen+1) + `"`),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d = marker
			require.ErrorIs(t, d.UnmarshalJSON(tc.data), ErrMaxStrLen)
			assert.Equal(t, marker, d)
		})
	}
}

var (
	jsonEscapeAllocSuccess = []byte(`"1234\u002e5678"`)
	jsonEscapeAllocError   = []byte(`"1234\uD800"`)
	jsonEscapeAllocParse   = []byte(`"1234\u002e"`)
	jsonEscapeAllocLong    = []byte(`"\u0030` + strings.Repeat("0", maxParseLen) + `"`)
	jsonEscapeAllocDecimal Decimal
	jsonEscapeAllocNull    NullDecimal
	errJSONEscapeAlloc     error
)

// TestAllocsJSONEscapes protects the codec-wide zero-allocation contract on
// the new stack-decoding path, including success, syntax error, length error,
// and the NullDecimal forwarding path.
func TestAllocsJSONEscapes(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}

	tests := []struct {
		name string
		fn   func()
	}{
		{
			name: "decimal_success",
			fn: func() {
				errJSONEscapeAlloc = jsonEscapeAllocDecimal.UnmarshalJSON(jsonEscapeAllocSuccess)
			},
		},
		{
			name: "decimal_invalid_surrogate",
			fn: func() {
				errJSONEscapeAlloc = jsonEscapeAllocDecimal.UnmarshalJSON(jsonEscapeAllocError)
			},
		},
		{
			name: "decimal_decoded_too_long",
			fn: func() {
				errJSONEscapeAlloc = jsonEscapeAllocDecimal.UnmarshalJSON(jsonEscapeAllocLong)
			},
		},
		{
			name: "decimal_escaped_parse_error",
			fn: func() {
				errJSONEscapeAlloc = jsonEscapeAllocDecimal.UnmarshalJSON(jsonEscapeAllocParse)
			},
		},
		{
			name: "null_decimal_success",
			fn: func() {
				errJSONEscapeAlloc = jsonEscapeAllocNull.UnmarshalJSON(jsonEscapeAllocSuccess)
			},
		},
		{
			name: "null_decimal_invalid_surrogate",
			fn: func() {
				errJSONEscapeAlloc = jsonEscapeAllocNull.UnmarshalJSON(jsonEscapeAllocError)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, 0, tc.fn)
		})
	}
}
