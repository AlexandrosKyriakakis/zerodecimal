package zerodecimal

import (
	"fmt"
	"testing"
)

// Package-level sinks for the IsRounded and FracDigits allocation gate (see
// alloc_test.go for why results must not stay local).
var (
	isRoundedAllocBool   bool
	isRoundedAllocDigits uint8
)

// TestIsRoundedFracDigitsAllocs holds both predicates to exactly zero
// allocations over the shared allocation matrix and over every shape the
// benchmarks use. There is no error path: both are infallible, so the gate has
// one closure per method and places value.
func TestIsRoundedFracDigitsAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	// places below, at and above every precision in the matrices.
	places := []uint8{0, 2, 18, 19, 255}
	gate := func(name string, a, b Decimal) {
		for _, p := range places {
			t.Run(fmt.Sprintf("is_rounded/%s/places_%d", name, p), func(t *testing.T) {
				requireAllocs(t, 0, func() {
					isRoundedAllocBool = a.IsRounded(p)
					isRoundedAllocBool = b.IsRounded(p)
				})
			})
		}
		t.Run("frac_digits/"+name, func(t *testing.T) {
			requireAllocs(t, 0, func() {
				isRoundedAllocDigits = a.FracDigits()
				isRoundedAllocDigits = b.FracDigits()
			})
		})
	}
	for _, s := range allocShapes {
		gate("alloc_"+s.name, s.a, s.b)
	}
	for _, s := range isRoundedShapes {
		gate(s.name, s.d, s.d.Neg())
	}
}
