//go:build go1.27 && !go1.28 && goexperiment.simd && amd64

package zerodecimal

import (
	"simd/archsimd"
	"unsafe"
)

// Go 1.27's SIMD API is experimental and explicitly unstable. Keep this file
// pinned to exactly Go 1.27; later toolchains use the scalar stub until the API
// and generated code are revalidated.

const sumSIMDDecimalSize = unsafe.Sizeof(Decimal{})

var (
	_ [24 - sumSIMDDecimalSize]byte
	_ [sumSIMDDecimalSize - 24]byte

	sumAVX512DecimalHi01   = [8]uint64{0, 3, 6, 9, 12, 15, 0, 0}
	sumAVX512DecimalHi2    = [8]uint64{0, 0, 0, 0, 0, 0, 2, 5}
	sumAVX512DecimalLo01   = [8]uint64{1, 4, 7, 10, 13, 0, 0, 0}
	sumAVX512DecimalLo2    = [8]uint64{0, 0, 0, 0, 0, 0, 3, 6}
	sumAVX512DecimalMeta01 = [8]uint64{2, 5, 8, 11, 14, 0, 0, 0}
	sumAVX512DecimalMeta2  = [8]uint64{0, 0, 0, 0, 0, 1, 4, 7}
)

// sumSIMDPrefix returns an exact positive prefix and the first unprocessed
// rest index. Returning a prefix rather than merely failing lets Sum continue
// scalar evaluation without rescanning compatible values before a late
// negative, wide coefficient, or precision mismatch.
func sumSIMDPrefix(first Decimal, rest []Decimal) (Decimal, int, bool) {
	if first.coef.isZero() || first.neg {
		return Decimal{}, 0, false
	}
	if archsimd.X86.AVX512() && len(rest) >= 16 {
		return sumAVX512PositivePrefix(first, rest)
	}
	if archsimd.X86.AVX2() && first.coef.hi == 0 && len(rest) >= 8 {
		return sumAVX2Positive64Prefix(first, rest)
	}
	return Decimal{}, 0, false
}

