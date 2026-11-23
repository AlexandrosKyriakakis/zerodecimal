package zerodecimal

import "testing"

// Package-level operands and sinks for BenchmarkRoundTo (see alloc_test.go for
// why neither may be local).
var (
	roundToBenchSink Decimal
	errRoundToBench  error

	roundToBenchShapes = [...]struct {
		name   string
		d      Decimal
		places uint8
	}{
		// A price at 6 places rounded to cents: the one-limb path, dominant in
		// money code.
		{"price_6dp_to_2dp", MustNew(123_456_789, -6), 2},
		// A two-limb coefficient at 19 places rounded to cents.
		{"wide_19dp_to_2dp", RequireFromString("12345678901234567890.1234567890123456789"), 2},
		// places equal to the precision: only the shortcut runs.
		{"noop_6dp_to_6dp", MustNew(123_456_789, -6), 6},
	}
	roundToBenchModes = [...]struct {
		name string
		mode RoundingMode
	}{
		{"to_nearest_away", ToNearestAway},
		{"to_nearest_even", ToNearestEven},
		{"away_from_zero", AwayFromZero},
		{"toward_zero", TowardZero},
		{"toward_positive", TowardPositive},
		{"toward_negative", TowardNegative},
	}
)

// BenchmarkRoundTo times RoundTo against the named method of the same mode.
// The named rows are what a caller that knows its mode at compile time writes:
// the wrapper inlines and the call into the core is static. The RoundTo rows
// take the mode as a run-time value, as a caller reading it from configuration
// does. The name keys let benchstat split the columns with -col /impl.
func BenchmarkRoundTo(b *testing.B) {
	for _, s := range roundToBenchShapes {
		for _, m := range roundToBenchModes {
			b.Run("impl=RoundTo/shape="+s.name+"/mode="+m.name, func(b *testing.B) {
				x, places, mode := s.d, s.places, m.mode
				b.ReportAllocs()
				for b.Loop() {
					roundToBenchSink, errRoundToBench = x.RoundTo(places, mode)
				}
			})
			b.Run("impl=named/shape="+s.name+"/mode="+m.name, func(b *testing.B) {
				benchRoundToNamed(b, s.d, s.places, m.mode)
			})
		}
	}
	// MustRoundTo adds only the error test to RoundTo.
	b.Run("impl=MustRoundTo/shape=price_6dp_to_2dp/mode=to_nearest_even", func(b *testing.B) {
		x, places, mode := roundToBenchShapes[0].d, roundToBenchShapes[0].places, ToNearestEven
		b.ReportAllocs()
		for b.Loop() {
			roundToBenchSink = x.MustRoundTo(places, mode)
		}
	})
	// The error path: a mode past TowardNegative returns before any work.
	b.Run("impl=RoundTo/shape=price_6dp_to_2dp/mode=invalid", func(b *testing.B) {
		x, places, mode := roundToBenchShapes[0].d, roundToBenchShapes[0].places, TowardNegative+1
		b.ReportAllocs()
		for b.Loop() {
			roundToBenchSink, errRoundToBench = x.RoundTo(places, mode)
		}
	})
}

// benchRoundToNamed times the named method of mode with the call static.
func benchRoundToNamed(b *testing.B, x Decimal, places uint8, mode RoundingMode) {
	b.ReportAllocs()
	switch mode {
	case ToNearestAway:
		for b.Loop() {
			roundToBenchSink = x.Round(places)
		}
	case ToNearestEven:
		for b.Loop() {
			roundToBenchSink = x.RoundBank(places)
		}
	case AwayFromZero:
		for b.Loop() {
			roundToBenchSink = x.RoundUp(places)
		}
	case TowardZero:
		for b.Loop() {
			roundToBenchSink = x.RoundDown(places)
		}
	case TowardPositive:
		for b.Loop() {
			roundToBenchSink = x.RoundCeil(places)
		}
	case TowardNegative:
		for b.Loop() {
			roundToBenchSink = x.RoundFloor(places)
		}
	}
}
