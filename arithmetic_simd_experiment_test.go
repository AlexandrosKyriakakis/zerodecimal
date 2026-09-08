//go:build go1.27 && !go1.28 && goexperiment.simd && arm64

package zerodecimal

import (
	"math/bits"
	"math/rand"
	"simd/archsimd"
	"testing"
	"unsafe"
)

// This file keeps the rejected arithmetic candidates reproducible without
// putting them on a production path. Decimal arithmetic is carry-dependent;
// the benchmarks below measure the cost of reconstructing those carries after
// lane-wise SIMD operations.

var (
	arithSIMDU128Sink u128
	arithSIMDWordSink uint64
	arithSIMDU128A    = u128{hi: 0xfedcba9876543210, lo: 0xf123456789abcdef}
	arithSIMDU128B    = u128{hi: 0x123456789abcdef0, lo: 0x23456789abcdef01}
)

func arithSIMDLoadU128(u *u128) archsimd.Uint64x2 {
	// u128 is laid out as [hi, lo], matching the two SIMD lanes.
	return archsimd.LoadUint64x2Array((*[2]uint64)(unsafe.Pointer(u)))
}

func arithSIMDAdd128(u, v u128) (u128, uint64) {
	x := arithSIMDLoadU128(&u)
	y := arithSIMDLoadU128(&v)
	s := x.Add(y)
	one := archsimd.BroadcastUint64x2(1)

	// The low limb occupies lane 1. Move its carry mask into lane 0, which
	// holds the high limb, then add that carry across the limb boundary.
	laneCarry := s.Less(x).ToInt64x2().ToBits().And(one)
	s2 := s.Add(laneCarry.HiToLo())
	overflow := laneCarry.Or(s2.Less(s).ToInt64x2().ToBits().And(one)).GetElem(0)
	return u128{hi: s2.GetElem(0), lo: s2.GetElem(1)}, overflow
}

func arithSIMDSub128(u, v u128) (u128, uint64) {
	x := arithSIMDLoadU128(&u)
	y := arithSIMDLoadU128(&v)
	d := x.Sub(y)
	one := archsimd.BroadcastUint64x2(1)
	laneBorrow := x.Less(y).ToInt64x2().ToBits().And(one)
	borrowHi := laneBorrow.HiToLo()
	d2 := d.Sub(borrowHi)
	borrow := laneBorrow.Or(d.Less(borrowHi).ToInt64x2().ToBits().And(one)).GetElem(0)
	return u128{hi: d2.GetElem(0), lo: d2.GetElem(1)}, borrow
}

func arithSIMDMul64(a, b uint64) (uint64, uint64) {
	a0, a1 := uint32(a), uint32(a>>32)
	b0, b1 := uint32(b), uint32(b>>32)
	av := [4]uint32{a0, a0, a1, a1}
	bv := [4]uint32{b0, b1, b0, b1}
	x := archsimd.LoadUint32x4Array(&av)
	y := archsimd.LoadUint32x4Array(&bv)
	loProducts := x.MulWidenLo(y)
	hiProducts := x.HiToLo().MulWidenLo(y.HiToLo())
	p00, p01 := loProducts.GetElem(0), loProducts.GetElem(1)
	p10, p11 := hiProducts.GetElem(0), hiProducts.GetElem(1)
	lo, c1 := bits.Add64(p00, p01<<32, 0)
	lo, c2 := bits.Add64(lo, p10<<32, 0)
	hi := p11 + (p01 >> 32) + (p10 >> 32) + c1 + c2
	return hi, lo
}

func arithSIMDSum128Scalar(xs []u128) (u128, uint64) {
	var sum u128
	var overflow uint64
	for _, x := range xs {
		var carry uint64
		sum, carry = add128(sum, x)
		overflow |= carry
	}
	return sum, overflow
}

