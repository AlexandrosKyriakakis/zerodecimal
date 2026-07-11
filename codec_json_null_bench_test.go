package zerodecimal

import "testing"

var (
	jsonNullBenchInput   = []byte(jsonNull)
	jsonNullBenchValue   = []byte(`"1234.5678"`)
	jsonNullBenchMarker  = RequireFromString("1234.5678")
	jsonNullBenchDecimal Decimal
	jsonNullBenchNull    NullDecimal
	errJSONNullBench     error
)

func BenchmarkUnmarshalJSONNullPolicy(b *testing.B) {
	b.Run("decimal_reject_null", func(b *testing.B) {
		jsonNullBenchDecimal = jsonNullBenchMarker
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonNullBenchInput)))
		for b.Loop() {
			errJSONNullBench = jsonNullBenchDecimal.UnmarshalJSON(jsonNullBenchInput)
		}
	})

	b.Run("null_decimal_clear_null", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonNullBenchInput)))
		for b.Loop() {
			jsonNullBenchNull = NewNullDecimal(jsonNullBenchMarker)
			errJSONNullBench = jsonNullBenchNull.UnmarshalJSON(jsonNullBenchInput)
		}
	})

	b.Run("decimal_accept_quoted_value", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(jsonNullBenchValue)))
		for b.Loop() {
			errJSONNullBench = jsonNullBenchDecimal.UnmarshalJSON(jsonNullBenchValue)
		}
	})
}
