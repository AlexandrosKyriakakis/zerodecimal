package zerodecimal

import "math/bits"

// MulDivRoundBy returns exactly d.MulDivRound(e, v.Decimal(), places, mode):
// the same value, precision and error, including MulDivRound's validation
// order (places, then mode, then the divisor, where a nil or zero Divisor is
// ErrDivideByZero as a zero f is). It serves a pro-rata share whose whole is
// shared by many calls, such as one quantity splitting several amounts.
//
// With s = places + v.Decimal().Prec() - d.Prec() - e.Prec(), one-limb
// operands whose product scaled by 10^s fits one limb take MulDivRound's own
// single divide. Any wider scaled product with s ≥ 0 is divided on v's
// reciprocal instead of with 128/64-bit divides, as is a product over a
// divisor scaled by 10^-s past 128 bits. A negative s whose scaled divisor
// fits 128 bits divides by that value as MulDivRound does: it is not v's.
func (d Decimal) MulDivRoundBy(e Decimal, v *Divisor, places uint8, mode RoundingMode) (Decimal, error) {
	if places > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	if v == nil || v.e.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	if d.coef.isZero() || e.coef.isZero() {
		return Decimal{}, nil
	}
	neg := d.neg != e.neg != v.e.neg
	// The result coefficient is d·e·10^s / c for v's coefficient c. Shapes
	// that need a call go to mulDivRoundByWide in tail position, so this
	// path keeps its operands in registers (see DivRoundBy).
	s := int(places) + int(v.e.prec) - int(d.prec) - int(e.prec)
	if d.coef.hi|e.coef.hi|v.e.coef.hi != 0 || s < 0 || s > int(MaxPrec) {
		return mulDivRoundByWide(d, e, v, places, mode, neg)
	}
	// One-limb operands and c: the scaled product {n2,n1,n0} spans at most
	// three limbs, as 10^s < 2^64.
	c := v.e.coef.lo
	var n2, n1, n0 uint64
	if ph, p := bits.Mul64(d.coef.lo, e.coef.lo); ph == 0 {
		n1, n0 = bits.Mul64(p, pow10u64[s&31])
		if n1 == 0 {
			// MulDivRound's narrow path as it is: one hardware divide
			// measured faster than a div2by1 step for a one-limb dividend
			// on arm64. A nonzero remainder means c ≥ 2, so q+1 cannot wrap.
			q, r := n0/c, n0%c
			if roundsUp(mode, r != 0, cmpHalf64(r, c), q&1 != 0, neg) {
				q++
			}
			return newDecimal(u128{lo: q}, neg, places), nil
		}
	} else {
		n2, n1, n0 = mul128by64to192(u128{hi: ph, lo: p}, pow10u64[s&31])
	}
	// n2 ≤ c-2 bounds the scaled product below (c-1)·2^128, so the quotient
	// is below 2^128 - 2^128/c < 2^128 - 2^64: it fits, and rounding it up
	// cannot wrap. The rest, every quotient that overflows or may round
	// past 2^128-1 included, is the outlined arm's.
	if n2 >= c-1 {
		return mulDivRoundByWide(d, e, v, places, mode, neg)
	}
	// quoRem's one-limb arm, inlined as in DivRoundBy: n2 < c keeps the
	// normalized numerator within three limbs and each digit within one.
	sh := uint(v.s)
	m2 := n2<<sh | n1>>(64-sh)
	m1 := n1<<sh | n0>>(64-sh)
	m0 := n0 << sh
	var q1 uint64
	if n2 != 0 || n1 >= c {
		q1, m1 = div2by1(m2, m1, v.dn.lo, v.recip)
	}
	q0, r0 := div2by1(m1, m0, v.dn.lo, v.recip)
	r := r0 >> sh
	q := u128{hi: q1, lo: q0}
	if roundsUp(mode, r != 0, cmpHalf64(r, c), q0&1 != 0, neg) {
		q = inc128(q)
	}
	return newDecimal(q, neg, places), nil
}

