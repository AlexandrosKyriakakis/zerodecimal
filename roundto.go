package zerodecimal

// RoundTo rounds d to places fractional digits under mode, a value chosen at
// run time: the form of the rounding family for a scale and a mode that
// arrive as configuration. Each mode returns exactly what its named method
// returns, representation for representation:
//
//	ToNearestAway  ≡ Round
//	ToNearestEven  ≡ RoundBank
//	AwayFromZero   ≡ RoundUp
//	TowardZero     ≡ RoundDown and Truncate
//	TowardPositive ≡ RoundCeil
//	TowardNegative ≡ RoundFloor
//
// places ≥ d.Prec() returns d unchanged, because rounding never adds
// precision. There is no ErrPrecOutOfRange: like Round, RoundTo only lowers
// precision, so places may be any uint8. A rounding that leaves a nonzero
// value carries precision places, and a zero result is the canonical Decimal{}.
//
// The only error is ErrInvalidRoundingMode, for a mode that is none of the six
// RoundingMode constants. It is checked first, as in MulRound and DivRound, so
// a bad mode fails even where places ≥ d.Prec() would have returned d; the
// failure returns the zero Decimal. RoundTo never allocates, on success or
// failure.
func (d Decimal) RoundTo(places uint8, mode RoundingMode) (Decimal, error) {
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	if places >= d.prec {
		return d, nil
	}
	// The cores are the ones the named wrappers call, with the same
	// sign-derived predicates for the directional modes (see dirCore), so each
	// arm is its method's body. The dispatch itself is too large to inline;
	// callers that know the mode at compile time get the inlinable wrappers by
	// calling the named method instead.
	switch mode {
	case ToNearestAway:
		return d.roundHalfAwayCore(places), nil
	case ToNearestEven:
		return d.roundBankCore(places), nil
	case AwayFromZero:
		return d.dirCore(places, true), nil
	case TowardPositive:
		return d.dirCore(places, !d.neg), nil
	case TowardNegative:
		return d.dirCore(places, d.neg), nil
	default: // TowardZero: valid() has ruled out every other value
		return d.truncCore(places), nil
	}
}

// MustRoundTo is RoundTo for a mode known to be valid: it panics with
// ErrInvalidRoundingMode instead of returning it.
func (d Decimal) MustRoundTo(places uint8, mode RoundingMode) Decimal {
	r, err := d.RoundTo(places, mode)
	if err != nil {
		panic(err)
	}
	return r
}
