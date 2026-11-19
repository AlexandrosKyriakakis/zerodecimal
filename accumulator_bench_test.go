package zerodecimal

import (
	"math/rand/v2"
	"testing"
)

// accBenchTerms is the stream length of every BenchmarkAccumulator row; the
// ns/term metric divides by it.
const accBenchTerms = 1000

// Package-level inputs and sinks (see alloc_test.go for why neither may be
// local). A quarter of the quantities are sells, so both per-sign totals and
// the chain's opposite-sign arm are exercised.
var (
	accBenchSink   Decimal
	errAccBench    error
	accBenchResult Accumulator

	// (a) quantities at 9 places, up to 10 000 units.
	accBenchQty9 = accBenchMake(func(rng *rand.Rand, i int) Decimal {
		return newDecimal(u128{lo: 1 + rng.Uint64N(10_000*pow10u64[9])}, i%4 == 3, 9)
	})
	// (b) money precisions in turn: cents (fees, net amounts), quantities
	// at 9 places, and 19-place prices from Div (two-limb coefficients).
	accBenchMixed = accBenchMake(func(rng *rand.Rand, i int) Decimal {
		switch i % 3 {
		case 0:
			return newDecimal(u128{lo: 1 + rng.Uint64N(100_000_00)}, i%4 == 3, 2)
		case 1:
			return newDecimal(u128{lo: 1 + rng.Uint64N(10_000*pow10u64[9])}, i%4 == 3, 9)
		default:
			// Below 542·2^64 ≈ 10^22: prices up to 1000 with all 19 digits.
			return newDecimal(u128{hi: rng.Uint64N(542), lo: rng.Uint64()}, i%4 == 3, 19)
		}
	})
	// (c) the notional shape: quantity at 9 places × price at 16 places, so the
	// 25-place product truncates to DefaultPrec.
	accBenchQty    = accBenchQty9
	accBenchPrices = accBenchMake(func(rng *rand.Rand, _ int) Decimal {
		return newDecimal(u128{lo: 1 + rng.Uint64N(1_000*pow10u64[16])}, false, 16)
	})
	accBenchProducts = make([]Decimal, accBenchTerms)
)

func accBenchMake(gen func(rng *rand.Rand, i int) Decimal) []Decimal {
	rng := rand.New(rand.NewPCG(0xacc_be4c, 0x1000))
	xs := make([]Decimal, accBenchTerms)
	for i := range xs {
		xs[i] = gen(rng, i)
	}
	return xs
}

func accBenchStreamAdd(xs []Decimal) (Decimal, error) {
	var a Accumulator
	for _, d := range xs {
		a.Add(d)
	}
	return a.Sum()
}

func accBenchChainAdd(xs []Decimal) Decimal {
	s := Decimal{}
	for _, d := range xs {
		s = s.MustAdd(d)
	}
	return s
}

func accBenchStreamAddMul(qty, price []Decimal) (Decimal, error) {
	var a Accumulator
	for i := range qty {
		a.AddMul(qty[i], price[i])
	}
	return a.Sum()
}

func accBenchChainAddMul(qty, price []Decimal) Decimal {
	s := Decimal{}
	for i := range qty {
		s = s.MustAdd(qty[i].MustMul(price[i]))
	}
	return s
}

func accBenchSliceAddMul(qty, price []Decimal) (Decimal, error) {
	products := accBenchProducts[:len(qty)]
	for i := range qty {
		products[i] = qty[i].MustMul(price[i])
	}
	return Sum(products[0], products[1:]...)
}

// BenchmarkAccumulator streams 1000 terms per op through the Accumulator and
// through its baselines: the MustAdd (or MustMul+MustAdd) chain from
// Decimal{}, and the variadic Sum over a prebuilt slice (for AddMul, the
// products are built into a reused slice first). ns/term is ns/op / 1000.
func BenchmarkAccumulator(b *testing.B) {
	reportPerTerm := func(b *testing.B) {
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/accBenchTerms, "ns/term")
	}
	addShapes := []struct {
		name string
		xs   []Decimal
	}{
		{"prec9", accBenchQty9},
		{"mixed_2_9_19", accBenchMixed},
	}
	for _, shape := range addShapes {
		xs := shape.xs
		b.Run("shape="+shape.name+"/impl=Accumulator", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				accBenchSink, errAccBench = accBenchStreamAdd(xs)
			}
			reportPerTerm(b)
		})
		b.Run("shape="+shape.name+"/impl=chain", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				accBenchSink = accBenchChainAdd(xs)
			}
			reportPerTerm(b)
		})
		b.Run("shape="+shape.name+"/impl=Sum", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				accBenchSink, errAccBench = Sum(xs[0], xs[1:]...)
			}
			reportPerTerm(b)
		})
	}
	b.Run("shape=addmul_9x16/impl=Accumulator", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			accBenchSink, errAccBench = accBenchStreamAddMul(accBenchQty, accBenchPrices)
		}
		reportPerTerm(b)
	})
	b.Run("shape=addmul_9x16/impl=chain", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			accBenchSink = accBenchChainAddMul(accBenchQty, accBenchPrices)
		}
		reportPerTerm(b)
	})
	b.Run("shape=addmul_9x16/impl=Sum", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			accBenchSink, errAccBench = accBenchSliceAddMul(accBenchQty, accBenchPrices)
		}
		reportPerTerm(b)
	})
}

// BenchmarkAccumulatorFinish times the finishers once per op on a VWAP
// numerator that fits 128 bits (Decimal.Div and DivRound underneath), on a
// total past 2^128 whose quotient fits at DefaultPrec (one wide division),
// and on one whose Div degrades precision (the binary search; DivRound still
// divides once, at the requested 16 places, which overflows here).
func BenchmarkAccumulatorFinish(b *testing.B) {
	var narrow, qty, wide Accumulator
	for i := range accBenchQty {
		narrow.AddMul(accBenchQty[i], accBenchPrices[i])
		qty.Add(accBenchQty[i])
	}
	den, err := qty.Sum()
	if err != nil {
		b.Fatal(err)
	}
	maxValue := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	wide.Add(maxValue)
	wide.Add(maxValue)
	wide.Add(RequireFromString("0.000000001"))
	wideDen := RequireFromString("123456789012345678901.5")
	for _, c := range []struct {
		name string
		acc  *Accumulator
		den  Decimal
	}{
		{"narrow", &narrow, den},
		{"wide", &wide, wideDen},
		{"wide_degraded", &wide, NewFromInt(7)},
	} {
		accBenchResult = *c.acc
		d := c.den
		q, err := accBenchResult.Div(d)
		if err != nil || (c.name == "wide_degraded") != (q.Prec() < DefaultPrec) {
			b.Fatalf("preflight %s: got %v, %v", c.name, q, err)
		}
		b.Run("Sum/"+c.name, func(b *testing.B) {
			accBenchResult = *c.acc
			b.ReportAllocs()
			for b.Loop() {
				accBenchSink, errAccBench = accBenchResult.Sum()
			}
		})
		b.Run("Div/"+c.name, func(b *testing.B) {
			accBenchResult = *c.acc
			b.ReportAllocs()
			for b.Loop() {
				accBenchSink, errAccBench = accBenchResult.Div(d)
			}
		})
		b.Run("DivRound/"+c.name, func(b *testing.B) {
			accBenchResult = *c.acc
			b.ReportAllocs()
			for b.Loop() {
				accBenchSink, errAccBench = accBenchResult.DivRound(d, 16, ToNearestAway)
			}
		})
	}
}
