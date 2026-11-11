package zerodecimal

import "math/bits"

// Divisor is a nonzero divisor with its normalization and reciprocal
// precomputed, so repeated divisions by the same value replace each 128/64-bit
// divide with multiplications. The zero Divisor (and a nil *Divisor) is
// invalid: every method given one returns ErrDivideByZero. A Divisor is
// immutable after construction and safe for concurrent use.
type Divisor struct {
	// e is the divisor exactly as given to NewDivisor. Decimal returns it,
	// and every cold path hands it to Div or DivRound, which makes those
	// paths identical by construction. A zero e.coef marks the Divisor
	// invalid, so the zero Divisor is invalid with no extra state.
	e Decimal
	// dn is e.coef shifted left by s until its top bit is set: the
	// normalized divisor. A one-limb coefficient keeps it in dn.lo with dn.hi
	// zero; a two-limb one spans both limbs.
	dn u128
	// recip is the reciprocal of dn in Möller and Granlund's sense:
	// ⌊(2^128-1)/dn.lo⌋ - 2^64 for a one-limb coefficient (div2by1's v) and
	// ⌊(2^192-1)/dn⌋ - 2^64 for a two-limb one (div3by2Recip's v).
	recip uint64
	// s is the normalization shift, 0..63.
	s uint8
}

// NewDivisor precomputes e for repeated division. ErrDivideByZero when e is zero.
func NewDivisor(e Decimal) (Divisor, error) {
	if e.coef.isZero() {
		return Divisor{}, ErrDivideByZero
	}
	// Each arm builds the result in its return statement, which keeps it in
	// registers; a named Divisor filled field by field went through memory.
	if e.coef.hi == 0 {
		//nolint:gosec // bits.LeadingZeros64 returns 0..63 for a nonzero limb, which always fits uint8
		s := uint8(bits.LeadingZeros64(e.coef.lo))
		dn := e.coef.lo << s
		// div256by64's reciprocal: ^dn < dn because dn has its top bit set,
		// so bits.Div64 cannot trap.
		recip, _ := bits.Div64(^dn, ^uint64(0), dn)
		return Divisor{e: e, dn: u128{lo: dn}, recip: recip, s: s}, nil
	}
	//nolint:gosec // bits.LeadingZeros64 returns 0..63 for a nonzero limb, which always fits uint8
	s := uint8(bits.LeadingZeros64(e.coef.hi))
	dn := e.coef.lsh(uint(s))
	return Divisor{e: e, dn: dn, recip: reciprocal3by2(dn), s: s}, nil
}

// MustNewDivisor is NewDivisor for a divisor known to be nonzero: it panics on error.
func MustNewDivisor(e Decimal) Divisor {
	v, err := NewDivisor(e)
	if err != nil {
		panic(err)
	}
	return v
}

// Decimal returns the divisor exactly as passed to NewDivisor (Decimal{} for an invalid Divisor).
func (v *Divisor) Decimal() Decimal {
	if v == nil {
		return Decimal{}
	}
	return v.e
}

