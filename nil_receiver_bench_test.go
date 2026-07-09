package zerodecimal

import "testing"

var (
	nilReceiverBenchText    = []byte("1234.5678")
	nilReceiverBenchJSON    = []byte(`"1234.5678"`)
	nilReceiverBenchBinary  = mustNilReceiverBenchBinary()
	nilReceiverBenchDecimal Decimal
	nilReceiverBenchStrict  StrictSQLDecimal
	nilReceiverBenchNull    NullDecimal
	nilReceiverBenchNilD    *Decimal
	nilReceiverBenchNilS    *StrictSQLDecimal
	nilReceiverBenchNilN    *NullDecimal
	errNilReceiverBench     error
)

func mustNilReceiverBenchBinary() []byte {
	b, err := RequireFromString("1234.5678").MarshalBinary()
	if err != nil {
		panic(err)
	}
	return b
}

// BenchmarkNilReceiverPaths measures the fail-fast branches independently.
func BenchmarkNilReceiverPaths(b *testing.B) {
	b.Run("decimal_unmarshal_text", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilD.UnmarshalText(nilReceiverBenchText)
		}
	})
	b.Run("decimal_unmarshal_json", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilD.UnmarshalJSON(nilReceiverBenchJSON)
		}
	})
	b.Run("decimal_unmarshal_binary", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilD.UnmarshalBinary(nilReceiverBenchBinary)
		}
	})
	b.Run("decimal_scan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilD.Scan("1234.5678")
		}
	})
	b.Run("strict_sql_decimal_scan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilS.Scan("1234.5678")
		}
	})
	b.Run("strict_sql_decimal_unmarshal_text", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilS.UnmarshalText(nilReceiverBenchText)
		}
	})
	b.Run("strict_sql_decimal_unmarshal_json", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilS.UnmarshalJSON(nilReceiverBenchJSON)
		}
	})
	b.Run("strict_sql_decimal_unmarshal_binary", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilS.UnmarshalBinary(nilReceiverBenchBinary)
		}
	})
	b.Run("null_decimal_scan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilN.Scan("1234.5678")
		}
	})
	b.Run("null_decimal_unmarshal_text", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilN.UnmarshalText(nilReceiverBenchText)
		}
	})
	b.Run("null_decimal_unmarshal_json", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNilN.UnmarshalJSON(nilReceiverBenchJSON)
		}
	})
}

// BenchmarkFallibleReceiverHotPaths measures the non-nil branches that gain
// a receiver check, keeping any production-path cost visible independently.
func BenchmarkFallibleReceiverHotPaths(b *testing.B) {
	b.Run("decimal_unmarshal_text", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchDecimal.UnmarshalText(nilReceiverBenchText)
		}
	})
	b.Run("decimal_unmarshal_json", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchDecimal.UnmarshalJSON(nilReceiverBenchJSON)
		}
	})
	b.Run("decimal_unmarshal_binary", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchDecimal.UnmarshalBinary(nilReceiverBenchBinary)
		}
	})
	b.Run("decimal_scan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchDecimal.Scan("1234.5678")
		}
	})
	b.Run("strict_sql_decimal_scan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchStrict.Scan("1234.5678")
		}
	})
	b.Run("strict_sql_decimal_unmarshal_text", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchStrict.UnmarshalText(nilReceiverBenchText)
		}
	})
	b.Run("strict_sql_decimal_unmarshal_json", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchStrict.UnmarshalJSON(nilReceiverBenchJSON)
		}
	})
	b.Run("strict_sql_decimal_unmarshal_binary", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchStrict.UnmarshalBinary(nilReceiverBenchBinary)
		}
	})
	b.Run("null_decimal_scan", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNull.Scan("1234.5678")
		}
	})
	b.Run("null_decimal_unmarshal_text", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNull.UnmarshalText(nilReceiverBenchText)
		}
	})
	b.Run("null_decimal_unmarshal_json", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			errNilReceiverBench = nilReceiverBenchNull.UnmarshalJSON(nilReceiverBenchJSON)
		}
	})
}
