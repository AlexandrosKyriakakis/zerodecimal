package zerodecimal

import (
	"runtime"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// TestAllocsStringFixedBoundaries covers the complete output-width edges
// that previously grew StringFixed's fixed 48-byte staging buffer. Every
// returned string owns exactly one allocation, while AppendFixed reuses a
// caller buffer whose capacity is exactly (not generously) sufficient.
func TestAllocsStringFixedBoundaries(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}

	maxCoef := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	maxPrec := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}, neg: true, prec: MaxPrec}
	tests := []struct {
		name   string
		d      Decimal
		places uint8
	}{
		{name: "zero_places_0", d: Zero, places: 0},
		{name: "max_coef_places_0", d: maxCoef, places: 0},
		{name: "negative_max_coef_prec_19_places_19", d: maxPrec, places: 19},
		{name: "one_point_five_places_47", d: RequireFromString("1.5"), places: 47},
		{name: "negative_max_coef_places_48", d: maxCoef.Neg(), places: 48},
		{name: "max_prec_places_100", d: maxPrec, places: 100},
		{name: "max_coef_places_255", d: maxCoef, places: 255},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := fixedOracle(tc.d, tc.places)
			requireAllocs(t, 1, func() {
				sinkString = tc.d.StringFixed(tc.places)
			})

			const prefix = "amount="
			dst := make([]byte, len(prefix), len(prefix)+len(want))
			copy(dst, prefix)
			data := unsafe.SliceData(dst)
			got := tc.d.AppendFixed(dst, tc.places)
			require.Equal(t, prefix+want, string(got))
			require.Equal(t, data, unsafe.SliceData(got),
				"AppendFixed must preserve the backing allocation at exact capacity")

			requireAllocs(t, 0, func() {
				sinkBytes = tc.d.AppendFixed(dst[:len(prefix)], tc.places)
			})
		})
	}
}

// TestAllocsStringFixedEveryPlacesValue closes the gaps between the named
// boundaries above: every value of the uint8 places domain must preserve the
// same one-allocation StringFixed and zero-allocation pre-sized AppendFixed
// contracts.
func TestAllocsStringFixedEveryPlacesValue(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}

	d := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}, neg: true, prec: MaxPrec}
	for places := 0; places <= 255; places++ {
		p := uint8(places)
		if got := testing.AllocsPerRun(allocRuns, func() {
			sinkString = d.StringFixed(p)
		}); got != 1 {
			t.Fatalf("StringFixed allocations at places=%d: got %v, want exactly 1", p, got)
		}

		want := fixedOracle(d, p)
		dst := make([]byte, 0, len(want))
		if got := testing.AllocsPerRun(allocRuns, func() {
			sinkBytes = d.AppendFixed(dst[:0], p)
		}); got != 0 {
			t.Fatalf("AppendFixed allocations at places=%d with exact capacity: got %v, want 0", p, got)
		}
	}
}

// TestStringFixedResultLifetime exercises the zero-copy []byte-to-string
// ownership transfer across collections and many subsequent formatter calls.
// The original result must remain stable; no stack or reusable-buffer alias
// may back the returned string.
func TestStringFixedResultLifetime(t *testing.T) {
	d := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}, neg: true, prec: MaxPrec}
	want := fixedOracle(d, 255)
	got := d.StringFixed(255)

	for i := range 10_000 {
		places := uint8(i)
		sinkString = NewFromInt(int64(i)).StringFixed(places)
	}
	runtime.GC()
	runtime.GC()
	require.Equal(t, want, got)
}
