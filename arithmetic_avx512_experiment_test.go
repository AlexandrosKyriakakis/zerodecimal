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
	arithAVX512DecimalHi01     = [8]uint64{0, 3, 6, 9, 12, 15, 0, 0}
	arithAVX512DecimalHi2      = [8]uint64{0, 0, 0, 0, 0, 0, 2, 5}
	arithAVX512DecimalLo01     = [8]uint64{1, 4, 7, 10, 13, 0, 0, 0}
	arithAVX512DecimalLo2      = [8]uint64{0, 0, 0, 0, 0, 0, 3, 6}
	arithAVX512DecimalMeta01   = [8]uint64{2, 5, 8, 11, 14, 0, 0, 0}
	arithAVX512DecimalMeta2    = [8]uint64{0, 0, 0, 0, 0, 1, 4, 7}
	arithAVX512U128BlockSink   [8]u128
	arithAVX512Uint64BlockSink [8]uint64
	arithAVX512U128Sink        u128
	arithAVX512WordSink        uint64
	arithAVX512DecimalSink     Decimal
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

func arithAVX512LoadDecimalCoefx8(ds *[8]Decimal) (archsimd.Uint64x8, archsimd.Uint64x8) {
	// Decimal is three machine words on amd64: [coef.hi, coef.lo, metadata].
	// Eight values therefore occupy three contiguous ZMM loads. Each output
	// limb draws from all three inputs, so assemble the v0/v1 lanes first and
	// blend in the v2 lanes. Metadata and padding lanes are never selected.
	base := unsafe.Pointer(&ds[0])
	v0 := archsimd.LoadUint64x8Array((*[8]uint64)(base))
	v1 := archsimd.LoadUint64x8Array((*[8]uint64)(unsafe.Add(base, 64)))
	v2 := archsimd.LoadUint64x8Array((*[8]uint64)(unsafe.Add(base, 128)))
	hi01 := v0.ConcatPermute(v1, archsimd.LoadUint64x8Array(&arithAVX512DecimalHi01))
	hi2 := v2.Permute(archsimd.LoadUint64x8Array(&arithAVX512DecimalHi2))
	lo01 := v0.ConcatPermute(v1, archsimd.LoadUint64x8Array(&arithAVX512DecimalLo01))
	lo2 := v2.Permute(archsimd.LoadUint64x8Array(&arithAVX512DecimalLo2))
	return hi2.IfElse(archsimd.Mask64x8FromBits(0xc0), hi01),
		lo2.IfElse(archsimd.Mask64x8FromBits(0xe0), lo01)
}

func arithAVX512AddVectors(
	ahi, alo, bhi, blo archsimd.Uint64x8,
) (archsimd.Uint64x8, archsimd.Uint64x8, archsimd.Mask64x8) {
	return arithAVX512AddVectorsOne(ahi, alo, bhi, blo, archsimd.BroadcastUint64x8(1))
}

