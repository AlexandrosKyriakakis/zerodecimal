package zerodecimal

// The rounding family is structured as thin exported wrappers over per-mode
// outlined cores. Each wrapper inlines an early-out (places ≥ d.prec returns d
// unchanged — rounding never adds precision) and then makes exactly ONE call
// into its mode's core; the mode is therefore a compile-time fact, so the core
// the compiler emits carries only the work that mode needs (a Truncate core,
// for example, dead-code-eliminates the entire remainder reconstruction).
//
// EXACTNESS / INFALLIBILITY (shared by every core). With places < d.prec the
// excess digit count k = d.prec - places satisfies 1 ≤ k ≤ MaxPrec, so the
// divmod helpers' precondition holds and the half-point 10^k/2 is exact (10^k
// is even for k ≥ 1). Splitting off r < 10^k, the kept quotient q is at most
// (2^128-1)/10, so the round-up step can never overflow — every mode is
// infallible. Results have precision places and route through newDecimal, so a
// value rounded to zero collapses to the canonical unsigned Decimal{}.

// truncCore reduces d toward zero to places fractional digits (Truncate /
// RoundDown). The remainder is dropped, so the one-limb path needs no
// remainder reconstruction at all; see the family comment for the exactness
// argument. Caller guarantees places < d.prec.
func (d Decimal) truncCore(places uint8) Decimal {
	k := d.prec - places
	if d.coef.hi == 0 {
		q, _ := divmod64Pow10(d.coef.lo, k)
		return newDecimal(u128{lo: q}, d.neg, places)
	}
	q, _ := divmod128Pow10Slow(d.coef, k)
	return newDecimal(q, d.neg, places)
}

// roundHalfAwayCore reduces d to places digits with ties away from zero
// (Round). On the one-limb path the quotient q ≤ (2^64-1)/10, so a plain q+1
// covers the step (a carry into the high limb is impossible); the two-limb
// path keeps inc128 because there a lo carry can reach the high limb. Caller
// guarantees places < d.prec.
func (d Decimal) roundHalfAwayCore(places uint8) Decimal {
	k := d.prec - places
	if d.coef.hi == 0 {
		q, r := divmod64Pow10(d.coef.lo, k)
		// half = 10^k/2 derived from the divisor already loaded for r.
		if r >= pow10u64[k&31]>>1 {
			q++
		}
		return newDecimal(u128{lo: q}, d.neg, places)
	}
	q, r := divmod128Pow10Slow(d.coef, k)
	if r >= pow10u64[k&31]>>1 {
		q = inc128(q)
	}
	return newDecimal(q, d.neg, places)
}

// roundBankCore reduces d to places digits with ties to even (RoundBank): a
// tie steps up only when the kept quotient is odd. Caller guarantees
// places < d.prec.
func (d Decimal) roundBankCore(places uint8) Decimal {
	k := d.prec - places
	if d.coef.hi == 0 {
		q, r := divmod64Pow10(d.coef.lo, k)
		half := pow10u64[k&31] >> 1
		if r > half || (r == half && q&1 == 1) {
			q++
		}
		return newDecimal(u128{lo: q}, d.neg, places)
	}
	q, r := divmod128Pow10Slow(d.coef, k)
	half := pow10u64[k&31] >> 1
	if r > half || (r == half && q.lo&1 == 1) {
		q = inc128(q)
	}
	return newDecimal(q, d.neg, places)
}

// dirCore reduces d to places digits stepping the magnitude up whenever a
// nonzero remainder is dropped AND the sign-derived predicate pred holds. The
// wrappers pass an r-independent pred so the step degenerates to the right
// directional rule: RoundUp → true (always away), RoundCeil → !d.neg (up only
// toward +∞), RoundFloor → d.neg (up only toward -∞). Caller guarantees
// places < d.prec.
func (d Decimal) dirCore(places uint8, pred bool) Decimal {
	k := d.prec - places
	if d.coef.hi == 0 {
		q, r := divmod64Pow10(d.coef.lo, k)
		if pred && r != 0 {
			q++
		}
		return newDecimal(u128{lo: q}, d.neg, places)
	}
	q, r := divmod128Pow10Slow(d.coef, k)
	if pred && r != 0 {
		q = inc128(q)
	}
	return newDecimal(q, d.neg, places)
}

// Round rounds d to places fractional digits with ties away from zero
// (shopspring Round semantics): 2.5 → 3 and -2.5 → -3. places counts
// fractional digits only; negative places (rounding integer positions) are
// unsupported by design, which keeps the whole rounding family infallible.
// places ≥ d.Prec() returns d unchanged.
func (d Decimal) Round(places uint8) Decimal {
	if places >= d.prec {
		return d
	}
	return d.roundHalfAwayCore(places)
}

// RoundBank rounds d to places fractional digits with ties to even
// (banker's rounding): 2.5 → 2, 3.5 → 4, -2.5 → -2. places ≥ d.Prec()
// returns d unchanged.
func (d Decimal) RoundBank(places uint8) Decimal {
	if places >= d.prec {
		return d
	}
	return d.roundBankCore(places)
}

// RoundUp rounds d to places fractional digits away from zero: any dropped
// nonzero remainder steps the magnitude up, so 1.01 → 1.1 and -1.01 → -1.1
// at one place. places ≥ d.Prec() returns d unchanged.
func (d Decimal) RoundUp(places uint8) Decimal {
	if places >= d.prec {
		return d
	}
	return d.dirCore(places, true)
}

// RoundDown rounds d to places fractional digits toward zero, simply
// dropping the excess digits: 1.09 → 1.0 and -1.09 → -1.0 at one place.
// places ≥ d.Prec() returns d unchanged.
func (d Decimal) RoundDown(places uint8) Decimal {
	if places >= d.prec {
		return d
	}
	return d.truncCore(places)
}

// RoundCeil rounds d to places fractional digits toward +∞: 1.01 → 1.1 but
// -1.09 → -1.0 at one place. places ≥ d.Prec() returns d unchanged.
func (d Decimal) RoundCeil(places uint8) Decimal {
	if places >= d.prec {
		return d
	}
	return d.dirCore(places, !d.neg)
}

// RoundFloor rounds d to places fractional digits toward -∞: -1.01 → -1.1
// but 1.09 → 1.0 at one place. places ≥ d.Prec() returns d unchanged.
func (d Decimal) RoundFloor(places uint8) Decimal {
	if places >= d.prec {
		return d
	}
	return d.dirCore(places, d.neg)
}

// Truncate drops every fractional digit past places, identical to RoundDown.
// places ≥ d.Prec() returns d unchanged.
func (d Decimal) Truncate(places uint8) Decimal {
	if places >= d.prec {
		return d
	}
	return d.truncCore(places)
}

// Floor returns the largest integer value ≤ d: 2.5 → 2 and -2.5 → -3, with
// precision 0 unless d already had none.
func (d Decimal) Floor() Decimal {
	if d.prec == 0 {
		return d
	}
	return d.dirCore(0, d.neg)
}

// Ceil returns the smallest integer value ≥ d: 2.5 → 3 and -2.5 → -2, with
// precision 0 unless d already had none.
func (d Decimal) Ceil() Decimal {
	if d.prec == 0 {
		return d
	}
	return d.dirCore(0, !d.neg)
}
