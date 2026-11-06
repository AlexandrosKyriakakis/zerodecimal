package zerodecimal

import "math/bits"

// MulDivRound returns d*e/f rounded once, directly from the exact quotient, to
// exactly places fractional digits: the pro-rata share amount × part / whole.
// The product keeps every digit and the quotient is never truncated first, so
// the final rounding is the only loss. The legacy chain Mul then Div truncates
// a product needing more than DefaultPrec places, truncates the quotient at
// DefaultPrec, and fails when the product exceeds 128 bits; MulExact then
// DivRound rounds once but fails wherever the exact product has no Decimal
// representation. A nonzero result carries precision places; zero remains
// canonical Decimal{}.
//
// Validation errors take precedence in argument order: places, mode, then a
// zero divisor f. ErrOverflow means the rounded coefficient at the requested
// precision does not fit 128 bits; no intermediate overflows on its own.
// Rounding to zero is a successful result, not underflow.
//
// When d.Prec()+e.Prec() ≤ DefaultPrec and Div keeps DefaultPrec digits,
// d.Mul(e).Div(f) rounded to places agrees with MulDivRound in ToNearestAway
// and TowardZero. In the other modes it differs only when the exact quotient
// lies less than 10^-DefaultPrec past a tie (ToNearestEven) or past a multiple
// of 10^-places (the modes that round away from zero): the truncated quotient
// lands on that boundary and rounds as if it were exact.
func (d Decimal) MulDivRound(e, f Decimal, places uint8, mode RoundingMode) (Decimal, error) {
	if places > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	if f.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	if d.coef.isZero() || e.coef.isZero() {
		return Decimal{}, nil
	}

	neg := d.neg != e.neg != f.neg
	// The result coefficient is d·e·10^s / f: s ∈ [-2·MaxPrec, 2·MaxPrec].
	s := int(places) + int(f.prec) - int(d.prec) - int(e.prec)
	if d.coef.hi|e.coef.hi|f.coef.hi == 0 && s >= 0 && s <= int(MaxPrec) {
		// One-limb operands whose scaled product still fits one limb: two
		// multiplies and one hardware divide, rounded in place. A nonzero
		// remainder means a divisor of at least 2, so q+1 cannot wrap.
		if ph, p := bits.Mul64(d.coef.lo, e.coef.lo); ph == 0 {
			if nh, n := bits.Mul64(p, pow10u64[s&31]); nh == 0 {
				v := f.coef.lo
				q, r := n/v, n%v
				if roundsUp(mode, r != 0, cmpHalf64(r, v), q&1 != 0, neg) {
					q++
				}
				return newDecimal(u128{lo: q}, neg, places), nil
			}
		}
	}
	return mulDivRoundWide(d, e, f, places, mode, neg)
}

// mulDivRoundWide is MulDivRound for any operands. A nonnegative s scales the
// 256-bit product: past 2^256 it already exceeds f·2^128, so the quotient
// cannot fit 128 bits and the result overflows. A negative s scales f by
// 10^-s instead, which stays below 2^128·10^(2·MaxPrec) < 2^255; past 2^128,
// divRoundPow10By divides by the power of ten and by f in turn.
func mulDivRoundWide(d, e, f Decimal, places uint8, mode RoundingMode, neg bool) (Decimal, error) {
	prod := mulToU256(d.coef, e.coef)
	var q u128
	var rem bool
	var halfCmp int
	fits := true
	// s = up - down. Each side is at most 2·MaxPrec, so neither sum nor the
	// difference taken in its branch wraps a uint8.
	if up, down := places+f.prec, d.prec+e.prec; up >= down {
		num, ok := mul256Pow10(prod, up-down)
		if !ok {
			return Decimal{}, ErrOverflow
		}
		q, rem, halfCmp, fits = divRoundWide(num, f.coef)
	} else {
		t := down - up // 1..2*MaxPrec
		if den := mulToU256(f.coef, pow10u128[t&63]); den.isZeroUpper() {
			q, rem, halfCmp, fits = divRoundWide(prod, den.lo128())
		} else {
			q, rem, halfCmp = divRoundPow10By(prod, t, f.coef)
		}
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

// mul256Pow10 returns u·10^s and whether it fits 256 bits.
//
// PRECONDITION (not checked): s ≤ 2·MaxPrec.
func mul256Pow10(u u256, s uint8) (u256, bool) {
	if s == 0 {
		return u, true
	}
	if s > MaxPrec {
		var ok bool
		if u, ok = mul256by64(u, pow10u64[MaxPrec]); !ok {
			return u256{}, false
		}
		s -= MaxPrec
	}
	return mul256by64(u, pow10u64[s&31])
}

// divRoundPow10By is divRoundWide for the denominator v·10^t when that
// exceeds 128 bits. The quotient ⌊num/(v·10^t)⌋ = ⌊⌊num/10^t⌋/v⌋ then fits 128
// bits (num < 2^256), so it divides by the power of ten through the reciprocal
// tables, num = w·10^t + r1, and then by v, w = q·v + r2. The remainder is
// r2·10^t + r1 over v·10^t, so twice it against the denominator has the sign
// of (2·r2 - v)·10^t + 2·r1 with 0 ≤ r1 < 10^t: any 2·r2 - v ≥ 1 is above
// half, 0 leaves the sign of r1, -1 that of 2·r1 - 10^t, and ≤ -2 is below.
//
// PRECONDITIONS (not checked): 1 ≤ t ≤ 2·MaxPrec, v != 0, v·10^t ≥ 2^128.
func divRoundPow10By(num u256, t uint8, v u128) (q u128, rem bool, halfCmp int) {
	var w u256
	var r1 u128
	if t <= MaxPrec {
		var r uint64
		w, r = divmodU256Pow10Wide(num, t)
		r1 = u128{lo: r}
	} else {
		wide, ra := divmodU256Pow10Wide(num, MaxPrec)
		var rb uint64
		w, rb = divmodU256Pow10Wide(wide, t-MaxPrec)
		// r1 = rb·10^MaxPrec + ra < 10^t ≤ 10^(2·MaxPrec) < 2^128.
		hi, lo := bits.Mul64(rb, pow10u64[MaxPrec])
		lo, c := bits.Add64(lo, ra, 0)
		r1 = u128{hi: hi + c, lo: lo}
	}

	// The quotient fits 128 bits, so hi128(w) < v: both divides' fit
	// preconditions hold.
	var r2 u128
	if v.hi == 0 {
		var r uint64
		q, r, _ = div256by64(w, v.lo)
		r2 = u128{lo: r}
	} else {
		q, r2 = div256by128(w, v)
	}

	rem = !r1.isZero() || !r2.isZero()
	switch c := cmpDouble128(r2, v); {
	case c > 0:
		halfCmp = 1
	case c == 0:
		if !r1.isZero() {
			halfCmp = 1
		}
	default:
		// 2·r2 < v < 2^128, so the doubling below cannot carry.
		if gap, _ := sub128(v, r2.lsh(1)); gap == (u128{lo: 1}) {
			halfCmp = cmpDouble128(r1, pow10u128[t&63])
		} else {
			halfCmp = -1
		}
	}
	return q, rem, halfCmp
}
