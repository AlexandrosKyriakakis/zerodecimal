package zerodecimal

import "math/bits"

// Add returns d + e computed exactly at precision max(d.prec, e.prec).
// ErrOverflow is returned iff the exact coefficient at that precision does
// not fit 128 bits — alignment itself can never fail, and opposite-sign
// operands can never overflow. The dominant same-precision, same-sign case
// is a single 128-bit add; every other combination outlines into addSlow.
func (d Decimal) Add(e Decimal) (Decimal, error) {
	if d.prec == e.prec && d.neg == e.neg {
		coef, carry := add128(d.coef, e.coef)
		if carry != 0 {
			return Decimal{}, ErrOverflow
		}
		// coef can be zero here only when both operands are canonical zeros,
		// so the fields below are canonical without a newDecimal pass.
		return Decimal{coef: coef, neg: d.neg, prec: d.prec}, nil
	}
	return addSlow(d, e, e.neg)
}

// Sub returns d - e computed exactly at precision max(d.prec, e.prec), with
// the same overflow contract as Add. Same-precision operands of opposite
// signs subtract as a magnitude add; everything else delegates to the shared
// signed-add core with the sign of e flipped (a zero e keeps its canonical
// unsigned form, so its sign is never flipped).
func (d Decimal) Sub(e Decimal) (Decimal, error) {
	if d.prec == e.prec && d.neg != e.neg {
		coef, carry := add128(d.coef, e.coef)
		if carry != 0 {
			return Decimal{}, ErrOverflow
		}
		// d.neg != e.neg means at least one operand is nonzero, and a
		// magnitude add of canonical operands with distinct signs cannot
		// produce zero, so the result needs no zero normalization.
		return Decimal{coef: coef, neg: d.neg, prec: d.prec}, nil
	}
	return addSlow(d, e, !e.neg && !e.coef.isZero())
}

// addSlow is the shared signed-add core behind Add and Sub: it returns
// d + (eNeg ? -1 : +1)·|e| at precision max(d.prec, e.prec). eNeg stands in
// for e.neg so Sub can flip the sign of e without materializing a negated
// Decimal. Same-precision opposite signs cost ONE sub128 with a conditional
// two's-complement fix keyed on the borrow — no compare-then-subtract double
// pass — and can never overflow. Results that cancel to zero normalize to
// the canonical Decimal{}.
func addSlow(d, e Decimal, eNeg bool) (Decimal, error) {
	if d.prec == e.prec {
		if d.neg == eNeg {
			coef, carry := add128(d.coef, e.coef)
			if carry != 0 {
				return Decimal{}, ErrOverflow
			}
			return newDecimal(coef, d.neg, d.prec), nil
		}
		diff, borrow := sub128(d.coef, e.coef)
		neg := d.neg
		if borrow != 0 {
			// |e| won: the wrapped difference recovers via two's complement
			// and the result takes e's sign.
			diff = neg128(diff)
			neg = eNeg
		}
		return newDecimal(diff, neg, d.prec), nil
	}
	return addUnaligned(d, e, eNeg)
}

// addUnaligned is the differing-precision arm of addSlow: the lower-precision
// coefficient widens by 10^diff into 192 bits via mul128by64to192, so the
// rescaled value is never materialized in 128 bits and alignment cannot
// overflow. ErrOverflow iff the exact result coefficient at the higher
// precision is ≥ 2^128; a result on the higher-precision operand's side of a
// mixed-sign subtraction always fits, because that coefficient is < 2^128.
//
// PRECONDITION (not checked): d.prec != e.prec, both ≤ MaxPrec.
func addUnaligned(d, e Decimal, eNeg bool) (Decimal, error) {
	lo, hi := d, e
	loNeg, hiNeg := d.neg, eNeg
	if d.prec > e.prec {
		lo, hi = e, d
		loNeg, hiNeg = eNeg, d.neg
	}
	w2, w1, w0 := mul128by64to192(lo.coef, pow10u64[(hi.prec-lo.prec)&31])

	if loNeg == hiNeg {
		// Same sign: 192-bit add of the zero-extended higher-precision
		// coefficient. The sum stays below 2^192 (widened < 10^19·2^128 and
		// 10^19 + 1 < 2^64), so the top limb is exact and must be zero.
		s0, c := bits.Add64(w0, hi.coef.lo, 0)
		s1, c := bits.Add64(w1, hi.coef.hi, c)
		if w2+c != 0 {
			return Decimal{}, ErrOverflow
		}
		return newDecimal(u128{hi: s1, lo: s0}, loNeg, hi.prec), nil
	}

	// Opposite signs: one 192-bit subtract; a borrow means the higher-
	// precision magnitude won and the wrapped difference recovers via a
	// 192-bit two's complement, mirroring the aligned path.
	r0, b := bits.Sub64(w0, hi.coef.lo, 0)
	r1, b := bits.Sub64(w1, hi.coef.hi, b)
	r2, b := bits.Sub64(w2, 0, b)
	neg := loNeg
	if b != 0 {
		neg = hiNeg
		r0, b = bits.Sub64(0, r0, 0)
		r1, b = bits.Sub64(0, r1, b)
		r2, _ = bits.Sub64(0, r2, b)
	}
	if r2 != 0 {
		return Decimal{}, ErrOverflow
	}
	return newDecimal(u128{hi: r1, lo: r0}, neg, hi.prec), nil
}

