package zerodecimal

import (
	"math/rand/v2"
	"testing"
)

// Package-level sinks and the mixed-value pool for the IsRounded and
// FracDigits benchmarks (see alloc_test.go for why neither may be local).
var (
	isRoundedBenchBool   bool
	isRoundedBenchDigits uint8

	// isRoundedMix holds 1024 values with every combination of limb count,
	// stored precision and trailing-zero count the sweeps use, in an order the
	// branch predictor cannot learn: the fixed-shape rows below measure the
	// kernels, this pool measures them under realistic misprediction.
	isRoundedMix = func() (pool [1024]Decimal) {
		rng := rand.New(rand.NewPCG(0xbe9c4, 0x15520d))
		for i := range pool {
			// A mantissa of 1..19 digits, then 0..19 appended zeros: one limb
			// until the product passes 2^64. It is at most 10^38 < 2^128, so
			// the multiply cannot overflow.
			m := rng.Uint64N(pow10u64[1+rng.IntN(19)]) + 1
			coef, _ := mul128by64(u128{lo: m}, pow10u64[rng.IntN(20)])
			pool[i] = newDecimal(coef, rng.Uint64()&1 == 1, uint8(rng.IntN(20)))
		}
		return pool
	}()
)

// BenchmarkIsRounded times IsRounded(2) against the money idiom it replaces,
// x.Equal(x.RoundBank(2)), for every shape. The idiom builds a rounded value
// and then compares unaligned precisions, widening to 192 bits whenever the
// rounding changed the precision.
func BenchmarkIsRounded(b *testing.B) {
	for _, s := range isRoundedShapes {
		d := s.d
		b.Run("IsRounded(2)/"+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				isRoundedBenchBool = d.IsRounded(2)
			}
		})
		b.Run("Equal+RoundBank(2)/"+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				isRoundedBenchBool = d.Equal(d.RoundBank(2))
			}
		})
	}
	b.Run("IsRounded(2)/mixed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			isRoundedBenchBool = isRoundedMix[i&1023].IsRounded(2)
		}
	})
	b.Run("Equal+RoundBank(2)/mixed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			d := isRoundedMix[i&1023]
			isRoundedBenchBool = d.Equal(d.RoundBank(2))
		}
	})
}

// BenchmarkFracDigits times FracDigits against Trim().Prec(), the only way to
// learn a value's significant fractional digits without it. Trim sheds one
// digit per division, so its cost grows with the zero count; FracDigits tests
// the lowest digit once and then runs at most five multiply-rotate rungs,
// whatever the zero count (a two-limb value first takes one div2by1 reduction).
func BenchmarkFracDigits(b *testing.B) {
	for _, s := range isRoundedShapes {
		d := s.d
		b.Run("FracDigits/"+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				isRoundedBenchDigits = d.FracDigits()
			}
		})
		b.Run("Trim+Prec/"+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				isRoundedBenchDigits = d.Trim().Prec()
			}
		})
	}
	b.Run("FracDigits/mixed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			isRoundedBenchDigits = isRoundedMix[i&1023].FracDigits()
		}
	})
	b.Run("Trim+Prec/mixed", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			isRoundedBenchDigits = isRoundedMix[i&1023].Trim().Prec()
		}
	})
}
