package zerodecimal

import "testing"

var (
	strictSQLBenchDst = NewStrictSQLDecimal(RequireFromString("1.25"))
	errStrictSQLBench error
)

func BenchmarkStrictSQLDecimalScan(b *testing.B) {
	tests := []struct {
		name string
		src  any
	}{
		{name: "string", src: "12345678901234567890.1234567890123456789"},
		{name: "bytes", src: []byte("12345678901234567890.1234567890123456789")},
		{name: "int64", src: int64(-9223372036854775808)},
		{name: "uint64", src: uint64(18446744073709551615)},
		{name: "float64_rejected", src: float64(0.5)},
		{name: "nil_rejected", src: nil},
		{name: "invalid_string", src: "not-a-number"},
	}
	for _, tc := range tests {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				errStrictSQLBench = strictSQLBenchDst.Scan(tc.src)
			}
		})
	}
}
