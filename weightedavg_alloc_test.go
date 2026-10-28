package zerodecimal

import (
	"errors"
	"testing"
)

// Package-level fixtures and sinks for the WeightedAvg allocation gates (see
// alloc_test.go for why neither may be local). Streaming closures copy a
// fixture into wavgAllocAvg first, so every run takes the same path.
var (
	wavgAllocAvg    WeightedAvg
	wavgAllocResult Decimal
	errWavgAlloc    error

	wavgAllocMax    = Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	wavgAllocCents  = RequireFromString("19.99")
	wavgAllocQty    = RequireFromString("1.5")
	wavgAllocPrice  = RequireFromString("50123.4567890123456")
	wavgAllocWideBy = RequireFromString("61728394506172839450.75")

	// wavgAllocVWAP holds two fills on opposite sides at 2 and 19 places.
	wavgAllocVWAP = wavgAllocBuild(func(w *WeightedAvg) {
		w.Add(wavgAllocCents, wavgAllocQty)
		w.Add(wavgAllocPrice, RequireFromString("-0.25"))
	})
	// wavgAllocWide holds 2·(2^128-1) over 123456789012345678901.5: Total
	// overflows, and Avg and AvgRound divide the wide numerator at
	// DefaultPrec.
	wavgAllocWide = wavgAllocBuild(func(w *WeightedAvg) {
		w.AddTotal(wavgAllocMax, wavgAllocWideBy)
		w.AddTotal(wavgAllocMax, wavgAllocWideBy)
	})
	// wavgAllocDegraded holds 2·(2^128-1) over 4: Avg finds its precision by
	// search, and AvgRound at 19 places overflows.
	wavgAllocDegraded = wavgAllocBuild(func(w *WeightedAvg) {
		w.AddTotal(wavgAllocMax, NewFromInt(2))
		w.AddTotal(wavgAllocMax, NewFromInt(2))
	})
	wavgAllocZeroWeight = wavgAllocBuild(func(w *WeightedAvg) {
		w.Add(wavgAllocCents, wavgAllocQty)
		w.Add(wavgAllocPrice, wavgAllocQty.Neg())
	})
	wavgAllocWeightOverflow = wavgAllocBuild(func(w *WeightedAvg) {
		w.AddTotal(One, wavgAllocMax)
		w.AddTotal(One, wavgAllocMax)
	})
	// wavgAllocQuoOverflow holds (2^128-1) over 10^-19: the numerator fits
	// 128 bits, so Decimal.Div divides it and finds no precision that holds
	// the quotient.
	wavgAllocQuoOverflow = wavgAllocBuild(func(w *WeightedAvg) {
		w.AddTotal(wavgAllocMax, Decimal{coef: u128{lo: 1}, prec: MaxPrec})
	})
	// wavgAllocWideQuoOverflow holds 2·(2^128-1) over 10^-19: the numerator
	// passes 128 bits, so the wide division runs, and it finds no precision
	// either.
	wavgAllocWideQuoOverflow = wavgAllocBuild(func(w *WeightedAvg) {
		w.AddTotal(wavgAllocMax, Decimal{coef: u128{lo: 1}, prec: MaxPrec})
		w.AddTotal(wavgAllocMax, Decimal{})
	})
	// wavgAllocAligned holds one fill of cents × a 9-place quantity, so the
	// next such row meets both sums at its own precision: Add and AddTotal
	// take wavgAddNoCarry.
	wavgAllocQty9         = RequireFromString("1.123456789")
	wavgAllocCentsProduct = wavgAllocCents.MustMul(wavgAllocQty9)
	wavgAllocAligned      = wavgAllocBuild(func(w *WeightedAvg) {
		w.Add(wavgAllocCents, wavgAllocQty9)
	})
	// wavgAllocNearCarry holds a 9-place weight whose low limb is 2^64-1, so
	// the next 9-place weight carries out of it and falls back to
	// Accumulator.Add.
	wavgAllocNearCarry = wavgAllocBuild(func(w *WeightedAvg) {
		w.AddTotal(Decimal{}, Decimal{coef: u128{lo: maxUint64}, prec: 9})
	})
	// wavgAllocSquare holds one (2^128-1)^2 product, which still fits the
	// numerator's 256 bits; a second one does not.
	wavgAllocSquare = wavgAllocBuild(func(w *WeightedAvg) {
		w.Add(wavgAllocMax, wavgAllocMax)
	})
	// wavgAllocSticky holds two (2^128-1)^2 products, a numerator past
	// 2^256, over a weight that nets to 1.
	wavgAllocSticky = wavgAllocBuild(func(w *WeightedAvg) {
		w.Add(wavgAllocMax, wavgAllocMax)
		w.Add(wavgAllocMax, wavgAllocMax)
		w.AddTotal(Decimal{}, wavgAllocMax.Neg())
		w.AddTotal(Decimal{}, Decimal{coef: u128{hi: maxUint64, lo: maxUint64 - 1}, neg: true})
	})
)

