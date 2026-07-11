package zerodecimal

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"math"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrictNullDecimalConstructionLayoutAndValue(t *testing.T) {
	t.Parallel()
	raw, err := NewFromHiLo(false, 0, 120, 2)
	require.NoError(t, err)
	n := NewStrictNullDecimal(raw)
	require.True(t, n.Valid)
	require.Equal(t, raw, n.Decimal)

	require.Equal(t, unsafe.Sizeof(NullDecimal{}), unsafe.Sizeof(StrictNullDecimal{}))
	require.Equal(t, unsafe.Alignof(NullDecimal{}), unsafe.Alignof(StrictNullDecimal{}))
	require.Equal(t, unsafe.Offsetof(NullDecimal{}.Decimal), unsafe.Offsetof(StrictNullDecimal{}.Decimal))
	require.Equal(t, unsafe.Offsetof(NullDecimal{}.Valid), unsafe.Offsetof(StrictNullDecimal{}.Valid))

	value, err := n.Value()
	require.NoError(t, err)
	require.Equal(t, driver.Value(raw.String()), value)

	value, err = (StrictNullDecimal{}).Value()
	require.NoError(t, err)
	require.Nil(t, value, "the zero value represents SQL NULL")

	// Invalid values are SQL NULL regardless of manually populated stale
	// storage; Scan always canonicalizes them back to the all-zero form.
	value, err = (StrictNullDecimal{Decimal: raw}).Value()
	require.NoError(t, err)
	require.Nil(t, value)

	value, err = NewStrictNullDecimal(Zero).Value()
	require.NoError(t, err)
	require.Equal(t, driver.Value("0"), value, "valid zero is distinct from SQL NULL")
}

func TestStrictNullDecimalAcceptedSources(t *testing.T) {
	t.Parallel()
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	maxUint := ^uint(0)
	tests := []struct {
		name string
		src  any
		want string
	}{
		{name: "string", src: "-1234.5678", want: "-1234.5678"},
		{name: "scientific_string", src: "1.5e3", want: "1500"},
		{name: "bytes", src: []byte("0.0000000000000000001"), want: "0.0000000000000000001"},
		{name: "int_min", src: minInt, want: strconv.FormatInt(int64(minInt), 10)},
		{name: "int_max", src: maxInt, want: strconv.FormatInt(int64(maxInt), 10)},
		{name: "int8_min", src: int8(math.MinInt8), want: "-128"},
		{name: "int16_min", src: int16(math.MinInt16), want: "-32768"},
		{name: "int32_min", src: int32(math.MinInt32), want: "-2147483648"},
		{name: "int64_min", src: int64(math.MinInt64), want: "-9223372036854775808"},
		{name: "uint_max", src: maxUint, want: strconv.FormatUint(uint64(maxUint), 10)},
		{name: "uint8_max", src: uint8(math.MaxUint8), want: "255"},
		{name: "uint16_max", src: uint16(math.MaxUint16), want: "65535"},
		{name: "uint32_max", src: uint32(math.MaxUint32), want: "4294967295"},
		{name: "uint64_max", src: uint64(math.MaxUint64), want: "18446744073709551615"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NewStrictNullDecimal(RequireFromString("99.5"))
			require.NoError(t, got.Scan(tc.src))
			require.Equal(t, NewStrictNullDecimal(RequireFromString(tc.want)), got)
		})
	}
}

