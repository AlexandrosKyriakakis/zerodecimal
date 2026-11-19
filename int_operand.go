package zerodecimal

import "math/bits"

// MulInt returns d × n, bit-identical to d.Mul(NewFromInt(n)) under every
// build tag, including ErrOverflow and the canonical zero of a zero product.
// When d.Prec() ≤ DefaultPrec, which always holds in the default build, the
// product is exact at d's precision and takes one 128×64-bit multiply of the
// coefficient by |n|, whose high word is the overflow test, with no Decimal
// operand built. Under a reduced-precision tag, a d with more than
// DefaultPrec places goes through Mul, which truncates the product to
// DefaultPrec digits. MinInt64 is a valid operand.
func (d Decimal) MulInt(n int64) (Decimal, error) {
	if DefaultPrec < MaxPrec && d.prec > DefaultPrec {
		// Reachable only under zerodecimal_prec9 or zerodecimal_prec12, where
		// Mul truncates the product to DefaultPrec digits.
		return d.Mul(NewFromInt(n))
	}
	mag, neg := intMag(n)
	coef, overflow := mul128by64(d.coef, mag)
	if overflow != 0 {
		return Decimal{}, ErrOverflow
	}
	return newDecimal(coef, d.neg != neg, d.prec), nil
}

// DivInt returns d ÷ n with Div's legacy contract: the quotient truncated
// toward zero at the largest precision p ≤ DefaultPrec whose coefficient fits
// 128 bits. It is identical to d.Div(NewFromInt(n)) under every build tag, bit
// for bit, including canonical zero. Its only error is ErrDivideByZero for
// n == 0: an integer divisor never enlarges a magnitude, so the quotient
// always fits at some precision. MinInt64 is a valid operand.
//
// When d.Prec() ≤ DefaultPrec, which always holds in the default build, Div's
// common case, a quotient coefficient below 2^128 at DefaultPrec, runs here
// without building a Decimal operand or dispatching on the divisor's width,
// since |n| fits one limb. The rest goes through Div: a quotient coefficient
// of 2^128 or more at DefaultPrec (it needs |d| ≥ 2^128/10^DefaultPrec),
// for which Div lowers the precision, and under a reduced-precision tag a d
// with more than DefaultPrec places, whose divisor Div scales instead.
func (d Decimal) DivInt(n int64) (Decimal, error) {
	if n == 0 {
		return Decimal{}, ErrDivideByZero
	}
	if DefaultPrec < MaxPrec && d.prec > DefaultPrec {
		// Reachable only under zerodecimal_prec9 or zerodecimal_prec12, where
		// Div scales the divisor instead of the dividend.
		return d.Div(NewFromInt(n))
	}
	mag, neg := intMag(n)
	// Div's numerator at p = DefaultPrec: d.coef·10^f with f = DefaultPrec -
	// d.prec ≤ MaxPrec, so 10^f fits one limb and the product three.
	w2, w1, w0 := mul128by64to192(d.coef, pow10u64[(DefaultPrec-d.prec)&31])
	if w2 == 0 {
		// A numerator below 2^128 yields a quotient that fits. Each bits.Div64
		// gets a high word below mag, its no-trap precondition.
		var q u128
		if w1 < mag {
			q.lo, _ = bits.Div64(w1, w0, mag)
		} else {
			q.hi = w1 / mag
			q.lo, _ = bits.Div64(w1%mag, w0, mag)
		}
		return newDecimal(q, d.neg != neg, DefaultPrec), nil
	}
	// div256by64 fails exactly when the quotient reaches 2^128, where Div
	// starts lowering the precision.
	if q, _, err := div256by64(u256{d0: w0, d1: w1, d2: w2}, mag); err == nil {
		return newDecimal(q, d.neg != neg, DefaultPrec), nil
	}
	return d.Div(NewFromInt(n))
}

// MustMulInt is MulInt for operands with proven bounds: it panics on error.
func (d Decimal) MustMulInt(n int64) Decimal {
	r, err := d.MulInt(n)
	if err != nil {
		panic(err)
	}
	return r
}

// MustDivInt is DivInt for operands with proven bounds: it panics on error.
func (d Decimal) MustDivInt(n int64) Decimal {
	r, err := d.DivInt(n)
	if err != nil {
		panic(err)
	}
	return r
}

// intMag returns |n| and whether n is negative: the coefficient and sign of
// NewFromInt(n), without building the Decimal.
func intMag(n int64) (mag uint64, neg bool) {
	neg = n < 0
	//nolint:gosec // deliberate two's-complement reinterpretation, negated just below
	mag = uint64(n)
	if neg {
		mag = -mag // exact for MinInt64: -(uint64(-2^63)) wraps back to 2^63
	}
	return mag, neg
}
