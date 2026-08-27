package zerodecimal

import "testing"

var (
	quoremBenchQ   Decimal
	quoremBenchR   Decimal
	errQuoremBench error
)

var quoremAlignmentBenchCases = []struct {
	name string
	d, e Decimal
}{
	{
		name: "same_precision_64bit_fast",
		d:    newDecimal(u128{lo: 123_456_789}, false, 4),
		e:    newDecimal(u128{lo: 97}, false, 4),
	},
	{
		name: "scaled_divisor_fits",
		d:    newDecimal(u128{hi: ^uint64(0), lo: ^uint64(0)}, false, MaxPrec),
		e:    newDecimal(u128{lo: 1}, false, 0),
	},
	{
		name: "scaled_divisor_wider_than_u128",
		d:    newDecimal(u128{lo: 1}, false, MaxPrec),
		e:    newDecimal(u128{hi: 1 << 63}, false, 0),
	},
}

func BenchmarkQuoRemAlignmentPaths(b *testing.B) {
	for _, bc := range quoremAlignmentBenchCases {
		b.Run(bc.name, func(b *testing.B) {
			if _, _, err := bc.d.QuoRem(bc.e); err != nil {
				b.Fatalf("preflight: %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				quoremBenchQ, quoremBenchR, errQuoremBench = bc.d.QuoRem(bc.e)
			}
		})
	}
}

func BenchmarkModAlignmentPaths(b *testing.B) {
	for _, bc := range quoremAlignmentBenchCases {
		b.Run(bc.name, func(b *testing.B) {
			if _, err := bc.d.Mod(bc.e); err != nil {
				b.Fatalf("preflight: %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				quoremBenchR, errQuoremBench = bc.d.Mod(bc.e)
			}
		})
	}
}
