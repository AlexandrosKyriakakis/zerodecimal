//go:build !zerodecimal_prec9 && !zerodecimal_prec12

package zerodecimal

// DefaultPrec is the truncation cap targeted by the legacy Mul, Div, and Avg
// operations. Strict parsing and the Exact/Round arithmetic APIs are not bound
// by it and accept or request up to MaxPrec fractional digits under every
// build tag. It is a compile-time constant, never a runtime knob; build with
// zerodecimal_prec9 or zerodecimal_prec12 to lower it.
const DefaultPrec uint8 = 19