func TestStrictNullDecimalNullAndFailureClearing(t *testing.T) {
	t.Parallel()
	marker := NewStrictNullDecimal(RequireFromString("987.654"))

	t.Run("sql_null_clears_without_error", func(t *testing.T) {
		got := marker
		require.NoError(t, got.Scan(nil))
		require.Equal(t, StrictNullDecimal{}, got)
	})

	tests := []struct {
		name    string
		src     any
		wantErr error
	}{
		{name: "invalid_string", src: "not-a-number", wantErr: ErrInvalidFormat},
		{name: "invalid_bytes", src: []byte("1..2"), wantErr: ErrInvalidFormat},
		{name: "empty_bytes", src: []byte(nil), wantErr: ErrEmptyString},
		{name: "float32_exact", src: float32(0.5), wantErr: ErrScanFloat},
		{name: "float32_inexact", src: float32(0.1), wantErr: ErrScanFloat},
		{name: "float64_exact", src: float64(0.5), wantErr: ErrScanFloat},
		{name: "float64_inexact", src: float64(0.1), wantErr: ErrScanFloat},
		{name: "float64_nan", src: math.NaN(), wantErr: ErrScanFloat},
		{name: "float64_pos_inf", src: math.Inf(1), wantErr: ErrScanFloat},
		{name: "float64_neg_inf", src: math.Inf(-1), wantErr: ErrScanFloat},
		{name: "bool", src: true, wantErr: ErrScanType},
		{name: "time", src: time.Unix(0, 0), wantErr: ErrScanType},
		{name: "uintptr", src: uintptr(1), wantErr: ErrScanType},
		{name: "unknown", src: struct{}{}, wantErr: ErrScanType},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := marker
			err := got.Scan(tc.src)
			require.ErrorIs(t, err, tc.wantErr)
			require.Equal(t, StrictNullDecimal{}, got, "every failed Scan must clear stale state")
		})
	}
	require.NotErrorIs(t, ErrScanFloat, ErrInexact)
}

func TestStrictNullDecimalPreservesLegacyNullDecimalPolicy(t *testing.T) {
	t.Parallel()
	legacy := NewNullDecimal(RequireFromString("7"))
	require.NoError(t, legacy.Scan(float64(0.5)))
	require.Equal(t, NewNullDecimal(RequireFromString("0.5")), legacy)

	strict := NewStrictNullDecimal(RequireFromString("7"))
	require.ErrorIs(t, strict.Scan(float64(0.5)), ErrScanFloat)
	require.Equal(t, StrictNullDecimal{}, strict)
}

func TestStrictNullDecimalJSONAndTextDelegation(t *testing.T) {
	t.Parallel()
	marker := NewStrictNullDecimal(RequireFromString("1.5"))

	jsonNullBytes, err := json.Marshal(StrictNullDecimal{})
	require.NoError(t, err)
	require.Equal(t, "null", string(jsonNullBytes))

	jsonValue, err := json.Marshal(marker)
	require.NoError(t, err)
	require.Equal(t, `"1.5"`, string(jsonValue), "the wrapper must not encode as an exported-field struct")

	var fromJSON StrictNullDecimal
	require.NoError(t, json.Unmarshal([]byte(`"1\u002e25"`), &fromJSON))
	require.Equal(t, NewStrictNullDecimal(RequireFromString("1.25")), fromJSON)

	fromJSON = marker
	require.NoError(t, json.Unmarshal([]byte(jsonNull), &fromJSON))
	require.Equal(t, StrictNullDecimal{}, fromJSON)

	fromJSON = marker
	err = json.Unmarshal([]byte(`"not-a-number"`), &fromJSON)
	require.ErrorIs(t, err, ErrInvalidFormat)
	require.Equal(t, marker, fromJSON, "codec errors mirror NullDecimal receiver atomicity")

	textNull, err := (StrictNullDecimal{}).MarshalText()
	require.NoError(t, err)
	require.Empty(t, textNull)
	textValue, err := marker.MarshalText()
	require.NoError(t, err)
	require.Equal(t, "1.5", string(textValue))

	var fromText StrictNullDecimal
	require.NoError(t, fromText.UnmarshalText([]byte("-2.5")))
	require.Equal(t, NewStrictNullDecimal(RequireFromString("-2.5")), fromText)
	require.NoError(t, fromText.UnmarshalText(nil))
	require.Equal(t, StrictNullDecimal{}, fromText)

	fromText = marker
	err = fromText.UnmarshalText([]byte("x"))
	require.ErrorIs(t, err, ErrInvalidFormat)
	require.Equal(t, marker, fromText)
}