// Mul returns d × e. The exact product carries d.prec + e.prec fractional
// digits (at most 2·MaxPrec); when that exceeds DefaultPrec the excess
// digits are truncated toward zero, so the result precision is
// min(d.prec + e.prec, DefaultPrec). ErrOverflow iff the truncated
// coefficient does not fit 128 bits. One-limb products that need no rescale
// take a single hardware multiply; everything else outlines into mulSlow.
func (d Decimal) Mul(e Decimal) (Decimal, error) {
	neg := d.neg != e.neg
	pSum := d.prec + e.prec
	if d.coef.hi|e.coef.hi == 0 && pSum <= DefaultPrec {
		hi, lo := bits.Mul64(d.coef.lo, e.coef.lo)
		return newDecimal(u128{hi: hi, lo: lo}, neg, pSum), nil
	}
	return mulSlow(d, e, neg, pSum)
}

// mulSlow is the outlined arm of Mul: full-width products and every product
// that must rescale from pSum down to DefaultPrec fractional digits. The
// rescale truncates toward zero. Overflow detection is exact in both shapes:
// the full product must fit 128 bits when no rescale applies, and
// divU256Pow10 reports overflow iff the truncated quotient is ≥ 2^128.
//
// PRECONDITION (not checked): neg == (d.neg != e.neg), pSum == d.prec + e.prec.
func mulSlow(d, e Decimal, neg bool, pSum uint8) (Decimal, error) {
	if pSum <= DefaultPrec {
		prod := mulToU256(d.coef, e.coef)
		if !prod.isZeroUpper() {
			return Decimal{}, ErrOverflow
		}
		return newDecimal(prod.lo128(), neg, pSum), nil
	}
	k := pSum - DefaultPrec
	if d.coef.hi|e.coef.hi == 0 && k <= MaxPrec {
		// One-limb coefficients with a one-pass rescale: a single multiply
		// and the reciprocal divide; the quotient of a 128-bit product by
		// 10^k (k ≥ 1) always fits, no overflow check needed.
		hi, lo := bits.Mul64(d.coef.lo, e.coef.lo)
		q, _ := divmod128Pow10(u128{hi: hi, lo: lo}, k)
		return newDecimal(q, neg, DefaultPrec), nil
	}
	q, err := divU256Pow10(mulToU256(d.coef, e.coef), k)
	if err != nil {
		return Decimal{}, err
	}
	return newDecimal(q, neg, DefaultPrec), nil
}

// bitlen10 holds bits.Len(10^f) — the minimal bit width of 10^f — for
// f = 0..38, the full range of scale factors Div can apply. It drives the
// adaptive-precision pre-check: together with the coefficient bit lengths it
// bounds the quotient width without performing the division.
var bitlen10 = [39]uint8{
	1, 4, 7, 10, 14, 17, 20, 24, 27, 30,
	34, 37, 40, 44, 47, 50, 54, 57, 60, 64,
	67, 70, 74, 77, 80, 84, 87, 90, 94, 97,
	100, 103, 107, 110, 113, 117, 120, 123, 127,
}

// bitLen128 returns the minimal number of bits to represent u; 0 for u == 0.
func bitLen128(u u128) int {
	if u.hi != 0 {
		return 64 + bits.Len64(u.hi)
	}
	return bits.Len64(u.lo)
}

