package zerodecimal

import "testing"

// Package-level operands and sinks for the RoundToMultiple allocation gate and
// benchmarks (see alloc_test.go for why neither may be local).
var (
	roundToMultiplePrice    = MustNew(10_137, -2)                         // a $101.37 limit price
	roundToMultipleTick     = MustNew(25, -2)                             // on a $0.25 tick
	roundToMultipleEntitled = MustNew(333_333_333, -7)                    // 33.3333333 shares to credit
	roundToMultipleQuotient = RequireFromString("33.3333333333333333333") // the same at 19 places, as Div returns it
	roundToMultipleRule     = MustNew(1, -3)                              // credited in 0.001-share units
	roundToMultipleQty      = NewFromInt(1_234)                           // an order quantity
	roundToMultipleBlock    = NewFromInt(100)                             // a board quantity of 100 shares
	roundToMultipleWideD    = RequireFromString("12345678901234567890.123456789")
	roundToMultipleMax      = Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	roundToMultipleFinest   = MustNew(1, -19)
	roundToMultipleZero     Decimal
	roundToMultipleSink     Decimal
	errRoundToMultiple      error
)

// BenchmarkRoundToMultiple times RoundToMultiple against the chains a caller
// writes without it. DivRound to 0 places then MulExact by the step returns the
// same value and representation for a positive step when the result fits 128
// bits at the step's precision; past that, MulExact drops trailing zeros to fit
// where RoundToMultiple reports ErrOverflow. Sub of Mod, the T-division idiom,
// is the TowardZero multiple at precision max(d.Prec(), step.Prec()).
func BenchmarkRoundToMultiple(b *testing.B) {
	cases := []struct {
		name    string
		d, step Decimal
		mode    RoundingMode
	}{
		// a limit price to a 0.25 tick, same precision: the one-limb path.
		{"tick", roundToMultiplePrice, roundToMultipleTick, ToNearestEven},
		// fractional shares to 0.001, d finer than the step: one-limb path.
		{"share_increment", roundToMultipleEntitled, roundToMultipleRule, TowardZero},
		// the same at 19 places, a two-limb d: the wide path, one-limb divisor.
		{"share_increment_19dp", roundToMultipleQuotient, roundToMultipleRule, TowardZero},
		// an integer quantity to a board quantity: one-limb path.
		{"block", roundToMultipleQty, roundToMultipleBlock, TowardZero},
		// a two-limb d: the wide path.
		{"wide", roundToMultipleWideD, roundToMultipleTick, ToNearestEven},
	}
	for _, c := range cases {
		d, s, m := c.d, c.step, c.mode
		b.Run("RoundToMultiple/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				roundToMultipleSink, errRoundToMultiple = d.RoundToMultiple(s, m)
			}
		})
		b.Run("DivRound+MulExact/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				q, err := d.DivRound(s, 0, m)
				if err != nil {
					b.Fatal(err)
				}
				roundToMultipleSink, errRoundToMultiple = q.MulExact(s)
			}
		})
	}
	for _, c := range cases {
		if c.mode != TowardZero {
			continue
		}
		d, s := c.d, c.step
		b.Run("Sub+Mod/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				roundToMultipleSink = d.MustSub(d.MustMod(s))
			}
		})
	}
}
