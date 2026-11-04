package zerodecimal

import "math/bits"

// RoundingMode selects how a discarded nonzero remainder affects the result
// of MulRound, DivRound, and AvgRound.
type RoundingMode uint8

const (
	// ToNearestAway rounds to the nearest value, with exact half-way cases
	// rounded away from zero. It has the same semantics as Decimal.Round.
	ToNearestAway RoundingMode = iota

	// ToNearestEven rounds to the nearest value, with exact half-way cases
	// rounded so the retained coefficient is even. It has the same semantics
	// as Decimal.RoundBank.
	ToNearestEven

	// AwayFromZero rounds every discarded nonzero remainder away from zero. It
	// has the same semantics as Decimal.RoundUp.
	AwayFromZero

	// TowardZero discards the remainder. It has the same semantics as
	// Decimal.RoundDown and Decimal.Truncate.
	TowardZero

	// TowardPositive rounds a discarded nonzero remainder toward +infinity. It
	// has the same semantics as Decimal.RoundCeil.
	TowardPositive

	// TowardNegative rounds a discarded nonzero remainder toward -infinity. It
	// has the same semantics as Decimal.RoundFloor.
	TowardNegative
)

// valid reports whether m names one of the package's six rounding modes.
func (m RoundingMode) valid() bool {
	return m <= TowardNegative
}

// MulExact returns the exact mathematical product d*e when that value has a
// Decimal representation. It preserves the natural precision d.Prec()+e.Prec
// whenever possible. If that exceeds MaxPrec, or its coefficient exceeds 128
// bits, trailing decimal zeros are removed only as needed to find an exact
// representation.
//
// ErrUnderflow reports a nonzero product below 10^-MaxPrec. ErrInexact reports
// a product whose magnitude is representable but whose nonzero fractional
// digits cannot fit the precision/coefficient limits. ErrOverflow reports a
// product whose magnitude exceeds the largest Decimal. The operation never
// allocates and does not depend on DefaultPrec.
func (d Decimal) MulExact(e Decimal) (Decimal, error) {
	prod := mulToU256(d.coef, e.coef)
	if u256Zero(prod) {
		return Decimal{}, nil
	}

	naturalPrec := d.prec + e.prec // 0..2*MaxPrec, hence no uint8 overflow.
	prec := naturalPrec
	work := prod
	if prec > MaxPrec {
		var rem uint64
		work, rem = divmodU256Pow10Wide(work, prec-MaxPrec)
		if rem != 0 {
			return mulExactRangeError(prod, naturalPrec, work)
		}
		prec = MaxPrec
	}

	for !work.isZeroUpper() {
		if prec == 0 {
			return Decimal{}, ErrOverflow
		}
		var rem uint64
		work, rem = divmodU256Pow10Wide(work, 1)
		if rem != 0 {
			return mulExactRangeError(prod, naturalPrec, work)
		}
		prec--
	}

	return newDecimal(work.lo128(), d.neg != e.neg, prec), nil
}

// mulExactRangeError classifies a failed exact-product rescale. truncated is
// the quotient after the mandatory drop to MaxPrec (or after a later one-digit
// fit attempt); zero therefore proves the exact nonzero value is below the
// minimum quantum. Magnitude overflow takes precedence over precision loss.
func mulExactRangeError(prod u256, naturalPrec uint8, truncated u256) (Decimal, error) {
	if mulMagnitudeOverflows(prod, naturalPrec) {
		return Decimal{}, ErrOverflow
	}
	if u256Zero(truncated) {
		return Decimal{}, ErrUnderflow
	}
	return Decimal{}, ErrInexact
}