func wavgAllocBuild(fill func(w *WeightedAvg)) WeightedAvg {
	var w WeightedAvg
	fill(&w)
	return w
}

func TestWeightedAvgAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	tests := []struct {
		name string
		fn   func()
	}{
		{name: "add", fn: func() { wavgAllocAvg = wavgAllocVWAP; wavgAllocAvg.Add(wavgAllocCents, wavgAllocQty) }},
		{name: "add_truncating", fn: func() { wavgAllocAvg = wavgAllocVWAP; wavgAllocAvg.Add(wavgAllocPrice, wavgAllocQty) }},
		{name: "add_wide_product", fn: func() { wavgAllocAvg = wavgAllocVWAP; wavgAllocAvg.Add(wavgAllocMax, wavgAllocQty) }},
		{name: "add_sets_sticky_overflow", fn: func() { wavgAllocAvg = wavgAllocSquare; wavgAllocAvg.Add(wavgAllocMax, wavgAllocMax) }},
		{name: "add_total", fn: func() { wavgAllocAvg = wavgAllocVWAP; wavgAllocAvg.AddTotal(wavgAllocCents, wavgAllocQty) }},
		{name: "add_aligned", fn: func() { wavgAllocAvg = wavgAllocAligned; wavgAllocAvg.Add(wavgAllocCents, wavgAllocQty9) }},
		{name: "add_total_aligned", fn: func() {
			wavgAllocAvg = wavgAllocAligned
			wavgAllocAvg.AddTotal(wavgAllocCentsProduct, wavgAllocQty9)
		}},
		{name: "add_weight_carry", fn: func() { wavgAllocAvg = wavgAllocNearCarry; wavgAllocAvg.Add(wavgAllocCents, wavgAllocQty9) }},
		{name: "reset", fn: func() { wavgAllocAvg = wavgAllocSticky; wavgAllocAvg.Reset() }},
		{name: "total", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocVWAP.Total() }},
		{name: "total_overflow", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocWide.Total() }},
		{name: "total_sticky", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocSticky.Total() }},
		{name: "weight", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocVWAP.Weight() }},
		{name: "weight_overflow", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocWeightOverflow.Weight() }},
		{name: "avg", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocVWAP.Avg() }},
		{name: "avg_wide_numerator", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocWide.Avg() }},
		{name: "avg_degraded", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocDegraded.Avg() }},
		{name: "avg_zero_weight", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocZeroWeight.Avg() }},
		{name: "avg_weight_overflow", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocWeightOverflow.Avg() }},
		{name: "avg_sticky", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocSticky.Avg() }},
		{name: "avg_quotient_overflow", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocQuoOverflow.Avg() }},
		{name: "avg_wide_quotient_overflow", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocWideQuoOverflow.Avg() }},
		{name: "avground", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocVWAP.AvgRound(2, ToNearestEven) }},
		{name: "avground_wide_numerator", fn: func() {
			wavgAllocResult, errWavgAlloc = wavgAllocWide.AvgRound(16, ToNearestAway)
		}},
		{name: "avground_overflow", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocDegraded.AvgRound(MaxPrec, TowardZero) }},
		{name: "avground_places", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocVWAP.AvgRound(MaxPrec+1, TowardZero) }},
		{name: "avground_mode", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocVWAP.AvgRound(2, RoundingMode(99)) }},
		{name: "avground_zero_weight", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocZeroWeight.AvgRound(2, TowardZero) }},
		{name: "avground_weight_overflow", fn: func() {
			wavgAllocResult, errWavgAlloc = wavgAllocWeightOverflow.AvgRound(2, TowardZero)
		}},
		{name: "avground_sticky", fn: func() { wavgAllocResult, errWavgAlloc = wavgAllocSticky.AvgRound(2, TowardZero) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, 0, tc.fn)
		})
	}
}

