//go:build zerodecimal_prec9

package zerodecimal

// DefaultPrec is lowered to 9 fractional digits (nanos), trading fractional
// resolution for integer range in division results.
const DefaultPrec uint8 = 9
