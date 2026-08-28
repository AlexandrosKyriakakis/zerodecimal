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
	ahi, alo, bhi, blo, signBit archsimd.Uint64x4,
) (archsimd.Uint64x4, archsimd.Uint64x4, archsimd.Mask64x4) {
	lo := alo.Add(blo)
	carry := arithAVX2LessUnsigned(lo, alo, signBit)
	hi0 := ahi.Add(bhi)
	// A true AVX2 mask lane is MaxUint64. Subtracting it increments hi with
	// one instruction and avoids materializing/masking a broadcast-one vector.
	hi := hi0.Sub(carry.ToInt64x4().AsUint64x4())
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
		sumHi, sumLo, ov = arithAVX2AddVectorsOne(sumHi, sumLo, xhi, xlo, signBit)
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
		sumHi = sumHi.Sub(carry.ToInt64x4().AsUint64x4())
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

// arithAVX2SumDecimalsPositive64x2 keeps two independent vector carry chains
// over eight Decimals per iteration. Validate and consume each group before
// loading the next one so the compiler can keep both accumulators in YMM
// registers instead of spilling under deinterleave's temporary pressure.
func arithAVX2SumDecimalsPositive64x2(ds []Decimal) (Decimal, bool) {
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
	signBit := archsimd.BroadcastUint64x4(1 << 63)
	metadataMask := archsimd.BroadcastUint64x4(0xffff)
	wantMetadata := archsimd.BroadcastUint64x4(uint64(prec) << 8)

	var sumAHi, sumALo, sumBHi, sumBLo archsimd.Uint64x4
	i := first
	for ; len(ds)-i >= 8; i += 8 {
		aHi, aLo, aMeta := arithAVX2LoadDecimal4(unsafe.Pointer(&ds[i]))
		aMeta = aMeta.And(metadataMask)
		aValidMetadata := aMeta.Equal(wantMetadata).Or(aMeta.Equal(zero))
		aValid := aValidMetadata.And(aHi.Equal(zero))
		if aValid.ToBits() != 0x0f {
			return Decimal{}, false
		}
		aSumLo := sumALo.Add(aLo)
		aCarry := arithAVX2LessUnsigned(aSumLo, sumALo, signBit)
		sumALo = aSumLo
		sumAHi = sumAHi.Sub(aCarry.ToInt64x4().AsUint64x4())

		bHi, bLo, bMeta := arithAVX2LoadDecimal4(unsafe.Pointer(&ds[i+4]))
		bMeta = bMeta.And(metadataMask)
		bValidMetadata := bMeta.Equal(wantMetadata).Or(bMeta.Equal(zero))
		bValid := bValidMetadata.And(bHi.Equal(zero))
		if bValid.ToBits() != 0x0f {
			return Decimal{}, false
		}
		bSumLo := sumBLo.Add(bLo)
		bCarry := arithAVX2LessUnsigned(bSumLo, sumBLo, signBit)
		sumBLo = bSumLo
		sumBHi = sumBHi.Sub(bCarry.ToInt64x4().AsUint64x4())
	}

	sumLo := sumALo.Add(sumBLo)
	mergeCarry := arithAVX2LessUnsigned(sumLo, sumALo, signBit)
	sumHi := sumAHi.Add(sumBHi).Sub(mergeCarry.ToInt64x4().AsUint64x4())
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

// arithAVX2SumDecimals64 handles both signs when every nonzero input has one
// common precision and a 64-bit coefficient. Separate positive and negative
// vector totals preserve Sum's order-independent cancellation semantics. Each
// subtotal fits 128 bits because len(ds) <= MaxInt on amd64.
func arithAVX2SumDecimals64(ds []Decimal) (Decimal, bool) {
	first := 0
	for first < len(ds) && ds[first].coef.isZero() {
		first++
	}
	if first == len(ds) {
		return Decimal{}, true
	}
	prec := ds[first].prec

	zero := archsimd.Uint64x4{}
	signBit := archsimd.BroadcastUint64x4(1 << 63)
	// Mask away the neg bit and padding while retaining precision. A nonzero
	// lane must then equal wantMetadata; canonical zero remains zero.
	metadataWithoutSignMask := archsimd.BroadcastUint64x4(0xfffe)
	wantMetadata := archsimd.BroadcastUint64x4(uint64(prec) << 8)

	var posHi, posLo, negHi, negLo archsimd.Uint64x4
	i := first
	for ; len(ds)-i >= 4; i += 4 {
		xhi, xlo, meta := arithAVX2LoadDecimal4(unsafe.Pointer(&ds[i]))
		normalizedMeta := meta.And(metadataWithoutSignMask)
		validMetadata := normalizedMeta.Equal(wantMetadata).Or(normalizedMeta.Equal(zero))
		valid := validMetadata.And(xhi.Equal(zero))
		if valid.ToBits() != 0x0f {
			return Decimal{}, false
		}

		// Move metadata bit zero into each lane's sign bit. A signed comparison
		// against zero expands the resulting negative marker into an AVX2 mask;
		// Int64x4 arithmetic right shift would require AVX-512.
		negMarker := meta.ShiftAllLeft(63).AsInt64x4()
		negMask := (archsimd.Int64x4{}).Greater(negMarker)
		negBits := negMask.ToInt64x4().AsUint64x4()
		posXLo := xlo.AndNot(negBits)
		negXLo := xlo.And(negBits)

		newPosLo := posLo.Add(posXLo)
		posCarry := arithAVX2LessUnsigned(newPosLo, posLo, signBit)
		posLo = newPosLo
		posHi = posHi.Sub(posCarry.ToInt64x4().AsUint64x4())

		newNegLo := negLo.Add(negXLo)
		negCarry := arithAVX2LessUnsigned(newNegLo, negLo, signBit)
		negLo = newNegLo
		negHi = negHi.Sub(negCarry.ToInt64x4().AsUint64x4())
	}

	var posHis, posLos, negHis, negLos [4]uint64
	posHi.StoreArray(&posHis)
	posLo.StoreArray(&posLos)
	negHi.StoreArray(&negHis)
	negLo.StoreArray(&negLos)
	var pos, neg u128
	for lane := range posHis {
		var carry uint64
		pos, carry = add128(pos, u128{hi: posHis[lane], lo: posLos[lane]})
		if carry != 0 {
			return Decimal{}, false
		}
		neg, carry = add128(neg, u128{hi: negHis[lane], lo: negLos[lane]})
		if carry != 0 {
			return Decimal{}, false
		}
	}
	for ; i < len(ds); i++ {
		d := ds[i]
		if d.coef.isZero() {
			continue
		}
		if d.prec != prec || d.coef.hi != 0 {
			return Decimal{}, false
		}
		var carry uint64
		if d.neg {
			neg, carry = add128(neg, d.coef)
		} else {
			pos, carry = add128(pos, d.coef)
		}
		if carry != 0 {
			return Decimal{}, false
		}
	}

	if neg.isZero() {
		return newDecimal(pos, false, prec), true
	}
	if pos.isZero() {
		return newDecimal(neg, true, prec), true
	}
	if cmp128(pos, neg) >= 0 {
		coef, _ := sub128(pos, neg)
		return newDecimal(coef, false, prec), true
	}
	coef, _ := sub128(neg, pos)
	return newDecimal(coef, true, prec), true
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

		want, err := arithScalarSumReference(ds)
		if err != nil {
			t.Fatalf("size %d: unexpected scalar error: %v", size, err)
		}
		got, ok := arithAVX2SumDecimalsPositive(ds)
		if !ok || got != want {
			t.Fatalf("size %d: got (%#v,%t), want %#v", size, got, ok, want)
		}

		want64, err := arithScalarSumReference(ds64)
		if err != nil {
			t.Fatalf("64-bit size %d: unexpected scalar error: %v", size, err)
		}
		got64, ok64 := arithAVX2SumDecimalsPositive64(ds64)
		if !ok64 || got64 != want64 {
			t.Fatalf("64-bit size %d: got (%#v,%t), want %#v", size, got64, ok64, want64)
		}
		got64x2, ok64x2 := arithAVX2SumDecimalsPositive64x2(ds64)
		if !ok64x2 || got64x2 != want64 {
			t.Fatalf("64-bit x2 size %d: got (%#v,%t), want %#v", size, got64x2, ok64x2, want64)
		}

		for i := range ds64 {
			ds64[i].neg = !ds64[i].coef.isZero() && i%3 == 0
		}
		wantMixed64, err := arithScalarSumReference(ds64)
		if err != nil {
			t.Fatalf("mixed 64-bit size %d: unexpected scalar error: %v", size, err)
		}
		gotMixed64, okMixed64 := arithAVX2SumDecimals64(ds64)
		if !okMixed64 || gotMixed64 != wantMixed64 {
			t.Fatalf("mixed 64-bit size %d: got (%#v,%t), want %#v", size, gotMixed64, okMixed64, wantMixed64)
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
	if _, ok := arithAVX2SumDecimalsPositive64x2([]Decimal{
		newDecimal(u128{hi: 1}, false, 0), NewFromInt(1), NewFromInt(2), NewFromInt(3),
		NewFromInt(4), NewFromInt(5), NewFromInt(6), NewFromInt(7),
	}); ok {
		t.Fatal("wide coefficient unexpectedly stayed on AVX2 64-bit x2 path")
	}
	if _, ok := arithAVX2SumDecimals64([]Decimal{newDecimal(u128{hi: 1}, true, 0), NewFromInt(1), NewFromInt(2), NewFromInt(3)}); ok {
		t.Fatal("wide coefficient unexpectedly stayed on mixed-sign AVX2 64-bit path")
	}
}

func BenchmarkArithmeticAVX2SumExperiment(b *testing.B) {
	if !archsimd.X86.AVX2() {
		b.Skip("CPU does not expose AVX2")
	}
	b.ReportAllocs()

	positive := make([]Decimal, 4096)
	positive64 := make([]Decimal, 4096)
	mixed64 := make([]Decimal, 4096)
	for i := range positive {
		coef := u128{hi: uint64(i & 1), lo: uint64(i)*0x9e3779b97f4a7c15 + 1}
		positive[i] = newDecimal(coef, false, 4)
		positive64[i] = newDecimal(u128{lo: coef.lo}, false, 4)
		mixed64[i] = newDecimal(u128{lo: coef.lo}, i%3 == 0, 4)
	}
	b.Run("DecimalSum4096Positive/scalar", func(b *testing.B) {
		var result Decimal
		var err error
		for b.Loop() {
			result, err = arithScalarSumReference(positive)
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
			result, err = arithScalarSumReference(positive64)
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
	b.Run("DecimalSum4096Positive64/avx2-2x", func(b *testing.B) {
		var result Decimal
		var ok bool
		for b.Loop() {
			result, ok = arithAVX2SumDecimalsPositive64x2(positive64)
		}
		if !ok {
			b.Fatal("unexpected AVX2 64-bit x2 fallback")
		}
		arithAVX2DecimalSink = result
	})
	b.Run("DecimalSum4096Mixed64/scalar", func(b *testing.B) {
		var result Decimal
		var err error
		for b.Loop() {
			result, err = arithScalarSumReference(mixed64)
		}
		if err != nil {
			b.Fatal(err)
		}
		arithAVX2DecimalSink = result
	})
	b.Run("DecimalSum4096Mixed64/avx2", func(b *testing.B) {
		var result Decimal
		var ok bool
		for b.Loop() {
			result, ok = arithAVX2SumDecimals64(mixed64)
		}
		if !ok {
			b.Fatal("unexpected mixed-sign AVX2 64-bit fallback")
		}
		arithAVX2DecimalSink = result
	})
}