// TestWeightedAvgAllocFixtures pins that each gated fixture takes the path
// its name claims, so a gate cannot silently measure another one.
func TestWeightedAvgAllocFixtures(t *testing.T) {
	if q, err := wavgAllocVWAP.Avg(); err != nil || q.Prec() != DefaultPrec {
		t.Fatalf("VWAP Avg: got %v, %v; want a DefaultPrec quotient", q, err)
	}
	if _, err := wavgAllocWide.Total(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("wide Total: got %v, want ErrOverflow", err)
	}
	if q, err := wavgAllocWide.Avg(); err != nil || q.Prec() != DefaultPrec {
		t.Fatalf("wide Avg: got %v, %v; want a DefaultPrec quotient", q, err)
	}
	if _, err := wavgAllocWide.AvgRound(16, ToNearestAway); err != nil {
		t.Fatalf("wide AvgRound: %v", err)
	}
	if q, err := wavgAllocDegraded.Avg(); err != nil || q.Prec() >= DefaultPrec {
		t.Fatalf("degraded Avg: got %v, %v; want a degraded precision", q, err)
	}
	if _, err := wavgAllocDegraded.AvgRound(MaxPrec, TowardZero); !errors.Is(err, ErrOverflow) {
		t.Fatalf("degraded AvgRound: got %v, want ErrOverflow", err)
	}
	if _, err := wavgAllocZeroWeight.Avg(); !errors.Is(err, ErrDivideByZero) {
		t.Fatalf("zero weight Avg: got %v, want ErrDivideByZero", err)
	}
	if _, err := wavgAllocWeightOverflow.Weight(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("weight overflow: got %v, want ErrOverflow", err)
	}
	if w, err := wavgAllocSticky.Weight(); err != nil || !w.Equal(One) {
		t.Fatalf("sticky fixture weight: got %v, %v; want 1", w, err)
	}
	if _, err := wavgAllocSticky.Avg(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("sticky Avg: got %v, want ErrOverflow", err)
	}
	// Both quotient overflows divide a numerator that is not sticky by a
	// valid nonzero weight; only the wide one's numerator passes 128 bits,
	// so its ErrOverflow comes from the wide division, not Decimal.Div.
	for _, f := range []struct {
		name string
		w    *WeightedAvg
		wide bool
	}{
		{"avg_quotient_overflow", &wavgAllocQuoOverflow, false},
		{"avg_wide_quotient_overflow", &wavgAllocWideQuoOverflow, true},
	} {
		_, totalErr := f.w.Total()
		weight, weightErr := f.w.Weight()
		_, avgErr := f.w.Avg()
		switch {
		case weightErr != nil || weight.IsZero() || f.w.num.overflow:
			t.Fatalf("%s: weight %v, %v, sticky %v; want a nonzero weight and no sticky flag", f.name, weight, weightErr, f.w.num.overflow)
		case errors.Is(totalErr, ErrOverflow) != f.wide:
			t.Fatalf("%s: Total error %v; want a numerator past 128 bits: %v", f.name, totalErr, f.wide)
		case !errors.Is(avgErr, ErrOverflow):
			t.Fatalf("%s: Avg error %v, want ErrOverflow", f.name, avgErr)
		}
	}
	// The aligned gates take wavgAddNoCarry for both sums; the carry gate
	// declines it for the weight.
	aligned := wavgAllocAligned
	if !wavgAddNoCarry(&aligned.num, wavgAllocCentsProduct) || !wavgAddNoCarry(&aligned.weight, wavgAllocQty9) {
		t.Fatal("add_aligned and add_total_aligned must take the no-carry add")
	}
	nearCarry := wavgAllocNearCarry
	if wavgAddNoCarry(&nearCarry.weight, wavgAllocQty9) || nearCarry.weight.prec != wavgAllocQty9.prec {
		t.Fatal("add_weight_carry must be aligned and carry out of the weight's low limb")
	}
	w := wavgAllocSquare
	if w.num.overflow {
		t.Fatal("one square must still fit the numerator")
	}
	w.Add(wavgAllocMax, wavgAllocMax)
	if !w.num.overflow {
		t.Fatal("add_sets_sticky_overflow must set the numerator's sticky flag")
	}
}
