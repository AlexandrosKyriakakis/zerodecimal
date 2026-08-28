//go:build go1.27 && !go1.28 && goexperiment.simd && amd64

package zerodecimal

import (
	"math/rand"
	"simd/archsimd"
	"testing"
	"unsafe"
)

// This file measures an AVX2 Sum candidate separately from the AVX-512
// experiments. AVX2 has no arbitrary 64-bit two-vector permute, so loading
// four 24-byte Decimals requires fixed pair and 128-bit-half shuffles.

var arithAVX2DecimalSink Decimal

func arithAVX2LoadDecimal4(base unsafe.Pointer) (archsimd.Uint64x4, archsimd.Uint64x4, archsimd.Uint64x4) {
	// Four Decimals occupy twelve words:
	//
	//   [h0 l0 m0 h1] [l1 m1 h2 l2] [m2 h3 l3 m3]
	//
	// Keep all loads contiguous, then deinterleave coefficient limbs and the
	// packed sign/precision metadata with AVX/AVX2 fixed shuffles only.
	v0 := archsimd.LoadUint64x4Array((*[4]uint64)(base))
	v1 := archsimd.LoadUint64x4Array((*[4]uint64)(unsafe.Add(base, 32)))
	v2 := archsimd.LoadUint64x4Array((*[4]uint64)(unsafe.Add(base, 64)))

	v0Swap := v0.ConcatPermute128Scalars(1, 0, v0)
	v1Swap := v1.ConcatPermute128Scalars(1, 0, v1)
	v2Swap := v2.ConcatPermute128Scalars(1, 0, v2)

	hi01 := v0.ConcatPermuteScalarsGrouped(0, 3, v0Swap)
	hi23 := v1Swap.ConcatPermuteScalarsGrouped(0, 3, v2)
	hi := hi01.ConcatPermute128Scalars(0, 2, hi23)

	lo01 := v0.ConcatPermuteScalarsGrouped(1, 2, v1)
	lo23 := v1.ConcatPermuteScalarsGrouped(1, 2, v2)
	lo := lo01.ConcatPermute128Scalars(0, 3, lo23)

	meta01 := v0Swap.ConcatPermuteScalarsGrouped(0, 3, v1)
	meta23 := v2.ConcatPermuteScalarsGrouped(0, 3, v2Swap)
	meta := meta01.ConcatPermute128Scalars(0, 2, meta23)

	return hi, lo, meta
}

func arithAVX2AddVectorsOne(
	ahi, alo, bhi, blo, one, signBit archsimd.Uint64x4,
) (archsimd.Uint64x4, archsimd.Uint64x4, archsimd.Mask64x4) {
	lo := alo.Add(blo)
	carry := arithAVX2LessUnsigned(lo, alo, signBit)
	hi0 := ahi.Add(bhi)
	hi := hi0.Add(one.Masked(carry))
	overflow := arithAVX2LessUnsigned(hi0, ahi, signBit).Or(arithAVX2LessUnsigned(hi, hi0, signBit))
	return hi, lo, overflow
}

func arithAVX2LessUnsigned(x, y, signBit archsimd.Uint64x4) archsimd.Mask64x4 {
	// AVX2 lacks an unsigned 64-bit comparison. Biasing both operands by the
	// sign bit converts their ordering to signed ordering. Pass the broadcast
	// bias in so the compiler hoists it instead of reconstructing it for every
	// comparison as Uint64x4.Less's generic emulation currently does.
	return y.Xor(signBit).AsInt64x4().Greater(x.Xor(signBit).AsInt64x4())
}