// DivBy returns exactly d.Div(v.Decimal()): the same value, precision and error.
//
// It runs Div's fast path, the quotient at DefaultPrec, on v's reciprocal,
// except that a one-limb scaled dividend over a one-limb divisor takes Div's
// own single divide. When that quotient does not fit 128 bits, and under the
// reduced-precision build tags when the divisor rather than the dividend
// would scale, it returns d.Div(v.Decimal()) itself.
func (d Decimal) DivBy(v *Divisor) (Decimal, error) {
	if v == nil || v.e.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	if d.coef.isZero() {
		return Decimal{}, nil
	}
	// divCoefAt at p = DefaultPrec: the quotient is ⌊d.coef·10^f / c⌋ for
	// v's coefficient c. Shapes that need a call go to divByWide in tail
	// position, so this path keeps its operands in registers: with calls
	// below, the compiler spilled them on every path.
	f := int(DefaultPrec) + int(v.e.prec) - int(d.prec)
	if v.e.coef.hi != 0 || f < 0 || (d.coef.hi != 0 && f > int(MaxPrec)) {
		return d.divByWide(v, f)
	}
	// A one-limb c, and a scaled dividend {n2,n1,n0} within three limbs.
	c := v.e.coef.lo
	var n2, n1, n0 uint64
	switch {
	case d.coef.hi == 0 && f <= int(MaxPrec):
		n1, n0 = bits.Mul64(d.coef.lo, pow10u64[f&31])
		if n1 == 0 {
			// One limb over one limb: a single hardware divide, as Div does;
			// it measured faster than a div2by1 step on arm64.
			return newDecimal(u128{lo: n0 / c}, d.neg != v.e.neg, DefaultPrec), nil
		}
	case d.coef.hi == 0:
		n2, n1, n0 = mul128by64to192(pow10u128[f&63], d.coef.lo)
	default:
		n2, n1, n0 = mul128by64to192(d.coef, pow10u64[f&31])
	}
	// The quotient fits 128 bits iff n2 < c (div256by64's test); otherwise
	// Div degrades the precision until it fits, or reports ErrOverflow.
	if n2 >= c {
		return d.Div(v.e)
	}
	// quoRem's one-limb arm without the remainder (see there for why the
	// shifted numerator and each digit fit), inlined: the call measured
	// 12-20% of DivBy on the one-limb rows.
	s := uint(v.s)
	m2 := n2<<s | n1>>(64-s)
	m1 := n1<<s | n0>>(64-s)
	m0 := n0 << s
	var q1 uint64
	if n2 != 0 || n1 >= c {
		q1, m1 = div2by1(m2, m1, v.dn.lo, v.recip)
	}
	q0, _ := div2by1(m1, m0, v.dn.lo, v.recip)
	return newDecimal(u128{hi: q1, lo: q0}, d.neg != v.e.neg, DefaultPrec), nil
}

// divByWide is DivBy for a two-limb divisor, a scaled dividend past three
// limbs, or a negative scale gap f (reachable only when DefaultPrec <
// MaxPrec, and left to Div): quoRem divides d.coef·10^f, and Div itself
// handles a quotient past 128 bits.
func (d Decimal) divByWide(v *Divisor, f int) (Decimal, error) {
	if f >= 0 {
		if q, _, fits := v.quoRem(mulToU256(d.coef, pow10u128[f&63])); fits {
			return newDecimal(q, d.neg != v.e.neg, DefaultPrec), nil
		}
	}
	return d.Div(v.e)
}

// MustDivBy is DivBy for operands with proven bounds: it panics on error.
func (d Decimal) MustDivBy(v *Divisor) Decimal {
	r, err := d.DivBy(v)
	if err != nil {
		panic(err)
	}
	return r
}