func arithSIMDSum128(xs []u128) (u128, uint64) {
	var sum u128
	var overflow uint64
	for i := range xs {
		var carry uint64
		sum, carry = arithSIMDAdd128(sum, xs[i])
		overflow |= carry
	}
	return sum, overflow
}

func TestArithmeticSIMDExperimentCorrectness(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for range 100_000 {
		u := u128{hi: rng.Uint64(), lo: rng.Uint64()}
		v := u128{hi: rng.Uint64(), lo: rng.Uint64()}

		want, wantCarry := add128(u, v)
		got, gotCarry := arithSIMDAdd128(u, v)
		if got != want || gotCarry != wantCarry {
			t.Fatalf("add: got (%#v,%d), want (%#v,%d)", got, gotCarry, want, wantCarry)
		}

		want, wantBorrow := sub128(u, v)
		got, gotBorrow := arithSIMDSub128(u, v)
		if got != want || gotBorrow != wantBorrow {
			t.Fatalf("sub: got (%#v,%d), want (%#v,%d)", got, gotBorrow, want, wantBorrow)
		}

		wantHi, wantLo := bits.Mul64(u.lo, v.lo)
		gotHi, gotLo := arithSIMDMul64(u.lo, v.lo)
		if gotHi != wantHi || gotLo != wantLo {
			t.Fatalf("mul: got (%#x,%#x), want (%#x,%#x)", gotHi, gotLo, wantHi, wantLo)
		}
	}
}

func BenchmarkArithmeticSIMDExperiment(b *testing.B) {
	b.Run("Add128Chain/scalar", func(b *testing.B) {
		x := arithSIMDU128A
		var carry uint64
		for b.Loop() {
			x, carry = add128(x, arithSIMDU128B)
		}
		arithSIMDU128Sink, arithSIMDWordSink = x, carry
	})
	b.Run("Add128Chain/simd", func(b *testing.B) {
		x := arithSIMDU128A
		var carry uint64
		for b.Loop() {
			x, carry = arithSIMDAdd128(x, arithSIMDU128B)
		}
		arithSIMDU128Sink, arithSIMDWordSink = x, carry
	})
	b.Run("Sub128Chain/scalar", func(b *testing.B) {
		x := arithSIMDU128A
		var borrow uint64
		for b.Loop() {
			x, borrow = sub128(x, arithSIMDU128B)
		}
		arithSIMDU128Sink, arithSIMDWordSink = x, borrow
	})
	b.Run("Sub128Chain/simd", func(b *testing.B) {
		x := arithSIMDU128A
		var borrow uint64
		for b.Loop() {
			x, borrow = arithSIMDSub128(x, arithSIMDU128B)
		}
		arithSIMDU128Sink, arithSIMDWordSink = x, borrow
	})
	b.Run("Mul64Chain/scalar", func(b *testing.B) {
		x := arithSIMDU128A.lo
		var hi uint64
		for b.Loop() {
			hi, x = bits.Mul64(x, arithSIMDU128B.lo)
		}
		arithSIMDWordSink, arithSIMDU128Sink.lo = hi, x
	})
	b.Run("Mul64Chain/simd32", func(b *testing.B) {
		x := arithSIMDU128A.lo
		var hi uint64
		for b.Loop() {
			hi, x = arithSIMDMul64(x, arithSIMDU128B.lo)
		}
		arithSIMDWordSink, arithSIMDU128Sink.lo = hi, x
	})

	xs := make([]u128, 4096)
	for i := range xs {
		xs[i] = u128{hi: uint64(i & 3), lo: uint64(i)*0x9e3779b97f4a7c15 + 1}
	}
	b.Run("Sum128x4096/scalar", func(b *testing.B) {
		for b.Loop() {
			arithSIMDU128Sink, arithSIMDWordSink = arithSIMDSum128Scalar(xs)
		}
	})
	b.Run("Sum128x4096/simd", func(b *testing.B) {
		for b.Loop() {
			arithSIMDU128Sink, arithSIMDWordSink = arithSIMDSum128(xs)
		}
	})
}