// arithAVX2SumDecimalsPositive specializes common-precision, nonnegative
// aggregation. Unsupported input returns false so a future production wrapper
// could preserve Sum's full mixed-sign, mixed-precision, wide-intermediate
// contract by falling back to the scalar implementation.
func arithAVX2SumDecimalsPositive(ds []Decimal) (Decimal, bool) {
	first := 0
	for first < len(ds) && ds[first].coef.isZero() {
		first++
	}
	if first == len(ds) {
		return Decimal{}, true
	}
	prec := ds[first].prec
	if ds[first].neg {
		return Decimal{}, false
	}

	zero := archsimd.Uint64x4{}
	one := archsimd.BroadcastUint64x4(1)
	signBit := archsimd.BroadcastUint64x4(1 << 63)
	metadataMask := archsimd.BroadcastUint64x4(0xffff)
	wantMetadata := archsimd.BroadcastUint64x4(uint64(prec) << 8)

	var sumHi, sumLo archsimd.Uint64x4
	var overflow archsimd.Mask64x4
	i := first
	for ; len(ds)-i >= 4; i += 4 {
		xhi, xlo, meta := arithAVX2LoadDecimal4(unsafe.Pointer(&ds[i]))
		meta = meta.And(metadataMask)
		valid := meta.Equal(wantMetadata).Or(meta.Equal(zero))
		if valid.ToBits() != 0x0f {
			return Decimal{}, false
		}

		var ov archsimd.Mask64x4
		sumHi, sumLo, ov = arithAVX2AddVectorsOne(sumHi, sumLo, xhi, xlo, one, signBit)
		overflow = overflow.Or(ov)
	}
	if overflow.ToBits() != 0 {
		return Decimal{}, false
	}

	var his, los [4]uint64
	sumHi.StoreArray(&his)
	sumLo.StoreArray(&los)
	var sum u128
	for lane := range his {
		var carry uint64
		sum, carry = add128(sum, u128{hi: his[lane], lo: los[lane]})
		if carry != 0 {
			return Decimal{}, false
		}
	}
	for ; i < len(ds); i++ {
		d := ds[i]
		if d.coef.isZero() {
			continue
		}
		if d.neg || d.prec != prec {
			return Decimal{}, false
		}
		var carry uint64
		sum, carry = add128(sum, d.coef)
		if carry != 0 {
			return Decimal{}, false
		}
	}
	return newDecimal(sum, false, prec), true
}

// arithAVX2SumDecimalsPositive64 is the likely real-world fast path: every
// vectorized coefficient fits in 64 bits. It still sums into 128 bits, but the
// high limbs then contain only low-limb carry counts. Those counts cannot
// overflow uint64 because a Go slice itself has at most MaxInt elements. This
// removes the expensive emulated unsigned high-limb overflow comparisons.
func arithAVX2SumDecimalsPositive64(ds []Decimal) (Decimal, bool) {
	first := 0
	for first < len(ds) && ds[first].coef.isZero() {
		first++
	}
	if first == len(ds) {
		return Decimal{}, true
	}
	prec := ds[first].prec
	if ds[first].neg {
		return Decimal{}, false
	}

	zero := archsimd.Uint64x4{}
	one := archsimd.BroadcastUint64x4(1)
	signBit := archsimd.BroadcastUint64x4(1 << 63)
	metadataMask := archsimd.BroadcastUint64x4(0xffff)
	wantMetadata := archsimd.BroadcastUint64x4(uint64(prec) << 8)

	var sumHi, sumLo archsimd.Uint64x4
	i := first
	for ; len(ds)-i >= 4; i += 4 {
		xhi, xlo, meta := arithAVX2LoadDecimal4(unsafe.Pointer(&ds[i]))
		meta = meta.And(metadataMask)
		validMetadata := meta.Equal(wantMetadata).Or(meta.Equal(zero))
		valid := validMetadata.And(xhi.Equal(zero))
		if valid.ToBits() != 0x0f {
			return Decimal{}, false
		}

		lo := sumLo.Add(xlo)
		carry := arithAVX2LessUnsigned(lo, sumLo, signBit)
		sumLo = lo
		sumHi = sumHi.Add(one.Masked(carry))
	}

	var his, los [4]uint64
	sumHi.StoreArray(&his)
	sumLo.StoreArray(&los)
	var sum u128
	for lane := range his {
		var carry uint64
		sum, carry = add128(sum, u128{hi: his[lane], lo: los[lane]})
		if carry != 0 {
			return Decimal{}, false
		}
	}
	for ; i < len(ds); i++ {
		d := ds[i]
		if d.coef.isZero() {
			continue
		}
		if d.neg || d.prec != prec || d.coef.hi != 0 {
			return Decimal{}, false
		}
		var carry uint64
		sum, carry = add128(sum, d.coef)
		if carry != 0 {
			return Decimal{}, false
		}
	}
	return newDecimal(sum, false, prec), true
}

