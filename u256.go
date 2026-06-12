package zerodecimal

import "math/bits"

// u256 is a 256-bit unsigned integer in four little-endian 64-bit limbs:
// value = d3·2^192 + d2·2^128 + d1·2^64 + d0.
type u256 struct {
	d0, d1, d2, d3 uint64
}

// isZeroUpper reports whether the value fits in 128 bits.
func (u u256) isZeroUpper() bool {
	return u.d2|u.d3 == 0
}

// lo128 returns the low 128 bits of u.
func (u u256) lo128() u128 {
	return u128{hi: u.d1, lo: u.d0}
}

// hi128 returns the high 128 bits of u.
func (u u256) hi128() u128 {
	return u128{hi: u.d3, lo: u.d2}
}

// mulToU256 returns the full 256-bit product u·v.
func mulToU256(u, v u128) u256 {
	// Dominant case: both operands fit in 64 bits and one multiply suffices.
	if u.hi|v.hi == 0 {
		hi, lo := bits.Mul64(u.lo, v.lo)
		return u256{d0: lo, d1: hi}
	}

	// Schoolbook: u·v = u.lo·v.lo + (u.hi·v.lo + u.lo·v.hi)·2^64
	// + u.hi·v.hi·2^128, accumulated limb by limb.
	d1, d0 := bits.Mul64(u.lo, v.lo)
	h1, l1 := bits.Mul64(u.hi, v.lo)
	h2, l2 := bits.Mul64(u.lo, v.hi)
	h3, l3 := bits.Mul64(u.hi, v.hi)

	d1, c1 := bits.Add64(d1, l1, 0)
	d1, c2 := bits.Add64(d1, l2, 0)

	d2, c3 := bits.Add64(h1, h2, 0)
	d2, c4 := bits.Add64(d2, c1+c2, 0)
	d2, c5 := bits.Add64(d2, l3, 0)

	// The exact product is < 2^256, so the top-limb sum cannot wrap.
	d3 := h3 + c3 + c4 + c5

	return u256{d0: d0, d1: d1, d2: d2, d3: d3}
}

// div256by128 returns q = u/v and r = u%v.
//
// PRECONDITIONS (documented, not checked): v.hi != 0, and hi128(u) < v so
// that the quotient fits in 128 bits. Divisors that fit in 64 bits must use
// quoRem64 or the reciprocal paths instead.
//
// This is Knuth Algorithm D with 64-bit digits in the style of libdivide's
// divllu (https://ridiculousfish.com/blog/posts/labor-of-division-episode-iv.html):
// normalize the divisor so its top bit is set, then emit one 64-bit quotient
// digit per 3-by-2 division step.
func div256by128(u u256, v u128) (q, r u128) {
	// Normalizing keeps every trial quotient within 2 of the true digit.
	//nolint:gosec // bits.LeadingZeros64 returns 0..63, which always fits uint
	n := uint(bits.LeadingZeros64(v.hi))
	v = v.lsh(n)

	// Dividend shifted left by n. No bits fall off the top: hi128(u) < v
	// implies u < v·2^128 ≤ 2^(256-n). n == 0 needs no branch because Go
	// defines x>>64 == 0.
	var a [4]uint64
	a[0] = u.d0 << n
	a[1] = u.d1<<n | u.d0>>(64-n)
	a[2] = u.d2<<n | u.d1>>(64-n)
	a[3] = u.d3<<n | u.d2>>(64-n)

	// The high quotient digit is nonzero — and its division step needed —
	// only if the dividend reaches v·2^64, i.e. {a3,a2,a1} ≥ {v.hi,v.lo,0}.
	// Skipping on a[2] > v.hi alone (as udecimal does) is wrong when
	// a[2] == v.hi and a[1] ≥ v.lo: the digit would not fit in 64 bits.
	var q1 uint64
	if a[3] != 0 || !less128(u128{hi: a[2], lo: a[1]}, v) {
		var rem u128
		q1, rem = div3by2(a[3], a[2], a[1], v)
		a[2], a[1] = rem.hi, rem.lo
	}

	q0, rem := div3by2(a[2], a[1], a[0], v)

	// Only the remainder carries the normalization scale; shift it back.
	return u128{hi: q1, lo: q0}, rem.rsh(n)
}

// div3by2 divides the three-limb value {u2,u1,u0} by the two-limb divisor v,
// returning the single 64-bit quotient digit and the 128-bit remainder.
//
// PRECONDITIONS: v.hi has its top bit set (normalized), and {u2,u1} < v,
// which guarantees the digit fits in 64 bits.
func div3by2(u2, u1, u0 uint64, v u128) (uint64, u128) {
	// Trial digit from the top two dividend limbs and the high divisor limb;
	// per Knuth (TAOCP 4.3.1, step D3) it never underestimates and
	// overestimates the true digit by at most 2. bits.Div64 traps when
	// u2 ≥ v.hi, so the reachable u2 == v.hi case (the precondition then
	// forces u1 < v.lo) clamps the digit to 2^64-1 instead; rc records
	// bit 64 of that case's partial remainder u1 + v.hi.
	var tq, r, rc uint64
	if u2 == v.hi {
		tq = ^uint64(0)
		r, rc = bits.Add64(u1, v.hi, 0)
	} else {
		tq, r = bits.Div64(u2, u1, v.hi)
	}

	// rem = {rc,r,u0} - tq·v.lo, which equals {u2,u1,u0} - tq·v. The true
	// remainder is < v < 2^128, so whenever rc is set the borrow below is
	// certain and the 128-bit result is already exact.
	c1h, c1l := bits.Mul64(tq, v.lo)
	remLo, borrow := bits.Sub64(u0, c1l, 0)
	remHi, borrow := bits.Sub64(r, c1h, borrow)
	rem := u128{hi: remHi, lo: remLo}

	// A borrow not covered by rc means the trial digit overshot by k = 1 or
	// 2 and rem wrapped: it now holds (true remainder) - k·v mod 2^128.
	// Recover the deficit d = k·v - (true remainder) ∈ (0, 2v] and step the
	// digit down.
	if borrow > rc {
		tq--
		d := neg128(rem)
		if less128(v, d) {
			tq--
			d, _ = sub128(d, v)
		}
		rem, _ = sub128(v, d)
	}

	return tq, rem
}
