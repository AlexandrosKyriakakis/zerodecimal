package zerodecimal

import "testing"

var (
	quoremAllocQ   Decimal
	quoremAllocR   Decimal
	errQuoremAlloc error

	quoremAlignmentAllocCases = []struct {
		name string
		d, e Decimal
	}{
		{
			name: "same_precision_fast",
			d:    newDecimal(u128{lo: 123_456_789}, false, 4),
			e:    newDecimal(u128{lo: 97}, false, 4),
		},
		{
			name: "wide_aligned_divisor",
			d:    newDecimal(u128{hi: ^uint64(0), lo: ^uint64(0)}, true, MaxPrec),
			e:    newDecimal(u128{hi: 1 << 63}, false, 0),
		},
	}
)

func TestQuoRemAlignmentExactAllocations(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are not meaningful under the race detector")
	}
	for _, tc := range quoremAlignmentAllocCases {
		t.Run("QuoRem/"+tc.name, func(t *testing.T) {
			_, _, err := tc.d.QuoRem(tc.e)
			if err != nil {
				t.Fatalf("preflight QuoRem: %v", err)
			}
			got := testing.AllocsPerRun(1_000, func() {
				quoremAllocQ, quoremAllocR, errQuoremAlloc = tc.d.QuoRem(tc.e)
			})
			if got != 0 {
				t.Fatalf("QuoRem allocations per run: got %v, want exactly 0", got)
			}
		})
		t.Run("Mod/"+tc.name, func(t *testing.T) {
			_, err := tc.d.Mod(tc.e)
			if err != nil {
				t.Fatalf("preflight Mod: %v", err)
			}
			got := testing.AllocsPerRun(1_000, func() {
				quoremAllocR, errQuoremAlloc = tc.d.Mod(tc.e)
			})
			if got != 0 {
				t.Fatalf("Mod allocations per run: got %v, want exactly 0", got)
			}
		})
	}
}
