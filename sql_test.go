package zerodecimal

import (
	"database/sql/driver"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanSupportedTypes(t *testing.T) {
	tests := []struct {
		name string
		src  any
		want string
	}{
		{name: "string", src: "1234.5678", want: "1234.5678"},
		{name: "string_scientific", src: "1.5e3", want: "1500"},
		{name: "string_max_coef", src: "340282366920938463463374607431768211455", want: "340282366920938463463374607431768211455"},
		{name: "bytes", src: []byte("-0.5"), want: "-0.5"},
		{name: "bytes_smallest_unit", src: []byte("0.0000000000000000001"), want: "0.0000000000000000001"},
		{name: "int64_min", src: int64(math.MinInt64), want: "-9223372036854775808"},
		{name: "int64_max", src: int64(math.MaxInt64), want: "9223372036854775807"},
		{name: "int32_negative", src: int32(-42), want: "-42"},
		{name: "int", src: int(12345), want: "12345"},
		{name: "uint64_max", src: uint64(math.MaxUint64), want: "18446744073709551615"},
		{name: "float64", src: float64(3.25), want: "3.25"},
		{name: "float64_negative_shortest", src: float64(-0.1), want: "-0.1"},
		{name: "float64_zero", src: float64(0), want: "0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d Decimal
			require.NoError(t, d.Scan(tc.src))
			assert.Equal(t, tc.want, d.String())
		})
	}
}

func TestScanErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     any
		wantErr error
	}{
		{name: "nil", src: nil, wantErr: ErrScanNil},
		{name: "float64_nan", src: math.NaN(), wantErr: ErrInvalidFloat},
		{name: "float64_pos_inf", src: math.Inf(1), wantErr: ErrInvalidFloat},
		{name: "float64_neg_inf", src: math.Inf(-1), wantErr: ErrInvalidFloat},
		{name: "string_garbage", src: "not-a-number", wantErr: ErrInvalidFormat},
		{name: "string_empty", src: "", wantErr: ErrEmptyString},
		{name: "bytes_garbage", src: []byte("1..2"), wantErr: ErrInvalidFormat},
		{name: "string_too_much_precision", src: "0.00000000000000000001", wantErr: ErrPrecOutOfRange},
		{name: "unsupported_bool", src: true, wantErr: ErrScanType},
		{name: "unsupported_time", src: time.Unix(0, 0), wantErr: ErrScanType},
		{name: "unsupported_float32", src: float32(1.5), wantErr: ErrScanType},
		{name: "unsupported_struct", src: struct{}{}, wantErr: ErrScanType},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString("42.5")
			err := d.Scan(tc.src)
			require.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, RequireFromString("42.5"), d, "failed Scan must not modify the receiver")
		})
	}
}

// TestScanTypeErrorNamesType verifies the precomputed scan-type errors report
// the offending Go type, the detail that makes driver misconfiguration
// debuggable. bool and time.Time — the legal driver.Value types Scan cannot
// convert — still name the offending type; other types wrap ErrScanType bare.
func TestScanTypeErrorNamesType(t *testing.T) {
	var d Decimal

	errBool := d.Scan(true)
	require.ErrorIs(t, errBool, ErrScanType)
	assert.Contains(t, errBool.Error(), "bool")

	errTime := d.Scan(time.Unix(0, 0))
	require.ErrorIs(t, errTime, ErrScanType)
	assert.Contains(t, errTime.Error(), "time.Time")

	errStruct := d.Scan(struct{}{})
	require.ErrorIs(t, errStruct, ErrScanType)
}

func TestValue(t *testing.T) {
	for _, tc := range codecBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := RequireFromString(tc.str).Value()
			require.NoError(t, err)
			assert.Equal(t, driver.Value(tc.str), v, "Value must be the canonical string")
		})
	}
}

func TestScanValueRoundTrip(t *testing.T) {
	for _, tc := range codecBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			var d Decimal
			require.NoError(t, d.Scan(tc.str))

			v, err := d.Value()
			require.NoError(t, err)

			var got Decimal
			require.NoError(t, got.Scan(v))
			assert.Equal(t, d, got)
		})
	}
}

func TestNewNullDecimal(t *testing.T) {
	n := NewNullDecimal(RequireFromString("1.5"))
	assert.True(t, n.Valid)
	assert.Equal(t, RequireFromString("1.5"), n.Decimal)
}

