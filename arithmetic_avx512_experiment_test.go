//go:build go1.27 && !go1.28 && goexperiment.simd && amd64

package zerodecimal

import (
	"math/bits"
	"math/rand"
	"simd/archsimd"
	"testing"
	"unsafe"
)

// This file keeps the amd64 AVX-512 arithmetic candidates reproducible
// without putting them on a production path. Unlike the latency-bound NEON
// experiment, these kernels process eight independent 128-bit carry chains at
// once. The AoS benchmarks include the permutations needed by u128's [hi, lo]
// memory layout; the SoA benchmark records the best case without that cost.

var (
	arithAVX512EvenIndices     = [8]uint64{0, 2, 4, 6, 8, 10, 12, 14}
	arithAVX512OddIndices      = [8]uint64{1, 3, 5, 7, 9, 11, 13, 15}
	arithAVX512InterleaveLo    = [8]uint64{0, 8, 1, 9, 2, 10, 3, 11}
	arithAVX512InterleaveHi    = [8]uint64{4, 12, 5, 13, 6, 14, 7, 15}
	arithAVX512U128BlockSink   [8]u128
	arithAVX512Uint64BlockSink [8]uint64
	arithAVX512U128Sink        u128
	arithAVX512WordSink        uint64
)

func arithAVX512LoadU128x8(xs *[8]u128) (archsimd.Uint64x8, archsimd.Uint64x8) {
	// Eight u128s are sixteen alternating [hi, lo] limbs. Two contiguous
	// 512-bit loads followed by VPERMI2Q split them into high- and low-limb
	// vectors without scalar packing.
	a := archsimd.LoadUint64x8Array((*[8]uint64)(unsafe.Pointer(&xs[0])))
	b := archsimd.LoadUint64x8Array((*[8]uint64)(unsafe.Pointer(&xs[4])))
	even := archsimd.LoadUint64x8Array(&arithAVX512EvenIndices)
	odd := archsimd.LoadUint64x8Array(&arithAVX512OddIndices)
	return a.ConcatPermute(b, even), a.ConcatPermute(b, odd)
}

func arithAVX512StoreU128x8(hi, lo archsimd.Uint64x8, out *[8]u128) {
	// Invert arithAVX512LoadU128x8's deinterleave so the result has u128's
	// ordinary AoS layout.
	indicesLo := archsimd.LoadUint64x8Array(&arithAVX512InterleaveLo)
	indicesHi := archsimd.LoadUint64x8Array(&arithAVX512InterleaveHi)
	a := hi.ConcatPermute(lo, indicesLo)
	b := hi.ConcatPermute(lo, indicesHi)
	a.StoreArray((*[8]uint64)(unsafe.Pointer(&out[0])))
	b.StoreArray((*[8]uint64)(unsafe.Pointer(&out[4])))
}

func arithAVX512AddVectors(
	ahi, alo, bhi, blo archsimd.Uint64x8,
) (archsimd.Uint64x8, archsimd.Uint64x8, archsimd.Mask64x8) {
	lo := alo.Add(blo)
	carry := lo.Less(alo)
	hi0 := ahi.Add(bhi)
	hi := hi0.Add(archsimd.BroadcastUint64x8(1).Masked(carry))
	overflow := hi0.Less(ahi).Or(hi.Less(hi0))
	return hi, lo, overflow
}

func arithAVX512SubVectors(
	ahi, alo, bhi, blo archsimd.Uint64x8,
) (archsimd.Uint64x8, archsimd.Uint64x8, archsimd.Mask64x8) {
	lo := alo.Sub(blo)
	borrow := alo.Less(blo)
	hi0 := ahi.Sub(bhi)
	borrowWord := archsimd.BroadcastUint64x8(1).Masked(borrow)
	hi := hi0.Sub(borrowWord)
	underflow := ahi.Less(bhi).Or(hi0.Less(borrowWord))
	return hi, lo, underflow
}

