package zerodecimal

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"math"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStrictSQLDecimalAcceptedSources(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	maxUint := ^uint(0)
	tests := []struct {
		name string
		src  any
		want string
	}{
		{name: "string", src: "-1234.5678", want: "-1234.5678"},
		{name: "string_scientific", src: "1.5e3", want: "1500"},
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
			got := NewStrictSQLDecimal(RequireFromString("99.5"))
			require.NoError(t, got.Scan(tc.src))
			assert.Equal(t, RequireFromString(tc.want), got.Decimal)
		})
	}
}

func TestStrictSQLDecimalRejectsFloatProvenance(t *testing.T) {
	tests := []struct {
		name string
		src  any
	}{
		{name: "float32_exact", src: float32(0.5)},
		{name: "float32_inexact", src: float32(0.1)},
		{name: "float64_exact", src: float64(0.5)},
		{name: "float64_inexact", src: float64(0.1)},
		{name: "float64_nan", src: math.NaN()},
		{name: "float64_pos_inf", src: math.Inf(1)},
		{name: "float64_neg_inf", src: math.Inf(-1)},
	}
	marker := NewStrictSQLDecimal(RequireFromString("42.25"))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := marker
			err := got.Scan(tc.src)
			require.ErrorIs(t, err, ErrScanFloat)
			assert.Equal(t, marker, got, "a rejected source must not modify the receiver")
		})
	}
	assert.NotErrorIs(t, ErrScanFloat, ErrInexact,
		"source-provenance policy must remain distinct from mathematical inexactness")
}

func TestStrictSQLDecimalErrorsAreAtomic(t *testing.T) {
	tests := []struct {
		name    string
		src     any
		wantErr error
	}{
		{name: "sql_null", src: nil, wantErr: ErrScanNil},
		{name: "invalid_string", src: "not-a-number", wantErr: ErrInvalidFormat},
		{name: "invalid_bytes", src: []byte("1..2"), wantErr: ErrInvalidFormat},
		{name: "empty_bytes", src: []byte(nil), wantErr: ErrEmptyString},
		{name: "bool", src: true, wantErr: ErrScanType},
		{name: "time", src: time.Unix(0, 0), wantErr: ErrScanType},
		{name: "uintptr", src: uintptr(1), wantErr: ErrScanType},
		{name: "unknown", src: struct{}{}, wantErr: ErrScanType},
	}
	marker := NewStrictSQLDecimal(RequireFromString("987.654"))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := marker
			err := got.Scan(tc.src)
			require.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, marker, got, "a failed Scan must be receiver-atomic")
		})
	}
}

func TestStrictSQLDecimalConstructionAndValue(t *testing.T) {
	d := RequireFromString("1234.5678")
	strict := NewStrictSQLDecimal(d)
	assert.Equal(t, d, strict.Decimal)
	assert.Equal(t, unsafe.Sizeof(d), unsafe.Sizeof(strict), "the wrapper must add no storage")

	v, err := strict.Value()
	require.NoError(t, err)
	assert.Equal(t, driver.Value("1234.5678"), v)

	zero, err := (StrictSQLDecimal{}).Value()
	require.NoError(t, err)
	assert.Equal(t, driver.Value("0"), zero, "a required value must never emit SQL NULL")
}

func TestStrictSQLDecimalNilPointerBindBecomesSQLNull(t *testing.T) {
	db := sql.OpenDB(strictSQLTestConnector{echoArgument: true})
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	// database/sql deliberately converts a nil pointer implementing Valuer to
	// SQL NULL without invoking its value-receiver Value method. Required bind
	// validation must therefore happen before the call into database/sql.
	var parameter *StrictSQLDecimal
	got := NewStrictNullDecimal(RequireFromString("77.7"))
	require.NoError(t, db.QueryRowContext(context.Background(), "echo", parameter).Scan(&got))
	assert.Equal(t, StrictNullDecimal{}, got)

	var required StrictSQLDecimal
	err := db.QueryRowContext(context.Background(), "echo", parameter).Scan(&required)
	require.ErrorIs(t, err, ErrScanNil)
}

