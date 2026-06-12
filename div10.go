package zerodecimal

import "math/bits"

//go:generate go run ./internal/gen

// pow10Entry packs every precomputed constant needed to divide by d = 10^k
// without a hardware DIV instruction:
//
//   - d: 10^k itself, for remainder reconstruction r = n - q·d.
//   - m: the Granlund-Montgomery-Warren magic ⌈2^(64+p)/5^k⌉ for the odd
//     factor 5^k of 10^k = 2^k·5^k; combined with the 2^k pre-shift it
//     divides any uint64 with one high multiply and no add-back fixup.
//   - p: the post-shift paired with m — the smallest p with 5^k ≤ 2^(p+k).
//     That minimal window is exactly where m fits in 64 bits AND the
//     fixup-free property holds (see divmod64Pow10).
//   - dn: d normalized so its top bit is set (d << s), as the Möller-Granlund
//     steps require.
//   - v: the Möller-Granlund reciprocal ⌊(2^128-1)/dn⌋ - 2^64 consumed by
//     div2by1 for multi-limb dividends.
//   - s: the normalization shift, bits.LeadingZeros64(d).
//
// The trailing padding pins the entry at exactly 32 bytes so two entries
// share a cache line and indexing compiles to a shift, not a multiply.
type pow10Entry struct {
	d, m, dn, v uint64
	p, s        uint8
	_           [6]byte
}

// div2by1 returns the quotient and remainder of the 128-bit dividend
// {u1,u0} divided by dn, replacing the ~18-cycle hardware DIV with two
// multiplies and a handful of ALU ops (Möller-Granlund, "Improved division
// by invariant integers", IEEE Trans. Computers 2011, Algorithm 4).
//
// PRECONDITIONS (not checked): dn has its top bit set (normalized), u1 < dn
// so the quotient fits 64 bits, and v == ⌊(2^128-1)/dn⌋ - 2^64. Under those,
// v itself is computable as bits.Div64(^dn, ^uint64(0), dn) without trapping
// because ^dn < dn whenever dn ≥ 2^63.
func div2by1(u1, u0, dn, v uint64) (q, r uint64) {
	// Candidate quotient {qh,ql} = (2^64+v)·u1 + u0: qh is at most 2 below
	// the true quotient and never above it.
	qh, ql := bits.Mul64(v, u1)
	ql, c := bits.Add64(ql, u0, 0)
	qh, _ = bits.Add64(qh, u1, c)

	// Speculatively step qh up so it is now correct or one too large; the
	// candidate remainder then wraps mod 2^64 exactly when qh overshot,
	// which the r > ql comparison detects (Möller-Granlund Lemma 2).
	qh++
	r = u0 - qh*dn

	// Overshoot correction fires with probability ≈ 1/2 on random input, so
	// it must stay in this branchless-friendly shape (compiles to CMOVs).
	if r > ql {
		qh--
		r += dn
	}
	// Taken with probability ≈ 2^-64; required for exactness, never worth
	// more than a predicted-untaken branch.
	if r >= dn {
		qh++
		r -= dn
	}
	return qh, r
}

// divmod64Pow10 returns q = u / 10^k and r = u % 10^k using one high
// multiply and no hardware DIV. Splitting 10^k = 2^k·5^k, the pre-shift
// u>>k leaves a dividend n2 < 2^(64-k) to divide by the odd 5^k via magic
// m = ⌈2^(64+p)/5^k⌉. With m·5^k = 2^(64+p)+e, 0 < e ≤ 5^k ≤ 2^(p+k), every
// n2 < 2^(64-k) satisfies n2·e < 2^(64+p), so ⌊n2·m/2^(64+p)⌋ == ⌊n2/5^k⌋
// exactly — no add-back fixup needed.
//
// PRECONDITION (not checked): 1 ≤ k ≤ MaxPrec. Callers guarantee it; k == 0
// is short-circuited by divmod128Pow10 before reaching here.
func divmod64Pow10(u uint64, k uint8) (q, r uint64) {
	e := pow10Tab[k&31] // &31 proves the index in range: no bounds check
	q, _ = bits.Mul64(u>>k, e.m)
	q >>= e.p
	r = u - q*e.d
	return q, r
}