func TestStrictNullDecimalNilReceiverGuards(t *testing.T) {
	t.Parallel()
	var n *StrictNullDecimal
	tests := []struct {
		name string
		call func() error
	}{
		{name: "scan_null", call: func() error { return n.Scan(nil) }},
		{name: "scan_value", call: func() error { return n.Scan("1.5") }},
		{name: "scan_float", call: func() error { return n.Scan(float64(0.5)) }},
		{name: "unmarshal_json_null", call: func() error { return n.UnmarshalJSON([]byte(jsonNull)) }},
		{name: "unmarshal_json_value", call: func() error { return n.UnmarshalJSON([]byte(`"1.5"`)) }},
		{name: "unmarshal_text_empty", call: func() error { return n.UnmarshalText(nil) }},
		{name: "unmarshal_text_value", call: func() error { return n.UnmarshalText([]byte("1.5")) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got error
			require.NotPanics(t, func() { got = tc.call() })
			require.Same(t, ErrNilReceiver, got)
		})
	}
}

func TestStrictNullDecimalDatabaseSQLRoundTrip(t *testing.T) {
	t.Parallel()
	db := sql.OpenDB(strictSQLTestConnector{echoArgument: true})
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	tests := []struct {
		name string
		want StrictNullDecimal
	}{
		{name: "null", want: StrictNullDecimal{}},
		{name: "valid_zero", want: NewStrictNullDecimal(Zero)},
		{name: "fraction", want: NewStrictNullDecimal(RequireFromString("-1234.5678"))},
		{name: "max", want: NewStrictNullDecimal(RequireFromString("340282366920938463463374607431768211455"))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NewStrictNullDecimal(RequireFromString("77.7"))
			err := db.QueryRowContext(context.Background(), "echo", tc.want).Scan(&got)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestStrictNullDecimalDatabaseSQLSources(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		src     driver.Value
		want    StrictNullDecimal
		wantErr error
	}{
		{name: "null", src: nil, want: StrictNullDecimal{}},
		{name: "string", src: "123.45", want: NewStrictNullDecimal(RequireFromString("123.45"))},
		{name: "bytes", src: []byte("-0.125"), want: NewStrictNullDecimal(RequireFromString("-0.125"))},
		{name: "int64", src: int64(math.MinInt64), want: NewStrictNullDecimal(NewFromInt(math.MinInt64))},
		{name: "float64", src: float64(0.5), wantErr: ErrScanFloat},
		{name: "bool", src: true, wantErr: ErrScanType},
		{name: "time", src: time.Unix(0, 0), wantErr: ErrScanType},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := sql.OpenDB(strictSQLTestConnector{source: tc.src})
			t.Cleanup(func() { require.NoError(t, db.Close()) })

			got := NewStrictNullDecimal(RequireFromString("77.7"))
			err := db.QueryRowContext(context.Background(), "source").Scan(&got)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, StrictNullDecimal{}, got, "wrapped database/sql errors must still clear")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestStrictNullDecimalDatabaseSQLNilReceiver(t *testing.T) {
	t.Parallel()
	for _, src := range []driver.Value{"1.5", nil, float64(0.5)} {
		db := sql.OpenDB(strictSQLTestConnector{source: src})
		var dest *StrictNullDecimal
		err := db.QueryRowContext(context.Background(), "source").Scan(dest)
		require.ErrorIs(t, err, ErrNilReceiver)
		require.NoError(t, db.Close())
	}
}

func TestStrictNullDecimalValueScanRoundTrip(t *testing.T) {
	t.Parallel()
	values := []StrictNullDecimal{{}, NewStrictNullDecimal(Zero)}
	for _, tc := range codecBoundaryCases {
		values = append(values, NewStrictNullDecimal(RequireFromString(tc.str)))
	}
	for _, want := range values {
		value, err := want.Value()
		require.NoError(t, err)
		var got StrictNullDecimal
		require.NoError(t, got.Scan(value))
		require.Equal(t, want, got)
	}
}

func TestStrictNullDecimalInterfaces(t *testing.T) {
	t.Parallel()
	var scanner sql.Scanner = new(StrictNullDecimal)
	var valuer driver.Valuer = StrictNullDecimal{}
	var jsonMarshaler json.Marshaler = StrictNullDecimal{}
	var jsonUnmarshaler json.Unmarshaler = new(StrictNullDecimal)
	assert.NotNil(t, scanner)
	assert.NotNil(t, valuer)
	assert.NotNil(t, jsonMarshaler)
	assert.NotNil(t, jsonUnmarshaler)
}
