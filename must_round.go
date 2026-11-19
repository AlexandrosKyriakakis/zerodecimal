package zerodecimal

// Panicking twins of the directly rounded operations, for call sites with
// proven operand bounds and constant places and mode. Each is its erroring
// form plus one branch and follows the contract of every other Must method: it
// panics exactly when the erroring form returns an error, with that bare
// sentinel as the panic value (match it with errors.Is after recover), and
// otherwise returns the identical result.

// MustMulRound is MulRound for operands with proven bounds: it panics on error.
// The panic value is the sentinel MulRound would return: ErrPrecOutOfRange,
// ErrInvalidRoundingMode, or ErrOverflow.
func (d Decimal) MustMulRound(e Decimal, places uint8, mode RoundingMode) Decimal {
	r, err := d.MulRound(e, places, mode)
	if err != nil {
		panic(err)
	}
	return r
}

// MustDivRound is DivRound for operands with proven bounds: it panics on error.
// The panic value is the sentinel DivRound would return: ErrPrecOutOfRange,
// ErrInvalidRoundingMode, ErrDivideByZero, or ErrOverflow.
func (d Decimal) MustDivRound(e Decimal, places uint8, mode RoundingMode) Decimal {
	r, err := d.DivRound(e, places, mode)
	if err != nil {
		panic(err)
	}
	return r
}

// MustMulAddRound is MulAddRound for operands with proven bounds: it panics on
// error. The panic value is the sentinel MulAddRound would return:
// ErrPrecOutOfRange, ErrInvalidRoundingMode, or ErrOverflow.
func (d Decimal) MustMulAddRound(e, f Decimal, places uint8, mode RoundingMode) Decimal {
	r, err := d.MulAddRound(e, f, places, mode)
	if err != nil {
		panic(err)
	}
	return r
}

// MustMulDivRound is MulDivRound for operands with proven bounds: it panics on
// error. The panic value is the sentinel MulDivRound would return:
// ErrPrecOutOfRange, ErrInvalidRoundingMode, ErrDivideByZero, or ErrOverflow.
func (d Decimal) MustMulDivRound(e, f Decimal, places uint8, mode RoundingMode) Decimal {
	r, err := d.MulDivRound(e, f, places, mode)
	if err != nil {
		panic(err)
	}
	return r
}

// MustAvgRound is AvgRound for operands with proven bounds: it panics on error.
// The panic value is the sentinel AvgRound would return: ErrPrecOutOfRange,
// ErrInvalidRoundingMode, or ErrOverflow.
func MustAvgRound(first Decimal, places uint8, mode RoundingMode, rest ...Decimal) Decimal {
	r, err := AvgRound(first, places, mode, rest...)
	if err != nil {
		panic(err)
	}
	return r
}