func TestArithmeticAVX2SumExperimentCorrectness(t *testing.T) {
	if !archsimd.X86.AVX2() {
		t.Skip("CPU does not expose AVX2")
	}

	rng := rand.New(rand.NewSource(2))
	sizes := []int{1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 63, 64, 65, 255, 256, 257, 4096}
	for _, size := range sizes {
		ds := make([]Decimal, size)
		ds64 := make([]Decimal, size)
		for i := range ds {
			if i%19 == 0 {
				continue
			}
			ds[i] = newDecimal(u128{hi: rng.Uint64() >> 20, lo: rng.Uint64()}, false, 4)
			ds64[i] = newDecimal(u128{lo: rng.Uint64()}, false, 4)
		}

		want, err := Sum(ds[0], ds[1:]...)
		if err != nil {
			t.Fatalf("size %d: unexpected scalar error: %v", size, err)
		}
		got, ok := arithAVX2SumDecimalsPositive(ds)
		if !ok || got != want {
			t.Fatalf("size %d: got (%#v,%t), want %#v", size, got, ok, want)
		}

		want64, err := Sum(ds64[0], ds64[1:]...)
		if err != nil {
			t.Fatalf("64-bit size %d: unexpected scalar error: %v", size, err)
		}
		got64, ok64 := arithAVX2SumDecimalsPositive64(ds64)
		if !ok64 || got64 != want64 {
			t.Fatalf("64-bit size %d: got (%#v,%t), want %#v", size, got64, ok64, want64)
		}
	}

	if _, ok := arithAVX2SumDecimalsPositive([]Decimal{NewFromInt(1), NewFromInt(-1)}); ok {
		t.Fatal("negative input unexpectedly stayed on AVX2 path")
	}
	if _, ok := arithAVX2SumDecimalsPositive([]Decimal{NewFromInt(1), MustNew(1, -1)}); ok {
		t.Fatal("mixed precision unexpectedly stayed on AVX2 path")
	}
	if _, ok := arithAVX2SumDecimalsPositive64([]Decimal{newDecimal(u128{hi: 1}, false, 0), NewFromInt(1), NewFromInt(2), NewFromInt(3)}); ok {
		t.Fatal("wide coefficient unexpectedly stayed on AVX2 64-bit path")
	}
}

func BenchmarkArithmeticAVX2SumExperiment(b *testing.B) {
	if !archsimd.X86.AVX2() {
		b.Skip("CPU does not expose AVX2")
	}
	b.ReportAllocs()

	positive := make([]Decimal, 4096)
	positive64 := make([]Decimal, 4096)
	for i := range positive {
		coef := u128{hi: uint64(i & 1), lo: uint64(i)*0x9e3779b97f4a7c15 + 1}
		positive[i] = newDecimal(coef, false, 4)
		positive64[i] = newDecimal(u128{lo: coef.lo}, false, 4)
	}
	b.Run("DecimalSum4096Positive/scalar", func(b *testing.B) {
		var result Decimal
		var err error
		for b.Loop() {
			result, err = Sum(positive[0], positive[1:]...)
		}
		if err != nil {
			b.Fatal(err)
		}
		arithAVX2DecimalSink = result
	})
	b.Run("DecimalSum4096Positive/avx2", func(b *testing.B) {
		var result Decimal
		var ok bool
		for b.Loop() {
			result, ok = arithAVX2SumDecimalsPositive(positive)
		}
		if !ok {
			b.Fatal("unexpected AVX2 fallback")
		}
		arithAVX2DecimalSink = result
	})
	b.Run("DecimalSum4096Positive64/scalar", func(b *testing.B) {
		var result Decimal
		var err error
		for b.Loop() {
			result, err = Sum(positive64[0], positive64[1:]...)
		}
		if err != nil {
			b.Fatal(err)
		}
		arithAVX2DecimalSink = result
	})
	b.Run("DecimalSum4096Positive64/avx2", func(b *testing.B) {
		var result Decimal
		var ok bool
		for b.Loop() {
			result, ok = arithAVX2SumDecimalsPositive64(positive64)
		}
		if !ok {
			b.Fatal("unexpected AVX2 64-bit fallback")
		}
		arithAVX2DecimalSink = result
	})
}