func sumAVX2LoadDecimal4(base unsafe.Pointer) (archsimd.Uint64x4, archsimd.Uint64x4, archsimd.Uint64x4) {
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

func sumAVX2LessUnsigned(x, y, signBit archsimd.Uint64x4) archsimd.Mask64x4 {
	return y.Xor(signBit).AsInt64x4().Greater(x.Xor(signBit).AsInt64x4())
}

func sumAVX2Positive64Prefix(first Decimal, rest []Decimal) (Decimal, int, bool) {
	prec := first.prec
	zero := archsimd.Uint64x4{}
	signBit := archsimd.BroadcastUint64x4(1 << 63)
	metadataMask := archsimd.BroadcastUint64x4(0xffff)
	wantMetadata := archsimd.BroadcastUint64x4(uint64(prec) << 8)

	var sumAHi, sumALo, sumBHi, sumBLo archsimd.Uint64x4
	i := 0
	for ; len(rest)-i >= 8; i += 8 {
		aHi, aLo, aMeta := sumAVX2LoadDecimal4(unsafe.Pointer(&rest[i]))
		aMeta = aMeta.And(metadataMask)
		aValidMetadata := aMeta.Equal(wantMetadata).Or(
			aMeta.Equal(zero).And(aLo.Equal(zero)),
		)
		if aValidMetadata.And(aHi.Equal(zero)).ToBits() != 0x0f {
			return sumAVX2Positive64Finish(first, rest, i, sumAHi, sumALo, sumBHi, sumBLo)
		}
		aSumLo := sumALo.Add(aLo)
		aCarry := sumAVX2LessUnsigned(aSumLo, sumALo, signBit)
		sumALo = aSumLo
		sumAHi = sumAHi.Sub(aCarry.ToInt64x4().AsUint64x4())

		bHi, bLo, bMeta := sumAVX2LoadDecimal4(unsafe.Pointer(&rest[i+4]))
		bMeta = bMeta.And(metadataMask)
		bValidMetadata := bMeta.Equal(wantMetadata).Or(
			bMeta.Equal(zero).And(bLo.Equal(zero)),
		)
		if bValidMetadata.And(bHi.Equal(zero)).ToBits() != 0x0f {
			// Group A is already represented by sumA. Finish it, then leave
			// group B and the remaining suffix for scalar continuation.
			return sumAVX2Positive64Finish(first, rest, i+4, sumAHi, sumALo, sumBHi, sumBLo)
		}
		bSumLo := sumBLo.Add(bLo)
		bCarry := sumAVX2LessUnsigned(bSumLo, sumBLo, signBit)
		sumBLo = bSumLo
		sumBHi = sumBHi.Sub(bCarry.ToInt64x4().AsUint64x4())
	}
	return sumAVX2Positive64Finish(first, rest, i, sumAHi, sumALo, sumBHi, sumBLo)
}

func sumAVX2Positive64Finish(
	first Decimal,
	rest []Decimal,
	next int,
	sumAHi, sumALo, sumBHi, sumBLo archsimd.Uint64x4,
) (Decimal, int, bool) {
	signBit := archsimd.BroadcastUint64x4(1 << 63)
	sumLo := sumALo.Add(sumBLo)
	mergeCarry := sumAVX2LessUnsigned(sumLo, sumALo, signBit)
	sumHi := sumAHi.Add(sumBHi).Sub(mergeCarry.ToInt64x4().AsUint64x4())

	var his, los [4]uint64
	sumHi.StoreArray(&his)
	sumLo.StoreArray(&los)
	sum := first.coef
	for lane := range his {
		var carry uint64
		sum, carry = add128(sum, u128{hi: his[lane], lo: los[lane]})
		if carry != 0 {
			return Decimal{}, 0, false
		}
	}

	for next < len(rest) {
		d := rest[next]
		if d.coef.isZero() {
			next++
			continue
		}
		if d.neg || d.prec != first.prec || d.coef.hi != 0 {
			return newDecimal(sum, false, first.prec), next, true
		}
		var carry uint64
		sum, carry = add128(sum, d.coef)
		if carry != 0 {
			return Decimal{}, 0, false
		}
		next++
	}
	return newDecimal(sum, false, first.prec), next, true
}

func sumAVX512Add(
	ahi, alo, bhi, blo archsimd.Uint64x8,
) (archsimd.Uint64x8, archsimd.Uint64x8, archsimd.Mask64x8) {
	lo := alo.Add(blo)
	carry := lo.Less(alo)
	hi0 := ahi.Add(bhi)
	hi := hi0.Sub(carry.ToInt64x8().AsUint64x8())
	overflow := hi0.Less(ahi).Or(hi.Less(hi0))
	return hi, lo, overflow
}

func sumAVX512PositivePrefix(first Decimal, rest []Decimal) (Decimal, int, bool) {
	hi01Indices := archsimd.LoadUint64x8Array(&sumAVX512DecimalHi01)
	hi2Indices := archsimd.LoadUint64x8Array(&sumAVX512DecimalHi2)
	lo01Indices := archsimd.LoadUint64x8Array(&sumAVX512DecimalLo01)
	lo2Indices := archsimd.LoadUint64x8Array(&sumAVX512DecimalLo2)
	meta01Indices := archsimd.LoadUint64x8Array(&sumAVX512DecimalMeta01)
	meta2Indices := archsimd.LoadUint64x8Array(&sumAVX512DecimalMeta2)
	zero := archsimd.Uint64x8{}
	metadataMask := archsimd.BroadcastUint64x8(0xffff)
	wantMetadata := archsimd.BroadcastUint64x8(uint64(first.prec) << 8)

	var sumAHi, sumALo, sumBHi, sumBLo archsimd.Uint64x8
	var overflow archsimd.Mask64x8
	i := 0
	for ; len(rest)-i >= 16; i += 16 {
		aHi, aLo, aMeta := sumAVX512LoadDecimal8(
			unsafe.Pointer(&rest[i]),
			hi01Indices, hi2Indices, lo01Indices, lo2Indices, meta01Indices, meta2Indices,
		)
		aMeta = aMeta.And(metadataMask)
		aZero := aHi.Equal(zero).And(aLo.Equal(zero))
		if aMeta.Equal(wantMetadata).Or(aMeta.Equal(zero).And(aZero)).ToBits() != 0xff {
			return sumAVX512PositiveFinish(first, rest, i, sumAHi, sumALo, sumBHi, sumBLo, overflow)
		}
		var ov archsimd.Mask64x8
		sumAHi, sumALo, ov = sumAVX512Add(sumAHi, sumALo, aHi, aLo)
		overflow = overflow.Or(ov)

		bHi, bLo, bMeta := sumAVX512LoadDecimal8(
			unsafe.Pointer(&rest[i+8]),
			hi01Indices, hi2Indices, lo01Indices, lo2Indices, meta01Indices, meta2Indices,
		)
		bMeta = bMeta.And(metadataMask)
		bZero := bHi.Equal(zero).And(bLo.Equal(zero))
		if bMeta.Equal(wantMetadata).Or(bMeta.Equal(zero).And(bZero)).ToBits() != 0xff {
			return sumAVX512PositiveFinish(first, rest, i+8, sumAHi, sumALo, sumBHi, sumBLo, overflow)
		}
		sumBHi, sumBLo, ov = sumAVX512Add(sumBHi, sumBLo, bHi, bLo)
		overflow = overflow.Or(ov)
	}
	return sumAVX512PositiveFinish(first, rest, i, sumAHi, sumALo, sumBHi, sumBLo, overflow)
}

func sumAVX512LoadDecimal8(
	base unsafe.Pointer,
	hi01Indices, hi2Indices, lo01Indices, lo2Indices, meta01Indices, meta2Indices archsimd.Uint64x8,
) (archsimd.Uint64x8, archsimd.Uint64x8, archsimd.Uint64x8) {
	v0 := archsimd.LoadUint64x8Array((*[8]uint64)(base))
	v1 := archsimd.LoadUint64x8Array((*[8]uint64)(unsafe.Add(base, 64)))
	v2 := archsimd.LoadUint64x8Array((*[8]uint64)(unsafe.Add(base, 128)))
	hi01 := v0.ConcatPermute(v1, hi01Indices)
	hi2 := v2.Permute(hi2Indices)
	hi := hi2.IfElse(archsimd.Mask64x8FromBits(0xc0), hi01)
	lo01 := v0.ConcatPermute(v1, lo01Indices)
	lo2 := v2.Permute(lo2Indices)
	lo := lo2.IfElse(archsimd.Mask64x8FromBits(0xe0), lo01)
	meta01 := v0.ConcatPermute(v1, meta01Indices)
	meta2 := v2.Permute(meta2Indices)
	meta := meta2.IfElse(archsimd.Mask64x8FromBits(0xe0), meta01)
	return hi, lo, meta
}

func sumAVX512PositiveFinish(
	first Decimal,
	rest []Decimal,
	next int,
	sumAHi, sumALo, sumBHi, sumBLo archsimd.Uint64x8,
	overflow archsimd.Mask64x8,
) (Decimal, int, bool) {
	if overflow.ToBits() != 0 {
		return Decimal{}, 0, false
	}
	sumHi, sumLo, mergeOverflow := sumAVX512Add(sumAHi, sumALo, sumBHi, sumBLo)
	if mergeOverflow.ToBits() != 0 {
		return Decimal{}, 0, false
	}

	var his, los [8]uint64
	sumHi.StoreArray(&his)
	sumLo.StoreArray(&los)
	sum := first.coef
	for lane := range his {
		var carry uint64
		sum, carry = add128(sum, u128{hi: his[lane], lo: los[lane]})
		if carry != 0 {
			return Decimal{}, 0, false
		}
	}

	for next < len(rest) {
		d := rest[next]
		if d.coef.isZero() {
			next++
			continue
		}
		if d.neg || d.prec != first.prec {
			return newDecimal(sum, false, first.prec), next, true
		}
		var carry uint64
		sum, carry = add128(sum, d.coef)
		if carry != 0 {
			return Decimal{}, 0, false
		}
		next++
	}
	return newDecimal(sum, false, first.prec), next, true
}