// DivRoundBy returns exactly d.DivRound(v.Decimal(), places, mode), including
// its validation order (places, then mode, then the divisor).
//
// A nonnegative scale gap places + v.Decimal().Prec() - d.Prec() scales the
// dividend, which is divided with v's reciprocal, except that a one-limb
// scaled dividend over a one-limb divisor takes DivRound's own single
// divide. A negative gap would scale the divisor instead, so it returns
// d.DivRound(v.Decimal(), places, mode) itself, as it does for a quotient
// past 128 bits (ErrOverflow).
func (d Decimal) DivRoundBy(v *Divisor, places uint8, mode RoundingMode) (Decimal, error) {
	if places > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	if v == nil || v.e.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	if d.coef.isZero() {
		return Decimal{}, nil
	}
	// The quotient is ⌊d.coef·10^f / c⌋ for v's coefficient c. As in DivBy,
	// shapes that need a call go to divRoundByWide in tail position.
	f := int(places) + int(v.e.prec) - int(d.prec)
	if v.e.coef.hi != 0 || f < 0 || (d.coef.hi != 0 && f > int(MaxPrec)) {
		return d.divRoundByWide(v, places, mode, f)
	}
	// A one-limb c, and a scaled dividend {n2,n1,n0} within three limbs.
	neg := d.neg != v.e.neg
	c := v.e.coef.lo
	var n2, n1, n0 uint64
	switch {
	case d.coef.hi == 0 && f <= int(MaxPrec):
		n1, n0 = bits.Mul64(d.coef.lo, pow10u64[f&31])
		if n1 == 0 {
			// DivRound's narrow path as it is: one hardware divide measured
			// faster than a div2by1 step for a one-limb dividend on arm64. A
			// nonzero remainder means a divisor of at least 2, so q+1 cannot
			// wrap.
			q, r := n0/c, n0%c
			if roundsUp(mode, r != 0, cmpHalf64(r, c), q&1 != 0, neg) {
				q++
			}
			return newDecimal(u128{lo: q}, neg, places), nil
		}
	case d.coef.hi == 0:
		n2, n1, n0 = mul128by64to192(pow10u128[f&63], d.coef.lo)
	default:
		n2, n1, n0 = mul128by64to192(d.coef, pow10u64[f&31])
	}
	// The quotient fits 128 bits iff n2 < c; otherwise DivRound reports
	// ErrOverflow.
	if n2 >= c {
		return d.DivRound(v.e, places, mode)
	}
	// quoRem's one-limb arm, inlined as in DivBy.
	s := uint(v.s)
	m2 := n2<<s | n1>>(64-s)
	m1 := n1<<s | n0>>(64-s)
	m0 := n0 << s
	var q1 uint64
	if n2 != 0 || n1 >= c {
		q1, m1 = div2by1(m2, m1, v.dn.lo, v.recip)
	}
	q0, r0 := div2by1(m1, m0, v.dn.lo, v.recip)
	r := r0 >> s
	q := u128{hi: q1, lo: q0}
	if roundsUp(mode, r != 0, cmpHalf64(r, c), q0&1 != 0, neg) {
		if q == (u128{hi: ^uint64(0), lo: ^uint64(0)}) {
			return Decimal{}, ErrOverflow
		}
		q = inc128(q)
	}
	return newDecimal(q, neg, places), nil
}

// divRoundByWide is DivRoundBy for a two-limb divisor, a scaled dividend
// past three limbs, or a negative scale gap f, which DivRound itself
// handles: it scales the divisor instead. divRound divides d.coef·10^f, and
// DivRound reports a quotient past 128 bits (ErrOverflow).
func (d Decimal) divRoundByWide(v *Divisor, places uint8, mode RoundingMode, f int) (Decimal, error) {
	if f < 0 {
		return d.DivRound(v.e, places, mode)
	}
	q, rem, halfCmp, fits := v.divRound(mulToU256(d.coef, pow10u128[f&63]))
	if !fits {
		return d.DivRound(v.e, places, mode)
	}
	neg := d.neg != v.e.neg
	if roundsUp(mode, rem, halfCmp, q.lo&1 != 0, neg) {
		if q == (u128{hi: ^uint64(0), lo: ^uint64(0)}) {
			return Decimal{}, ErrOverflow
		}
		q = inc128(q)
	}
	return newDecimal(q, neg, places), nil
}

