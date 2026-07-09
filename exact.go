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
		work, rem = div256byPow10Wide(work, prec-MaxPrec)
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
		work, rem = div256byPow10Wide(work, 1)
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
	prod := mulToU256(d.coef, e.coef)
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
		q, rem, halfCmp, fits = divRoundWide(prod, u128{lo: pow10u64[k&31]})
	} else {
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
		num := mulToU256(d.coef, pow10u128[f&63])
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
func divRoundWide(num u256, den u128) (q u128, rem bool, halfCmp int, fits bool) {
	if den.hi == 0 {
		var r uint64
		var err error
		q, r, err = div256by64(num, den.lo)
		if err != nil {
			return u128{}, false, 0, false
		}
		ru := u128{lo: r}
		return q, r != 0, cmpDouble128(ru, den), true
	}
	if !less128(num.hi128(), den) {
		return u128{}, false, 0, false
	}
	q, r := div256by128(num, den)
	return q, !r.isZero(), cmpDouble128(r, den), true
}

// roundQuotient applies mode to a positive magnitude quotient. halfCmp is the
// comparison of twice the exact remainder with the positive denominator.
func roundQuotient(q u128, rem bool, halfCmp int, neg bool, mode RoundingMode) (u128, error) {
	up := false
	switch mode {
	case ToNearestAway:
		up = rem && halfCmp >= 0
	case ToNearestEven:
		up = rem && (halfCmp > 0 || (halfCmp == 0 && q.lo&1 != 0))
	case AwayFromZero:
		up = rem
	case TowardZero:
	case TowardPositive:
		up = rem && !neg
	case TowardNegative:
		up = rem && neg
	default:
		return u128{}, ErrInvalidRoundingMode
	}
	if !up {
		return q, nil
	}
	if q == (u128{hi: ^uint64(0), lo: ^uint64(0)}) {
		return u128{}, ErrOverflow
	}
	return inc128(q), nil
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

// div256byPow10Wide divides a full-width value by 10^k and retains the exact
// remainder. k is at most MaxPrec, so the divisor is one limb.
func div256byPow10Wide(u u256, k uint8) (u256, uint64) {
	d := pow10u64[k&31]
	q3, r := bits.Div64(0, u.d3, d)
	q2, r := bits.Div64(r, u.d2, d)
	q1, r := bits.Div64(r, u.d1, d)
	q0, r := bits.Div64(r, u.d0, d)
	return u256{d0: q0, d1: q1, d2: q2, d3: q3}, r
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
