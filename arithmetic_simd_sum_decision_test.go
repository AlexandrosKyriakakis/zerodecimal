//go:build go1.27 && !go1.28 && goexperiment.simd && amd64

package zerodecimal

import (
	"simd/archsimd"
	"strconv"
	"testing"
)

func arithScalarSumReference(ds []Decimal) (Decimal, error) {
	first, rest := ds[0], ds[1:]
	for first.coef.isZero() && len(rest) > 0 {
		first, rest = rest[0], rest[1:]
	}
	return sumScalar(first, rest)
}

func BenchmarkArithmeticSIMDSumDecision(b *testing.B) {
	if !archsimd.X86.AVX2() {
		b.Skip("CPU does not expose AVX2")
	}
	b.ReportAllocs()

	for _, size := range []int{8, 16, 32, 64, 128, 256, 1024, 4096} {
		ds := make([]Decimal, size)
		for i := range ds {
			ds[i] = newDecimal(u128{lo: uint64(i)*0x9e3779b97f4a7c15 + 1}, false, 4)
		}
		name := strconv.Itoa(size)
		b.Run("Positive64/"+name+"/scalar", func(b *testing.B) {
			var result Decimal
			var err error
			for b.Loop() {
				result, err = arithScalarSumReference(ds)
			}
			if err != nil {
				b.Fatal(err)
			}
			arithAVX2DecimalSink = result
		})
		b.Run("Positive64/"+name+"/simd", func(b *testing.B) {
			var result Decimal
			var err error
			for b.Loop() {
				result, err = Sum(ds[0], ds[1:]...)
			}
			if err != nil {
				b.Fatal(err)
			}
			arithAVX2DecimalSink = result
		})
	}

	compatible := make([]Decimal, 4096)
	for i := range compatible {
		compatible[i] = newDecimal(u128{lo: uint64(i)*0x9e3779b97f4a7c15 + 1}, false, 4)
	}
	negativeLast := append([]Decimal(nil), compatible...)
	negativeLast[len(negativeLast)-1].neg = true
	mixedPrecisionLast := append([]Decimal(nil), compatible...)
	mixedPrecisionLast[len(mixedPrecisionLast)-1].prec = 3
	wideLast := append([]Decimal(nil), compatible...)
	wideLast[len(wideLast)-1].coef.hi = 1

	for _, tc := range []struct {
		name string
		ds   []Decimal
	}{
		{name: "NegativeLast", ds: negativeLast},
		{name: "MixedPrecisionLast", ds: mixedPrecisionLast},
		{name: "WideLast", ds: wideLast},
	} {
		b.Run("Fallback4096/"+tc.name+"/scalar", func(b *testing.B) {
			var result Decimal
			var err error
			for b.Loop() {
				result, err = arithScalarSumReference(tc.ds)
			}
			if err != nil {
				b.Fatal(err)
			}
			arithAVX2DecimalSink = result
		})
		b.Run("Fallback4096/"+tc.name+"/continuation", func(b *testing.B) {
			var result Decimal
			var err error
			for b.Loop() {
				result, err = Sum(tc.ds[0], tc.ds[1:]...)
			}
			if err != nil {
				b.Fatal(err)
			}
			arithAVX2DecimalSink = result
		})
	}
}