// divRound is divRoundWide(num, c) for v's coefficient c, on v's reciprocal:
// the truncated quotient q = ⌊num/c⌋, whether the remainder num mod c is
// nonzero, halfCmp = sign(2·(num mod c) - c), and whether q fits 128 bits.
// q, rem and halfCmp are meaningful only when it does; they then round
// exactly as divRoundWide's results do in roundQuotient: roundsUp decides,
// and stepping up q = 2^128-1 is ErrOverflow. Any numerator width is
// accepted (see quoRem), so a caller that scales a dividend or a product up
// to 256 bits can use it as a drop-in for divRoundWide.
//
// PRECONDITION (not checked): v is valid (v.e.coef != 0).
func (v *Divisor) divRound(num u256) (q u128, rem bool, halfCmp int, fits bool) {
	var r u128
	if q, r, fits = v.quoRem(num); !fits {
		return u128{}, false, 0, false
	}
	// divRoundWide's two facts about r < c. A one-limb c compares without
	// doubling (cmpHalf64). A two-limb one doubles r as cmpDouble128 does,
	// written out because cmpDouble128 sits just over the inline budget and
	// its frame measured 4% on the two-limb DivRoundBy row: a carry out of
	// 2·r already exceeds c.
	c := v.e.coef
	if c.hi == 0 {
		return q, r.lo != 0, cmpHalf64(r.lo, c.lo), true
	}
	lo, carry := bits.Add64(r.lo, r.lo, 0)
	hi, carry := bits.Add64(r.hi, r.hi, carry)
	if carry != 0 {
		return q, true, 1, true
	}
	return q, !r.isZero(), cmp128(u128{hi: hi, lo: lo}, c), true
}

// quoRem returns q = ⌊num/c⌋ and r = num mod c for v's coefficient c, and
// whether q fits 128 bits; q and r are meaningful only when it does. It is
// the division of divCoefAt and divRoundWide on v's precomputed
// normalization and reciprocal, with no divide instruction:
//
//   - one-limb c: div256by64 without its per-call reciprocal divide,
//     skipping the high quotient digit when it is zero;
//   - two-limb c: div256by128 with div3by2Recip in place of div3by2.
//
// Any numerator width is accepted. divRound derives divRoundWide's rounding
// facts from q and r. DivBy and DivRoundBy inline the one-limb arm for
// numerators within three limbs, and divide a one-limb numerator over a
// one-limb c with a hardware divide, which is faster on arm64.
//
// PRECONDITION (not checked): v is valid (v.e.coef != 0).
func (v *Divisor) quoRem(num u256) (q, r u128, fits bool) {
	s := uint(v.s)
	if c := v.e.coef.lo; v.e.coef.hi == 0 {
		// The quotient fits 128 bits iff num < c·2^128: div256by64's test.
		if num.d3 != 0 || num.d2 >= c {
			return u128{}, u128{}, false
		}
		// Then num<<s < c·2^(128+s) < 2^192 keeps the normalized numerator
		// within three limbs, and n2 < dn bounds the leading digit to one
		// limb (see div256by64). Go defines x>>64 == 0, so s == 0 needs no
		// branch.
		n2 := num.d2<<s | num.d1>>(64-s)
		n1 := num.d1<<s | num.d0>>(64-s)
		n0 := num.d0 << s
		// The high digit ⌊{d2,d1}/c⌋ is zero, and its step skipped, iff
		// d2 = 0 and d1 < c; then n2 = 0 and n1 < dn already.
		var q1 uint64
		if num.d2 != 0 || num.d1 >= c {
			q1, n1 = div2by1(n2, n1, v.dn.lo, v.recip)
		}
		q0, r0 := div2by1(n1, n0, v.dn.lo, v.recip)
		// Only the remainder carries the normalization scale.
		return u128{hi: q1, lo: q0}, u128{lo: r0 >> s}, true
	}
	// The quotient fits 128 bits iff hi128(num) < c: div256by128's
	// precondition, and divCoefAt's exact test.
	if !less128(num.hi128(), v.e.coef) {
		return u128{}, u128{}, false
	}
	// Then num<<s loses no bits: num < c·2^128 ≤ 2^(256-s).
	a3 := num.d3<<s | num.d2>>(64-s)
	a2 := num.d2<<s | num.d1>>(64-s)
	a1 := num.d1<<s | num.d0>>(64-s)
	a0 := num.d0 << s
	// The high digit is nonzero, and its step needed, only if
	// {a3,a2,a1} ≥ dn·2^64 (see div256by128).
	var q1 uint64
	if a3 != 0 || !less128(u128{hi: a2, lo: a1}, v.dn) {
		var rem u128
		q1, rem = div3by2Recip(a3, a2, a1, v.dn, v.recip)
		a2, a1 = rem.hi, rem.lo
	}
	q0, rem := div3by2Recip(a2, a1, a0, v.dn, v.recip)
	// Only the remainder carries the normalization scale.
	return u128{hi: q1, lo: q0}, rem.rsh(s), true
}