func arithAVX512Add8(a, b, out *[8]u128) uint8 {
	ahi, alo := arithAVX512LoadU128x8(a)
	bhi, blo := arithAVX512LoadU128x8(b)
	hi, lo, overflow := arithAVX512AddVectors(ahi, alo, bhi, blo)
	arithAVX512StoreU128x8(hi, lo, out)
	return overflow.ToBits()
}

func arithAVX512Sub8(a, b, out *[8]u128) uint8 {
	ahi, alo := arithAVX512LoadU128x8(a)
	bhi, blo := arithAVX512LoadU128x8(b)
	hi, lo, underflow := arithAVX512SubVectors(ahi, alo, bhi, blo)
	arithAVX512StoreU128x8(hi, lo, out)
	return underflow.ToBits()
}

func arithScalarAdd8(a, b, out *[8]u128) uint8 {
	var overflow uint8
	for i := range a {
		var carry uint64
		out[i], carry = add128(a[i], b[i])
		overflow |= uint8(carry) << i
	}
	return overflow
}

func arithScalarSub8(a, b, out *[8]u128) uint8 {
	var underflow uint8
	for i := range a {
		var borrow uint64
		out[i], borrow = sub128(a[i], b[i])
		underflow |= uint8(borrow) << i
	}
	return underflow
}

func arithAVX512Add8SoA(
	ahi, alo, bhi, blo, outHi, outLo *[8]uint64,
) uint8 {
	hi, lo, overflow := arithAVX512AddVectors(
		archsimd.LoadUint64x8Array(ahi),
		archsimd.LoadUint64x8Array(alo),
		archsimd.LoadUint64x8Array(bhi),
		archsimd.LoadUint64x8Array(blo),
	)
	hi.StoreArray(outHi)
	lo.StoreArray(outLo)
	return overflow.ToBits()
}

func arithScalarAdd8SoA(
	ahi, alo, bhi, blo, outHi, outLo *[8]uint64,
) uint8 {
	var overflow uint8
	for i := range ahi {
		lo, carry := bits.Add64(alo[i], blo[i], 0)
		hi, carry := bits.Add64(ahi[i], bhi[i], carry)
		outHi[i], outLo[i] = hi, lo
		overflow |= uint8(carry) << i
	}
	return overflow
}

func arithAVX512Mul64x8(a, b, outHi, outLo *[8]uint64) {
	x := archsimd.LoadUint64x8Array(a)
	y := archsimd.LoadUint64x8Array(b)
	mask32 := archsimd.BroadcastUint64x8(1<<32 - 1)
	shift32 := archsimd.BroadcastUint64x8(32)
	x0, x1 := x.And(mask32), x.ShiftRight(shift32)
	y0, y1 := y.And(mask32), y.ShiftRight(shift32)
	p00 := x0.Mul(y0)
	p01 := x0.Mul(y1)
	p10 := x1.Mul(y0)
	p11 := x1.Mul(y1)
	middle := p00.ShiftRight(shift32).Add(p01.And(mask32)).Add(p10.And(mask32))
	hi := p11.Add(p01.ShiftRight(shift32)).Add(p10.ShiftRight(shift32)).Add(middle.ShiftRight(shift32))
	lo := x.Mul(y)
	hi.StoreArray(outHi)
	lo.StoreArray(outLo)
}

func arithScalarMul64x8(a, b, outHi, outLo *[8]uint64) {
	for i := range a {
		outHi[i], outLo[i] = bits.Mul64(a[i], b[i])
	}
}

