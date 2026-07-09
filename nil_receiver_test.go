package zerodecimal

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNilReceiverErrorPrecedence(t *testing.T) {
	validBinary, err := RequireFromString("1.5").MarshalBinary()
	require.NoError(t, err)

	var decimal *Decimal
	var strict *StrictSQLDecimal
	var nullable *NullDecimal
	tests := []struct {
		name string
		call func() error
	}{
		{name: "decimal_unmarshal_text_valid", call: func() error { return decimal.UnmarshalText([]byte("1.5")) }},
		{name: "decimal_unmarshal_text_empty", call: func() error { return decimal.UnmarshalText(nil) }},
		{name: "decimal_unmarshal_json_valid", call: func() error { return decimal.UnmarshalJSON([]byte(`"1.5"`)) }},
		{name: "decimal_unmarshal_json_null", call: func() error { return decimal.UnmarshalJSON([]byte(jsonNull)) }},
		{name: "decimal_unmarshal_json_malformed", call: func() error { return decimal.UnmarshalJSON([]byte(`{"x":1}`)) }},
		{name: "decimal_unmarshal_binary_valid", call: func() error { return decimal.UnmarshalBinary(validBinary) }},
		{name: "decimal_unmarshal_binary_invalid", call: func() error { return decimal.UnmarshalBinary(nil) }},
		{name: "decimal_scan_valid", call: func() error { return decimal.Scan("1.5") }},
		{name: "decimal_scan_sql_null", call: func() error { return decimal.Scan(nil) }},
		{name: "decimal_scan_unsupported", call: func() error { return decimal.Scan(true) }},
		{name: "strict_scan_valid", call: func() error { return strict.Scan("1.5") }},
		{name: "strict_scan_sql_null", call: func() error { return strict.Scan(nil) }},
		{name: "strict_scan_float", call: func() error { return strict.Scan(float64(1.5)) }},
		{name: "strict_unmarshal_text_valid", call: func() error { return strict.UnmarshalText([]byte("1.5")) }},
		{name: "strict_unmarshal_text_invalid", call: func() error { return strict.UnmarshalText([]byte("x")) }},
		{name: "strict_unmarshal_json_valid", call: func() error { return strict.UnmarshalJSON([]byte(`"1.5"`)) }},
		{name: "strict_unmarshal_json_null", call: func() error { return strict.UnmarshalJSON([]byte(jsonNull)) }},
		{name: "strict_unmarshal_json_invalid", call: func() error { return strict.UnmarshalJSON([]byte(`{"x":1}`)) }},
		{name: "strict_unmarshal_binary_valid", call: func() error { return strict.UnmarshalBinary(validBinary) }},
		{name: "strict_unmarshal_binary_invalid", call: func() error { return strict.UnmarshalBinary(nil) }},
		{name: "null_scan_valid", call: func() error { return nullable.Scan("1.5") }},
		{name: "null_scan_sql_null", call: func() error { return nullable.Scan(nil) }},
		{name: "null_scan_invalid", call: func() error { return nullable.Scan(true) }},
		{name: "null_unmarshal_text_valid", call: func() error { return nullable.UnmarshalText([]byte("1.5")) }},
		{name: "null_unmarshal_text_empty", call: func() error { return nullable.UnmarshalText(nil) }},
		{name: "null_unmarshal_text_invalid", call: func() error { return nullable.UnmarshalText([]byte("x")) }},
		{name: "null_unmarshal_json_valid", call: func() error { return nullable.UnmarshalJSON([]byte(`"1.5"`)) }},
		{name: "null_unmarshal_json_null", call: func() error { return nullable.UnmarshalJSON([]byte(jsonNull)) }},
		{name: "null_unmarshal_json_invalid", call: func() error { return nullable.UnmarshalJSON([]byte(`"x"`)) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got error
			require.NotPanics(t, func() { got = tc.call() })
			require.ErrorIs(t, got, ErrNilReceiver)
			require.Same(t, ErrNilReceiver, got, "entry points must return the bare sentinel")
		})
	}
}

func TestNilReceiverEncodingJSONIntegration(t *testing.T) {
	tests := []struct {
		name string
		dest any
	}{
		{name: "decimal", dest: (*Decimal)(nil)},
		{name: "strict_sql_decimal", dest: (*StrictSQLDecimal)(nil)},
		{name: "null_decimal", dest: (*NullDecimal)(nil)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got error
			require.NotPanics(t, func() {
				got = json.Unmarshal([]byte(`"1.5"`), tc.dest)
			})
			var invalid *json.InvalidUnmarshalError
			require.ErrorAs(t, got, &invalid)
		})
	}

	// A pointer slot is the stdlib-supported way to start from nil: it
	// allocates the concrete receiver before dispatching to UnmarshalJSON.
	var d *Decimal
	require.NoError(t, json.Unmarshal([]byte(`"1.5"`), &d))
	require.NotNil(t, d)
	assert.Equal(t, RequireFromString("1.5"), *d)

	var strict *StrictSQLDecimal
	require.NoError(t, json.Unmarshal([]byte(`"1.5"`), &strict))
	require.NotNil(t, strict)
	assert.Equal(t, RequireFromString("1.5"), strict.Decimal)
}

