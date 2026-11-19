package zerodecimal

import (
	"errors"
	"testing"
)

// Package-level fixtures and sinks for the Accumulator allocation gates (see
// alloc_test.go for why neither may be local). Streaming closures copy a
// fixture into accAllocAcc first, so every run takes the same path.
var (
	accAllocAcc    Accumulator
	accAllocResult Decimal
	errAccAlloc    error

	accAllocMax    = Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	accAllocTerm   = RequireFromString("1234.5678")
	accAllocWide   = RequireFromString("123456789012345678901234567.8901")
	accAllocCents  = RequireFromString("19.99")
	accAllocFine   = RequireFromString("0.0000000000000000001")
	accAllocQty    = RequireFromString("1.5")
	accAllocPrice  = RequireFromString("50123.4567890123456")
	accAllocDen    = NewFromInt(7)
	accAllocWideBy = RequireFromString("123456789012345678901.5")

	accAllocAt4  = accAllocBuild(func(a *Accumulator) { a.Add(accAllocTerm) })
	accAllocAt19 = accAllocBuild(func(a *Accumulator) { a.Add(accAllocFine) })
	// accAllocWideTotal holds 2·(2^128-1)+10^-9: Sum overflows, Div and
	// DivRound take the wide division.
	accAllocWideTotal = accAllocBuild(func(a *Accumulator) {
		a.Add(accAllocMax)
		a.Add(accAllocMax)
		a.Add(RequireFromString("0.000000001"))
	})
	accAllocSticky = accAllocBuild(func(a *Accumulator) {
		a.AddMul(accAllocMax, accAllocMax)
		a.AddMul(accAllocMax, accAllocMax)
	})
	// accAllocNearFull holds (2^128-1)^2 = 2^256-2^129+1 at precision 0: it
	// fits, but raising it to MaxPrec does not, so Add(accAllocFine) sets the
	// sticky flag.
	accAllocNearFull = accAllocBuild(func(a *Accumulator) { a.AddMul(accAllocMax, accAllocMax) })
	// accAllocTwoLimbFine is a two-limb coefficient (about 2^104) at MaxPrec:
	// times accAllocFine its 38 places take addMulWide's truncation, one
	// reciprocal pass by 10^19 by default and two under prec9/prec12, and
	// the truncated term stays nonzero under every tag.
	accAllocTwoLimbFine = Decimal{coef: u128{hi: 1 << 40, lo: 5}, prec: MaxPrec}
)

func accAllocBuild(fill func(a *Accumulator)) Accumulator {
	var a Accumulator
	fill(&a)
	return a
}

func TestAccumulatorAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	tests := []struct {
		name string
		fn   func()
	}{
		{name: "add_aligned", fn: func() { accAllocAcc = accAllocAt4; accAllocAcc.Add(accAllocTerm) }},
		{name: "add_aligned_two_limb", fn: func() { accAllocAcc = accAllocAt4; accAllocAcc.Add(accAllocWide) }},
		{name: "sub_aligned", fn: func() { accAllocAcc = accAllocAt4; accAllocAcc.Sub(accAllocTerm) }},
		{name: "add_raises_precision", fn: func() { accAllocAcc = accAllocAt4; accAllocAcc.Add(accAllocFine) }},
		{name: "add_coarser_term", fn: func() { accAllocAcc = accAllocAt19; accAllocAcc.Add(accAllocCents) }},
		{name: "add_zero", fn: func() { accAllocAcc = accAllocAt19; accAllocAcc.Add(Decimal{}) }},
		{name: "add_sets_sticky_overflow", fn: func() { accAllocAcc = accAllocNearFull; accAllocAcc.Add(accAllocFine) }},
		{name: "add_raise_when_sticky", fn: func() { accAllocAcc = accAllocSticky; accAllocAcc.Add(accAllocFine) }},
		{name: "addmul_exact", fn: func() { accAllocAcc = accAllocAt4; accAllocAcc.AddMul(accAllocQty, accAllocCents) }},
		{name: "addmul_truncating", fn: func() { accAllocAcc = accAllocAt19; accAllocAcc.AddMul(accAllocQty, accAllocPrice) }},
		{name: "addmul_wide", fn: func() { accAllocAcc = accAllocAt4; accAllocAcc.AddMul(accAllocWide, accAllocWide) }},
		{name: "addmul_wide_truncating", fn: func() {
			accAllocAcc = accAllocAt19
			accAllocAcc.AddMul(accAllocTwoLimbFine, accAllocFine)
		}},
		{name: "addmul_sets_sticky_overflow", fn: func() { accAllocAcc = accAllocAt19; accAllocAcc.AddMul(accAllocMax, accAllocMax) }},
		{name: "reset", fn: func() { accAllocAcc = accAllocWideTotal; accAllocAcc.Reset() }},
		{name: "sum", fn: func() { accAllocResult, errAccAlloc = accAllocAt4.Sum() }},
		{name: "sum_overflow", fn: func() { accAllocResult, errAccAlloc = accAllocWideTotal.Sum() }},
		{name: "sum_sticky", fn: func() { accAllocResult, errAccAlloc = accAllocSticky.Sum() }},
		{name: "div", fn: func() { accAllocResult, errAccAlloc = accAllocAt4.Div(accAllocDen) }},
		{name: "div_wide_total", fn: func() { accAllocResult, errAccAlloc = accAllocWideTotal.Div(accAllocWideBy) }},
		{name: "div_degraded_wide_total", fn: func() { accAllocResult, errAccAlloc = accAllocWideTotal.Div(accAllocDen) }},
		{name: "div_by_zero", fn: func() { accAllocResult, errAccAlloc = accAllocAt4.Div(Decimal{}) }},
		{name: "div_sticky", fn: func() { accAllocResult, errAccAlloc = accAllocSticky.Div(accAllocDen) }},
		{name: "divround", fn: func() { accAllocResult, errAccAlloc = accAllocAt4.DivRound(accAllocDen, 2, ToNearestEven) }},
		{name: "divround_wide_total", fn: func() {
			accAllocResult, errAccAlloc = accAllocWideTotal.DivRound(accAllocWideBy, 16, ToNearestAway)
		}},
		{name: "divround_overflow", fn: func() { accAllocResult, errAccAlloc = accAllocWideTotal.DivRound(accAllocDen, 19, TowardZero) }},
		{name: "divround_places", fn: func() { accAllocResult, errAccAlloc = accAllocAt4.DivRound(accAllocDen, MaxPrec+1, TowardZero) }},
		{name: "divround_mode", fn: func() { accAllocResult, errAccAlloc = accAllocAt4.DivRound(accAllocDen, 2, RoundingMode(99)) }},
		{name: "divround_by_zero", fn: func() { accAllocResult, errAccAlloc = accAllocAt4.DivRound(Decimal{}, 2, TowardZero) }},
		{name: "divround_sticky", fn: func() { accAllocResult, errAccAlloc = accAllocSticky.DivRound(accAllocDen, 2, TowardZero) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, 0, tc.fn)
		})
	}
}

