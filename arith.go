package zerodecimal

import "math/bits"

// Add returns d + e computed exactly at precision max(d.prec, e.prec).
// ErrOverflow is returned iff the exact coefficient at that precision does
// not fit 128 bits — alignment itself can never fail, and opposite-sign
// operands can never overflow. Both same-precision cases run inline: same
// signs add as a single 128-bit magnitude add, opposite signs subtract as a
// single 128-bit magnitude subtract with a conditional two's-complement fix
// keyed on the borrow. Only differing precisions outline, straight into
// addUnaligned.
func (d Decimal) Add(e Decimal) (Decimal, error) {
	if d.prec == e.prec {
		if d.neg == e.neg {
			if d.coef.hi|e.coef.hi == 0 {
				// One-limb operands: a single Add64 whose carry becomes the hi
				// limb can never overflow 128 bits, so the ErrOverflow branch and
				// the serial add128 carry chain drop off this dominant path. coef
				// can be zero here only when both operands are canonical zeros, so
				// the fields below are canonical without a newDecimal pass.
				lo, c := bits.Add64(d.coef.lo, e.coef.lo, 0)
				return Decimal{coef: u128{hi: c, lo: lo}, neg: d.neg, prec: d.prec}, nil
			}
			coef, carry := add128(d.coef, e.coef)
			if carry != 0 {
				return Decimal{}, ErrOverflow
			}
			// coef can be zero here only when both operands are canonical zeros,
			// so the fields below are canonical without a newDecimal pass.
			return Decimal{coef: coef, neg: d.neg, prec: d.prec}, nil
		}
		// Opposite signs: |d| - |e| as one magnitude subtract. A borrow means
		// |e| won, so the wrapped difference recovers via two's complement and
		// the result takes e's sign. A magnitude subtract never overflows, and
		// newDecimal normalizes the d == -e cancel-to-zero result to the
		// canonical Decimal{} (the raw-literal shortcut of the same-sign arm
		// does not apply once cancellation is possible).
		diff, borrow := sub128(d.coef, e.coef)
		neg := d.neg
		if borrow != 0 {
			diff = neg128(diff)
			neg = e.neg
		}
		return newDecimal(diff, neg, d.prec), nil
	}
	return addUnaligned(d, e, e.neg)
}

// Sub returns d - e computed exactly at precision max(d.prec, e.prec), with
// the same overflow contract as Add. Both same-precision cases run inline:
// opposite signs subtract as a magnitude add, same signs as a single 128-bit
// magnitude subtract with a conditional two's-complement fix keyed on the
// borrow. Only differing precisions outline, straight into addUnaligned (the
// shared differing-precision arm) with the sign of e flipped — a zero e keeps
// its canonical unsigned form, so its sign is never flipped.
func (d Decimal) Sub(e Decimal) (Decimal, error) {
	if d.prec == e.prec {
		if d.neg != e.neg {
			coef, carry := add128(d.coef, e.coef)
			if carry != 0 {
				return Decimal{}, ErrOverflow
			}
			// d.neg != e.neg means at least one operand is nonzero, and a
			// magnitude add of canonical operands with distinct signs cannot
			// produce zero, so the result needs no zero normalization.
			return Decimal{coef: coef, neg: d.neg, prec: d.prec}, nil
		}
		// Same sign: |d| - |e| as one magnitude subtract. A borrow means |e|
		// won, so the wrapped difference recovers via two's complement and the
		// result takes the opposite sign. A magnitude subtract never overflows;
		// e == 0 can never borrow, so the sign flip is safe, and newDecimal
		// normalizes the d == e cancel-to-zero result to the canonical Decimal{}.
		diff, borrow := sub128(d.coef, e.coef)
		neg := d.neg
		if borrow != 0 {
			diff = neg128(diff)
			neg = !d.neg
		}
		return newDecimal(diff, neg, d.prec), nil
	}
	return addUnaligned(d, e, !e.neg && !e.coef.isZero())
}

