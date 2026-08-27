//go:build zerodecimal_prec9

package zerodecimal

// DefaultPrec is lowered to 9 fractional digits for legacy Mul, Div, and Avg
// truncation. Strict parsing and Exact/Round arithmetic still use MaxPrec.
const DefaultPrec uint8 = 9