// TestAccumulatorAllocFixtures pins that each gated finisher fixture takes
// the path its name claims, so a gate cannot silently measure another one.
func TestAccumulatorAllocFixtures(t *testing.T) {
	if _, err := accAllocWideTotal.Sum(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("wide total Sum: got %v, want ErrOverflow", err)
	}
	if q, err := accAllocWideTotal.Div(accAllocWideBy); err != nil || q.Prec() != DefaultPrec {
		t.Fatalf("wide total Div: got %v, %v; want a DefaultPrec quotient", q, err)
	}
	if q, err := accAllocWideTotal.Div(accAllocDen); err != nil || q.Prec() >= DefaultPrec {
		t.Fatalf("degraded wide Div: got %v, %v; want a degraded precision", q, err)
	}
	if _, err := accAllocWideTotal.DivRound(accAllocWideBy, 16, ToNearestAway); err != nil {
		t.Fatalf("wide DivRound: %v", err)
	}
	if _, err := accAllocWideTotal.DivRound(accAllocDen, 19, TowardZero); !errors.Is(err, ErrOverflow) {
		t.Fatalf("wide DivRound overflow: got %v, want ErrOverflow", err)
	}
	if _, err := accAllocSticky.Sum(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("sticky Sum: got %v, want ErrOverflow", err)
	}
	// The two *_sets_sticky_overflow gates must measure the transition: the
	// fixture is clear before the term and flagged after it.
	if accAllocAt19.overflow || accAllocNearFull.overflow {
		t.Fatal("the sticky-transition fixtures must start without the flag")
	}
	if _, err := accAllocNearFull.Sum(); !errors.Is(err, ErrOverflow) || accAllocNearFull.prec != 0 {
		t.Fatalf("near-full fixture: got %v at precision %d; want a wide total at precision 0", err, accAllocNearFull.prec)
	}
	a := accAllocAt19
	a.AddMul(accAllocMax, accAllocMax)
	if !a.overflow {
		t.Fatal("addmul_sets_sticky_overflow must set the flag")
	}
	a = accAllocNearFull
	a.Add(accAllocFine)
	if !a.overflow || a.prec != MaxPrec {
		t.Fatal("add_sets_sticky_overflow must raise the precision and set the flag")
	}
	a = accAllocSticky
	a.Add(accAllocFine)
	if !a.overflow || a.prec != MaxPrec {
		t.Fatal("add_raise_when_sticky must raise the precision with the flag kept")
	}

	// addmul_wide_truncating: a two-limb operand takes addMulWide, and the
	// 38-place product truncates to DefaultPrec; the result must be the value
	// legacy Mul has, added to the fixture's 10^-19.
	d, e := accAllocTwoLimbFine, accAllocFine
	if d.coef.hi == 0 || d.prec+e.prec <= DefaultPrec {
		t.Fatal("addmul_wide_truncating must have a two-limb operand and truncate")
	}
	a = accAllocAt19
	a.AddMul(d, e)
	got, err := a.Sum()
	product := d.MustMul(e)
	if want := accAllocFine.MustAdd(product); err != nil || !got.Equal(want) || product.IsZero() {
		t.Fatalf("addmul_wide_truncating: got %v, %v; want %v with a nonzero product %v", got, err, want, product)
	}
}
