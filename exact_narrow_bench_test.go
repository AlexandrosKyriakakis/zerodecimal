package zerodecimal

import "testing"

// Package-level operands and sinks for the direct-round allocation gate and
// benchmarks (see alloc_test.go for why neither may be local).
var (
	directQty      = MustNew(1_500_000, -6)
	directPrice    = MustNew(5_012_345, -2)
	directCentsQty = MustNew(12345, -2).MustMul(MustNew(1_500_000, -6))
	directTotalQty = MustNew(2_000_000, -6)
	directWideA    = RequireFromString("12345678901234567890.123456789")
	directWideB    = RequireFromString("98765432109.8765432109")
	directSink     Decimal
	errDirect      error
)

// BenchmarkDirectRoundShapes times MulRound and DivRound on the operand
// widths their division paths dispatch on, each next to the legacy chain it
// replaces where that chain is exact up to the final rounding (one rounding
// either way, so the rows compare like for like).
func BenchmarkDirectRoundShapes(b *testing.B) {
	mulCases := []struct {
		name   string
		a, b   Decimal
		places uint8
	}{
		// qty (6dp) × price (2dp) to cents: a one-limb product, k = 6.
		{"qty_x_price/one_limb", directQty, directPrice, 2},
		// a two-limb product, k = 10.
		{"two_limb", RequireFromString("123456789.123456789"), RequireFromString("7.000000001"), 8},
		// a product past 128 bits, k = 17.
		{"wide_product", directWideA, directWideB, 2},
		// 19dp × 19dp to cents: k = 36, two reciprocal passes.
		{"k_past_max_prec", RequireFromString("0.1234567890123456789"), RequireFromString("0.9876543210987654321"), 2},
	}
	for _, c := range mulCases {
		x, y, p := c.a, c.b, c.places
		b.Run("MulRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				directSink, errDirect = x.MulRound(y, p, ToNearestEven)
			}
		})
	}
	b.Run("Mul+RoundBank/qty_x_price/one_limb", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			directSink = directQty.MustMul(directPrice).RoundBank(2)
		}
	})

	divCases := []struct {
		name   string
		a, b   Decimal
		places uint8
	}{
		// cents×qty / qty to cents (a pro-rata share): one-limb dividend.
		{"share/one_limb", directCentsQty, directTotalQty, 2},
		// 1.5/3.7 at 19 digits: two-limb dividend, one-limb quotient.
		{"one_limb_quotient", RequireFromString("1.5"), RequireFromString("3.7"), 19},
		// 1234.5678/3 at 19 digits: a two-limb quotient.
		{"two_limb_quotient", RequireFromString("1234.5678"), NewFromInt(3), 19},
		// two-limb divisor.
		{"wide_divisor", directWideA, directWideB, 19},
	}
	for _, c := range divCases {
		x, y, p := c.a, c.b, c.places
		b.Run("DivRound/"+c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				directSink, errDirect = x.DivRound(y, p, ToNearestEven)
			}
		})
	}
	b.Run("DivExact/one_eighth", func(b *testing.B) {
		one, eight := NewFromInt(1), NewFromInt(8)
		b.ReportAllocs()
		for b.Loop() {
			directSink, errDirect = one.DivExact(eight)
		}
	})
}