func TestNullDecimalScan(t *testing.T) {
	t.Run("nil_clears_to_invalid", func(t *testing.T) {
		n := NewNullDecimal(RequireFromString("1.5"))
		require.NoError(t, n.Scan(nil))
		assert.Equal(t, NullDecimal{}, n)
	})
	t.Run("value_sets_valid", func(t *testing.T) {
		var n NullDecimal
		require.NoError(t, n.Scan("-999.99"))
		assert.True(t, n.Valid)
		assert.Equal(t, RequireFromString("-999.99"), n.Decimal)
	})
	t.Run("int64_sets_valid", func(t *testing.T) {
		var n NullDecimal
		require.NoError(t, n.Scan(int64(-7)))
		assert.True(t, n.Valid)
		assert.Equal(t, NewFromInt(-7), n.Decimal)
	})
	t.Run("error_clears_to_invalid", func(t *testing.T) {
		n := NewNullDecimal(RequireFromString("1.5"))
		err := n.Scan("garbage")
		require.ErrorIs(t, err, ErrInvalidFormat)
		assert.Equal(t, NullDecimal{}, n, "a failed scan must not leave a stale value behind")
	})
	t.Run("unsupported_type_clears_to_invalid", func(t *testing.T) {
		n := NewNullDecimal(RequireFromString("1.5"))
		err := n.Scan(true)
		require.ErrorIs(t, err, ErrScanType)
		assert.Equal(t, NullDecimal{}, n)
	})
}

func TestNullDecimalValue(t *testing.T) {
	t.Run("invalid_is_sql_null", func(t *testing.T) {
		var n NullDecimal
		v, err := n.Value()
		require.NoError(t, err)
		assert.Nil(t, v)
	})
	t.Run("valid_is_canonical_string", func(t *testing.T) {
		v, err := NewNullDecimal(RequireFromString("1234.5678")).Value()
		require.NoError(t, err)
		assert.Equal(t, driver.Value("1234.5678"), v)
	})
}

func TestNullDecimalJSON(t *testing.T) {
	t.Run("marshal_invalid_is_null", func(t *testing.T) {
		b, err := NullDecimal{}.MarshalJSON()
		require.NoError(t, err)
		assert.Equal(t, "null", string(b))
	})
	t.Run("marshal_valid_is_quoted", func(t *testing.T) {
		b, err := NewNullDecimal(RequireFromString("1.5")).MarshalJSON()
		require.NoError(t, err)
		assert.Equal(t, `"1.5"`, string(b))
	})
	t.Run("unmarshal_null_clears_to_invalid", func(t *testing.T) {
		n := NewNullDecimal(RequireFromString("1.5"))
		require.NoError(t, n.UnmarshalJSON([]byte("null")))
		assert.Equal(t, NullDecimal{}, n)
	})
	t.Run("unmarshal_quoted_sets_valid", func(t *testing.T) {
		var n NullDecimal
		require.NoError(t, n.UnmarshalJSON([]byte(`"2.5"`)))
		assert.True(t, n.Valid)
		assert.Equal(t, RequireFromString("2.5"), n.Decimal)
	})
	t.Run("unmarshal_unquoted_sets_valid", func(t *testing.T) {
		var n NullDecimal
		require.NoError(t, n.UnmarshalJSON([]byte(`2.5`)))
		assert.True(t, n.Valid)
		assert.Equal(t, RequireFromString("2.5"), n.Decimal)
	})
	t.Run("unmarshal_garbage_leaves_unchanged", func(t *testing.T) {
		n := NewNullDecimal(RequireFromString("1.5"))
		err := n.UnmarshalJSON([]byte(`"abc"`))
		require.ErrorIs(t, err, ErrInvalidFormat)
		assert.Equal(t, NewNullDecimal(RequireFromString("1.5")), n)
	})
}

func TestNullDecimalText(t *testing.T) {
	t.Run("marshal_invalid_is_empty", func(t *testing.T) {
		b, err := NullDecimal{}.MarshalText()
		require.NoError(t, err)
		assert.Empty(t, b)
	})
	t.Run("marshal_valid_is_canonical", func(t *testing.T) {
		b, err := NewNullDecimal(RequireFromString("-0.5")).MarshalText()
		require.NoError(t, err)
		assert.Equal(t, "-0.5", string(b))
	})
	t.Run("unmarshal_empty_clears_to_invalid", func(t *testing.T) {
		n := NewNullDecimal(RequireFromString("1.5"))
		require.NoError(t, n.UnmarshalText(nil))
		assert.Equal(t, NullDecimal{}, n)
	})
	t.Run("unmarshal_value_sets_valid", func(t *testing.T) {
		var n NullDecimal
		require.NoError(t, n.UnmarshalText([]byte("3.5")))
		assert.True(t, n.Valid)
		assert.Equal(t, RequireFromString("3.5"), n.Decimal)
	})
	t.Run("unmarshal_garbage_leaves_unchanged", func(t *testing.T) {
		n := NewNullDecimal(RequireFromString("1.5"))
		err := n.UnmarshalText([]byte("abc"))
		require.ErrorIs(t, err, ErrInvalidFormat)
		assert.Equal(t, NewNullDecimal(RequireFromString("1.5")), n)
	})
}
