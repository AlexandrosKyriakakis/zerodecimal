package zerodecimal

// Trim returns the numerically identical Decimal with the smallest
// precision: trailing fractional zeros are stripped from the representation
// (1.500 becomes 1.5), the same canonical form parsing produces. Arithmetic
// keeps trailing zeros, so == on results is representation equality; Trim is
// the canonicalizer — numerically equal values Trim to identical
// representations, safe for == and map keys. It is infallible and exact:
// Trim never changes the value.
func (d Decimal) Trim() Decimal {
	if d.prec == 0 {
		return d
	}
	return d.trimCore()
}

// trimCore strips the trailing fractional zeros of d. Caller guarantees
// d.prec > 0. A two-limb coefficient sheds digits through the reciprocal
// divider until the high limb clears or a nonzero digit stops the trim; the
// dominant one-limb tail then runs on strength-reduced constant divisions,
// exactly like the parser's trim. The early return is load-bearing: a
// nonzero digit of a two-limb coefficient must stop everything — lo%10 alone
// says nothing there (2^64 mod 10 is 6).
func (d Decimal) trimCore() Decimal {
	coef, prec := d.coef, d.prec
	for prec > 0 && coef.hi != 0 {
		q, r := divmod128Pow10Slow(coef, 1)
		if r != 0 {
			return newDecimal(coef, d.neg, prec)
		}
		coef, prec = q, prec-1
	}
	for prec > 0 && coef.lo%10 == 0 {
		coef.lo /= 10
		prec--
	}
	return newDecimal(coef, d.neg, prec)
}

// Rescale returns d represented with exactly prec fractional digits.
// Lowering the precision rounds ties to even, identical to RoundBank(prec);
// raising it appends zeros by scaling the coefficient, returning ErrOverflow
// when the scaled coefficient exceeds 128 bits. prec > MaxPrec returns
// ErrPrecOutOfRange. A zero d stays the canonical Decimal{} at precision 0
// for every prec within range — the canonical-zero invariant outranks the
// requested representation.
func (d Decimal) Rescale(prec uint8) (Decimal, error) {
	if prec == d.prec {
		return d, nil
	}
	return d.rescaleCore(prec)
}

// rescaleCore is the outlined arm of Rescale. Caller guarantees
// prec != d.prec.
func (d Decimal) rescaleCore(prec uint8) (Decimal, error) {
	if prec > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	if prec < d.prec {
		return d.roundBankCore(prec), nil
	}
	coef, overflow := mul128by64(d.coef, pow10u64[(prec-d.prec)&31])
	if overflow != 0 {
		return Decimal{}, ErrOverflow
	}
	return newDecimal(coef, d.neg, prec), nil
}

// MustRescale is Rescale for arguments with proven bounds: it panics instead
// of returning an error.
func (d Decimal) MustRescale(prec uint8) Decimal {
	r, err := d.Rescale(prec)
	if err != nil {
		panic(err)
	}
	return r
}
