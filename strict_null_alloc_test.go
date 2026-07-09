package zerodecimal

import (
	"database/sql/driver"
	"testing"
	"time"
)

var (
	strictNullAllocMarker   = NewStrictNullDecimal(RequireFromString("1.25"))
	strictNullAllocDst      = strictNullAllocMarker
	strictNullAllocUncached = NewStrictNullDecimal(RequireFromString("1234.5678"))
	strictNullAllocCached   = NewStrictNullDecimal(RequireFromString("1.25"))
	strictNullAllocValue    driver.Value
	strictNullAllocBytes    []byte
	errStrictNullAlloc      error
	strictNullAllocNil      *StrictNullDecimal

	strictNullAllocJSONValue   = []byte(`"1234\u002e5678"`)
	strictNullAllocJSONNull    = []byte(jsonNull)
	strictNullAllocJSONInvalid = []byte(`"x"`)
	strictNullAllocTextValue   = []byte("1234.5678")
	strictNullAllocTextInvalid = []byte("x")
)

var strictNullAllocSources = []struct {
	name string
	src  any
}{
	{name: "null", src: nil},
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
	{name: "uintptr", src: uintptr(1)},
	{name: "unknown", src: struct{}{}},
}

func TestStrictNullDecimalScanAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, tc := range strictNullAllocSources {
		t.Run(tc.name, func(t *testing.T) {
			fn := func() {
				strictNullAllocDst = strictNullAllocMarker
				errStrictNullAlloc = strictNullAllocDst.Scan(tc.src)
			}
			requireAllocs(t, 0, fn)
		})
	}
}

func TestStrictNullDecimalValueAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	requireAllocs(t, 0, func() {
		strictNullAllocValue, errStrictNullAlloc = (StrictNullDecimal{}).Value()
	})
	requireAllocs(t, 2, func() {
		strictNullAllocValue, errStrictNullAlloc = strictNullAllocUncached.Value()
	})
	if strCacheEnabled {
		requireAllocs(t, 0, func() {
			strictNullAllocValue, errStrictNullAlloc = strictNullAllocCached.Value()
		})
	}
}

func TestStrictNullDecimalCodecAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	tests := []struct {
		name string
		want float64
		fn   func()
	}{
		{name: "unmarshal_json_value", want: 0, fn: func() {
			strictNullAllocDst = strictNullAllocMarker
			errStrictNullAlloc = strictNullAllocDst.UnmarshalJSON(strictNullAllocJSONValue)
		}},
		{name: "unmarshal_json_null", want: 0, fn: func() {
			strictNullAllocDst = strictNullAllocMarker
			errStrictNullAlloc = strictNullAllocDst.UnmarshalJSON(strictNullAllocJSONNull)
		}},
		{name: "unmarshal_json_invalid", want: 0, fn: func() {
			strictNullAllocDst = strictNullAllocMarker
			errStrictNullAlloc = strictNullAllocDst.UnmarshalJSON(strictNullAllocJSONInvalid)
		}},
		{name: "unmarshal_text_value", want: 0, fn: func() {
			strictNullAllocDst = strictNullAllocMarker
			errStrictNullAlloc = strictNullAllocDst.UnmarshalText(strictNullAllocTextValue)
		}},
		{name: "unmarshal_text_null", want: 0, fn: func() {
			strictNullAllocDst = strictNullAllocMarker
			errStrictNullAlloc = strictNullAllocDst.UnmarshalText(nil)
		}},
		{name: "unmarshal_text_invalid", want: 0, fn: func() {
			strictNullAllocDst = strictNullAllocMarker
			errStrictNullAlloc = strictNullAllocDst.UnmarshalText(strictNullAllocTextInvalid)
		}},
		{name: "marshal_json_value", want: 1, fn: func() {
			strictNullAllocBytes, errStrictNullAlloc = strictNullAllocUncached.MarshalJSON()
		}},
		{name: "marshal_json_null", want: 1, fn: func() {
			strictNullAllocBytes, errStrictNullAlloc = (StrictNullDecimal{}).MarshalJSON()
		}},
		{name: "marshal_text_value", want: 1, fn: func() {
			strictNullAllocBytes, errStrictNullAlloc = strictNullAllocUncached.MarshalText()
		}},
		{name: "marshal_text_null", want: 0, fn: func() {
			strictNullAllocBytes, errStrictNullAlloc = (StrictNullDecimal{}).MarshalText()
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, tc.want, tc.fn)
		})
	}
}

func TestStrictNullDecimalNilReceiverAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	tests := []struct {
		name string
		fn   func()
	}{
		{name: "scan", fn: func() { errStrictNullAlloc = strictNullAllocNil.Scan(strictNullAllocTextValue) }},
		{name: "unmarshal_json", fn: func() { errStrictNullAlloc = strictNullAllocNil.UnmarshalJSON(strictNullAllocJSONValue) }},
		{name: "unmarshal_text", fn: func() { errStrictNullAlloc = strictNullAllocNil.UnmarshalText(strictNullAllocTextValue) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, 0, tc.fn)
		})
	}
}