// mulDivRoundByWide is MulDivRoundBy for every shape its inline arms leave:
// an operand or divisor past one limb, a scale gap s outside 0..MaxPrec, and
// a one-limb scaled product whose high limb reaches c-1. It is
// mulDivRoundWide with v's reciprocal in place of both divisions by c: a
// nonnegative s scales the product (past 2^256 the quotient cannot fit 128
// bits) for v.divRound, and a negative s scales c by 10^-s instead, which
// past 128 bits divRoundScaledBy divides out in two steps. A scaled divisor
// within 128 bits is not v's coefficient: divRoundWide divides by it, as in
// mulDivRoundWide.
func mulDivRoundByWide(d, e Decimal, v *Divisor, places uint8, mode RoundingMode, neg bool) (Decimal, error) {
	prod := mulToU256(d.coef, e.coef)
	var q u128
	var rem bool
	var halfCmp int
	fits := true
	// s = up - down, as in mulDivRoundWide: each side is at most 2·MaxPrec,
	// so neither sum nor the difference taken in its branch wraps a uint8.
	if up, down := places+v.e.prec, d.prec+e.prec; up >= down {
		num, ok := mul256Pow10(prod, up-down)
		if !ok {
			return Decimal{}, ErrOverflow
		}
		q, rem, halfCmp, fits = v.divRound(num)
	} else {
		t := down - up // 1..2*MaxPrec
		if den := mulToU256(v.e.coef, pow10u128[t&63]); den.isZeroUpper() {
			q, rem, halfCmp, fits = divRoundWide(prod, den.lo128())
		} else {
			q, rem, halfCmp = divRoundScaledBy(prod, t, v)
		}
	}
	if !fits {
		return Decimal{}, ErrOverflow
	}
	// roundQuotient for a valid mode, inline.
	if roundsUp(mode, rem, halfCmp, q.lo&1 != 0, neg) {
		if q == (u128{hi: ^uint64(0), lo: ^uint64(0)}) {
			return Decimal{}, ErrOverflow
		}
		q = inc128(q)
	}
	return newDecimal(q, neg, places), nil
}

// divRoundScaledBy is divRoundPow10By(num, t, c) for v's coefficient c: the
// truncated quotient of num over c·10^t, whether the remainder is nonzero,
// and twice the remainder compared with c·10^t. It divides by c first, on
// v's reciprocal, num = w·c + r2, and then w by the power of ten,
// w = q·10^t + r1, since ⌊⌊num/c⌋/10^t⌋ = ⌊num/(c·10^t)⌋; divRoundPow10
// reports r1's facts. The remainder of num over c·10^t is r1·c + r2, nonzero
// when either part is, and twice it less c·10^t is c·(2·r1 - 10^t) + 2·r2:
// 10^t is even, so 2·r1 - 10^t is zero or at least 2 in magnitude, and with
// 0 ≤ 2·r2 < 2·c the comparison of r1 with 10^t/2 decides, unless r1 is
// exactly half, when r2 > 0 tips it above. A quotient w past 128 bits, which
// quoRem does not produce, leaves the division to divRoundPow10By, which
// divides by the power of ten first.
//
// PRECONDITIONS (not checked): 1 ≤ t ≤ 2·MaxPrec, v is valid, and
// c·10^t ≥ 2^128, so the quotient fits 128 bits.
func divRoundScaledBy(num u256, t uint8, v *Divisor) (q u128, rem bool, halfCmp int) {
	w, r2, fits := v.quoRem(num)
	if !fits {
		return divRoundPow10By(num, t, v.e.coef)
	}
	// w < 2^128, so its quotient by 10^t fits too.
	q, rem, halfCmp, _ = divRoundPow10(u256{d0: w.lo, d1: w.hi}, t)
	if !r2.isZero() {
		rem = true
		if halfCmp == 0 {
			halfCmp = 1
		}
	}
	return q, rem, halfCmp
}
