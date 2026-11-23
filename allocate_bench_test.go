package zerodecimal

import (
	"strconv"
	"testing"
)

// Package-level inputs and sinks for the Allocate and Split benchmarks (see
// alloc_test.go for why neither may be local).
var (
	allocateBenchFee    = MustNew(12_665, -2) // $126.65 of fees
	allocateBenchQty3   = allocateBenchQty(3)
	allocateBenchQty16  = allocateBenchQty(16)
	allocateBenchQty64  = allocateBenchQty(64)
	allocateBenchQty1k  = allocateBenchQty(1000)
	allocateBenchEqual3 = []Decimal{NewFromInt(1), NewFromInt(1), NewFromInt(1)}
	// W in [2^64, 2^128): nineteen-place weights.
	allocateBenchMid = []Decimal{
		RequireFromString("1.2345678901234567891"),
		RequireFromString("9.8765432109876543219"),
		RequireFromString("0.5"),
	}
	// W ≥ 2^128: a huge whole weight beside a nineteen-place one.
	allocateBenchWide = []Decimal{RequireFromString("1e30"), RequireFromString("0.0000000000000000003"), NewFromInt(7)}

	allocateBenchDst  = make([]Decimal, 0, 1000)
	allocateBenchSink []Decimal
	allocateBenchOne  Decimal
	errAllocateBench  error
)

// allocateBenchQty returns n fill quantities at six places, the weights of a
// fee split over the fills of an order.
func allocateBenchQty(n int) []Decimal {
	qs := make([]Decimal, n)
	for i := range qs {
		qs[i] = MustNew(int64(100_000+(i*7_919_113)%5_000_000), -6)
	}
	return qs
}

// BenchmarkAllocate times Allocate on the fee-split shape at cents and on
// each division path, beside the per-share chains it replaces: MulDivRound on
// each share, which rounds once but does not make the shares sum to the
// total, and the legacy fee.Mul(w.Div(W)), which truncates twice and loses
// the residual.
func BenchmarkAllocate(b *testing.B) {
	cases := []struct {
		name    string
		weights []Decimal
		places  uint8
	}{
		{"fee/3", allocateBenchQty3, 2},
		{"fee/16", allocateBenchQty16, 2},
		{"fee/64", allocateBenchQty64, 2},
		{"fee/1000", allocateBenchQty1k, 2},
		{"fee_19dp/3", allocateBenchQty3, 19},
		{"equal/3", allocateBenchEqual3, 2},
		{"mid_sum/3", allocateBenchMid, 2},
		{"wide_sum/3", allocateBenchWide, 2},
	}
	for _, c := range cases {
		weights, places := c.weights, c.places
		b.Run("Allocate/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				allocateBenchSink, errAllocateBench = Allocate(allocateBenchDst, allocateBenchFee, weights, places)
			}
		})
	}
	for _, c := range cases[:4] {
		weights := c.weights
		b.Run("MulDivRound_each/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sum := MustSum(weights[0], weights[1:]...)
				dst := allocateBenchDst[:len(weights)]
				for i, w := range weights {
					dst[i], errAllocateBench = allocateBenchFee.MulDivRound(w, sum, 2, ToNearestEven)
				}
				allocateBenchSink = dst
			}
		})
		b.Run("Mul+Div_each/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				sum := MustSum(weights[0], weights[1:]...)
				dst := allocateBenchDst[:len(weights)]
				for i, w := range weights {
					dst[i] = allocateBenchFee.MustMul(w.MustDiv(sum))
				}
				allocateBenchSink = dst
			}
		})
	}
}

// BenchmarkSplit times Split beside the legacy even split it replaces, one
// truncating Div whose share is applied n times and loses the residual.
func BenchmarkSplit(b *testing.B) {
	for _, n := range []int{3, 64, 1000} {
		b.Run("Split/"+strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				allocateBenchSink, errAllocateBench = Split(allocateBenchDst, allocateBenchFee, n, 2)
			}
		})
	}
	b.Run("Div_once/3", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			allocateBenchOne = allocateBenchFee.MustDiv(NewFromInt(3))
		}
	})
}
