package zerodecimal

import "testing"

var (
	aggregateAllocResult Decimal
	errAggregateAlloc    error

	aggregateAllocOrdinary = []Decimal{
		RequireFromString("1234.5678"),
		RequireFromString("-34.5678"),
		RequireFromString("0.0000000000000000001"),
	}
	aggregateAllocCancellation = func() []Decimal {
		maxValue, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 0)
		if err != nil {
			panic(err)
		}
		return []Decimal{maxValue, maxValue, maxValue.Neg()}
	}()
	aggregateAllocOverflow = append(append([]Decimal(nil), aggregateAllocCancellation...), One)
	aggregateAllocLong     = func() []Decimal {
		xs := make([]Decimal, 1024)
		for i := range xs {
			xs[i] = NewFromInt(int64(i%17) - 8)
		}
		return xs
	}()
)

func TestAggregateAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	tests := []struct {
		name string
		fn   func()
	}{
		{name: "sum_ordinary", fn: func() {
			aggregateAllocResult, errAggregateAlloc = Sum(aggregateAllocOrdinary[0], aggregateAllocOrdinary[1:]...)
		}},
		{name: "sum_cancellation", fn: func() {
			aggregateAllocResult, errAggregateAlloc = Sum(aggregateAllocCancellation[0], aggregateAllocCancellation[1:]...)
		}},
		{name: "sum_final_overflow", fn: func() {
			aggregateAllocResult, errAggregateAlloc = Sum(aggregateAllocOverflow[0], aggregateAllocOverflow[1:]...)
		}},
		{name: "sum_long", fn: func() {
			aggregateAllocResult, errAggregateAlloc = Sum(aggregateAllocLong[0], aggregateAllocLong[1:]...)
		}},
		{name: "avg_wide_intermediate", fn: func() {
			aggregateAllocResult, errAggregateAlloc = Avg(aggregateAllocCancellation[0], aggregateAllocCancellation[1:]...)
		}},
		{name: "avg_long", fn: func() {
			aggregateAllocResult, errAggregateAlloc = Avg(aggregateAllocLong[0], aggregateAllocLong[1:]...)
		}},
		{name: "avg_exact_success", fn: func() {
			aggregateAllocResult, errAggregateAlloc = AvgExact(One, NewFromInt(2))
		}},
		{name: "avg_exact_inexact", fn: func() {
			aggregateAllocResult, errAggregateAlloc = AvgExact(One, NewFromInt(2), NewFromInt(4))
		}},
		{name: "avg_exact_long", fn: func() {
			aggregateAllocResult, errAggregateAlloc = AvgExact(aggregateAllocLong[0], aggregateAllocLong[1:]...)
		}},
		{name: "avg_round", fn: func() {
			aggregateAllocResult, errAggregateAlloc = AvgRound(aggregateAllocOrdinary[0], 4, ToNearestEven, aggregateAllocOrdinary[1:]...)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, 0, tc.fn)
		})
	}
}
