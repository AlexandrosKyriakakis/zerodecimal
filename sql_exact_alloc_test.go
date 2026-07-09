package zerodecimal

import (
	"database/sql/driver"
	"testing"
	"time"
)

var (
	strictSQLAllocDst      = NewStrictSQLDecimal(RequireFromString("1.25"))
	errStrictSQLAlloc      error
	strictSQLAllocValue    driver.Value
	strictSQLAllocUncached = NewStrictSQLDecimal(RequireFromString("1234.5678"))
	strictSQLAllocCached   = NewStrictSQLDecimal(RequireFromString("1.25"))
)

var strictSQLAllocSources = []struct {
	name string
	src  any
}{
	{name: "string_valid", src: "1234.5678"},
	{name: "bytes_valid", src: []byte("-0.125")},
	{name: "int", src: int(-1)},
	{name: "int8", src: int8(-1)},
	{name: "int16", src: int16(-1)},
	{name: "int32", src: int32(-1)},
	{name: "int64", src: int64(-1)},
	{name: "uint", src: uint(1)},
	{name: "uint8", src: uint8(1)},
	{name: "uint16", src: uint16(1)},
	{name: "uint32", src: uint32(1)},
	{name: "uint64", src: uint64(1)},
	{name: "string_invalid", src: "not-a-number"},
	{name: "bytes_invalid", src: []byte("1..2")},
	{name: "float32", src: float32(0.5)},
	{name: "float64", src: float64(0.5)},
	{name: "bool", src: true},
	{name: "time", src: time.Unix(0, 0)},
	{name: "nil", src: nil},
	{name: "uintptr", src: uintptr(1)},
	{name: "unknown", src: struct{}{}},
}

func TestStrictSQLDecimalScanAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, tc := range strictSQLAllocSources {
		t.Run(tc.name, func(t *testing.T) {
			fn := func() { errStrictSQLAlloc = strictSQLAllocDst.Scan(tc.src) }
			requireAllocs(t, 0, fn)
		})
	}
}

func TestStrictSQLDecimalValueAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	requireAllocs(t, 2, func() {
		strictSQLAllocValue, errStrictSQLAlloc = strictSQLAllocUncached.Value()
	})
	if strCacheEnabled {
		requireAllocs(t, 0, func() {
			strictSQLAllocValue, errStrictSQLAlloc = strictSQLAllocCached.Value()
		})
	}
}