func TestStrictSQLDecimalValueRoundTrip(t *testing.T) {
	for _, tc := range codecBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			want := NewStrictSQLDecimal(RequireFromString(tc.str))
			v, err := want.Value()
			require.NoError(t, err)

			var got StrictSQLDecimal
			require.NoError(t, got.Scan(v))
			assert.Equal(t, want, got)
		})
	}
}

func TestStrictSQLDecimalLegacyScanCompatibility(t *testing.T) {
	var legacy Decimal
	require.NoError(t, legacy.Scan(float64(0.5)))
	assert.Equal(t, RequireFromString("0.5"), legacy)

	strict := NewStrictSQLDecimal(RequireFromString("7"))
	require.ErrorIs(t, strict.Scan(float64(0.5)), ErrScanFloat)
	assert.Equal(t, RequireFromString("7"), strict.Decimal)
}

// strictSQLTestConnector and the driver below exercise the real database/sql
// conversion pipeline without requiring a networked database. In echo mode,
// database/sql must call StrictSQLDecimal.Value before the driver returns that
// driver.Value as a row. In source mode, the configured legal driver.Value is
// returned directly to StrictSQLDecimal.Scan.
type strictSQLTestConnector struct {
	source       driver.Value
	echoArgument bool
}

func (c strictSQLTestConnector) Connect(context.Context) (driver.Conn, error) {
	return strictSQLTestConn(c), nil
}

func (strictSQLTestConnector) Driver() driver.Driver {
	return strictSQLTestDriver{}
}

type strictSQLTestDriver struct{}

func (strictSQLTestDriver) Open(string) (driver.Conn, error) {
	return nil, driver.ErrBadConn
}

type strictSQLTestConn strictSQLTestConnector

func (strictSQLTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, driver.ErrSkip
}

func (strictSQLTestConn) Close() error {
	return nil
}

func (strictSQLTestConn) Begin() (driver.Tx, error) {
	return nil, driver.ErrSkip
}

func (c strictSQLTestConn) QueryContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
	src := c.source
	if c.echoArgument {
		if len(args) != 1 || !driver.IsValue(args[0].Value) {
			return nil, driver.ErrSkip
		}
		src = args[0].Value
	}
	return &strictSQLTestRows{source: src}, nil
}

type strictSQLTestRows struct {
	source driver.Value
	done   bool
}

func (*strictSQLTestRows) Columns() []string {
	return []string{"amount"}
}

func (*strictSQLTestRows) Close() error {
	return nil
}

func (r *strictSQLTestRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	dest[0] = r.source
	r.done = true
	return nil
}

func TestStrictSQLDecimalDatabaseSQLRoundTrip(t *testing.T) {
	db := sql.OpenDB(strictSQLTestConnector{echoArgument: true})
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	for _, tc := range codecBoundaryCases {
		t.Run(tc.name, func(t *testing.T) {
			want := NewStrictSQLDecimal(RequireFromString(tc.str))
			var got StrictSQLDecimal
			err := db.QueryRowContext(context.Background(), "echo", want).Scan(&got)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestStrictSQLDecimalDatabaseSQLSources(t *testing.T) {
	tests := []struct {
		name    string
		src     driver.Value
		want    string
		wantErr error
	}{
		{name: "string", src: "123.45", want: "123.45"},
		{name: "bytes", src: []byte("-0.125"), want: "-0.125"},
		{name: "int64", src: int64(math.MinInt64), want: "-9223372036854775808"},
		{name: "float64", src: float64(0.5), wantErr: ErrScanFloat},
		{name: "bool", src: true, wantErr: ErrScanType},
		{name: "time", src: time.Unix(0, 0), wantErr: ErrScanType},
		{name: "null", src: nil, wantErr: ErrScanNil},
	}
	marker := NewStrictSQLDecimal(RequireFromString("77.7"))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := sql.OpenDB(strictSQLTestConnector{source: tc.src})
			t.Cleanup(func() { require.NoError(t, db.Close()) })

			got := marker
			err := db.QueryRowContext(context.Background(), "source").Scan(&got)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Equal(t, marker, got, "database/sql errors must remain receiver-atomic")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, RequireFromString(tc.want), got.Decimal)
		})
	}
}
