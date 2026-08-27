package zerodecimal

import "testing"

var (
	digitRunBenchSink Decimal
	errDigitRunBench  error
)

// BenchmarkParseLong measures the plain long-input path where Go 1.27's
// experimental SIMD scanner can replace the portable eight-byte scan.
func BenchmarkParseLong(b *testing.B) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"21_digits", "123456789012345678901"},
		{"22_digits", "1234567890123456789012"},
		{"24_digits", "123456789012345678901234"},
		{"26_digits", "12345678901234567890123456"},
		{"28_digits", "1234567890123456789012345678"},
		{"30_digits", "123456789012345678901234567890"},
		{"32_digits", "12345678901234567890123456789012"},
		{"39_digits", "123456789012345678901234567890123456789"},
		{"max_uint128", "340282366920938463463374607431768211455"},
		{"21_byte_decimal", "1.1234567890123456789"},
		{"22_byte_decimal", "12.1234567890123456789"},
		{"24_byte_decimal", "1234.1234567890123456789"},
		{"26_byte_decimal", "123456.1234567890123456789"},
		{"28_byte_decimal", "12345678.1234567890123456789"},
		{"32_byte_decimal", "123456789012.1234567890123456789"},
		{"39_byte_decimal", "1234567890123456789.1234567890123456789"},
		{"40_byte_decimal", "12345678901234567890.1234567890123456789"},
		{"invalid_at_40", "123456789012345678901234567890123456789x"},
		{"80_digits", "12345678901234567890123456789012345678901234567890123456789012345678901234567890"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				digitRunBenchSink, errDigitRunBench = NewFromString(tc.text)
			}
		})
	}
}