// divmod128Pow10 returns q = u / 10^k and r = u % 10^k. The remainder
// always fits uint64 because 10^k < 2^64 for every k ≤ MaxPrec.
//
// Only dispatch lives here; the two-limb division is outlined into
// divmod128Pow10Slow so the cold normalization-and-div2by1 machinery stays
// out of the one-limb path's instruction stream. The dispatcher still cannot
// inline under the compiler's default budget — the inlined divmod64Pow10
// fast path plus one outlined call exceed it before any branches are
// counted — so callers on a measured hot path that already know u.hi == 0
// should invoke divmod64Pow10 directly; it fits the budget on its own.
//
// PRECONDITION (not checked): k ≤ MaxPrec.
func divmod128Pow10(u u128, k uint8) (u128, uint64) {
	// k == 0 must short-circuit: string formatting divides by 10^prec and
	// prec can legitimately be 0, where the table's magic is meaningless.
	if k == 0 {
		return u, 0
	}
	// Dominant case (coefficients fit one limb): single-multiply magic path.
	if u.hi == 0 {
		q, r := divmod64Pow10(u.lo, k)
		return u128{lo: q}, r
	}
	return divmod128Pow10Slow(u, k)
}

// divmod128Pow10Slow returns q = u / 10^k and r = u % 10^k for dividends
// that occupy both limbs, via two Möller-Granlund div2by1 passes over the
// normalized dividend. divmod128Pow10 reaches it only when u.hi != 0, but
// correctness needs nothing beyond the k precondition.
//
// PRECONDITION (not checked): 1 ≤ k ≤ MaxPrec.
func divmod128Pow10Slow(u u128, k uint8) (u128, uint64) {
	e := pow10Tab[k&31]
	s := uint(e.s)
	// Normalize the dividend by s so dn = 10^k<<s has its top bit set.
	// a2 < 2^s ≤ dn keeps the first div2by1 digit within 64 bits, and the
	// complementary shifts make s == 0 (k == 19) branch-free because Go
	// defines x>>64 == 0.
	a2 := u.hi >> (64 - s)
	a1 := u.hi<<s | u.lo>>(64-s)
	a0 := u.lo << s
	q1, r1 := div2by1(a2, a1, e.dn, e.v)
	q0, r0 := div2by1(r1, a0, e.dn, e.v)
	// Only the remainder carries the normalization scale; shift it back.
	return u128{hi: q1, lo: q0}, r0 >> s
}

// divU256Pow10 returns ⌊u / 10^k⌋, or ErrOverflow when that quotient does
// not fit 128 bits. The remainder is discarded: Mul rescaling truncates
// toward zero.
//
// PRECONDITION (not checked): k ≤ 2·MaxPrec. Mul's rescale factor is
// pSum - DefaultPrec ≤ 2·MaxPrec - DefaultPrec, which exceeds MaxPrec only
// under the reduced-precision build tags; that range is served by splitting
// the divisor, never by widening the tables past 10^19.
func divU256Pow10(u u256, k uint8) (u128, error) {
	// k == 0 divides by 1: only the fits-in-128-bits check remains.
	if k == 0 {
		if !u.isZeroUpper() {
			return u128{}, ErrOverflow
		}
		return u.lo128(), nil
	}

	// 10^k for k > MaxPrec exceeds uint64, so peel off a full 10^MaxPrec
	// pass first. Truncating twice is exact, ⌊⌊u/a⌋/b⌋ == ⌊u/(a·b)⌋, and
	// the second pass's overflow pre-check stays the exact test for the
	// combined divisor: ⌊u/10^19⌋ ≥ 10^(k-19)·2^128 iff u ≥ 10^k·2^128.
	// The intermediate quotient may itself exceed 128 bits, hence the
	// full-width helper.
	if k > MaxPrec {
		u = divU256Pow10Wide(u, MaxPrec)
		k -= MaxPrec
	}

	// Quotient ≥ 2^128 iff u ≥ 10^k·2^128, i.e. iff the high 128 bits of u
	// reach 10^k — exact, and it implies d3 == 0 for the division below.
	if u.d3 != 0 || u.d2 >= pow10u64[k&31] {
		return u128{}, ErrOverflow
	}

	e := pow10Tab[k&31]
	s := uint(e.s)
	// Normalize the three live limbs. The top normalized limb d2>>(64-s) is
	// provably zero (d2 < 10^k < 2^(64-s)), so the leading div2by1 step
	// would return digit 0 with remainder n2 — it is skipped. n2 < dn holds
	// because d2<<s ≤ dn-2^s and d1>>(64-s) ≤ 2^s-1 sum to at most dn-1.
	n2 := u.d2<<s | u.d1>>(64-s)
	n1 := u.d1<<s | u.d0>>(64-s)
	n0 := u.d0 << s
	q1, r1 := div2by1(n2, n1, e.dn, e.v)
	q0, _ := div2by1(r1, n0, e.dn, e.v)
	return u128{hi: q1, lo: q0}, nil
}