func arithAVX512Sum128(xs []u128) (u128, uint64) {
	var sumHi, sumLo archsimd.Uint64x8
	var overflow archsimd.Mask64x8
	one := archsimd.BroadcastUint64x8(1)
	i := 0
	for ; len(xs)-i >= 8; i += 8 {
		block := (*[8]u128)(unsafe.Pointer(&xs[i]))
		xhi, xlo := arithAVX512LoadU128x8(block)
		lo := sumLo.Add(xlo)
		carry := lo.Less(sumLo)
		hi0 := sumHi.Add(xhi)
		hi := hi0.Add(one.Masked(carry))
		overflow = overflow.Or(hi0.Less(sumHi)).Or(hi.Less(hi0))
		sumHi, sumLo = hi, lo
	}

	var his, los [8]uint64
	sumHi.StoreArray(&his)
	sumLo.StoreArray(&los)
	var sum u128
	anyOverflow := uint64(0)
	if overflow.ToBits() != 0 {
		anyOverflow = 1
	}
	for lane := range his {
		var carry uint64
		sum, carry = add128(sum, u128{hi: his[lane], lo: los[lane]})
		anyOverflow |= carry
	}
	for ; i < len(xs); i++ {
		var carry uint64
		sum, carry = add128(sum, xs[i])
		anyOverflow |= carry
	}
	return sum, anyOverflow
}

func arithScalarSum128(xs []u128) (u128, uint64) {
	var sum u128
	var overflow uint64
	for _, x := range xs {
		var carry uint64
		sum, carry = add128(sum, x)
		overflow |= carry
	}
	return sum, overflow
}

func TestArithmeticAVX512ExperimentCorrectness(t *testing.T) {
	if !archsimd.X86.AVX512() {
		t.Skip("CPU does not expose AVX-512F+CD+BW+DQ+VL")
	}

	rng := rand.New(rand.NewSource(1))
	for range 12_500 {
		var a, b, got, want [8]u128
		var ma, mb, gotHi, gotLo, wantHi, wantLo [8]uint64
		for i := range a {
			a[i] = u128{hi: rng.Uint64(), lo: rng.Uint64()}
			b[i] = u128{hi: rng.Uint64(), lo: rng.Uint64()}
			ma[i], mb[i] = rng.Uint64(), rng.Uint64()
		}

		wantFlags := arithScalarAdd8(&a, &b, &want)
		gotFlags := arithAVX512Add8(&a, &b, &got)
		if got != want || gotFlags != wantFlags {
			t.Fatalf("add8: got (%#v,%08b), want (%#v,%08b)", got, gotFlags, want, wantFlags)
		}

		wantFlags = arithScalarSub8(&a, &b, &want)
		gotFlags = arithAVX512Sub8(&a, &b, &got)
		if got != want || gotFlags != wantFlags {
			t.Fatalf("sub8: got (%#v,%08b), want (%#v,%08b)", got, gotFlags, want, wantFlags)
		}

		arithScalarMul64x8(&ma, &mb, &wantHi, &wantLo)
		arithAVX512Mul64x8(&ma, &mb, &gotHi, &gotLo)
		if gotHi != wantHi || gotLo != wantLo {
			t.Fatalf("mul64x8: got (%#v,%#v), want (%#v,%#v)", gotHi, gotLo, wantHi, wantLo)
		}
	}

	xs := make([]u128, 4099)
	for i := range xs {
		xs[i] = u128{hi: uint64(i & 3), lo: uint64(i)*0x9e3779b97f4a7c15 + 1}
	}
	want, wantOverflow := arithScalarSum128(xs)
	got, gotOverflow := arithAVX512Sum128(xs)
	if got != want || gotOverflow != wantOverflow {
		t.Fatalf("sum128: got (%#v,%d), want (%#v,%d)", got, gotOverflow, want, wantOverflow)
	}
}

