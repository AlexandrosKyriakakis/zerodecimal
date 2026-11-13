package zerodecimal

import (
	"errors"
	"math/bits"
	"testing"
)

// Allocation-gate fixtures and sinks (see alloc_test.go for the rules). The
// benchmark operands in divisor_bench_test.go supply the success shapes.
var (
	divisorAllocResult  Decimal
	divisorAllocDivisor Divisor
	errDivisorAlloc     error

	divisorAllocNil     *Divisor
	divisorAllocZero    Divisor
	divisorAllocThree   = MustNewDivisor(NewFromInt(3))
	divisorAllocTiny    = MustNewDivisor(newDecimal(u128{lo: 1}, false, MaxPrec))
	divisorAllocMax     = newDecimal(u128{hi: maxUint64, lo: maxUint64}, false, 0)
	divisorAllocNarrow  = MustNew(7, -1) // 0.7: one limb when scaled to DefaultPrec
	divisorAllocUnitDiv = MustNewDivisor(NewFromInt(1))
)

// TestDivisorAllocs gates NewDivisor, DivBy and DivRoundBy (and the Must
// twins and Decimal) at exactly zero allocations on every success shape and
// every error path: one-limb and two-limb divisors, one-limb and wide
// numerators, the delegations to Div's degradation and to DivRound, overflow,
// and each validation error.
func TestDivisorAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	tests := []struct {
		name string
		fn   func()
	}{
		{name: "new_divisor_one_limb", fn: func() {
			divisorAllocDivisor, errDivisorAlloc = NewDivisor(divisorWhole)
		}},
		{name: "new_divisor_two_limb", fn: func() {
			divisorAllocDivisor, errDivisorAlloc = NewDivisor(divisorWideDivisor)
		}},
		{name: "new_divisor_zero", fn: func() {
			divisorAllocDivisor, errDivisorAlloc = NewDivisor(Decimal{})
		}},
		{name: "must_new_divisor", fn: func() {
			divisorAllocDivisor = MustNewDivisor(divisorWhole)
		}},
		{name: "decimal", fn: func() {
			divisorAllocResult = divisorWholeDiv.Decimal()
		}},
		{name: "div_by_one_limb_numerator", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorAllocNarrow.DivBy(&divisorAllocUnitDiv)
		}},
		{name: "div_by_one_limb_divisor", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorProducts[0].DivBy(&divisorWholeDiv)
		}},
		{name: "div_by_wide_numerator", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorNotional.DivBy(&divisorTotalQtyDiv)
		}},
		{name: "div_by_two_limb_divisor", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorWideDividend.DivBy(&divisorWideDivisorDiv)
		}},
		{name: "div_by_degraded_precision", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorAllocMax.DivBy(&divisorAllocThree)
		}},
		{name: "div_by_overflow", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorAllocMax.DivBy(&divisorAllocTiny)
		}},
		{name: "div_by_nil", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorAllocNarrow.DivBy(divisorAllocNil)
		}},
		{name: "div_by_zero_divisor", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorAllocNarrow.DivBy(&divisorAllocZero)
		}},
		{name: "div_by_zero_dividend", fn: func() {
			divisorAllocResult, errDivisorAlloc = Decimal{}.DivBy(&divisorWholeDiv)
		}},
		{name: "must_div_by", fn: func() {
			divisorAllocResult = divisorProducts[1].MustDivBy(&divisorWholeDiv)
		}},
		{name: "div_round_by_narrow", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorProducts[0].DivRoundBy(&divisorWholeDiv, 2, ToNearestEven)
		}},
		{name: "div_round_by_one_limb_divisor", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorCost.DivRoundBy(&divisorWholeDiv, 16, ToNearestAway)
		}},
		{name: "div_round_by_two_limb_divisor", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorWideDividend.DivRoundBy(&divisorWideDivisorDiv, 19, TowardPositive)
		}},
		{name: "div_round_by_negative_gap", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorProducts[2].DivRoundBy(&divisorAllocThree, 2, ToNearestEven)
		}},
		{name: "div_round_by_overflow", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorAllocMax.DivRoundBy(&divisorAllocTiny, 0, AwayFromZero)
		}},
		{name: "div_round_by_places", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorAllocNarrow.DivRoundBy(&divisorWholeDiv, MaxPrec+1, ToNearestEven)
		}},
		{name: "div_round_by_mode", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorAllocNarrow.DivRoundBy(&divisorWholeDiv, 2, TowardNegative+1)
		}},
		{name: "div_round_by_nil", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorAllocNarrow.DivRoundBy(divisorAllocNil, 2, ToNearestEven)
		}},
		{name: "div_round_by_zero_divisor", fn: func() {
			divisorAllocResult, errDivisorAlloc = divisorAllocNarrow.DivRoundBy(&divisorAllocZero, 2, ToNearestEven)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, 0, tc.fn)
		})
	}
}

// TestDivisorAllocShapes pins each allocation-gate fixture to the path its
// name claims, so a fixture change cannot silently retarget the gate.
func TestDivisorAllocShapes(t *testing.T) {
	t.Parallel()
	// The one-limb numerator gates reach the hardware-divide arms: the
	// dividend scaled by its gap still fits one limb, over a one-limb divisor.
	// The one-limb divisor gates scale past one limb, into the reciprocal.
	scaledHi := func(d Decimal, v *Divisor, places uint8) uint64 {
		f := int(places) + int(v.e.prec) - int(d.prec)
		if d.coef.hi != 0 || v.e.coef.hi != 0 || f < 0 || f > int(MaxPrec) {
			t.Fatalf("fixture d=%v v=%v places=%d is not a one-limb shape", d, v.Decimal(), places)
		}
		hi, _ := bits.Mul64(d.coef.lo, pow10u64[f])
		return hi
	}
	if scaledHi(divisorAllocNarrow, &divisorAllocUnitDiv, DefaultPrec) != 0 {
		t.Fatal("div_by_one_limb_numerator fixture must scale within one limb")
	}
	if scaledHi(divisorProducts[0], &divisorWholeDiv, 2) != 0 {
		t.Fatal("div_round_by_narrow fixture must scale within one limb")
	}
	if scaledHi(divisorProducts[0], &divisorWholeDiv, DefaultPrec) == 0 {
		t.Fatal("div_by_one_limb_divisor fixture must scale past one limb")
	}
	got, err := divisorAllocMax.DivBy(&divisorAllocThree)
	if err != nil || got.Prec() >= DefaultPrec {
		t.Fatalf("degraded_precision fixture: got %v, %v; want a precision below DefaultPrec", got, err)
	}
	if _, err := divisorAllocMax.DivBy(&divisorAllocTiny); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow fixture: got %v", err)
	}
	if _, err := divisorAllocMax.DivRoundBy(&divisorAllocTiny, 0, AwayFromZero); !errors.Is(err, ErrOverflow) {
		t.Fatalf("round overflow fixture: got %v", err)
	}
	if f := 2 + int(divisorAllocThree.e.prec) - int(divisorProducts[2].prec); f >= 0 {
		t.Fatalf("negative_gap fixture has gap %d", f)
	}
	if divisorWideDivisorDiv.e.coef.hi == 0 || divisorWholeDiv.e.coef.hi != 0 {
		t.Fatal("divisor fixtures must be two-limb and one-limb respectively")
	}
	if divisorNotional.coef.hi == 0 {
		t.Fatal("the VWAP notional must have a two-limb coefficient")
	}
}