func arithAVX512AddVectorsOne(
	ahi, alo, bhi, blo, one archsimd.Uint64x8,
) (archsimd.Uint64x8, archsimd.Uint64x8, archsimd.Mask64x8) {
	lo := alo.Add(blo)
	carry := lo.Less(alo)
	hi0 := ahi.Add(bhi)
	hi := hi0.Add(one.Masked(carry))
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

// arithAVX512SumDecimals is an end-to-end prototype for Sum's common
// same-precision narrow path. It includes Decimal's 24-byte AoS stride, sign
// separation, canonical-zero handling, precision validation, vector-lane
// reduction, and overflow detection. false asks the caller to use Sum's full
// scalar/wide implementation; this prototype never weakens Sum's contract.
func arithAVX512SumDecimalsScalarMeta(ds []Decimal) (Decimal, bool) {
	first := 0
	for first < len(ds) && ds[first].coef.isZero() {
		first++
	}
	if first == len(ds) {
		return Decimal{}, true
	}
	prec := ds[first].prec

	var posHi, posLo, negHi, negLo archsimd.Uint64x8
	var overflow archsimd.Mask64x8
	i := first
	for ; len(ds)-i >= 8; i += 8 {
		var negBits uint8
		for lane := range 8 {
			d := ds[i+lane]
			if d.coef.isZero() {
				continue
			}
			if d.prec != prec {
				return Decimal{}, false
			}
			if d.neg {
				negBits |= 1 << lane
			}
		}

		xhi, xlo := arithAVX512LoadDecimalCoefx8((*[8]Decimal)(unsafe.Pointer(&ds[i])))
		var ov archsimd.Mask64x8
		switch negBits {
		case 0:
			posHi, posLo, ov = arithAVX512AddVectors(posHi, posLo, xhi, xlo)
			overflow = overflow.Or(ov)
		case 0xff:
			negHi, negLo, ov = arithAVX512AddVectors(negHi, negLo, xhi, xlo)
			overflow = overflow.Or(ov)
		default:
			negMask := archsimd.Mask64x8FromBits(negBits)
			posMask := archsimd.Mask64x8FromBits(^negBits)
			posHi, posLo, ov = arithAVX512AddVectors(posHi, posLo, xhi.Masked(posMask), xlo.Masked(posMask))
			overflow = overflow.Or(ov)
			negHi, negLo, ov = arithAVX512AddVectors(negHi, negLo, xhi.Masked(negMask), xlo.Masked(negMask))
			overflow = overflow.Or(ov)
		}
	}
	if overflow.ToBits() != 0 {
		return Decimal{}, false
	}

	var posHis, posLos, negHis, negLos [8]uint64
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
		if d.prec != prec {
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

// arithAVX512SumDecimals moves Decimal's metadata checks into AVX-512 too.
// Three ZMM loads cover exactly eight 24-byte Decimal values. Hoisting every
// permutation and mask constant outside the loop avoids reloading six ZMM
// constants for each 192-byte block.
func arithAVX512SumDecimals(ds []Decimal) (Decimal, bool) {
	first := 0
	for first < len(ds) && ds[first].coef.isZero() {
		first++
	}
	if first == len(ds) {
		return Decimal{}, true
	}
	prec := ds[first].prec

	hi01Indices := archsimd.LoadUint64x8Array(&arithAVX512DecimalHi01)
	hi2Indices := archsimd.LoadUint64x8Array(&arithAVX512DecimalHi2)
	lo01Indices := archsimd.LoadUint64x8Array(&arithAVX512DecimalLo01)
	lo2Indices := archsimd.LoadUint64x8Array(&arithAVX512DecimalLo2)
	meta01Indices := archsimd.LoadUint64x8Array(&arithAVX512DecimalMeta01)
	meta2Indices := archsimd.LoadUint64x8Array(&arithAVX512DecimalMeta2)
	zero := archsimd.Uint64x8{}
	one := archsimd.BroadcastUint64x8(1)
	precisionMask := archsimd.BroadcastUint64x8(0xff00)
	wantPrecision := archsimd.BroadcastUint64x8(uint64(prec) << 8)

	var posHi, posLo, negHi, negLo archsimd.Uint64x8
	var overflow archsimd.Mask64x8
	i := first
	for ; len(ds)-i >= 8; i += 8 {
		base := unsafe.Pointer(&ds[i])
		v0 := archsimd.LoadUint64x8Array((*[8]uint64)(base))
		v1 := archsimd.LoadUint64x8Array((*[8]uint64)(unsafe.Add(base, 64)))
		v2 := archsimd.LoadUint64x8Array((*[8]uint64)(unsafe.Add(base, 128)))

		hi01 := v0.ConcatPermute(v1, hi01Indices)
		hi2 := v2.Permute(hi2Indices)
		xhi := hi2.IfElse(archsimd.Mask64x8FromBits(0xc0), hi01)
		lo01 := v0.ConcatPermute(v1, lo01Indices)
		lo2 := v2.Permute(lo2Indices)
		xlo := lo2.IfElse(archsimd.Mask64x8FromBits(0xe0), lo01)
		meta01 := v0.ConcatPermute(v1, meta01Indices)
		meta2 := v2.Permute(meta2Indices)
		meta := meta2.IfElse(archsimd.Mask64x8FromBits(0xe0), meta01)

		// Canonical zeros carry precision zero, so exclude their lanes when
		// checking the common nonzero precision. Padding bytes in Decimal's
		// metadata word are deliberately ignored.
		nonzeroBits := xhi.Or(xlo).NotEqual(zero).ToBits()
		mismatchBits := meta.And(precisionMask).NotEqual(wantPrecision).ToBits() & nonzeroBits
		if mismatchBits != 0 {
			return Decimal{}, false
		}
		negBits := meta.And(one).NotEqual(zero).ToBits() & nonzeroBits

		var ov archsimd.Mask64x8
		switch negBits {
		case 0:
			posHi, posLo, ov = arithAVX512AddVectorsOne(posHi, posLo, xhi, xlo, one)
			overflow = overflow.Or(ov)
		case 0xff:
			negHi, negLo, ov = arithAVX512AddVectorsOne(negHi, negLo, xhi, xlo, one)
			overflow = overflow.Or(ov)
		default:
			negMask := archsimd.Mask64x8FromBits(negBits)
			posMask := archsimd.Mask64x8FromBits(^negBits)
			posHi, posLo, ov = arithAVX512AddVectorsOne(
				posHi, posLo, xhi.Masked(posMask), xlo.Masked(posMask), one,
			)
			overflow = overflow.Or(ov)
			negHi, negLo, ov = arithAVX512AddVectorsOne(
				negHi, negLo, xhi.Masked(negMask), xlo.Masked(negMask), one,
			)
			overflow = overflow.Or(ov)
		}
	}
	if overflow.ToBits() != 0 {
		return Decimal{}, false
	}

	var posHis, posLos, negHis, negLos [8]uint64
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
		if d.prec != prec {
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

	for _, size := range []int{1, 2, 7, 8, 9, 63, 64, 65, 4096} {
		ds := make([]Decimal, size)
		for i := range ds {
			if i%17 == 0 {
				continue
			}
			ds[i] = newDecimal(u128{hi: uint64(i & 1), lo: rng.Uint64() & ((1 << 40) - 1)}, i%3 == 0, 4)
		}
		want, err := Sum(ds[0], ds[1:]...)
		if err != nil {
			t.Fatalf("decimal sum size %d: unexpected scalar error: %v", size, err)
		}
		gotScalarMeta, okScalarMeta := arithAVX512SumDecimalsScalarMeta(ds)
		if !okScalarMeta || gotScalarMeta != want {
			t.Fatalf("decimal sum scalar metadata size %d: got (%#v,%t), want %#v", size, gotScalarMeta, okScalarMeta, want)
		}
		gotVectorMeta, okVectorMeta := arithAVX512SumDecimals(ds)
		if !okVectorMeta || gotVectorMeta != want {
			t.Fatalf("decimal sum vector metadata size %d: got (%#v,%t), want %#v", size, gotVectorMeta, okVectorMeta, want)
		}
	}

	mixedPrecision := []Decimal{NewFromInt(1), MustNew(1, -1)}
	if _, ok := arithAVX512SumDecimalsScalarMeta(mixedPrecision); ok {
		t.Fatal("decimal sum scalar metadata: mixed precision unexpectedly stayed on AVX-512 path")
	}
	if _, ok := arithAVX512SumDecimals(mixedPrecision); ok {
		t.Fatal("decimal sum vector metadata: mixed precision unexpectedly stayed on AVX-512 path")
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

	positive := make([]Decimal, 4096)
	mixedSigns := make([]Decimal, 4096)
	for i := range positive {
		coef := u128{hi: uint64(i & 1), lo: uint64(i)*0x9e3779b97f4a7c15 + 1}
		positive[i] = newDecimal(coef, false, 4)
		mixedSigns[i] = newDecimal(coef, i%3 == 0, 4)
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
		arithAVX512DecimalSink = result
	})
	b.Run("DecimalSum4096Positive/avx512", func(b *testing.B) {
		var result Decimal
		var ok bool
		for b.Loop() {
			result, ok = arithAVX512SumDecimals(positive)
		}
		if !ok {
			b.Fatal("unexpected AVX-512 fallback")
		}
		arithAVX512DecimalSink = result
	})
	b.Run("DecimalSum4096Positive/avx512-scalar-metadata", func(b *testing.B) {
		var result Decimal
		var ok bool
		for b.Loop() {
			result, ok = arithAVX512SumDecimalsScalarMeta(positive)
		}
		if !ok {
			b.Fatal("unexpected AVX-512 fallback")
		}
		arithAVX512DecimalSink = result
	})
	b.Run("DecimalSum4096MixedSigns/scalar", func(b *testing.B) {
		var result Decimal
		var err error
		for b.Loop() {
			result, err = Sum(mixedSigns[0], mixedSigns[1:]...)
		}
		if err != nil {
			b.Fatal(err)
		}
		arithAVX512DecimalSink = result
	})
	b.Run("DecimalSum4096MixedSigns/avx512", func(b *testing.B) {
		var result Decimal
		var ok bool
		for b.Loop() {
			result, ok = arithAVX512SumDecimals(mixedSigns)
		}
		if !ok {
			b.Fatal("unexpected AVX-512 fallback")
		}
		arithAVX512DecimalSink = result
	})
	b.Run("DecimalSum4096MixedSigns/avx512-scalar-metadata", func(b *testing.B) {
		var result Decimal
		var ok bool
		for b.Loop() {
			result, ok = arithAVX512SumDecimalsScalarMeta(mixedSigns)
		}
		if !ok {
			b.Fatal("unexpected AVX-512 fallback")
		}
		arithAVX512DecimalSink = result
	})
}
