package zerodecimal

import "math/bits"

// MulAddRound returns d*e + f rounded once, directly from the exact value, to
// exactly places fractional digits: the quantity × price + fee shape. The
// product keeps all d.Prec()+e.Prec() of its fractional digits and f joins it
// at the larger of that and f.Prec(), so the final rounding is the only loss.
// Chaining MulRound and Add rounds twice; chaining legacy Mul and Add
// truncates a product needing more than DefaultPrec digits and fails when an
// intermediate exceeds 128 bits. A nonzero result carries precision places;
// zero remains canonical Decimal{}.
//
// Validation errors take precedence in argument order: places is checked
// first, then mode. ErrOverflow means the rounded coefficient at the requested
// precision does not fit 128 bits; no intermediate overflows on its own.
// Rounding to zero is a successful result, not underflow.
//
// When d.Prec()+e.Prec() ≤ DefaultPrec, legacy Mul is exact, so wherever
// d.Mul(e) and then Add(f) succeed, rounding that sum to places in the same
// mode (RoundBank for ToNearestEven, Round for ToNearestAway, and so on)
// yields the value MulAddRound returns — unless that value's coefficient at
// places exceeds 128 bits, which MulAddRound reports as ErrOverflow.
func (d Decimal) MulAddRound(e, f Decimal, places uint8, mode RoundingMode) (Decimal, error) {
	if places > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	pPrec := d.prec + e.prec // 0..2*MaxPrec
	if pPrec <= MaxPrec && d.coef.hi|e.coef.hi|f.coef.hi == 0 {
		// One-limb operands at a combined precision P = max(pPrec, f.prec) ≤
		// MaxPrec: when the product, both operands aligned to P and their sum
		// fit one limb, the whole operation is three multiplies, one add and
		// one reciprocal division, rounded in place.
		prec := max(pPrec, f.prec)
		ph, p := bits.Mul64(d.coef.lo, e.coef.lo)
		ah, a := bits.Mul64(p, pow10u64[(prec-pPrec)&31])
		bh, b := bits.Mul64(f.coef.lo, pow10u64[(prec-f.prec)&31])
		if ph|ah|bh == 0 {
			if n, neg, ok := addSigned64(a, d.neg != e.neg, b, f.neg); ok {
				if places >= prec {
					// Exact: scale up; a 64×64-bit product always fits 128 bits.
					hi, lo := bits.Mul64(n, pow10u64[(places-prec)&31])
					return newDecimal(u128{hi: hi, lo: lo}, neg, places), nil
				}
				// q ≤ (2^64-1)/10, so q+1 cannot wrap.
				k := prec - places
				q, r := divmod64Pow10(n, k)
				if roundsUp(mode, r != 0, cmpUint64(r, pow10u64[k&31]>>1), q&1 != 0, neg) {
					q++
				}
				return newDecimal(u128{lo: q}, neg, places), nil
			}
		}
	}
	return mulAddRoundWide(d, e, f, places, mode)
}

// mulAddRoundWide is MulAddRound for any operands. The exact value is
// n / 10^prec with prec = max(d.prec+e.prec, f.prec) ≤ 2·MaxPrec and n the
// signed sum of the 256-bit product and f's coefficient, each aligned to prec.
// The product widens by at most 10^MaxPrec (only when f.prec is the larger
// precision) and f by at most 10^(2·MaxPrec), which keeps f below 2^255.
//
// A 256-bit overflow of either step implies a result overflow, so neither
// needs a wider type. Scaling the product up past 2^256 happens only with
// prec = f.prec ≤ MaxPrec, and f is then below 2^128, so |n| > 2^256 - 2^128
// and even the integer part |n| / 10^MaxPrec exceeds 2^128. A same-sign sum
// past 2^256 has |n| / 10^(2·MaxPrec) > 2^128 likewise. Rounding never
// shrinks a magnitude below its truncation, so the result overflows too.
func mulAddRoundWide(d, e, f Decimal, places uint8, mode RoundingMode) (Decimal, error) {
	pPrec := d.prec + e.prec
	prec := max(pPrec, f.prec)
	a := mulToU256(d.coef, e.coef)
	if prec > pPrec {
		var ok bool
		if a, ok = mul256by64(a, pow10u64[(prec-pPrec)&31]); !ok {
			return Decimal{}, ErrOverflow
		}
	}
	b := mulToU256(f.coef, pow10u128[(prec-f.prec)&63])
	n, neg, ok := addSigned256(a, d.neg != e.neg, b, f.neg)
	if !ok {
		return Decimal{}, ErrOverflow
	}

	if places >= prec {
		// Exact: scale the sum up to places (at most MaxPrec digits).
		if !n.isZeroUpper() {
			return Decimal{}, ErrOverflow
		}
		coef, overflow := mul128by64(n.lo128(), pow10u64[(places-prec)&31])
		if overflow != 0 {
			return Decimal{}, ErrOverflow
		}
		return newDecimal(coef, neg, places), nil
	}
	q, rem, halfCmp, fits := divRoundPow10(n, prec-places)
	if !fits {
		return Decimal{}, ErrOverflow
	}
	q, err := roundQuotient(q, rem, halfCmp, neg, mode)
	if err != nil {
		return Decimal{}, err
	}
	return newDecimal(q, neg, places), nil
}

// addSigned64 returns the magnitude and sign of (aNeg ? -a : a) + (bNeg ? -b :
// b), ok unless a same-sign sum carries out of 64 bits. A zero sum's sign is
// meaningless; newDecimal canonicalizes it.
func addSigned64(a uint64, aNeg bool, b uint64, bNeg bool) (uint64, bool, bool) {
	if aNeg == bNeg {
		s, carry := bits.Add64(a, b, 0)
		return s, aNeg, carry == 0
	}
	if a >= b {
		return a - b, aNeg, true
	}
	return b - a, bNeg, true
}

// addSigned256 is addSigned64 for 256-bit magnitudes.
func addSigned256(a u256, aNeg bool, b u256, bNeg bool) (u256, bool, bool) {
	if aNeg == bNeg {
		s, carry := add256(a, b)
		return s, aNeg, carry == 0
	}
	diff, borrow := sub256(a, b)
	if borrow != 0 {
		return neg256(diff), bNeg, true
	}
	return diff, aNeg, true
}
