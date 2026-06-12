//go:build zerodecimal_prec12

package zerodecimal

// DefaultPrec is lowered to 12 fractional digits, matching alpacadecimal's
// fixed scale for drop-in interoperability.
const DefaultPrec uint8 = 12
