package zerodecimal

import (
	"errors"
	"testing"
)

// Sinks and run-time inputs for the RoundTo allocation gates. The modes live
// in package variables, not literals, so the measured closures load them like
// a caller reading a configured mode would (see alloc_test.go for the rules
// every gate here follows).
var (
	// One result and one error sink per call in a measured closure, so every
	// call's error can be asserted after the measurement.
	roundToAllocResult  Decimal
	roundToAllocResult2 Decimal
	errRoundToAlloc     error
	errRoundToAlloc2    error

	roundToAllocModes = [...]struct {
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
	roundToAllocBadModes = [...]struct {
		name string
		mode RoundingMode
	}{
		{"first_invalid", TowardNegative + 1},
		{"max_uint8", ^RoundingMode(0)},
	}
)

// TestRoundToAllocs gates RoundTo and MustRoundTo at exactly zero allocations
// on every value shape: each valid mode rounding down and at a no-op places,
// and the ErrInvalidRoundingMode path both when a rounding would have happened
// and when places is at or above the precision. The error of each call in a
// measured closure is asserted after the measurement, not just the last one.
func TestRoundToAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, s := range allocShapes {
		for _, m := range roundToAllocModes {
			t.Run(s.name+"/success/"+m.name, func(t *testing.T) {
				requireAllocs(t, 0, func() {
					roundToAllocResult, errRoundToAlloc = s.a.RoundTo(2, m.mode)
					roundToAllocResult2, errRoundToAlloc2 = s.b.RoundTo(MaxPrec, m.mode)
				})
				if errRoundToAlloc != nil || errRoundToAlloc2 != nil {
					t.Fatalf("valid mode %d failed: rounding call %v, no-op call %v",
						m.mode, errRoundToAlloc, errRoundToAlloc2)
				}
			})
			t.Run(s.name+"/must/"+m.name, func(t *testing.T) {
				requireAllocs(t, 0, func() {
					roundToAllocResult = s.a.MustRoundTo(2, m.mode)
					roundToAllocResult2 = s.b.MustRoundTo(0, m.mode)
				})
			})
		}
		for _, m := range roundToAllocBadModes {
			t.Run(s.name+"/invalid/"+m.name, func(t *testing.T) {
				requireAllocs(t, 0, func() {
					roundToAllocResult, errRoundToAlloc = s.a.RoundTo(2, m.mode)
					roundToAllocResult2, errRoundToAlloc2 = s.b.RoundTo(MaxPrec, m.mode)
				})
				if !errors.Is(errRoundToAlloc, ErrInvalidRoundingMode) || !errors.Is(errRoundToAlloc2, ErrInvalidRoundingMode) {
					t.Fatalf("invalid mode %d: got %v (rounding call) and %v (no-op call), want ErrInvalidRoundingMode from both",
						m.mode, errRoundToAlloc, errRoundToAlloc2)
				}
			})
		}
	}
}

// TestRoundToMustPanicAllocs gates the panic path of MustRoundTo: raising the
// sentinel and recovering it allocates nothing, because the sentinel is an
// existing value and the panic argument is not boxed.
func TestRoundToMustPanicAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	var recovered any
	bad := roundToAllocBadModes[0].mode
	for _, s := range allocShapes {
		t.Run(s.name, func(t *testing.T) {
			requireAllocs(t, 0, func() {
				defer func() { recovered = recover() }()
				roundToAllocResult = s.a.MustRoundTo(2, bad)
			})
			if err, ok := recovered.(error); !ok || !errors.Is(err, ErrInvalidRoundingMode) {
				t.Fatalf("panic value: got %v, want ErrInvalidRoundingMode", recovered)
			}
		})
	}
}
