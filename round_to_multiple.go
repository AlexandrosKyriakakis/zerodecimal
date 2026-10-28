package zerodecimal

import "math/bits"

// RoundToMultiple returns d rounded in mode to a multiple of |step|, where step
// is a tick size, a board quantity or a share-increment rule such as 0.001
// shares or 25 cents. The result is q·|step|, where q is d/|step| rounded to an integer in mode
// directly from the exact quotient, so no intermediate truncation can move a
// tie or drop a sticky digit. The step's sign is ignored: the modes behave as
// in d.DivRound(step.Abs(), 0, mode), and the result keeps d's sign, so
// TowardPositive and TowardNegative round toward ±∞ in value. A nonzero result
// carries step's precision, so a d already on a multiple comes back as its
// value at that precision and a d with more fractional digits loses the
// excess. Zero remains canonical Decimal{}; rounding to zero is a successful
// result.
//
// Validation checks mode first (ErrInvalidRoundingMode), then a zero step
// (ErrDivideByZero), so both take precedence over a zero d. ErrOverflow means
// q·|step| does not fit a 128-bit coefficient at step's precision.
func (d Decimal) RoundToMultiple(step Decimal, mode RoundingMode) (Decimal, error) {
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	if step.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	if d.coef.isZero() {
		return Decimal{}, nil
	}
	if d.coef.hi|step.coef.hi == 0 {
		// One-limb operands: d/|step| = num/den once the precisions are
		// aligned. When the scaled side still fits one limb, that is one
		// hardware divide, rounded in place. A nonzero remainder means den ≥ 2,
		// so q+1 cannot wrap, and q·|step| is a 64×64-bit product, which always
		// fits 128 bits.
		num, den := d.coef.lo, step.coef.lo
		var carry uint64
		if step.prec >= d.prec {
			carry, num = bits.Mul64(num, pow10u64[(step.prec-d.prec)&31])
		} else {
			carry, den = bits.Mul64(den, pow10u64[(d.prec-step.prec)&31])
		}
		if carry == 0 {
			q, r := num/den, num%den
			if roundsUp(mode, r != 0, cmpHalf64(r, den), q&1 != 0, d.neg) {
				q++
			}
			hi, lo := bits.Mul64(q, step.coef.lo)
			return newDecimal(u128{hi: hi, lo: lo}, d.neg, step.prec), nil
		}
	}
	return roundToMultipleWide(d, step, mode)
}

// roundToMultipleWide is RoundToMultiple for any operands. divRoundAt divides
// |d| by |step| exactly at 0 places (it ignores both signs); a truncated
// quotient of 2^128 or more overflows the result too, because step's
// coefficient is at least 1. The rounded quotient times that coefficient is
// the result's coefficient at step's precision.
//
// A step of 10^-p below d's precision, such as a 0.001-share rule applied to a
// 19-place quantity, is rounding to p places: the quotient comes from the
// reciprocal tables, as in the Round family, instead of a 128/64 divide (a
// software routine on arm64). It reaches here only with a two-limb d. The
// quotient is at most (2^128-1)/10, so neither rounding up nor the product
// q·1 can overflow.
func roundToMultipleWide(d, step Decimal, mode RoundingMode) (Decimal, error) {
	if step.coef == (u128{lo: 1}) && d.prec > step.prec {
		k := d.prec - step.prec // 1..MaxPrec
		q, r := divmod128Pow10Slow(d.coef, k)
		if roundsUp(mode, r != 0, cmpUint64(r, pow10u64[k&31]>>1), q.lo&1 != 0, d.neg) {
			q = inc128(q)
		}
		return newDecimal(q, d.neg, step.prec), nil
	}
	q, rem, halfCmp, fits := divRoundAt(d, step, 0)
	if !fits {
		return Decimal{}, ErrOverflow
	}
	q, err := roundQuotient(q, rem, halfCmp, d.neg, mode)
	if err != nil {
		return Decimal{}, err
	}
	var coef u128
	if step.coef.hi == 0 {
		var overflow uint64
		if coef, overflow = mul128by64(q, step.coef.lo); overflow != 0 {
			return Decimal{}, ErrOverflow
		}
	} else {
		prod := mulToU256(q, step.coef)
		if !prod.isZeroUpper() {
			return Decimal{}, ErrOverflow
		}
		coef = prod.lo128()
	}
	return newDecimal(coef, d.neg, step.prec), nil
}