// Div returns d ÷ e truncated toward zero at adaptive precision: the result
// is trunc(d/e · 10^p) with precision p, for the LARGEST p ≤ DefaultPrec
// whose quotient coefficient fits 128 bits. Exact results therefore keep
// p = DefaultPrec (their trailing zeros are not trimmed), while quotients of
// huge magnitudes degrade precision gracefully instead of failing.
// ErrOverflow only when even the integer quotient (p = 0) does not fit;
// ErrDivideByZero when e is zero. Dividing zero by anything nonzero is Zero.
func (d Decimal) Div(e Decimal) (Decimal, error) {
	if e.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	if d.coef.isZero() {
		return Decimal{}, nil
	}
	neg := d.neg != e.neg

	// Estimate the largest p that provably fits: with f = p + e.prec - d.prec,
	// the quotient is < 2^(bitLen(d.coef) + bitlen10[f] - bitLen(e.coef) + 1),
	// so it fits whenever that exponent is ≤ 128; f ≤ 0 fits trivially
	// (the quotient is at most d.coef). Both disjuncts are monotone in p, so
	// the first hit walking down from DefaultPrec is the largest such p.
	bound := 127 + bitLen128(e.coef) - bitLen128(d.coef)
	p := int(DefaultPrec)
	for p > 0 {
		f := p + int(e.prec) - int(d.prec)
		if f <= 0 || int(bitlen10[f]) <= bound {
			break
		}
		p--
	}

	coef, ok := divCoefAt(d, e, p)
	for !ok && p > 0 {
		// Unreachable when the estimate held (it guarantees a fit for every
		// break except the forced p == 0 floor); kept as a correctness
		// backstop so a pre-check bug degrades precision instead of results.
		p--
		coef, ok = divCoefAt(d, e, p)
	}
	if !ok {
		return Decimal{}, ErrOverflow
	}
	// The pre-check is conservative by at most one digit: probe p+1 once and
	// keep it when it fits, which realizes the largest-p contract with at
	// most one extra division.
	if p < int(DefaultPrec) {
		if c2, ok2 := divCoefAt(d, e, p+1); ok2 {
			return newDecimal(c2, neg, uint8(p)+1), nil
		}
	}
	return newDecimal(coef, neg, uint8(p)), nil
}

// divCoefAt computes trunc(|d| / |e| · 10^p) and reports whether it fits 128
// bits. For a nonnegative scale gap f = p + e.prec - d.prec the numerator
// d.coef·10^f (f ≤ 38) is divided by e.coef; for negative f the divisor
// scales instead — e.coef·10^(-f) with -f ≤ d.prec — and a scaled divisor
// that overflows 128 bits exceeds every possible numerator, making the
// quotient an exact zero.
//
// PRECONDITIONS (not checked): e.coef != 0 and 0 ≤ p ≤ DefaultPrec.
func divCoefAt(d, e Decimal, p int) (u128, bool) {
	f := p + int(e.prec) - int(d.prec)
	if f < 0 {
		den, overflow := mul128by64(e.coef, pow10u64[(-f)&31])
		if overflow != 0 {
			return u128{}, true
		}
		if den.hi == 0 {
			q, _ := quoRem64(d.coef, den.lo)
			return q, true
		}
		// The zero-extended dividend has hi128 == 0 < den, satisfying
		// div256by128's quotient-fits precondition.
		q, _ := div256by128(u256{d0: d.coef.lo, d1: d.coef.hi}, den)
		return q, true
	}
	num := mulToU256(d.coef, pow10u128[f&63])
	if e.coef.hi == 0 {
		q, _, err := div256by64(num, e.coef.lo)
		if err != nil {
			return u128{}, false
		}
		return q, true
	}
	// Exact fits test: the quotient fits 128 bits iff hi128(num) < e.coef.
	if !less128(num.hi128(), e.coef) {
		return u128{}, false
	}
	q, _ := div256by128(num, e.coef)
	return q, true
}

// QuoRem returns the truncated quotient q = trunc(d/e) and the remainder
// r = d - q·e (T-division, matching Go's integer operators and the
// shopspring/udecimal convention). q has precision 0 and the sign of
// d.neg != e.neg; r has precision f = max(d.prec, e.prec), the sign of d,
// and |r| < |e|; the identity d = q·e + r always holds. ErrDivideByZero when
// e is zero; ErrOverflow when the quotient does not fit a 128-bit
// coefficient, or when the divisor aligned to precision f does not.
func (d Decimal) QuoRem(e Decimal) (Decimal, Decimal, error) {
	if e.coef.isZero() {
		return Decimal{}, Decimal{}, ErrDivideByZero
	}
	qNeg := d.neg != e.neg
	if d.prec == e.prec && d.coef.hi|e.coef.hi == 0 {
		// Already-aligned one-limb operands: the scale factors are both 1, so
		// the whole T-division is a single hardware divide — no 256-bit
		// numerator, no reciprocal setup, and no overflow is possible.
		// e.coef.lo != 0 here because the zero check above ruled out hi|lo == 0.
		q := d.coef.lo / e.coef.lo
		r := d.coef.lo - q*e.coef.lo
		return newDecimal(u128{lo: q}, qNeg, 0), newDecimal(u128{lo: r}, d.neg, d.prec), nil
	}
	f := max(d.prec, e.prec)
	num := mulToU256(d.coef, pow10u128[(f-d.prec)&63])
	den, overflow := mul128by64(e.coef, pow10u64[(f-e.prec)&31])
	if overflow != 0 {
		return Decimal{}, Decimal{}, ErrOverflow
	}
	if den.hi == 0 {
		q, r, err := div256by64(num, den.lo)
		if err != nil {
			return Decimal{}, Decimal{}, err
		}
		return newDecimal(q, qNeg, 0), newDecimal(u128{lo: r}, d.neg, f), nil
	}
	// Exact fits test, as in divCoefAt.
	if !less128(num.hi128(), den) {
		return Decimal{}, Decimal{}, ErrOverflow
	}
	q, r := div256by128(num, den)
	return newDecimal(q, qNeg, 0), newDecimal(r, d.neg, f), nil
}