// divU256Pow10Wide returns the full-width truncating quotient ⌊u / 10^k⌋.
// It serves the k > MaxPrec passes of divU256Pow10, whose intermediate
// quotient can exceed 128 bits, so no overflow check applies here.
//
// PRECONDITION (not checked): 1 ≤ k ≤ MaxPrec.
func divU256Pow10Wide(u u256, k uint8) u256 {
	e := pow10Tab[k&31]
	s := uint(e.s)
	// u<<s spans five limbs; the top one n4 < 2^s ≤ dn keeps every div2by1
	// digit within 64 bits, exactly as in divmod128Pow10.
	n4 := u.d3 >> (64 - s)
	n3 := u.d3<<s | u.d2>>(64-s)
	n2 := u.d2<<s | u.d1>>(64-s)
	n1 := u.d1<<s | u.d0>>(64-s)
	n0 := u.d0 << s
	q3, r := div2by1(n4, n3, e.dn, e.v)
	q2, r := div2by1(r, n2, e.dn, e.v)
	q1, r := div2by1(r, n1, e.dn, e.v)
	q0, _ := div2by1(r, n0, e.dn, e.v)
	return u256{d0: q0, d1: q1, d2: q2, d3: q3}
}

// div256by64 returns q = ⌊u/v⌋ and r = u mod v for an ARBITRARY 64-bit
// divisor (Div's fast path when the divisor coefficient fits one limb).
// The single hardware DIV computes the reciprocal; both quotient limbs then
// come from div2by1, instead of two to three dependent hardware DIVs.
func div256by64(u u256, v uint64) (u128, uint64, error) {
	if v == 0 {
		return u128{}, 0, ErrDivideByZero
	}
	// Quotient fits 128 bits iff u < v·2^128, i.e. iff the high 128 bits of
	// u stay below v — exact, and it implies d3 == 0 for the division below.
	if u.d3 != 0 || u.d2 >= v {
		return u128{}, 0, ErrOverflow
	}

	//nolint:gosec // bits.LeadingZeros64 returns 0..63, which always fits uint
	s := uint(bits.LeadingZeros64(v))
	dn := v << s
	// ^dn < dn because dn has its top bit set, so bits.Div64 cannot trap.
	recip, _ := bits.Div64(^dn, ^uint64(0), dn)

	// Same shape as divU256Pow10: the top normalized limb d2>>(64-s) is
	// provably zero (d2 < v < 2^(64-s)) and n2 ≤ dn-1, so two div2by1 steps
	// produce the full quotient.
	n2 := u.d2<<s | u.d1>>(64-s)
	n1 := u.d1<<s | u.d0>>(64-s)
	n0 := u.d0 << s
	q1, r1 := div2by1(n2, n1, dn, recip)
	q0, r0 := div2by1(r1, n0, dn, recip)
	return u128{hi: q1, lo: q0}, r0 >> s, nil
}