func TestStrictSQLDecimalCodecForwardersDelegateAtomically(t *testing.T) {
	validBinary, err := RequireFromString("-1234.500").MarshalBinary()
	require.NoError(t, err)

	tests := []struct {
		name        string
		strictCall  func(*StrictSQLDecimal) error
		decimalCall func(*Decimal) error
	}{
		{
			name:        "text_valid",
			strictCall:  func(d *StrictSQLDecimal) error { return d.UnmarshalText([]byte("-12.345e2")) },
			decimalCall: func(d *Decimal) error { return d.UnmarshalText([]byte("-12.345e2")) },
		},
		{
			name:        "text_invalid",
			strictCall:  func(d *StrictSQLDecimal) error { return d.UnmarshalText([]byte("1..2")) },
			decimalCall: func(d *Decimal) error { return d.UnmarshalText([]byte("1..2")) },
		},
		{
			name:        "json_escaped_valid",
			strictCall:  func(d *StrictSQLDecimal) error { return d.UnmarshalJSON([]byte(`"1\u002e5"`)) },
			decimalCall: func(d *Decimal) error { return d.UnmarshalJSON([]byte(`"1\u002e5"`)) },
		},
		{
			name:        "json_null",
			strictCall:  func(d *StrictSQLDecimal) error { return d.UnmarshalJSON([]byte(jsonNull)) },
			decimalCall: func(d *Decimal) error { return d.UnmarshalJSON([]byte(jsonNull)) },
		},
		{
			name:        "json_invalid",
			strictCall:  func(d *StrictSQLDecimal) error { return d.UnmarshalJSON([]byte(`"x"`)) },
			decimalCall: func(d *Decimal) error { return d.UnmarshalJSON([]byte(`"x"`)) },
		},
		{
			name:        "binary_valid",
			strictCall:  func(d *StrictSQLDecimal) error { return d.UnmarshalBinary(validBinary) },
			decimalCall: func(d *Decimal) error { return d.UnmarshalBinary(validBinary) },
		},
		{
			name:        "binary_invalid",
			strictCall:  func(d *StrictSQLDecimal) error { return d.UnmarshalBinary([]byte{0xff}) },
			decimalCall: func(d *Decimal) error { return d.UnmarshalBinary([]byte{0xff}) },
		},
	}

	marker := NewStrictSQLDecimal(RequireFromString("987.654"))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := marker
			want := marker.Decimal
			wantErr := tc.decimalCall(&want)
			gotErr := tc.strictCall(&got)

			require.Equal(t, wantErr, gotErr, "the wrapper must return Decimal's exact sentinel")
			assert.Equal(t, want, got.Decimal, "the wrapper must preserve Decimal's state transition")
			if gotErr != nil {
				assert.Equal(t, marker, got, "a codec error must leave the wrapper unchanged")
			}
		})
	}
}

func TestNilReceiverDatabaseSQLIntegration(t *testing.T) {
	tests := []struct {
		name string
		src  any
		dest any
	}{
		{name: "decimal_value", src: "1.5", dest: (*Decimal)(nil)},
		{name: "decimal_sql_null", src: nil, dest: (*Decimal)(nil)},
		{name: "strict_value", src: "1.5", dest: (*StrictSQLDecimal)(nil)},
		{name: "strict_sql_null", src: nil, dest: (*StrictSQLDecimal)(nil)},
		{name: "null_decimal_value", src: "1.5", dest: (*NullDecimal)(nil)},
		{name: "null_decimal_sql_null", src: nil, dest: (*NullDecimal)(nil)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := sql.OpenDB(strictSQLTestConnector{source: tc.src})
			t.Cleanup(func() { require.NoError(t, db.Close()) })

			var got error
			require.NotPanics(t, func() {
				got = db.QueryRowContext(context.Background(), "source").Scan(tc.dest)
			})
			require.ErrorIs(t, got, ErrNilReceiver)
		})
	}
}

var (
	nilReceiverAllocDecimal  *Decimal
	nilReceiverAllocStrict   *StrictSQLDecimal
	nilReceiverAllocNullable *NullDecimal
	nilReceiverAllocText         = []byte("1.5")
	nilReceiverAllocJSON         = []byte(jsonNull)
	nilReceiverAllocBinary       = []byte{0xFF}
	nilReceiverAllocScanSrc  any = "1.5"
	errNilReceiverAlloc      error
)

