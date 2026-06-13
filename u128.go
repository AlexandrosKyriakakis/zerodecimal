package zerodecimal

import "math/bits"

// u128 is a 128-bit unsigned integer: value = hi·2^64 + lo.
//
// Every primitive below returns raw carry/borrow/overflow words instead of
// errors: only the Decimal layer knows whether a wrapped result means
// overflow, a sign flip, or nothing at all. All of these functions must stay
// within the compiler's inlining budget — they sit on every hot path.
type u128 struct {
	hi, lo uint64
}

// isZero reports whether u == 0.
func (u u128) isZero() bool {
	return u.hi|u.lo == 0
}

// eq reports whether u == v.
func (u u128) eq(v u128) bool {
	return u == v
}

// less128 reports whether u < v. The borrow bit of a full 128-bit subtract
// keeps it branch-free, unlike a hi/lo comparison ladder.
func less128(u, v u128) bool {
	_, borrow := bits.Sub64(u.lo, v.lo, 0)
	_, borrow = bits.Sub64(u.hi, v.hi, borrow)
	return borrow != 0
}

// cmp128 returns -1 if u < v, 0 if u == v, and +1 if u > v.
func cmp128(u, v u128) int {
	if u == v {
		return 0
	}
	if less128(u, v) {
		return -1
	}
	return 1
}

// cmp128i returns -1 if u < v, 0 if u == v, and +1 if u > v using two
// full-width subtracts: borrow of u-v (b1) flags u<v, borrow of v-u (b2)
// flags u>v, and b2-b1 collapses to exactly -1/0/+1 with no compare ladder
// or branch. Faster than cmp128's eq-then-less two-pass on the hot path.
func cmp128i(u, v u128) int {
	_, b1 := bits.Sub64(u.lo, v.lo, 0)
	_, b1 = bits.Sub64(u.hi, v.hi, b1)
	_, b2 := bits.Sub64(v.lo, u.lo, 0)
	_, b2 = bits.Sub64(v.hi, u.hi, b2)
	//nolint:gosec // b1,b2 are 0/1 borrow words; the difference is in [-1,1].
	return int(b2) - int(b1)
}

// add128 returns u+v mod 2^128 and the carry out of bit 127 (0 or 1).
func add128(u, v u128) (u128, uint64) {
	lo, carry := bits.Add64(u.lo, v.lo, 0)
	hi, carry := bits.Add64(u.hi, v.hi, carry)
	return u128{hi: hi, lo: lo}, carry
}

// sub128 returns u-v mod 2^128 and the borrow out of bit 127 (0 or 1);
// borrow 1 means u < v and the difference wrapped.
func sub128(u, v u128) (u128, uint64) {
	lo, borrow := bits.Sub64(u.lo, v.lo, 0)
	hi, borrow := bits.Sub64(u.hi, v.hi, borrow)
	return u128{hi: hi, lo: lo}, borrow
}

// neg128 returns the two's complement (2^128 - u) mod 2^128, so that
// neg128 of a wrapped sub128 result recovers the absolute difference.
func neg128(u u128) u128 {
	lo, borrow := bits.Sub64(0, u.lo, 0)
	hi, _ := bits.Sub64(0, u.hi, borrow)
	return u128{hi: hi, lo: lo}
}

// inc128 returns u+1 mod 2^128. Callers use it only where wrap-around is
// impossible by construction (e.g. rounding up a quotient already divided
// by a power of ten).
func inc128(u u128) u128 {
	lo, carry := bits.Add64(u.lo, 1, 0)
	return u128{hi: u.hi + carry, lo: lo}
}

// mul128by64 returns u·v mod 2^128 and the overflow word: bits 128..191 of
// the full product. One word always suffices because u·v < 2^192.
func mul128by64(u u128, v uint64) (u128, uint64) {
	d2, d1, d0 := mul128by64to192(u, v)
	return u128{hi: d1, lo: d0}, d2
}

// mul128by64to192 returns the full 192-bit product u·v as three little-endian
// 64-bit words. The top word cannot wrap: the high word of u.hi·v is at most
// 2^64-2 and the carry folded into it is at most 1.
func mul128by64to192(u u128, v uint64) (d2, d1, d0 uint64) {
	p1, p0 := bits.Mul64(u.lo, v)
	d2, m := bits.Mul64(u.hi, v)
	d1, carry := bits.Add64(p1, m, 0)
	return d2 + carry, d1, p0
}

// lsh returns u<<n.
//
// PRECONDITION: n < 64. The complementary shift lo>>(64-n) makes n == 0 fall
// out branch-free because Go defines x>>64 == 0.
func (u u128) lsh(n uint) u128 {
	return u128{
		hi: u.hi<<n | u.lo>>(64-n),
		lo: u.lo << n,
	}
}

// rsh returns u>>n.
//
// PRECONDITION: n < 64 (see lsh).
func (u u128) rsh(n uint) u128 {
	return u128{
		hi: u.hi >> n,
		lo: u.lo>>n | u.hi<<(64-n),
	}
}

// quoRem64 returns q = u/v and r = u%v via two hardware divisions. The
// quotient always fits because q ≤ u, and the second bits.Div64 cannot trap
// on quotient overflow because its high word u.hi%v is < v.
//
// PRECONDITION: v != 0.
//
// This is the fallback for ARBITRARY divisors only; division by a power of
// ten always goes through the precomputed-reciprocal paths instead.
func quoRem64(u u128, v uint64) (u128, uint64) {
	qhi := u.hi / v
	qlo, r := bits.Div64(u.hi%v, u.lo, v)
	return u128{hi: qhi, lo: qlo}, r
}