// reciprocal3by2 returns ⌊(2^192-1)/d⌋ - 2^64, the reciprocal div3by2Recip
// consumes. It implements Algorithm 6 of Möller and Granlund, "Improved
// division by invariant integers" (IEEE Trans. Computers 60(2), 2011), step
// for step: the 2-by-1 reciprocal of d.hi, computed as div256by64 computes
// its own, then corrected by at most four decrements for d.lo.
//
// PRECONDITION (not checked): d.hi has its top bit set (normalized).
func reciprocal3by2(d u128) uint64 {
	// ⌊(2^128-1)/d.hi⌋ - 2^64; ^d.hi < d.hi, so bits.Div64 cannot trap.
	v, _ := bits.Div64(^d.hi, ^uint64(0), d.hi)
	// p is the low limb of (2^64+v)·d.hi + d.lo; a carry out of it means
	// (2^64+v)·d exceeds 2^192 - 1, and v steps down by one or two.
	p := d.hi*v + d.lo
	if p < d.lo {
		v--
		if p >= d.hi {
			v--
			p -= d.hi
		}
		p -= d.hi
	}
	// Fold in v·d.lo the same way: each carry is one more decrement.
	t1, t0 := bits.Mul64(v, d.lo)
	p += t1
	if p < t1 {
		v--
		if p > d.hi || (p == d.hi && t0 >= d.lo) {
			v--
		}
	}
	return v
}

// div3by2Recip divides the three-limb value {u2,u1,u0} by the normalized
// two-limb divisor d, returning the single 64-bit quotient digit and the
// 128-bit remainder: div3by2 with the digit taken from a precomputed
// reciprocal instead of a 128/64 divide. It implements Algorithm 5 of Möller
// and Granlund (see reciprocal3by2), step for step.
//
// PRECONDITIONS (not checked): d.hi has its top bit set, {u2,u1} < d, and
// v == reciprocal3by2(d).
func div3by2Recip(u2, u1, u0 uint64, d u128, v uint64) (uint64, u128) {
	// Candidate digit: the high limb q of (2^64+v)·u2 + u1, whose low limb q0
	// steers the correction below. q+1 is the true digit, one too large, or
	// (rarely) one too small.
	q, q0 := bits.Mul64(v, u2)
	q0, c := bits.Add64(q0, u1, 0)
	q, _ = bits.Add64(q, u2, c)

	// {r1,r0} = {u1,u0} - q·d - d mod 2^128: the remainder for digit q+1.
	r1 := u1 - d.hi*q
	t1, t0 := bits.Mul64(d.lo, q)
	r0, b := bits.Sub64(u0, t0, 0)
	r1, _ = bits.Sub64(r1, t1, b)
	r0, b = bits.Sub64(r0, d.lo, 0)
	r1, _ = bits.Sub64(r1, d.hi, b)
	q++

	// r1 ≥ q0 exactly when q+1 overshot and the remainder wrapped.
	if r1 >= q0 {
		q--
		r0, c = bits.Add64(r0, d.lo, 0)
		r1, _ = bits.Add64(r1, d.hi, c)
	}
	// Rarely the digit is still one too small.
	if r1 > d.hi || (r1 == d.hi && r0 >= d.lo) {
		q++
		r0, b = bits.Sub64(r0, d.lo, 0)
		r1, _ = bits.Sub64(r1, d.hi, b)
	}
	return q, u128{hi: r1, lo: r0}
}