// TestAllocsNilReceiverEntryPoints pins the bare sentinel's zero-allocation
// contract at every guarded boundary.
func TestAllocsNilReceiverEntryPoints(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	tests := []struct {
		name string
		fn   func()
	}{
		{name: "decimal_unmarshal_text", fn: func() { errNilReceiverAlloc = nilReceiverAllocDecimal.UnmarshalText(nilReceiverAllocText) }},
		{name: "decimal_unmarshal_json", fn: func() { errNilReceiverAlloc = nilReceiverAllocDecimal.UnmarshalJSON(nilReceiverAllocJSON) }},
		{name: "decimal_unmarshal_binary", fn: func() { errNilReceiverAlloc = nilReceiverAllocDecimal.UnmarshalBinary(nilReceiverAllocBinary) }},
		{name: "decimal_scan", fn: func() { errNilReceiverAlloc = nilReceiverAllocDecimal.Scan(nilReceiverAllocScanSrc) }},
		{name: "strict_scan", fn: func() { errNilReceiverAlloc = nilReceiverAllocStrict.Scan(nilReceiverAllocScanSrc) }},
		{name: "strict_unmarshal_text", fn: func() { errNilReceiverAlloc = nilReceiverAllocStrict.UnmarshalText(nilReceiverAllocText) }},
		{name: "strict_unmarshal_json", fn: func() { errNilReceiverAlloc = nilReceiverAllocStrict.UnmarshalJSON(nilReceiverAllocJSON) }},
		{name: "strict_unmarshal_binary", fn: func() { errNilReceiverAlloc = nilReceiverAllocStrict.UnmarshalBinary(nilReceiverAllocBinary) }},
		{name: "null_scan", fn: func() { errNilReceiverAlloc = nilReceiverAllocNullable.Scan(nilReceiverAllocScanSrc) }},
		{name: "null_unmarshal_text", fn: func() { errNilReceiverAlloc = nilReceiverAllocNullable.UnmarshalText(nilReceiverAllocText) }},
		{name: "null_unmarshal_json", fn: func() { errNilReceiverAlloc = nilReceiverAllocNullable.UnmarshalJSON(nilReceiverAllocJSON) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, 0, tc.fn)
			require.Same(t, ErrNilReceiver, errNilReceiverAlloc)
		})
	}
}

var (
	strictCodecAllocDst           = NewStrictSQLDecimal(RequireFromString("987.654"))
	strictCodecAllocTextValid     = []byte("1234.5678")
	strictCodecAllocTextInvalid   = []byte("1..2")
	strictCodecAllocJSONValid     = []byte(`"1234.5678"`)
	strictCodecAllocJSONNull      = []byte(jsonNull)
	strictCodecAllocJSONInvalid   = []byte(`"x"`)
	strictCodecAllocBinaryValid   = mustStrictCodecAllocBinary()
	strictCodecAllocBinaryInvalid = []byte{0xff}
)

func mustStrictCodecAllocBinary() []byte {
	b, err := RequireFromString("1234.5678").MarshalBinary()
	if err != nil {
		panic(err)
	}
	return b
}

func TestAllocsStrictSQLDecimalCodecForwarders(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	marker := NewStrictSQLDecimal(RequireFromString("987.654"))
	tests := []struct {
		name    string
		fn      func()
		wantErr error
	}{
		{name: "text_valid", fn: func() { errNilReceiverAlloc = strictCodecAllocDst.UnmarshalText(strictCodecAllocTextValid) }},
		{name: "text_invalid", fn: func() { errNilReceiverAlloc = strictCodecAllocDst.UnmarshalText(strictCodecAllocTextInvalid) }, wantErr: ErrInvalidFormat},
		{name: "json_valid", fn: func() { errNilReceiverAlloc = strictCodecAllocDst.UnmarshalJSON(strictCodecAllocJSONValid) }},
		{name: "json_null", fn: func() { errNilReceiverAlloc = strictCodecAllocDst.UnmarshalJSON(strictCodecAllocJSONNull) }, wantErr: ErrJSONNull},
		{name: "json_invalid", fn: func() { errNilReceiverAlloc = strictCodecAllocDst.UnmarshalJSON(strictCodecAllocJSONInvalid) }, wantErr: ErrInvalidFormat},
		{name: "binary_valid", fn: func() { errNilReceiverAlloc = strictCodecAllocDst.UnmarshalBinary(strictCodecAllocBinaryValid) }},
		{name: "binary_invalid", fn: func() { errNilReceiverAlloc = strictCodecAllocDst.UnmarshalBinary(strictCodecAllocBinaryInvalid) }, wantErr: ErrInvalidBinaryData},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			strictCodecAllocDst = marker
			requireAllocs(t, 0, tc.fn)
			if tc.wantErr == nil {
				require.NoError(t, errNilReceiverAlloc)
				return
			}
			require.Same(t, tc.wantErr, errNilReceiverAlloc)
			assert.Equal(t, marker, strictCodecAllocDst, "error path must remain atomic")
		})
	}
}
