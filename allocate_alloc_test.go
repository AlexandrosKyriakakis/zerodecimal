package zerodecimal

import (
	"errors"
	"testing"
)

// Package-level inputs and sinks for the Allocate and Split allocation gates
// (see alloc_test.go for why neither may be local).
var (
	allocateAllocTotal = MustNew(1_000, -2) // $10.00
	allocateAllocCents = MustNew(1_005, -2) // $10.05, inexact at one place
	allocateAllocFee   = []Decimal{MustNew(100, 0), MustNew(250, 0), MustNew(375, -1)}
	allocateAllocEqual = []Decimal{NewFromInt(1), NewFromInt(1), NewFromInt(1)}
	// 64 weights fill the larger stack buffer; 65 need the scratch buffer.
	allocateAlloc64 = allocateAllocWeights(64)
	allocateAlloc65 = allocateAllocWeights(65)
	// W in [2^64, 2^128): nineteen-place weights.
	allocateAllocMid = []Decimal{
		RequireFromString("1.2345678901234567891"),
		RequireFromString("9.8765432109876543219"),
		RequireFromString("0.5"),
	}
	// W ≥ 2^128: a huge whole weight beside a nineteen-place one.
	allocateAllocWide = []Decimal{RequireFromString("1e30"), RequireFromString("0.0000000000000000003")}
	allocateAllocNeg  = []Decimal{NewFromInt(1), NewFromInt(-1)}
	allocateAllocZero = []Decimal{{}, {}}
	// Totals past 2^128 cents (see TestLargestRemainderOverflowBoundary):
	// the largest truncated share is 2^128-2, 2^128-1 taking a leftover unit,
	// or 2^128; and 2^128-1 keeping its value after a no-write ranking.
	allocateAllocEdgeWeights = allocateInts(2, 2, 2, 2, 2, 2, 2, 2, 2, 1)
	// The same shares padded with zero weights to 65, past the stack buffer.
	allocateAllocEdgeWeights65 = append(allocateInts(2, 2, 2, 2, 2, 2, 2, 2, 2, 1), make([]Decimal, 55)...)
	allocateAllocBelow         = RequireFromString("323268248574891540290205877060179800882")
	allocateAllocEdgeOver      = RequireFromString("323268248574891540290205877060179800883")
	allocateAllocOver          = RequireFromString("323268248574891540290205877060179800884")
	allocateAllocKeepWeights   = allocateInts(6, 6, 6, 8, 8, 2, 2, 5, 8, 2)
	allocateAllocKeep          = RequireFromString("225437068085121732044485677423546440089")
	// Split: two shares of exactly 2^128-1, and seven of which five need 2^128.
	allocateAllocSplitFits = RequireFromString("68056473384187692692674921486353642291")
	allocateAllocSplitOver = RequireFromString("238197656844656924424362225202237748019")

	allocateAllocDst = make([]Decimal, 0, 65)
	allocateAllocOut []Decimal
	errAllocateAlloc error
)

func allocateAllocWeights(n int) []Decimal {
	ws := make([]Decimal, n)
	for i := range ws {
		ws[i] = NewFromInt(int64(1 + i%7))
	}
	return ws
}

// TestAllocateAllocs pins the documented allocation counts: none with enough
// dst capacity up to 64 weights, on every division path, past 2^128 units
// (including the no-write ranking that settles an edge share) and on every
// error path; exactly one scratch buffer when more weights are ranked, also
// when the ranking settles an ErrOverflow, but none past 64 weights for a
// zero total or an error found before ranking; and one more for a dst that
// must grow. Split never allocates beyond growing dst.
func TestAllocateAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	if allocatePathOf(allocateAllocMid) != "W<2^128" || allocatePathOf(allocateAllocWide) != "W>=2^128" {
		t.Fatal("the mid_sum and wide_sum inputs no longer take their division paths")
	}
	tests := []struct {
		name    string
		want    float64
		wantErr error
		fn      func()
	}{
		{"fee_split", 0, nil, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocTotal, allocateAllocFee, 2)
		}},
		{"equal_weights", 0, nil, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocTotal, allocateAllocEqual, 2)
		}},
		{"stack_64", 0, nil, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocTotal, allocateAlloc64, 2)
		}},
		{"mid_sum", 0, nil, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocTotal, allocateAllocMid, 2)
		}},
		{"wide_sum", 0, nil, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocTotal, allocateAllocWide, 2)
		}},
		{"past_2^128_units", 0, nil, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocBelow, allocateAllocEdgeWeights, 1)
		}},
		{"edge_keeps_value", 0, nil, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocKeep, allocateAllocKeepWeights, 1)
		}},
		{"zero_total", 0, nil, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, Decimal{}, allocateAlloc65, 2)
		}},
		{"scratch_65", 1, nil, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocTotal, allocateAlloc65, 2)
		}},
		{"grow_dst", 1, nil, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(nil, allocateAllocTotal, allocateAllocFee, 2)
		}},
		{"places_error", 0, ErrPrecOutOfRange, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocTotal, allocateAlloc65, MaxPrec+1)
		}},
		{"negative_weight_error", 0, ErrNegativeWeight, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocTotal, allocateAllocNeg, 2)
		}},
		{"zero_weight_sum_error", 0, ErrDivideByZero, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocTotal, allocateAllocZero, 2)
		}},
		{"inexact_error", 0, ErrInexact, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocCents, allocateAlloc65, 1)
		}},
		{"overflow_error", 0, ErrOverflow, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocOver, allocateAllocEdgeWeights, 1)
		}},
		{"edge_overflow_error", 0, ErrOverflow, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocEdgeOver, allocateAllocEdgeWeights, 1)
		}},
		{"overflow_error_65", 0, ErrOverflow, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocOver, allocateAllocEdgeWeights65, 1)
		}},
		{"edge_overflow_error_65", 1, ErrOverflow, func() {
			allocateAllocOut, errAllocateAlloc = Allocate(allocateAllocDst, allocateAllocEdgeOver, allocateAllocEdgeWeights65, 1)
		}},
		{"split", 0, nil, func() {
			allocateAllocOut, errAllocateAlloc = Split(allocateAllocDst, allocateAllocTotal, 65, 2)
		}},
		{"split_grow_dst", 1, nil, func() {
			allocateAllocOut, errAllocateAlloc = Split(nil, allocateAllocTotal, 3, 2)
		}},
		{"split_error", 0, ErrDivideByZero, func() {
			allocateAllocOut, errAllocateAlloc = Split(allocateAllocDst, allocateAllocTotal, 0, 2)
		}},
		{"split_past_2^128_units", 0, nil, func() {
			allocateAllocOut, errAllocateAlloc = Split(allocateAllocDst, allocateAllocSplitFits, 2, 1)
		}},
		{"split_overflow_error", 0, ErrOverflow, func() {
			allocateAllocOut, errAllocateAlloc = Split(allocateAllocDst, allocateAllocSplitOver, 7, 1)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, tc.want, tc.fn)
			if !errors.Is(errAllocateAlloc, tc.wantErr) {
				t.Fatalf("measured the wrong path: err = %v, want %v", errAllocateAlloc, tc.wantErr)
			}
		})
	}
}