// addUnaligned is the differing-precision arm shared by Add and Sub: the
// lower-precision coefficient widens by 10^diff into 192 bits via
// mul128by64to192, so the rescaled value is never materialized in 128 bits and
// alignment cannot overflow. ErrOverflow iff the exact result coefficient at
// the higher precision is ≥ 2^128; a result on the higher-precision operand's
// side of a mixed-sign subtraction always fits, because that coefficient is
// < 2^128. eNeg stands in for the (possibly flipped, for Sub) sign of e so a
// caller need not materialize a negated Decimal.
//
// PRECONDITION (not checked): d.prec != e.prec, both ≤ MaxPrec. Both call
// sites are guarded by a prec-equality test that satisfies this.
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
//
// Mul is the legacy compatibility operation: discarded digits are not
// reported. Use MulExact when loss must be an error or MulRound to round once
// from the full product to an explicit scale.
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
// ErrDivideByZero when e is zero. Dividing zero by anything nonzero returns
// canonical Decimal{}.
//
// Div is the legacy compatibility operation: a discarded remainder is not
// reported. Use DivExact when loss must be an error or DivRound to round once
// from the full quotient and remainder to an explicit scale.
//
// The common case is fast-pathed: p = DefaultPrec is the maximum precision, so
// when its quotient already fits 128 bits (the overwhelmingly common shape) it
// trivially realizes the largest-p contract and Div returns immediately,
// skipping the bound estimate, descent loop and p+1 probe entirely. Those run
// only on the degradation path, when even p = DefaultPrec overflows.
func (d Decimal) Div(e Decimal) (Decimal, error) {
	if e.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	if d.coef.isZero() {
		return Decimal{}, nil
	}
	neg := d.neg != e.neg

	// Fast path: p = DefaultPrec is the largest p the contract allows, so a fit
	// there is immediately the answer — no estimate needed.
	if coef, ok := divCoefAt(d, e, int(DefaultPrec)); ok {
		return newDecimal(coef, neg, DefaultPrec), nil
	}

	// Degradation path: p = DefaultPrec overflowed, so find the largest fitting
	// p < DefaultPrec. Estimate it: with f = p + e.prec - d.prec, the quotient
	// is < 2^(bitLen(d.coef) + bitlen10[f] - bitLen(e.coef) + 1), so it fits
	// whenever that exponent is ≤ 128; f ≤ 0 fits trivially (the quotient is at
	// most d.coef). Both disjuncts are monotone in p, so the first hit walking
	// down is the largest such p. Start the descent at DefaultPrec-1: both break
	// disjuncts at p = DefaultPrec imply a fit there, contradicting the failed
	// fast-path attempt, so p = DefaultPrec can never be the chosen estimate.
	bound := 127 + bitLen128(e.coef) - bitLen128(d.coef)
	p := int(DefaultPrec) - 1
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
	// keep it when it fits, which realizes the largest-p contract with at most
	// one extra division. Guard with p+1 < DefaultPrec: p+1 == DefaultPrec is
	// already known to overflow from the failed fast-path attempt.
	if p+1 < int(DefaultPrec) {
		if c2, ok2 := divCoefAt(d, e, p+1); ok2 {
			//nolint:gosec // p ≤ DefaultPrec-2 in this arm, so p+1 fits uint8
			return newDecimal(c2, neg, uint8(p)+1), nil
		}
	}
	//nolint:gosec // 0 ≤ p ≤ DefaultPrec-1 on the degrade path, so it fits uint8
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
		// 128/64 fast path: when the numerator fits 128 bits the quotient
		// always fits too (dividend < 2^128 ⇒ quotient < 2^128), so ok is
		// unconditionally true and no overflow test is needed. e.coef.lo != 0
		// here because Div ruled out a zero divisor and hi == 0 ⇒ lo != 0.
		if num.isZeroUpper() {
			if num.d1 < e.coef.lo {
				// num.d1 < e.coef.lo is exactly bits.Div64's documented
				// no-trap precondition (high word below the divisor), so the
				// single divide cannot panic on quotient overflow.
				q, _ := bits.Div64(num.d1, num.d0, e.coef.lo)
				return u128{lo: q}, true
			}
			q, _ := quoRem64(num.lo128(), e.coef.lo)
			return q, true
		}
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
// coefficient.
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
	// f = max(d.prec, e.prec) ⇒ at least one factor below is 10^0. Skip the
	// multiply-by-one for whichever operand is already aligned: scaling by 1
	// is a no-op the schoolbook path would otherwise pay 4 Mul64 + 6 Add64
	// for (mulToU256) or a divisor multiply that provably never overflows.
	var num u256
	if f == d.prec {
		num = u256{d0: d.coef.lo, d1: d.coef.hi}
	} else {
		num = mulToU256(d.coef, pow10u128[(f-d.prec)&63])
	}
	den := e.coef
	if f != e.prec {
		var overflow uint64
		den, overflow = mul128by64(e.coef, pow10u64[(f-e.prec)&31])
		if overflow != 0 {
			// f != e.prec implies f == d.prec, so num is exactly d.coef and
			// fits 128 bits. An overflowing aligned divisor is strictly larger
			// than every 128-bit numerator; therefore trunc(d/e) is zero and d
			// itself is the exact remainder. Returning d also preserves its
			// contracted sign and precision without manufacturing a wide den.
			return Decimal{}, d, nil
		}
	}
	if den.hi == 0 {
		// 128/64 fast path mirroring divCoefAt: a numerator that fits 128 bits
		// yields a quotient that fits too, so ErrOverflow is impossible here.
		// den.lo != 0 because the zero check above ruled out hi|lo == 0.
		if num.isZeroUpper() {
			if num.d1 < den.lo {
				// num.d1 < den.lo is bits.Div64's no-trap precondition.
				q, r := bits.Div64(num.d1, num.d0, den.lo)
				return newDecimal(u128{lo: q}, qNeg, 0), newDecimal(u128{lo: r}, d.neg, f), nil
			}
			q, r := quoRem64(num.lo128(), den.lo)
			return newDecimal(q, qNeg, 0), newDecimal(u128{lo: r}, d.neg, f), nil
		}
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

// aggregateAccum is the exact fixed-width accumulator shared by Sum and the
// average helpers. Every input is aligned to the greatest input precision and
// added to a separate unsigned total for its sign. An aligned term is less
// than 2^128*10^MaxPrec < 2^192. A variadic call contains at most MaxInt+1
// terms, which is at most 2^63 on every supported Go architecture, so either
// same-sign total is strictly less than 2^255 and always fits u256.
//
// Keeping the signs separate makes cancellation independent of operand order;
// one final magnitude subtraction recovers the exact signed total.
type aggregateAccum struct {
	pos, neg u256
	prec     uint8
}

// accumulateAggregateAdaptive accumulates same-precision inputs into separate
// positive and negative 128-bit subtotals. Keeping the signs separate retains
// Sum's order-independent cancellation semantics. first must be nonzero unless
// rest is empty; callers discard leading canonical zeros so first.prec is the
// common prefix precision without carrying a separate precision result through
// the hot-loop ABI.
//
// A nonnegative state indexes the first rest input not represented by the two
// returned prefix subtotals. A negative state means the entire input remained
// narrow: the first coefficient is then the final magnitude, while -1 denotes
// a nonnegative result and -2 a negative result. Computing that magnitude here
// keeps the successful common-precision path in one call.
func accumulateAggregateAdaptive(first Decimal, rest []Decimal) (pos, neg u128, state int) {
	prec := first.prec
	if first.neg {
		neg = first.coef
	} else {
		pos = first.coef
	}

	for i, d := range rest {
		if d.coef.isZero() {
			continue
		}
		if d.prec != prec {
			return pos, neg, i
		}
		var carry uint64
		if d.neg {
			neg, carry = add128(neg, d.coef)
		} else {
			pos, carry = add128(pos, d.coef)
		}
		if carry != 0 {
			// Recover the exact pre-add subtotal from the wrapped result; this
			// subtraction is cold and lets the successful loop retain one add and
			// one carry branch per element.
			if d.neg {
				neg, _ = sub128(neg, d.coef)
			} else {
				pos, _ = sub128(pos, d.coef)
			}
			return pos, neg, i
		}
	}
	if neg.isZero() {
		return pos, u128{}, -1
	}
	if pos.isZero() {
		return neg, u128{}, -2
	}
	if cmp128(pos, neg) >= 0 {
		coef, _ := sub128(pos, neg)
		return coef, u128{}, -1
	}
	coef, _ := sub128(neg, pos)
	return coef, u128{}, -2
}

// aggregateFinishWide is the cold narrow-to-wide handoff. prefixPos and
// prefixNeg contain exactly the inputs before suffix, whose first element
// caused either a precision mismatch or a subtotal carry.
func aggregateFinishWide(a *aggregateAccum, prefixPos, prefixNeg u128, prec uint8, suffix []Decimal) {
	aggregatePromote128(a, prefixPos, prefixNeg, prec)
	for _, d := range suffix {
		if d.coef.isZero() {
			continue
		}
		if d.prec > a.prec {
			// After alignment both exact same-sign subtotals are prefixes of
			// the <2^255 totals proved by aggregateAccum's bound, so neither
			// multiplication can overflow.
			scale := pow10u64[(d.prec-a.prec)&31]
			a.pos = aggregateMul256by64(a.pos, scale)
			a.neg = aggregateMul256by64(a.neg, scale)
			a.prec = d.prec
		}
		// Keep the aligned add local to this suffix loop. aggregateAddDecimal
		// remains shared by the from-scratch path, but a helper call per suffix
		// element measurably regresses early handoffs.
		d2, d1, d0 := mul128by64to192(d.coef, pow10u64[(a.prec-d.prec)&31])
		if d.neg {
			a.neg = aggregateAdd192(a.neg, d2, d1, d0)
		} else {
			a.pos = aggregateAdd192(a.pos, d2, d1, d0)
		}
	}
}

func aggregatePromote128(a *aggregateAccum, pos, neg u128, prec uint8) {
	*a = aggregateAccum{
		pos:  u256{d0: pos.lo, d1: pos.hi},
		neg:  u256{d0: neg.lo, d1: neg.hi},
		prec: prec,
	}
}

// accumulateAggregate aligns and accumulates first and rest exactly. It scans
// precision separately so every term is scaled only once, by a single-limb
// power of ten, and it never allocates.
func accumulateAggregate(first Decimal, rest []Decimal) aggregateAccum {
	prec := first.prec
	for _, d := range rest {
		prec = max(prec, d.prec)
	}

	a := aggregateAccum{prec: prec}
	aggregateAddDecimal(&a, first)
	for _, d := range rest {
		aggregateAddDecimal(&a, d)
	}
	return a
}

func aggregateAddDecimal(a *aggregateAccum, d Decimal) {
	if d.coef.isZero() {
		return
	}
	d2, d1, d0 := mul128by64to192(d.coef, pow10u64[(a.prec-d.prec)&31])
	if d.neg {
		a.neg = aggregateAdd192(a.neg, d2, d1, d0)
	} else {
		a.pos = aggregateAdd192(a.pos, d2, d1, d0)
	}
}

func aggregateAdd192(u u256, d2, d1, d0 uint64) u256 {
	d0, carry := bits.Add64(u.d0, d0, 0)
	d1, carry = bits.Add64(u.d1, d1, carry)
	d2, carry = bits.Add64(u.d2, d2, carry)
	d3, _ := bits.Add64(u.d3, 0, carry) // the aggregateAccum bound proves no carry out
	return u256{d0: d0, d1: d1, d2: d2, d3: d3}
}

// signedMagnitude returns |pos-neg| and the sign of the difference.
func (a aggregateAccum) signedMagnitude() (u256, bool) {
	cmp := cmp256(a.pos, a.neg)
	if cmp >= 0 {
		return aggregateSub256(a.pos, a.neg), false
	}
	return aggregateSub256(a.neg, a.pos), true
}

// aggregateSub256 returns a-b. Callers guarantee a >= b.
func aggregateSub256(a, b u256) u256 {
	d0, borrow := bits.Sub64(a.d0, b.d0, 0)
	d1, borrow := bits.Sub64(a.d1, b.d1, borrow)
	d2, borrow := bits.Sub64(a.d2, b.d2, borrow)
	d3, _ := bits.Sub64(a.d3, b.d3, borrow)
	return u256{d0: d0, d1: d1, d2: d2, d3: d3}
}

// Sum returns the exact sum first + rest[0] + ... + rest[n-1] at the
// greatest input precision. ErrOverflow is returned iff the final exact
// coefficient does not fit 128 bits; overflowing partial sums that later
// cancel do not cause a spurious error. Its representation is independent of
// operand order: a nonzero result carries the greatest input precision and
// zero is canonical. Precision is not lowered to rescue a final overflow: a
// value that would fit only after dropping trailing-zero fractional places
// still returns ErrOverflow when its coefficient does not fit at the
// contracted greatest input precision.
func Sum(first Decimal, rest ...Decimal) (Decimal, error) {
	if len(rest) == 1 {
		// For two operands Sum's exact greatest-precision contract is identical
		// to Add's, including mixed precision, cancellation, and overflow. Use
		// Add's fully inlined pair path instead of setting up an accumulator.
		return first.Add(rest[0])
	}
	for first.coef.isZero() && len(rest) > 0 {
		first, rest = rest[0], rest[1:]
	}
	pos, neg128, state := accumulateAggregateAdaptive(first, rest)
	if state < 0 {
		return newDecimal(pos, state == -2, first.prec), nil
	}
	var wide aggregateAccum
	aggregateFinishWide(&wide, pos, neg128, first.prec, rest[state:])
	coef, neg := wide.signedMagnitude()
	if !coef.isZeroUpper() {
		return Decimal{}, ErrOverflow
	}
	return newDecimal(coef.lo128(), neg, wide.prec), nil
}

// MustSum is Sum for operands with proven bounds: it panics on error.
func MustSum(first Decimal, rest ...Decimal) Decimal {
	s, err := Sum(first, rest...)
	if err != nil {
		panic(err)
	}
	return s
}

// aggregateDiv256by64 returns the full-width quotient and remainder u/v.
// Unlike div256by64, no 128-bit quotient limit applies.
func aggregateDiv256by64(u u256, v uint64) (u256, uint64) {
	q3, r := bits.Div64(0, u.d3, v)
	q2, r := bits.Div64(r, u.d2, v)
	q1, r := bits.Div64(r, u.d1, v)
	q0, r := bits.Div64(r, u.d0, v)
	return u256{d0: q0, d1: q1, d2: q2, d3: q3}, r
}

// aggregateMul256by64 returns the low 256 bits of u*v. Callers prove their
// aligned aggregate or mean coefficient fits, so no high carry is discarded.
func aggregateMul256by64(u u256, v uint64) u256 {
	carry, d0 := bits.Mul64(u.d0, v)
	hi, lo := bits.Mul64(u.d1, v)
	d1, c := bits.Add64(lo, carry, 0)
	carry = hi + c // hi <= v-1, so this cannot wrap
	hi, lo = bits.Mul64(u.d2, v)
	d2, c := bits.Add64(lo, carry, 0)
	carry = hi + c
	_, lo = bits.Mul64(u.d3, v)
	d3, _ := bits.Add64(lo, carry, 0) // high carry is impossible by the caller's bound
	return u256{d0: d0, d1: d1, d2: d2, d3: d3}
}

func aggregateAdd128(u u256, v u128) u256 {
	d0, carry := bits.Add64(u.d0, v.lo, 0)
	d1, carry := bits.Add64(u.d1, v.hi, carry)
	d2, carry := bits.Add64(u.d2, 0, carry)
	d3, _ := bits.Add64(u.d3, 0, carry)
	return u256{d0: d0, d1: d1, d2: d2, d3: d3}
}

// aggregateAverageAt derives trunc(total/count*10^(places-sourcePrec)) from
// the one full-width division total = count*base + baseRem. It also returns
// the exact fractional remainder and denominator at places, which lets
// AvgRound decide every rounding mode without a truncated intermediate.
func aggregateAverageAt(base u256, baseRem, count uint64, sourcePrec, places uint8) (u256, u128, u128) {
	if places >= sourcePrec {
		scale := pow10u64[(places-sourcePrec)&31]
		q := aggregateMul256by64(base, scale)
		// The exact mean is bounded by the greatest input magnitude. At any
		// supported result precision its truncated coefficient is <2^192, so
		// aggregateMul256by64 cannot discard a high carry here.
		hi, lo := bits.Mul64(baseRem, scale)
		correction, rem := quoRem64(u128{hi: hi, lo: lo}, count)
		return aggregateAdd128(q, correction), u128{lo: rem}, u128{lo: count}
	}

	drop := sourcePrec - places
	scale := pow10u64[drop&31]
	q, qRem := divmodU256Pow10Wide(base, drop)
	hi, lo := bits.Mul64(count, qRem)
	lo, carry := bits.Add64(lo, baseRem, 0)
	hi += carry // count*qRem+baseRem < count*scale < 2^127
	denHi, denLo := bits.Mul64(count, scale)
	return q, u128{hi: hi, lo: lo}, u128{hi: denHi, lo: denLo}
}

func aggregateBase(a aggregateAccum, count uint64) (u256, uint64, bool) {
	total, neg := a.signedMagnitude()
	q, rem := aggregateDiv256by64(total, count)
	return q, rem, neg
}

// aggregateGreatestFit returns the mean coefficient at the greatest places
// <= limit that fits 128 bits, plus its exact fractional remainder. Fit is
// monotone in places because floor(|mean|*10^places) never decreases. Probe
// limit first so ordinary values take one calculation; only a degraded result
// pays for the binary search over lower precisions.
func aggregateGreatestFit(base u256, baseRem, count uint64, sourcePrec, limit uint8) (u256, u128, uint8, bool) {
	q, rem, _ := aggregateAverageAt(base, baseRem, count, sourcePrec, limit)
	if q.isZeroUpper() {
		return q, rem, limit, true
	}
	if limit == 0 {
		return u256{}, u128{}, 0, false
	}

	lo, hi := uint8(0), limit-1
	var bestQ u256
	var bestRem u128
	var bestPlaces uint8
	found := false
	for lo <= hi {
		mid := (lo + hi) / 2
		mq, mr, _ := aggregateAverageAt(base, baseRem, count, sourcePrec, mid)
		if mq.isZeroUpper() {
			bestQ, bestRem, bestPlaces, found = mq, mr, mid, true
			lo = mid + 1
		} else {
			if mid == 0 {
				break
			}
			hi = mid - 1
		}
	}
	return bestQ, bestRem, bestPlaces, found
}

// Avg returns the arithmetic mean (first + rest...)/(1 + len(rest)),
// truncated toward zero at the greatest precision at or below DefaultPrec
// whose coefficient fits 128 bits. This is the same adaptive-precision
// representation as Div on every previously successful input, but the exact
// aggregate is divided while still wide: a representable mean is not rejected
// merely because its intermediate sum exceeds 128 bits.
//
// Avg is the legacy compatibility operation: discarded digits are not
// reported. Use AvgExact when loss must be an error or AvgRound to round once
// from the exact wide aggregate to an explicit scale.
func Avg(first Decimal, rest ...Decimal) (Decimal, error) {
	// Convert before adding: len(rest) <= MaxInt, hence this remains exact even
	// when len(rest)+1 would overflow a signed int on a 64-bit architecture.
	count := uint64(len(rest)) + 1
	if len(rest) == 1 {
		// Reuse Add's pair fast path when its exact sum fits. If it overflows,
		// the mean can still be representable, so continue into the wide handoff.
		if total, err := first.Add(rest[0]); err == nil {
			return total.Div(NewFromUint64(count))
		}
	}
	for first.coef.isZero() && len(rest) > 0 {
		first, rest = rest[0], rest[1:]
	}
	pos, neg128, state := accumulateAggregateAdaptive(first, rest)
	if state < 0 {
		// Div implements the same greatest-fitting-precision contract as Avg.
		// The direct route is valid because the exact same-precision total and
		// the unsigned count are both representable Decimals.
		return newDecimal(pos, state == -2, first.prec).Div(NewFromUint64(count))
	}
	var wide aggregateAccum
	aggregateFinishWide(&wide, pos, neg128, first.prec, rest[state:])
	base, baseRem, neg := aggregateBase(wide, count)
	q, _, places, ok := aggregateGreatestFit(base, baseRem, count, wide.prec, DefaultPrec)
	if !ok {
		// The mean lies between its Decimal inputs, so its integer coefficient
		// must fit. Keep the guard as a defensive invariant check.
		return Decimal{}, ErrOverflow
	}
	return newDecimal(q.lo128(), neg, places), nil
}

// AvgExact returns the exact arithmetic mean when it has a Decimal
// representation. Like DivExact, it uses the greatest precision at or below
// MaxPrec whose coefficient fits, retaining exact trailing zeros.
// ErrUnderflow reports a nonzero mean below 10^-MaxPrec; ErrInexact reports a
// mean that requires discarded digits. Use AvgRound instead when loss at a
// caller-selected scale is intentional. The operation never allocates.
func AvgExact(first Decimal, rest ...Decimal) (Decimal, error) {
	a := accumulateAggregate(first, rest)
	count := uint64(len(rest)) + 1
	base, baseRem, neg := aggregateBase(a, count)
	q, rem, places, ok := aggregateGreatestFit(base, baseRem, count, a.prec, MaxPrec)
	if !ok {
		// The mean lies between its Decimal inputs, so its integer coefficient
		// must fit. Keep the guard as a defensive invariant check.
		return Decimal{}, ErrOverflow
	}
	if rem.isZero() {
		return newDecimal(q.lo128(), neg, places), nil
	}
	if q.lo128().isZero() {
		return Decimal{}, ErrUnderflow
	}
	return Decimal{}, ErrInexact
}

// AvgRound returns the arithmetic mean rounded directly to exactly places
// fractional digits. It retains the exact aggregate quotient and remainder
// through the rounding decision, so it cannot double-round. A nonzero result
// carries precision places; zero remains canonical Decimal{}. Validation
// checks places before mode, matching MulRound and DivRound. ErrOverflow means
// the rounded coefficient at the requested precision does not fit 128 bits.
func AvgRound(first Decimal, places uint8, mode RoundingMode, rest ...Decimal) (Decimal, error) {
	if places > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	a := accumulateAggregate(first, rest)
	count := uint64(len(rest)) + 1
	base, baseRem, neg := aggregateBase(a, count)
	q, rem, den := aggregateAverageAt(base, baseRem, count, a.prec, places)
	if !q.isZeroUpper() {
		return Decimal{}, ErrOverflow
	}
	coef, err := roundQuotient(q.lo128(), !rem.isZero(), cmpDouble128(rem, den), neg, mode)
	if err != nil {
		return Decimal{}, err
	}
	return newDecimal(coef, neg, places), nil
}

// MustAvg is Avg for operands with proven bounds: it panics on error.
func MustAvg(first Decimal, rest ...Decimal) Decimal {
	a, err := Avg(first, rest...)
	if err != nil {
		panic(err)
	}
	return a
}
