//go:build !zerodecimal_prec9 && !zerodecimal_prec12

package zerodecimal

// DefaultPrec is the target number of fractional digits for division results
// and the cap Mul rescales products down to; strict parsing is not bound by it
// and accepts up to MaxPrec fractional digits under every build tag. It is a
// compile-time constant (never a runtime knob) so precision checks fold to
// immediate compares; build with -tags zerodecimal_prec9 or zerodecimal_prec12
// to lower it.
const DefaultPrec uint8 = 19