// Mod returns the remainder of QuoRem: d - trunc(d/e)·e, carrying the sign
// of d and precision max(d.prec, e.prec), with the same error contract.
func (d Decimal) Mod(e Decimal) (Decimal, error) {
	_, r, err := d.QuoRem(e)
	return r, err
}

// MustAdd is Add for operands with proven bounds: it panics on error.
func (d Decimal) MustAdd(e Decimal) Decimal {
	r, err := d.Add(e)
	if err != nil {
		panic(err)
	}
	return r
}

// MustSub is Sub for operands with proven bounds: it panics on error.
func (d Decimal) MustSub(e Decimal) Decimal {
	r, err := d.Sub(e)
	if err != nil {
		panic(err)
	}
	return r
}

// MustMul is Mul for operands with proven bounds: it panics on error.
func (d Decimal) MustMul(e Decimal) Decimal {
	r, err := d.Mul(e)
	if err != nil {
		panic(err)
	}
	return r
}

// MustDiv is Div for operands with proven bounds: it panics on error.
func (d Decimal) MustDiv(e Decimal) Decimal {
	r, err := d.Div(e)
	if err != nil {
		panic(err)
	}
	return r
}

// MustQuoRem is QuoRem for operands with proven bounds: it panics on error.
func (d Decimal) MustQuoRem(e Decimal) (Decimal, Decimal) {
	q, r, err := d.QuoRem(e)
	if err != nil {
		panic(err)
	}
	return q, r
}

// MustMod is Mod for operands with proven bounds: it panics on error.
func (d Decimal) MustMod(e Decimal) Decimal {
	r, err := d.Mod(e)
	if err != nil {
		panic(err)
	}
	return r
}

// Min returns the numerically smallest argument (1.5 and 1.50 compare
// equal; the first of equal values wins). It is infallible — comparison
// never overflows.
func Min(first Decimal, rest ...Decimal) Decimal {
	m := first
	for _, d := range rest {
		if d.Cmp(m) < 0 {
			m = d
		}
	}
	return m
}

// Max returns the numerically largest argument (1.5 and 1.50 compare equal;
// the first of equal values wins). It is infallible — comparison never
// overflows.
func Max(first Decimal, rest ...Decimal) Decimal {
	m := first
	for _, d := range rest {
		if d.Cmp(m) > 0 {
			m = d
		}
	}
	return m
}

// Sum returns first + rest[0] + ... + rest[n-1], folding Add left to right,
// and stops with ErrOverflow as soon as a partial sum overflows.
func Sum(first Decimal, rest ...Decimal) (Decimal, error) {
	s := first
	for _, d := range rest {
		var err error
		s, err = s.Add(d)
		if err != nil {
			return Decimal{}, err
		}
	}
	return s, nil
}

// MustSum is Sum for operands with proven bounds: it panics on error.
func MustSum(first Decimal, rest ...Decimal) Decimal {
	s, err := Sum(first, rest...)
	if err != nil {
		panic(err)
	}
	return s
}

// Avg returns the arithmetic mean (first + rest...)/(1 + len(rest)) with
// Div's adaptive precision and error contract; the intermediate Sum can also
// overflow.
func Avg(first Decimal, rest ...Decimal) (Decimal, error) {
	s, err := Sum(first, rest...)
	if err != nil {
		return Decimal{}, err
	}
	return s.Div(NewFromInt(int64(len(rest)) + 1))
}

// MustAvg is Avg for operands with proven bounds: it panics on error.
func MustAvg(first Decimal, rest ...Decimal) Decimal {
	a, err := Avg(first, rest...)
	if err != nil {
		panic(err)
	}
	return a
}
