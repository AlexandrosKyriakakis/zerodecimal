package zerodecimal

// Rounding modes consumed by roundTo. They stay untyped constants so every
// exported wrapper passes a compile-time mode.
const (
	roundHalfAway = iota // ties away from zero, others to nearest
	roundHalfEven        // ties to even, others to nearest
	roundAway            // any remainder steps away from zero
	roundToCeil          // toward +∞: positive remainders step up
	roundToFloor         // toward -∞: negative remainders step up in magnitude
	roundTrunc           // toward zero: remainders are dropped
)

// roundTo is the single core behind every exported rounding method: it
// reduces d to places fractional digits under the given mode. places ≥
// d.prec returns d unchanged — rounding never adds precision. Otherwise the
// k = d.prec - places excess digits split off as a remainder r < 10^k and
// the mode decides whether the kept magnitude steps up by one unit. The
// half-point 10^k/2 is exact (10^k is even for k ≥ 1) and the step can never
// overflow (the quotient is at most (2^128-1)/10), so every mode is
// infallible. Results have precision places and zero-normalize, so rounding
// a negative value to zero yields the canonical unsigned Decimal{}.
func (d Decimal) roundTo(places uint8, mode int) Decimal {
	if places >= d.prec {
		return d
	}
	k := d.prec - places
	q, r := divmod128Pow10(d.coef, k)
	half := pow10u64[k&31] >> 1
	var up bool
	switch mode {
	case roundHalfAway:
		up = r >= half
	case roundHalfEven:
		up = r > half || (r == half && q.lo&1 == 1)
	case roundAway:
		up = r != 0
	case roundToCeil:
		up = !d.neg && r != 0
	case roundToFloor:
		up = d.neg && r != 0
	}
	if up {
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
	return d.roundTo(places, roundHalfAway)
}

// RoundBank rounds d to places fractional digits with ties to even
// (banker's rounding): 2.5 → 2, 3.5 → 4, -2.5 → -2. places ≥ d.Prec()
// returns d unchanged.
func (d Decimal) RoundBank(places uint8) Decimal {
	return d.roundTo(places, roundHalfEven)
}

// RoundUp rounds d to places fractional digits away from zero: any dropped
// nonzero remainder steps the magnitude up, so 1.01 → 1.1 and -1.01 → -1.1
// at one place. places ≥ d.Prec() returns d unchanged.
func (d Decimal) RoundUp(places uint8) Decimal {
	return d.roundTo(places, roundAway)
}

// RoundDown rounds d to places fractional digits toward zero, simply
// dropping the excess digits: 1.09 → 1.0 and -1.09 → -1.0 at one place.
// places ≥ d.Prec() returns d unchanged.
func (d Decimal) RoundDown(places uint8) Decimal {
	return d.roundTo(places, roundTrunc)
}

// RoundCeil rounds d to places fractional digits toward +∞: 1.01 → 1.1 but
// -1.09 → -1.0 at one place. places ≥ d.Prec() returns d unchanged.
func (d Decimal) RoundCeil(places uint8) Decimal {
	return d.roundTo(places, roundToCeil)
}

// RoundFloor rounds d to places fractional digits toward -∞: -1.01 → -1.1
// but 1.09 → 1.0 at one place. places ≥ d.Prec() returns d unchanged.
func (d Decimal) RoundFloor(places uint8) Decimal {
	return d.roundTo(places, roundToFloor)
}

// Truncate drops every fractional digit past places, identical to RoundDown.
// places ≥ d.Prec() returns d unchanged.
func (d Decimal) Truncate(places uint8) Decimal {
	return d.roundTo(places, roundTrunc)
}

// Floor returns the largest integer value ≤ d: 2.5 → 2 and -2.5 → -3, with
// precision 0 unless d already had none.
func (d Decimal) Floor() Decimal {
	return d.roundTo(0, roundToFloor)
}

// Ceil returns the smallest integer value ≥ d: 2.5 → 3 and -2.5 → -2, with
// precision 0 unless d already had none.
func (d Decimal) Ceil() Decimal {
	return d.roundTo(0, roundToCeil)
}
