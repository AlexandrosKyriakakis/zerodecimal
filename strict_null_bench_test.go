package zerodecimal

import (
	"database/sql/driver"
	"testing"
)

var (
	strictNullBenchDst   = NewStrictNullDecimal(RequireFromString("1.25"))
	strictNullBenchValid = NewStrictNullDecimal(RequireFromString("12345678901234567890.1234567890123456789"))
	strictNullBenchNull  StrictNullDecimal
	strictNullBenchValue driver.Value
	errStrictNullBench   error
)

func BenchmarkStrictNullDecimalScan(b *testing.B) {
	tests := []struct {
		name string
		src  any
	}{
		{name: "null", src: nil},
		{name: "string", src: "12345678901234567890.1234567890123456789"},
		{name: "bytes", src: []byte("12345678901234567890.1234567890123456789")},
		{name: "integer", src: int64(-9223372036854775808)},
		{name: "rejected_float", src: float64(0.5)},
		{name: "invalid_string", src: "not-a-number"},
	}
	for _, tc := range tests {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				errStrictNullBench = strictNullBenchDst.Scan(tc.src)
			}
		})
	}
}

func BenchmarkStrictNullDecimalValue(b *testing.B) {
	b.Run("null", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			strictNullBenchValue, errStrictNullBench = strictNullBenchNull.Value()
		}
	})
	b.Run("valid", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			strictNullBenchValue, errStrictNullBench = strictNullBenchValid.Value()
		}
	})
}