// DivExact returns the exact mathematical quotient d/e when it has a Decimal
// representation. Because division has no natural finite precision, it uses
// the greatest precision at or below MaxPrec whose coefficient fits 128 bits;
// this mirrors Div's precision-preserving behavior without truncating a
// remainder. Exact trailing zeros are therefore retained.
//
// ErrDivideByZero takes precedence for a zero divisor. Otherwise ErrUnderflow
// reports a nonzero quotient below 10^-MaxPrec, ErrInexact reports a
// non-terminating or otherwise unrepresentable fractional quotient, and
// ErrOverflow reports magnitude beyond the largest Decimal. The operation
// never allocates and does not depend on DefaultPrec.
func (d Decimal) DivExact(e Decimal) (Decimal, error) {
	if e.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	if d.coef.isZero() {
		return Decimal{}, nil
	}

	neg := d.neg != e.neg
	q, rem, _, fits := divRoundAt(d, e, int(MaxPrec))
	if fits {
		return finishDivExact(q, rem, MaxPrec, neg)
	}

	// Fit is monotone in precision. When MaxPrec overflows, binary-search the
	// remaining range instead of paying up to 19 more full-width divisions on
	// large exact integers. The final probe is the greatest fitting precision.
	lo, hi := 0, int(MaxPrec)-1
	best := -1
	var bestQ u128
	var bestRem bool
	for lo <= hi {
		mid := (lo + hi) / 2 // bounds are within 0..MaxPrec; cannot overflow
		mq, mr, _, mf := divRoundAt(d, e, mid)
		if mf {
			best, bestQ, bestRem = mid, mq, mr
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best < 0 {
		return Decimal{}, ErrOverflow
	}
	return finishDivExact(bestQ, bestRem, uint8(best), neg)
}

func finishDivExact(q u128, rem bool, places uint8, neg bool) (Decimal, error) {
	if !rem {
		return newDecimal(q, neg, places), nil
	}
	if places == 0 && q == (u128{hi: ^uint64(0), lo: ^uint64(0)}) {
		// max coefficient plus a positive fractional remainder is strictly
		// larger than the greatest Decimal, not merely inexact.
		return Decimal{}, ErrOverflow
	}
	if q.isZero() {
		return Decimal{}, ErrUnderflow
	}
	// If d/e is not integral after scaling by 10^places, it cannot be integral
	// at any smaller precision either.
	return Decimal{}, ErrInexact
}

// MulRound returns d*e rounded directly from the full 256-bit product to
// exactly places fractional digits. Unlike Mul followed by Round, no
// intermediate truncation occurs, so tie and sticky information is preserved.
// A nonzero result carries precision places; zero remains canonical Decimal{}.
//
// Validation errors take precedence in argument order: places is checked
// first, then mode. ErrOverflow means the rounded coefficient at the requested
// precision does not fit 128 bits. Rounding to zero is a successful result,
// not underflow, because loss was explicitly requested by the caller.
func (d Decimal) MulRound(e Decimal, places uint8, mode RoundingMode) (Decimal, error) {
	if places > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	if d.coef.isZero() || e.coef.isZero() {
		return Decimal{}, nil
	}

	neg := d.neg != e.neg
	naturalPrec := d.prec + e.prec
	var prod u256
	if d.coef.hi|e.coef.hi == 0 {
		// One-limb operands, the dominant money shape: the product is one
		// hardware multiply, without mulToU256's frame.
		hi, lo := bits.Mul64(d.coef.lo, e.coef.lo)
		if hi == 0 && places < naturalPrec && naturalPrec-places <= MaxPrec {
			// A one-limb product rescaled by at most 10^MaxPrec: one reciprocal
			// multiply, rounded in place. q ≤ (2^64-1)/10, so q+1 cannot wrap.
			k := naturalPrec - places
			q, r := divmod64Pow10(lo, k)
			if roundsUp(mode, r != 0, cmpUint64(r, pow10u64[k&31]>>1), q&1 != 0, neg) {
				q++
			}
			return newDecimal(u128{lo: q}, neg, places), nil
		}
		prod = u256{d0: lo, d1: hi}
	} else {
		prod = mulToU256(d.coef, e.coef)
	}
	if places >= naturalPrec {
		if !prod.isZeroUpper() {
			return Decimal{}, ErrOverflow
		}
		coef := prod.lo128()
		var overflow uint64
		coef, overflow = mul128by64(coef, pow10u64[(places-naturalPrec)&31])
		if overflow != 0 {
			return Decimal{}, ErrOverflow
		}
		return newDecimal(coef, neg, places), nil
	}

	k := naturalPrec - places // 1..2*MaxPrec
	var q u128
	var rem bool
	var halfCmp int
	var fits bool
	if k <= MaxPrec {
		q, rem, halfCmp, fits = divRoundPow10(prod, k)
	} else {
		// A two-limb 10^k: divRoundWide directly, saving divRoundPow10's
		// frame on the way to the same division.
		q, rem, halfCmp, fits = divRoundWide(prod, pow10u128[k&63])
	}
	if !fits {
		return Decimal{}, ErrOverflow
	}
	q, err := roundQuotient(q, rem, halfCmp, neg, mode)
	if err != nil {
		return Decimal{}, err
	}
	return newDecimal(q, neg, places), nil
}

// DivRound returns d/e rounded directly to exactly places fractional digits.
// The quotient and remainder are computed at the requested precision, so no
// intermediate Decimal can erase digits that affect the final rounding. A
// nonzero result carries precision places; zero remains canonical Decimal{}.
//
// Validation errors take precedence in argument order: places, mode, then a
// zero divisor. ErrOverflow means the rounded coefficient at the requested
// precision does not fit 128 bits. Rounding to zero is successful.
func (d Decimal) DivRound(e Decimal, places uint8, mode RoundingMode) (Decimal, error) {
	if places > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	if e.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	if d.coef.isZero() {
		return Decimal{}, nil
	}

	neg := d.neg != e.neg
	if f := int(places) + int(e.prec) - int(d.prec); d.coef.hi|e.coef.hi == 0 && f >= 0 && f <= int(MaxPrec) {
		// One-limb operands whose scaled dividend d·10^f still fits one limb:
		// a single hardware divide, rounded in place. A nonzero remainder
		// means a divisor of at least 2, so q+1 cannot wrap.
		if hi, lo := bits.Mul64(d.coef.lo, pow10u64[f&31]); hi == 0 {
			v := e.coef.lo
			q, r := lo/v, lo%v
			if roundsUp(mode, r != 0, cmpHalf64(r, v), q&1 != 0, neg) {
				q++
			}
			return newDecimal(u128{lo: q}, neg, places), nil
		}
	}
	q, rem, halfCmp, fits := divRoundAt(d, e, int(places))
	if !fits {
		return Decimal{}, ErrOverflow
	}
	q, err := roundQuotient(q, rem, halfCmp, neg, mode)
	if err != nil {
		return Decimal{}, err
	}
	return newDecimal(q, neg, places), nil
}

// divRoundAt computes floor(|d/e|*10^places), whether its remainder is
// nonzero, and sign(2*remainder-denominator). It reports fits=false precisely
// when the truncated coefficient is at least 2^128. The scale gap is bounded
// by [-MaxPrec, 2*MaxPrec], so all powers are table-backed.
func divRoundAt(d, e Decimal, places int) (q u128, rem bool, halfCmp int, fits bool) {
	f := places + int(e.prec) - int(d.prec)
	if f >= 0 {
		var num u256
		if d.coef.hi == 0 && f <= int(MaxPrec) {
			// One-limb dividend scaled by a one-limb power: a single hardware
			// multiply, without mulToU256's frame.
			hi, lo := bits.Mul64(d.coef.lo, pow10u64[f&31])
			num = u256{d0: lo, d1: hi}
		} else {
			num = mulToU256(d.coef, pow10u128[f&63])
		}
		return divRoundWide(num, e.coef)
	}

	den := mulToU256(e.coef, pow10u128[(-f)&63])
	if den.isZeroUpper() {
		return divRoundWide(u256{d0: d.coef.lo, d1: d.coef.hi}, den.lo128())
	}
	// A scaled denominator above 128 bits is strictly greater than the
	// unscaled 128-bit numerator, hence q=0 and the entire numerator remains.
	return u128{}, !d.coef.isZero(), cmpDouble128Wide(d.coef, den), true
}

// divRoundWide divides a 256-bit numerator by a nonzero 128-bit denominator,
// retaining exactly the two facts rounding needs about the remainder.
//
// A one-limb denominator takes the narrowest divide the numerator allows. A
// one-limb numerator is a single hardware divide; a numerator whose high limb
// is below the denominator has a one-limb quotient and needs one 128/64
// divide. Only wider numerators pay for div256by64, which derives a
// reciprocal with a 128/64 divide of its own before two div2by1 steps — a
// loss for a divisor used once. The remainder then fits one limb, so the half
// comparison needs no 128-bit doubling.
func divRoundWide(num u256, den u128) (q u128, rem bool, halfCmp int, fits bool) {
	if den.hi == 0 {
		v := den.lo
		var r uint64
		switch {
		case num.d1|num.d2|num.d3 == 0:
			q, r = u128{lo: num.d0 / v}, num.d0%v
		case num.d2|num.d3 == 0 && num.d1 < v:
			// num.d1 < v is bits.Div64's no-trap precondition.
			var q0 uint64
			q0, r = bits.Div64(num.d1, num.d0, v)
			q = u128{lo: q0}
		default:
			var err error
			q, r, err = div256by64(num, v)
			if err != nil {
				return u128{}, false, 0, false
			}
		}
		return q, r != 0, cmpHalf64(r, v), true
	}
	if !less128(num.hi128(), den) {
		return u128{}, false, 0, false
	}
	q, r := div256by128(num, den)
	return q, !r.isZero(), cmpDouble128(r, den), true
}

// divRoundPow10 is divRoundWide for the denominator 10^k, 1 ≤ k ≤ 2·MaxPrec.
// Up to 10^MaxPrec the quotient comes from the precomputed power-of-ten
// reciprocals (div10.go) rather than from div256by64, which would derive a
// reciprocal per call with a 128/64 divide, and the half comparison is the
// remainder against 10^k/2, exact because 10^k is even. A two-limb 10^k goes
// to divRoundWide: its 256/128 division skips a zero high quotient digit and
// measured faster there than two reciprocal passes.
//
// PRECONDITION (not checked): 1 ≤ k ≤ 2·MaxPrec.
func divRoundPow10(num u256, k uint8) (q u128, rem bool, halfCmp int, fits bool) {
	if k > MaxPrec {
		return divRoundWide(num, pow10u128[k&63])
	}
	q, r, fits := quoRemPow10Fit(num, k)
	if !fits {
		return u128{}, false, 0, false
	}
	return q, r != 0, cmpUint64(r, pow10u64[k&31]>>1), true
}

// quoRemPow10Fit returns ⌊u/10^k⌋ and u mod 10^k, and whether the quotient
// fits 128 bits, on the narrowest reciprocal path the dividend allows.
//
// PRECONDITION (not checked): 1 ≤ k ≤ MaxPrec.
func quoRemPow10Fit(u u256, k uint8) (u128, uint64, bool) {
	if u.isZeroUpper() {
		if u.d1 == 0 {
			q, r := divmod64Pow10(u.d0, k)
			return u128{lo: q}, r, true
		}
		q, r := divmod128Pow10Slow(u.lo128(), k)
		return q, r, true
	}
	// The quotient fits 128 bits iff u < 10^k·2^128, i.e. iff the high 128
	// bits of u stay below 10^k (the divU256Pow10 test).
	if u.d3 != 0 || u.d2 >= pow10u64[k&31] {
		return u128{}, 0, false
	}
	// divU256Pow10's two div2by1 steps, keeping the remainder: d2 < 10^k
	// makes the top normalized limb zero, so the leading step is skipped.
	e := &pow10Tab[k&31] // &-address: load referenced fields, no 40-byte copy
	s := uint(e.s)
	n2 := u.d2<<s | u.d1>>(64-s)
	n1 := u.d1<<s | u.d0>>(64-s)
	n0 := u.d0 << s
	q1, r1 := div2by1(n2, n1, e.dn, e.v)
	q0, r0 := div2by1(r1, n0, e.dn, e.v)
	return u128{hi: q1, lo: q0}, r0 >> s, true
}

// cmpUint64 returns -1, 0 or +1 as a is less than, equal to or greater than b.
func cmpUint64(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpHalf64 returns sign(2*r-den) for r < den: cmpDouble128 for a one-limb
// remainder. r < den means 2*r ≥ den exactly when r ≥ den-r, with no doubling
// that could carry out of the limb.
func cmpHalf64(r, den uint64) int {
	return cmpUint64(r, den-r)
}

// roundQuotient applies mode to a positive magnitude quotient. halfCmp is the
// comparison of twice the exact remainder with the positive denominator.
func roundQuotient(q u128, rem bool, halfCmp int, neg bool, mode RoundingMode) (u128, error) {
	if !mode.valid() {
		// Public callers validate first, but keep the helper total so a future
		// internal caller cannot silently turn an invalid mode into TowardZero.
		return u128{}, ErrInvalidRoundingMode
	}
	if !roundsUp(mode, rem, halfCmp, q.lo&1 != 0, neg) {
		return q, nil
	}
	if q == (u128{hi: ^uint64(0), lo: ^uint64(0)}) {
		return u128{}, ErrOverflow
	}
	return inc128(q), nil
}

// roundsUp reports whether mode steps a truncated magnitude quotient up by
// one unit: rem says the discarded remainder is nonzero, halfCmp compares
// twice it with the denominator, odd is the quotient's lowest bit and neg the
// result's sign. It inlines, so the narrow paths of MulRound and DivRound
// round without roundQuotient's frame.
//
// PRECONDITION (not checked): mode.valid(); TowardZero is the default arm.
func roundsUp(mode RoundingMode, rem bool, halfCmp int, odd, neg bool) bool {
	switch mode {
	case ToNearestAway:
		return rem && halfCmp >= 0
	case ToNearestEven:
		return rem && (halfCmp > 0 || (halfCmp == 0 && odd))
	case AwayFromZero:
		return rem
	case TowardPositive:
		return rem && !neg
	case TowardNegative:
		return rem && neg
	}
	return false
}

// cmpDouble128 returns sign(2*r-den), where r and den are 128-bit values.
func cmpDouble128(r, den u128) int {
	lo, carry := bits.Add64(r.lo, r.lo, 0)
	hi, carry := bits.Add64(r.hi, r.hi, carry)
	if carry != 0 {
		return 1 // 2*r >= 2^128 > den
	}
	return cmp128(u128{hi: hi, lo: lo}, den)
}

// cmpDouble128Wide returns sign(2*r-den) for a 128-bit r and 256-bit den.
func cmpDouble128Wide(r u128, den u256) int {
	lo, carry := bits.Add64(r.lo, r.lo, 0)
	hi, carry := bits.Add64(r.hi, r.hi, carry)
	twice := u256{d0: lo, d1: hi, d2: carry}
	return cmp256(twice, den)
}

func mulMagnitudeOverflows(prod u256, prec uint8) bool {
	maxAtPrec := mulToU256(
		u128{hi: ^uint64(0), lo: ^uint64(0)},
		pow10u128[prec&63],
	)
	return cmp256(prod, maxAtPrec) > 0
}

func u256Zero(u u256) bool {
	return u.d0|u.d1|u.d2|u.d3 == 0
}
