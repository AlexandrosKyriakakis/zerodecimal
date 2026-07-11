package zerodecimal

import "testing"

var (
	fixedBenchString string
	fixedBenchBytes  []byte
)

var fixedBenchmarkCases = []struct {
	name   string
	d      Decimal
	places uint8
}{
	{name: "zero_places_0", d: Zero, places: 0},
	{name: "typical_places_4", d: RequireFromString("1234.5678"), places: 4},
	{name: "max_coef_prec_19_places_19", d: Decimal{coef: u128{hi: maxUint64, lo: maxUint64}, prec: 19}, places: 19},
	{name: "max_coef_places_47", d: Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}, places: 47},
	{name: "negative_max_coef_places_48", d: Decimal{coef: u128{hi: maxUint64, lo: maxUint64}, neg: true}, places: 48},
	{name: "max_prec_places_100", d: Decimal{coef: u128{hi: maxUint64, lo: maxUint64}, prec: 19}, places: 100},
	{name: "max_coef_places_255", d: Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}, places: 255},
}

func BenchmarkStringFixed(b *testing.B) {
	for _, tc := range fixedBenchmarkCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				fixedBenchString = tc.d.StringFixed(tc.places)
			}
		})
	}
}

func BenchmarkAppendFixedPreallocated(b *testing.B) {
	for _, tc := range fixedBenchmarkCases {
		b.Run(tc.name, func(b *testing.B) {
			capacity := len(tc.d.StringFixed(tc.places))
			dst := make([]byte, 0, capacity)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				fixedBenchBytes = tc.d.AppendFixed(dst[:0], tc.places)
			}
		})
	}
}