func BenchmarkArithmeticAVX512Experiment(b *testing.B) {
	if !archsimd.X86.AVX512() {
		b.Skip("CPU does not expose AVX-512F+CD+BW+DQ+VL")
	}
	b.ReportAllocs()

	var a, c [8]u128
	var ahi, alo, chi, clo [8]uint64
	var ma, mc [8]uint64
	for i := range a {
		a[i] = u128{hi: 0xfedcba9876543210 - uint64(i), lo: 0xf123456789abcdef + uint64(i)}
		c[i] = u128{hi: 0x123456789abcdef0 + uint64(i), lo: 0x23456789abcdef01 - uint64(i)}
		ahi[i], alo[i], chi[i], clo[i] = a[i].hi, a[i].lo, c[i].hi, c[i].lo
		ma[i], mc[i] = a[i].lo, c[i].lo
	}

	b.Run("Add8SoA/scalar", func(b *testing.B) {
		var outHi, outLo [8]uint64
		var flags uint8
		for b.Loop() {
			flags = arithScalarAdd8SoA(&ahi, &alo, &chi, &clo, &outHi, &outLo)
		}
		arithAVX512Uint64BlockSink, arithAVX512WordSink = outHi, uint64(flags)
		arithAVX512U128Sink.lo = outLo[0]
	})
	b.Run("Add8SoA/avx512", func(b *testing.B) {
		var outHi, outLo [8]uint64
		var flags uint8
		for b.Loop() {
			flags = arithAVX512Add8SoA(&ahi, &alo, &chi, &clo, &outHi, &outLo)
		}
		arithAVX512Uint64BlockSink, arithAVX512WordSink = outHi, uint64(flags)
		arithAVX512U128Sink.lo = outLo[0]
	})
	b.Run("Add8AoS/scalar", func(b *testing.B) {
		var out [8]u128
		var flags uint8
		for b.Loop() {
			flags = arithScalarAdd8(&a, &c, &out)
		}
		arithAVX512U128BlockSink, arithAVX512WordSink = out, uint64(flags)
	})
	b.Run("Add8AoS/avx512", func(b *testing.B) {
		var out [8]u128
		var flags uint8
		for b.Loop() {
			flags = arithAVX512Add8(&a, &c, &out)
		}
		arithAVX512U128BlockSink, arithAVX512WordSink = out, uint64(flags)
	})
	b.Run("Sub8AoS/scalar", func(b *testing.B) {
		var out [8]u128
		var flags uint8
		for b.Loop() {
			flags = arithScalarSub8(&a, &c, &out)
		}
		arithAVX512U128BlockSink, arithAVX512WordSink = out, uint64(flags)
	})
	b.Run("Sub8AoS/avx512", func(b *testing.B) {
		var out [8]u128
		var flags uint8
		for b.Loop() {
			flags = arithAVX512Sub8(&a, &c, &out)
		}
		arithAVX512U128BlockSink, arithAVX512WordSink = out, uint64(flags)
	})
	b.Run("Mul64x8/scalar", func(b *testing.B) {
		var hi, lo [8]uint64
		for b.Loop() {
			arithScalarMul64x8(&ma, &mc, &hi, &lo)
		}
		arithAVX512Uint64BlockSink, arithAVX512U128Sink.lo = hi, lo[0]
	})
	b.Run("Mul64x8/avx512", func(b *testing.B) {
		var hi, lo [8]uint64
		for b.Loop() {
			arithAVX512Mul64x8(&ma, &mc, &hi, &lo)
		}
		arithAVX512Uint64BlockSink, arithAVX512U128Sink.lo = hi, lo[0]
	})

	xs := make([]u128, 4096)
	for i := range xs {
		xs[i] = u128{hi: uint64(i & 3), lo: uint64(i)*0x9e3779b97f4a7c15 + 1}
	}
	b.Run("Sum128x4096/scalar", func(b *testing.B) {
		for b.Loop() {
			arithAVX512U128Sink, arithAVX512WordSink = arithScalarSum128(xs)
		}
	})
	b.Run("Sum128x4096/avx512", func(b *testing.B) {
		for b.Loop() {
			arithAVX512U128Sink, arithAVX512WordSink = arithAVX512Sum128(xs)
		}
	})
}
