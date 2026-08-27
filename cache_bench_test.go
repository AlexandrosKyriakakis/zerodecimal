package zerodecimal

import "testing"

var (
	cacheBenchString string
	cacheBenchOK     bool
)

func cacheBenchmarkValues(n int) []Decimal {
	values := make([]Decimal, n)
	const stride = 7919 // coprime to 200001: walk the cache without clustering
	for i := range values {
		cents := (i*stride)%(2*cacheSpan+1) - cacheSpan
		neg := cents < 0
		if neg {
			cents = -cents
		}
		values[i] = newDecimal(u128{lo: uint64(cents)}, neg, 2)
	}
	return values
}

// BenchmarkCachedString isolates cache probe cost without result allocation.
// The hit cases run only in an explicit zerodecimal_strcache build; miss is
// reported in every mode so the compiled-out probe cost remains visible.
func BenchmarkCachedString(b *testing.B) {
	b.Run("hot_hit", func(b *testing.B) {
		if !strCacheEnabled {
			b.Skip("run with -tags=zerodecimal_strcache")
		}
		d := RequireFromString("123.45")
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			cacheBenchString, cacheBenchOK = cachedString(d)
		}
	})

	b.Run("dispersed_hits_8192", func(b *testing.B) {
		if !strCacheEnabled {
			b.Skip("run with -tags=zerodecimal_strcache")
		}
		values := cacheBenchmarkValues(8192)
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			cacheBenchString, cacheBenchOK = cachedString(values[i&(len(values)-1)])
		}
	})

	b.Run("dispersed_hits_full_window", func(b *testing.B) {
		if !strCacheEnabled {
			b.Skip("run with -tags=zerodecimal_strcache")
		}
		values := cacheBenchmarkValues(2*cacheSpan + 1)
		b.ReportAllocs()
		b.ResetTimer()
		idx := 0
		for range b.N {
			cacheBenchString, cacheBenchOK = cachedString(values[idx])
			idx++
			if idx == len(values) {
				idx = 0
			}
		}
	})

	b.Run("miss_prec_3", func(b *testing.B) {
		d := RequireFromString("123.456")
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			cacheBenchString, cacheBenchOK = cachedString(d)
		}
	})
}

// BenchmarkStringCachePaths records the end-to-end String tradeoff: cached
// hot/dispersed values versus an uncached formatter result.
func BenchmarkStringCachePaths(b *testing.B) {
	b.Run("hot_hit", func(b *testing.B) {
		if !strCacheEnabled {
			b.Skip("run with -tags=zerodecimal_strcache")
		}
		d := RequireFromString("123.45")
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			cacheBenchString = d.String()
		}
	})

	b.Run("dispersed_hits_8192", func(b *testing.B) {
		if !strCacheEnabled {
			b.Skip("run with -tags=zerodecimal_strcache")
		}
		values := cacheBenchmarkValues(8192)
		b.ReportAllocs()
		b.ResetTimer()
		for i := range b.N {
			cacheBenchString = values[i&(len(values)-1)].String()
		}
	})

	b.Run("dispersed_hits_full_window", func(b *testing.B) {
		if !strCacheEnabled {
			b.Skip("run with -tags=zerodecimal_strcache")
		}
		values := cacheBenchmarkValues(2*cacheSpan + 1)
		b.ReportAllocs()
		b.ResetTimer()
		idx := 0
		for range b.N {
			cacheBenchString = values[idx].String()
			idx++
			if idx == len(values) {
				idx = 0
			}
		}
	})

	b.Run("miss_prec_3", func(b *testing.B) {
		d := RequireFromString("123.456")
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			cacheBenchString = d.String()
		}
	})
}
