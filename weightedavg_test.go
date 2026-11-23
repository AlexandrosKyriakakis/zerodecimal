package zerodecimal

import (
	"errors"
	"math/big"
	"math/rand/v2"
	"os/exec"
	"strconv"
	"testing"

	"github.com/quagmt/udecimal"
	"github.com/stretchr/testify/require"
)

// wavgMax is the largest Decimal, (2^128-1) at precision 0.
var wavgMax = Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}

// wavgRow is one streamed row: Add(value, weight), or AddTotal(value,
// weight) when total is set, value then being the caller's numerator term.
type wavgRow struct {
	total         bool
	value, weight Decimal
}

func (r wavgRow) apply(w *WeightedAvg) {
	if r.total {
		w.AddTotal(r.value, r.weight)
		return
	}
	w.Add(r.value, r.weight)
}

func wavgRun(rows []wavgRow) *WeightedAvg {
	w := new(WeightedAvg)
	for _, r := range rows {
		r.apply(w)
	}
	return w
}

// wavgOracle is the exact model of a WeightedAvg: its numerator and its
// weight as accOracle models (accumulator_test.go). An Add row contributes
// value×weight truncated toward zero at min(pSum, DefaultPrec) places, the
// value Mul has; an AddTotal row contributes its total as given.
type wavgOracle struct{ num, weight accOracle }

func wavgOracleRun(rows []wavgRow) wavgOracle {
	numOps := make([]accOp, len(rows))
	weightOps := make([]accOp, len(rows))
	for i, r := range rows {
		numOps[i] = accOp{kind: accAddMul, d: r.value, e: r.weight}
		if r.total {
			numOps[i] = accOp{kind: accAdd, d: r.value}
		}
		weightOps[i] = accOp{kind: accAdd, d: r.weight}
	}
	return wavgOracle{num: accOracleRun(numOps), weight: accOracleRun(weightOps)}
}

// wavgSum is Accumulator.Sum's contract on the model o: the exact total at
// the greatest precision added, canonical zero, and ErrOverflow when the
// total needs more than 128 bits or a same-sign total more than 256.
func wavgSum(o accOracle) (Decimal, error) {
	total := o.total()
	if o.sticky() || total.CmpAbs(mod128big) >= 0 {
		return Decimal{}, ErrOverflow
	}
	return newDecimal(exactU128FromBig(new(big.Int).Abs(total)), total.Sign() < 0, o.prec), nil
}

// divisor applies the checks Avg and AvgRound make before dividing: a total
// weight past 128 bits, a zero one, then a sticky numerator. It returns the
// exact signed weight total.
func (o wavgOracle) divisor() (*big.Int, error) {
	if _, err := wavgSum(o.weight); err != nil {
		return nil, err
	}
	w := o.weight.total()
	if w.Sign() == 0 {
		return nil, ErrDivideByZero
	}
	if o.num.sticky() {
		return nil, ErrOverflow
	}
	return w, nil
}

// avg is Avg's contract on the exact sums: after divisor's checks, Div's
// truncation of the exact quotient at the greatest precision ≤ DefaultPrec
// whose coefficient fits 128 bits, or ErrOverflow when even 0 places do not.
func (o wavgOracle) avg() (Decimal, error) {
	w, err := o.divisor()
	if err != nil {
		return Decimal{}, err
	}
	n := o.num.total()
	neg := n.Sign()*w.Sign() < 0
	// The coefficient at p places is |n|·10^(p+weight.prec) / (|w|·10^num.prec).
	num := new(big.Int).Mul(new(big.Int).Abs(n), bp10(int(o.weight.prec)))
	den := new(big.Int).Mul(new(big.Int).Abs(w), bp10(int(o.num.prec)))
	for p := int(DefaultPrec); p >= 0; p-- {
		q := new(big.Int).Quo(new(big.Int).Mul(num, bp10(p)), den)
		if q.BitLen() <= 128 {
			return newDecimal(exactU128FromBig(q), neg, uint8(p)), nil
		}
	}
	return Decimal{}, ErrOverflow
}

// avgRound is AvgRound's contract on the exact sums: places, then mode, then
// divisor's checks, then the exact quotient rounded once, ErrOverflow when
// the rounded coefficient does not fit 128 bits.
func (o wavgOracle) avgRound(places uint8, mode RoundingMode) (Decimal, error) {
	switch {
	case places > MaxPrec:
		return Decimal{}, ErrPrecOutOfRange
	case mode > TowardNegative:
		return Decimal{}, ErrInvalidRoundingMode
	}
	w, err := o.divisor()
	if err != nil {
		return Decimal{}, err
	}
	exact := new(big.Rat).SetFrac(
		new(big.Int).Mul(o.num.total(), bp10(int(o.weight.prec))),
		new(big.Int).Mul(w, bp10(int(o.num.prec))),
	)
	coef, neg := exactRoundRat(exact, places, mode)
	if coef.BitLen() > 128 {
		return Decimal{}, ErrOverflow
	}
	return newDecimal(exactU128FromBig(coef), neg, places), nil
}

func requireWavgResult(t *testing.T, name string, want Decimal, wantErr error, got Decimal, gotErr error, ctx ...any) {
	t.Helper()
	require.Equalf(t, wantErr, gotErr, "%s error: ctx=%+v", name, ctx)
	require.Equalf(t, want, got, "%s: ctx=%+v", name, ctx)
}

// requireWavgAvgRound holds w.AvgRound(places, mode) to the oracle: value,
// representation and error.
func requireWavgAvgRound(t *testing.T, w *WeightedAvg, o wavgOracle, places uint8, mode RoundingMode, ctx ...any) {
	t.Helper()
	want, wantErr := o.avgRound(places, mode)
	got, err := w.AvgRound(places, mode)
	requireWavgResult(t, "AvgRound", want, wantErr, got, err, places, mode, ctx)
}

// requireWavgFinishers holds every finisher of w to the oracle, and pins Avg
// and AvgRound to Total followed by Decimal.Div and Decimal.DivRound by
// Weight wherever both sums fit.
func requireWavgFinishers(t *testing.T, w *WeightedAvg, o wavgOracle, places uint8, mode RoundingMode, ctx ...any) {
	t.Helper()
	wantTotal, wantTotalErr := wavgSum(o.num)
	total, totalErr := w.Total()
	requireWavgResult(t, "Total", wantTotal, wantTotalErr, total, totalErr, ctx)
	wantWeight, wantWeightErr := wavgSum(o.weight)
	weight, weightErr := w.Weight()
	requireWavgResult(t, "Weight", wantWeight, wantWeightErr, weight, weightErr, ctx)
	want, wantErr := o.avg()
	avg, avgErr := w.Avg()
	requireWavgResult(t, "Avg", want, wantErr, avg, avgErr, ctx)
	requireWavgAvgRound(t, w, o, places, mode, ctx...)
	if totalErr != nil || weightErr != nil {
		return
	}
	want, wantErr = total.Div(weight)
	requireWavgResult(t, "Avg vs Total().Div(Weight())", want, wantErr, avg, avgErr, ctx)
	want, wantErr = total.DivRound(weight, places, mode)
	got, err := w.AvgRound(places, mode)
	requireWavgResult(t, "AvgRound vs Total().DivRound(Weight())", want, wantErr, got, err, places, mode, ctx)
}

// wavgMoneyRow returns a fill of money-shaped values: a quantity of up to
// 10 000 units at 0..9 places with the given sign, and a positive price
// below 50 000 at 2..16 places.
func wavgMoneyRow(rng *rand.Rand, neg bool) (qty, price Decimal) {
	qtyPrec := uint8(rng.IntN(10))
	qty = newDecimal(u128{lo: 1 + rng.Uint64N(10_000*pow10u64[qtyPrec])}, neg, qtyPrec)
	return qty, accRandPrice(rng, 2+uint8(rng.IntN(15)))
}

// wavgRandRows returns n rows, Add and AddTotal alike, whose operands have
// coefficient widths uniform over 1..maxBits, any precision and sign, and are
// canonical zero one time in twelve.
func wavgRandRows(rng *rand.Rand, n, maxBits int) []wavgRow {
	rows := make([]wavgRow, n)
	for i := range rows {
		rows[i] = wavgRow{total: rng.IntN(3) == 0, value: accRandDecimal(rng, maxBits), weight: accRandDecimal(rng, maxBits)}
	}
	return rows
}

// wavgWide returns a Decimal of sign neg at a random precision whose
// coefficient is at least 2^(bits-1).
func wavgWide(rng *rand.Rand, bits int, neg bool) Decimal {
	d := newDecimal(shapedCoef(rng, bits), neg, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
	if bits > 64 {
		d.coef.hi |= 1 << (bits - 65)
	} else {
		d.coef.lo |= 1 << (bits - 1)
	}
	return d
}

// wavgRandStream returns a stream for the oracle tests; iter selects the
// shape, so every shape recurs:
//   - random rows of every width (two in six);
//   - same-sign totals of 120..128 bits over small weights: numerators past
//     2^128 whose quotient often still fits, or fits only after Avg lowers
//     its precision;
//   - same-sign weights of 127..128 bits, whose total passes 2^128;
//   - four same-sign products of two 128-bit operands, past 2^256 (a sticky
//     numerator), whose weights AddTotal rows cancel down to a small one;
//   - rows followed by AddTotal rows that cancel every weight.
func wavgRandStream(rng *rand.Rand, iter int) []wavgRow {
	widths := [...]int{16, 40, 64, 96, 128}
	var rows []wavgRow
	switch iter % 6 {
	case 2:
		neg := rng.IntN(2) == 0
		for range 2 + rng.IntN(5) {
			rows = append(rows, wavgRow{total: true, value: wavgWide(rng, 120+rng.IntN(9), neg), weight: accRandDecimal(rng, 24)})
		}
	case 3:
		neg := rng.IntN(2) == 0
		for range 2 + rng.IntN(3) {
			rows = append(rows, wavgRow{value: accRandDecimal(rng, 64), weight: wavgWide(rng, 127+rng.IntN(2), neg)})
		}
	case 4:
		neg := rng.IntN(2) == 0
		for range 4 {
			weight := wavgWide(rng, 128, rng.IntN(2) == 0)
			weight.prec = 0
			value := wavgWide(rng, 128, neg != weight.neg)
			value.prec = 0
			rows = append(rows, wavgRow{value: value, weight: weight}, wavgRow{total: true, weight: weight.Neg()})
		}
		rows = append(rows, wavgRow{total: true, weight: accRandDecimal(rng, 64)})
	case 5:
		n := 1 + rng.IntN(8)
		rows = wavgRandRows(rng, n, widths[(iter/6)%len(widths)])
		for i := range n {
			rows = append(rows, wavgRow{total: true, value: accRandDecimal(rng, 64), weight: rows[i].weight.Neg()})
		}
	default:
		rows = wavgRandRows(rng, rng.IntN(17), widths[(iter/6)%len(widths)])
	}
	return rows
}

// TestWeightedAvgZeroValueAndReset pins the empty WeightedAvg (canonical zero
// sums, ErrDivideByZero from both means, after AvgRound's validation), that
// the finishers leave the state unchanged so a stream can continue, and that
// Reset restores the zero value, sticky overflow included.
func TestWeightedAvgZeroValueAndReset(t *testing.T) {
	t.Parallel()
	var w WeightedAvg
	total, err := w.Total()
	require.NoError(t, err)
	require.Equal(t, Decimal{}, total, "an empty total is canonical zero")
	weight, err := w.Weight()
	require.NoError(t, err)
	require.Equal(t, Decimal{}, weight)
	_, err = w.Avg()
	require.Equal(t, ErrDivideByZero, err, "an empty mean has no weight")
	_, err = w.AvgRound(2, ToNearestEven)
	require.Equal(t, ErrDivideByZero, err)
	_, err = w.AvgRound(MaxPrec+1, ToNearestEven)
	require.Equal(t, ErrPrecOutOfRange, err, "validation precedes the zero weight")

	w.Add(RequireFromString("10.25"), NewFromInt(100))
	w.AddTotal(MustNew(520, 0), NewFromInt(50)) // 50 at 10.40, multiplied by the caller
	before := w
	avg, err := w.Avg()
	require.NoError(t, err)
	require.Equal(t, MustNew(103, -1).MustRescale(DefaultPrec), avg, "1545/150 = 10.3")
	_, err = w.AvgRound(4, ToNearestEven)
	require.NoError(t, err)
	_, err = w.Total()
	require.NoError(t, err)
	_, err = w.Weight()
	require.NoError(t, err)
	require.Equal(t, before, w, "finishers do not change the state")

	w.Add(RequireFromString("10.10"), NewFromInt(25))
	rounded, err := w.AvgRound(4, ToNearestEven)
	require.NoError(t, err)
	require.Equal(t, MustNew(102714, -4), rounded, "1797.5/175 = 10.27142857…")
	w.Reset()
	require.Equal(t, WeightedAvg{}, w, "Reset restores the zero value")

	w.Add(wavgMax, wavgMax)
	w.Add(wavgMax, wavgMax)
	_, err = w.Total()
	require.Equal(t, ErrOverflow, err)
	w.Reset()
	require.Equal(t, WeightedAvg{}, w, "Reset clears the sticky overflow")
	w.Add(NewFromInt(7), NewFromInt(2))
	avg, err = w.Avg()
	require.NoError(t, err)
	require.Equal(t, NewFromInt(7).MustRescale(DefaultPrec), avg)
}

// TestWeightedAvgEdgeCases pins the edge cases with explicit values, and holds
// each one to the exact oracle at every places and mode.
func TestWeightedAvgEdgeCases(t *testing.T) {
	t.Parallel()
	d := RequireFromString
	maxMinus1 := Decimal{coef: u128{hi: maxUint64, lo: maxUint64 - 1}}
	halfMax := Decimal{coef: u128{hi: maxUint64 >> 1, lo: maxUint64}} // ⌊(2^128-1)/2⌋
	// longFills and shortFills are the same three fills bought and sold.
	longFills := []wavgRow{
		{value: d("10.25"), weight: NewFromInt(100)},
		{value: d("10.40"), weight: NewFromInt(50)},
		{value: d("10.10"), weight: NewFromInt(25)},
	}
	shortFills := make([]wavgRow, len(longFills))
	for i, r := range longFills {
		shortFills[i] = wavgRow{value: r.value, weight: r.weight.Neg()}
	}
	vwap, err := wavgRun(longFills).Avg()
	require.NoError(t, err)

	cases := []struct {
		name  string
		rows  []wavgRow
		check func(t *testing.T, w *WeightedAvg)
	}{
		{
			name: "single_row",
			rows: []wavgRow{{value: d("10.25"), weight: NewFromInt(3)}},
			check: func(t *testing.T, w *WeightedAvg) {
				total, err := w.Total()
				require.NoError(t, err)
				require.Equal(t, d("30.75"), total)
				avg, err := w.Avg()
				require.NoError(t, err)
				require.Equal(t, d("10.25").MustRescale(DefaultPrec), avg, "Div keeps DefaultPrec places")
				rounded, err := w.AvgRound(2, ToNearestEven)
				require.NoError(t, err)
				require.Equal(t, d("10.25"), rounded)
			},
		},
		{
			name: "single_total_row",
			rows: []wavgRow{{total: true, value: d("30.75"), weight: NewFromInt(3)}},
			check: func(t *testing.T, w *WeightedAvg) {
				avg, err := w.Avg()
				require.NoError(t, err)
				require.Equal(t, d("10.25").MustRescale(DefaultPrec), avg)
			},
		},
		{
			// Short fills: every weight negative, the mean still the price.
			name: "all_negative_weights",
			rows: shortFills,
			check: func(t *testing.T, w *WeightedAvg) {
				total, err := w.Total()
				require.NoError(t, err)
				require.Equal(t, MustNew(-179750, -2), total)
				weight, err := w.Weight()
				require.NoError(t, err)
				require.Equal(t, NewFromInt(-175), weight)
				avg, err := w.Avg()
				require.NoError(t, err)
				require.Equal(t, vwap, avg, "the same fills sold average like the fills bought")
				require.False(t, avg.IsNegative())
				rounded, err := w.AvgRound(4, ToNearestEven)
				require.NoError(t, err)
				require.Equal(t, MustNew(102714, -4), rounded)
			},
		},
		{
			// Mixed-sign weights that cancel: the numerator is -5, the weight 0.
			name: "weights_cancel",
			rows: []wavgRow{{value: NewFromInt(10), weight: NewFromInt(5)}, {value: NewFromInt(11), weight: NewFromInt(-5)}},
			check: func(t *testing.T, w *WeightedAvg) {
				total, err := w.Total()
				require.NoError(t, err)
				require.Equal(t, NewFromInt(-5), total)
				weight, err := w.Weight()
				require.NoError(t, err)
				require.Equal(t, Decimal{}, weight, "a cancelled weight is canonical zero")
				_, err = w.Avg()
				require.Equal(t, ErrDivideByZero, err)
				_, err = w.AvgRound(2, ToNearestAway)
				require.Equal(t, ErrDivideByZero, err)
			},
		},
		{
			// 2·(2^128-1) over 4: Total overflows, the mean (2^128-1)/2 fits
			// only at 0 places, and AvgRound rounds its half in every mode.
			name: "wide_numerator_degraded",
			rows: []wavgRow{{total: true, value: wavgMax, weight: NewFromInt(2)}, {total: true, value: wavgMax, weight: NewFromInt(2)}},
			check: func(t *testing.T, w *WeightedAvg) {
				_, err := w.Total()
				require.Equal(t, ErrOverflow, err)
				avg, err := w.Avg()
				require.NoError(t, err)
				require.Equal(t, halfMax, avg)
				up := Decimal{coef: u128{hi: 1 << 63}}
				for mode, want := range map[RoundingMode]Decimal{
					ToNearestAway: up, ToNearestEven: up, AwayFromZero: up,
					TowardZero: halfMax, TowardPositive: up, TowardNegative: halfMax,
				} {
					got, roundErr := w.AvgRound(0, mode)
					require.NoError(t, roundErr)
					require.Equal(t, want, got, "mode %d", mode)
				}
				_, err = w.AvgRound(1, TowardZero)
				require.Equal(t, ErrOverflow, err)
			},
		},
		{
			// Three (2^128-1)·10^-19 over 3: a wide total whose mean is the
			// largest coefficient at 19 places.
			name: "wide_numerator_fits",
			rows: []wavgRow{
				{total: true, value: Decimal{coef: wavgMax.coef, prec: MaxPrec}, weight: One},
				{total: true, value: Decimal{coef: wavgMax.coef, prec: MaxPrec}, weight: One},
				{total: true, value: Decimal{coef: wavgMax.coef, prec: MaxPrec}, weight: One},
			},
			check: func(t *testing.T, w *WeightedAvg) {
				_, err := w.Total()
				require.Equal(t, ErrOverflow, err)
				rounded, err := w.AvgRound(MaxPrec, ToNearestEven)
				require.NoError(t, err)
				require.Equal(t, Decimal{coef: wavgMax.coef, prec: MaxPrec}, rounded)
			},
		},
		{
			name: "weight_overflows",
			rows: []wavgRow{{total: true, value: One, weight: wavgMax}, {total: true, value: One, weight: One}},
			check: func(t *testing.T, w *WeightedAvg) {
				total, err := w.Total()
				require.NoError(t, err)
				require.Equal(t, NewFromInt(2), total)
				_, err = w.Weight()
				require.Equal(t, ErrOverflow, err)
				_, err = w.Avg()
				require.Equal(t, ErrOverflow, err)
				_, err = w.AvgRound(2, ToNearestEven)
				require.Equal(t, ErrOverflow, err)
			},
		},
		{
			// Two (2^128-1)^2 products pass 2^256; the weights net to 1.
			name: "sticky_numerator",
			rows: []wavgRow{
				{value: wavgMax, weight: wavgMax},
				{value: wavgMax, weight: wavgMax},
				{total: true, weight: wavgMax.Neg()},
				{total: true, weight: maxMinus1.Neg()},
			},
			check: func(t *testing.T, w *WeightedAvg) {
				weight, err := w.Weight()
				require.NoError(t, err)
				require.Equal(t, One, weight)
				_, err = w.Total()
				require.Equal(t, ErrOverflow, err)
				_, err = w.Avg()
				require.Equal(t, ErrOverflow, err)
				_, err = w.AvgRound(2, ToNearestEven)
				require.Equal(t, ErrOverflow, err)
			},
		},
		{
			// A zero weight takes precedence over a sticky numerator.
			name: "sticky_numerator_zero_weight",
			rows: []wavgRow{
				{value: wavgMax, weight: wavgMax},
				{value: wavgMax, weight: wavgMax},
				{total: true, weight: wavgMax.Neg()},
				{total: true, weight: wavgMax.Neg()},
			},
			check: func(t *testing.T, w *WeightedAvg) {
				_, err := w.Avg()
				require.Equal(t, ErrDivideByZero, err)
				_, err = w.AvgRound(2, ToNearestEven)
				require.Equal(t, ErrDivideByZero, err)
			},
		},
		{
			// (2^128-1) over 10^-19: no precision holds the quotient.
			name: "quotient_overflows",
			rows: []wavgRow{{total: true, value: wavgMax, weight: Decimal{coef: u128{lo: 1}, prec: MaxPrec}}},
			check: func(t *testing.T, w *WeightedAvg) {
				_, err := w.Avg()
				require.Equal(t, ErrOverflow, err)
				_, err = w.AvgRound(0, TowardZero)
				require.Equal(t, ErrOverflow, err)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := wavgRun(tc.rows)
			tc.check(t, w)
			o := wavgOracleRun(tc.rows)
			requireWavgFinishers(t, w, o, 2, ToNearestEven)
			for places := uint8(0); places <= MaxPrec+1; places++ {
				for mode := ToNearestAway; mode <= TowardNegative+1; mode++ {
					requireWavgAvgRound(t, w, o, places, mode)
				}
			}
		})
	}
}

// TestWeightedAvgAddTruncatesProducts pins what Add's doc comment says it
// loses: value×weight is truncated at DefaultPrec places, as Mul truncates
// it, before it reaches the exact numerator, while AddTotal keeps an exact
// product as given. Under zerodecimal_prec9, 0.5 × 0.000000001 needs 10
// places, so Add's mean of 0.5 rounds to 0, while the wider builds keep it.
// 0.5 × 10^-DefaultPrec needs DefaultPrec+1 places, so Add truncates it under
// every tag; MulExact holds it exactly only below DefaultPrec 19.
func TestWeightedAvgAddTruncatesProducts(t *testing.T) {
	t.Parallel()
	half := RequireFromString("0.5")
	for _, weight := range []Decimal{
		RequireFromString("0.000000001"),
		{coef: u128{lo: 1}, prec: DefaultPrec},
	} {
		var byAdd WeightedAvg
		byAdd.Add(half, weight)
		total, err := byAdd.Total()
		require.NoError(t, err)
		got, err := byAdd.AvgRound(2, ToNearestAway)
		require.NoError(t, err)
		truncated := half.Prec()+weight.Prec() > DefaultPrec
		if truncated {
			require.Equal(t, Decimal{}, total, "Mul truncates the product to zero: weight=%v", weight)
			require.Equal(t, Decimal{}, got, "weight=%v", weight)
		}

		product, err := half.MulExact(weight)
		if err != nil {
			require.Equal(t, ErrUnderflow, err, "only 0.5 × 10^-19 has no exact Decimal")
			require.Equal(t, MaxPrec, DefaultPrec)
			continue
		}
		var byTotal WeightedAvg
		byTotal.AddTotal(product, weight)
		exact, err := byTotal.AvgRound(2, ToNearestAway)
		require.NoError(t, err)
		require.Equal(t, MustNew(50, -2), exact, "AddTotal keeps the exact product: the mean is 0.5")
		if !truncated {
			require.Equal(t, product, total, "weight=%v", weight)
			require.Equal(t, exact, got, "weight=%v", weight)
		}
	}
}

// TestWeightedAvgValidationOrder pins AvgRound's order (places, mode, then
// the weight: past 128 bits, zero; then the numerator) and Avg's, with bare
// sentinels.
func TestWeightedAvgValidationOrder(t *testing.T) {
	t.Parallel()
	maxMinus1 := Decimal{coef: u128{hi: maxUint64, lo: maxUint64 - 1}}
	sticky := []wavgRow{{value: wavgMax, weight: wavgMax}, {value: wavgMax, weight: wavgMax}}
	streams := map[string]struct {
		rows []wavgRow
		want error // from both means once places and mode are valid
	}{
		"empty":                {nil, ErrDivideByZero},
		"weight_overflow":      {[]wavgRow{{value: One, weight: wavgMax}, {value: One, weight: wavgMax}}, ErrOverflow},
		"sticky_zero_weight":   {append(sticky[:2:2], wavgRow{total: true, weight: wavgMax.Neg()}, wavgRow{total: true, weight: wavgMax.Neg()}), ErrDivideByZero},
		"sticky_weight_one":    {append(sticky[:2:2], wavgRow{total: true, weight: wavgMax.Neg()}, wavgRow{total: true, weight: maxMinus1.Neg()}), ErrOverflow},
		"sticky_weight_wide":   {sticky, ErrOverflow},
		"quotient_overflows":   {[]wavgRow{{total: true, value: wavgMax, weight: Decimal{coef: u128{lo: 1}, prec: MaxPrec}}}, ErrOverflow},
		"zero_weight_nonempty": {[]wavgRow{{value: One, weight: One}, {total: true, weight: NewFromInt(-1)}}, ErrDivideByZero},
	}
	for name, s := range streams {
		w := wavgRun(s.rows)
		_, err := w.AvgRound(MaxPrec+1, RoundingMode(99))
		require.Equal(t, ErrPrecOutOfRange, err, name)
		_, err = w.AvgRound(MaxPrec, RoundingMode(99))
		require.Equal(t, ErrInvalidRoundingMode, err, name)
		_, err = w.AvgRound(0, TowardZero)
		require.Equal(t, s.want, err, name)
		_, err = w.Avg()
		require.Equal(t, s.want, err, name)
	}
}

// TestWeightedAvgMatchesChains streams money-shaped rows (quantities at 0..9
// places of either sign, prices at 2..16 places, multipliers 1, 100 and
// 0.01) and holds every finisher to the legacy chains over the same rows,
// under every DefaultPrec:
//   - AddTotal(price·mult·qty, qty) then Avg is the MustAdd chains then
//     Div (MustDiv wherever it succeeds), a VWAP with a multiplier;
//   - Add(price, qty) then Avg is the MustMul and MustAdd chains then Div,
//     and AvgRound(p, ToNearestAway) and AvgRound(p, TowardZero) are that
//     quotient's Round(p) and Truncate(p), for every p < DefaultPrec. Prices
//     below 50 000 keep every quotient at DefaultPrec digits, so the legacy
//     relation's magnitude bound holds throughout (asserted).
//
// One stream in four mixes sides, and one in sixteen ends with a row that
// cancels the total quantity, so both chains divide by zero.
func TestWeightedAvgMatchesChains(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0xc4a1_5eed, 0x3e16_4a))
	mults := [...]Decimal{NewFromInt(1), NewFromInt(100), MustNew(1, -2)}
	zeroWeights := 0
	for stream := range 3_000 {
		mult := mults[stream%len(mults)]
		side := rng.IntN(2) == 0
		var byTotal, byProduct WeightedAvg
		notional, qtyPrice, qtySum := Decimal{}, Decimal{}, Decimal{}
		addRow := func(qty, price Decimal) {
			value := price.MustMul(mult).MustMul(qty)
			byTotal.AddTotal(value, qty)
			byProduct.Add(price, qty)
			notional = notional.MustAdd(value)
			qtyPrice = qtyPrice.MustAdd(price.MustMul(qty))
			qtySum = qtySum.MustAdd(qty)
		}
		for range 1 + rng.IntN(24) {
			neg := side
			if stream%4 == 1 {
				neg = rng.IntN(2) == 0
			}
			addRow(wavgMoneyRow(rng, neg))
		}
		if stream%16 == 0 {
			_, price := wavgMoneyRow(rng, false)
			addRow(qtySum.Neg(), price)
		}

		for _, sum := range []struct {
			name  string
			w     *WeightedAvg
			chain Decimal
			read  func(*WeightedAvg) (Decimal, error)
		}{
			{"Total by AddTotal", &byTotal, notional, (*WeightedAvg).Total},
			{"Total by Add", &byProduct, qtyPrice, (*WeightedAvg).Total},
			{"Weight", &byProduct, qtySum, (*WeightedAvg).Weight},
		} {
			got, err := sum.read(sum.w)
			require.NoError(t, err)
			// Equal value: a chain that cancels to zero restarts at precision 0.
			require.Truef(t, sum.chain.Equal(got), "%s: chain=%v got=%v stream=%d", sum.name, sum.chain, got, stream)
		}

		want, wantErr := notional.Div(qtySum)
		got, err := byTotal.Avg()
		requireWavgResult(t, "AddTotal+Avg vs MustAdd chains + Div", want, wantErr, got, err, stream)
		legacy, legacyErr := qtyPrice.Div(qtySum)
		got, err = byProduct.Avg()
		requireWavgResult(t, "Add+Avg vs MustMul/MustAdd chains + Div", legacy, legacyErr, got, err, stream)
		if wantErr != nil {
			require.Equal(t, ErrDivideByZero, wantErr)
			require.True(t, qtySum.IsZero())
			_, err = byProduct.AvgRound(16, ToNearestAway)
			require.Equal(t, ErrDivideByZero, err)
			zeroWeights++
			continue
		}
		require.Truef(t, legacy.IsZero() || legacy.Prec() == DefaultPrec, "the money quotient keeps DefaultPrec digits: %v", legacy)
		for p := range DefaultPrec {
			rounded, err := byProduct.AvgRound(p, ToNearestAway)
			require.NoError(t, err)
			require.Equalf(t, legacy.Round(p), rounded, "Div+Round(%d): legacy=%v stream=%d", p, legacy, stream)
			truncated, err := byProduct.AvgRound(p, TowardZero)
			require.NoError(t, err)
			require.Equalf(t, legacy.Truncate(p), truncated, "Div+Truncate(%d): legacy=%v stream=%d", p, legacy, stream)
		}
	}
	require.GreaterOrEqual(t, zeroWeights, 3_000/16, "every cancelled stream must divide by zero")
}

// wavgRoundedTo16 is n/d by udecimal's Div, which truncates at 19 places,
// then RoundHAZ(16). ok is false for a zero d, which udecimal's Div rejects
// with a division-by-zero error.
func wavgRoundedTo16(n, d udecimal.Decimal) (q udecimal.Decimal, ok bool) {
	if d.IsZero() {
		return udecimal.Decimal{}, false
	}
	return n.MustDiv(d).RoundHAZ(16), true
}

// TestWeightedAvgUdecimalParity replays weighted means with udecimal,
// which never fails, and requires bit-identical results:
//   - a VWAP over value = price.Mul(multiplier).Mul(qty), finished by MustDiv
//     (or 0 for a zero count) and then by the multiplier: AddTotal(value,
//     qty) and Avg, with ErrDivideByZero standing for the 0;
//   - a grouped weighted average price, Σ qty·price over Σ qty, where an
//     unpriced row adds only its quantity, rounded to 16 places: Add(price,
//     qty), or AddTotal(Decimal{}, qty) for an unpriced row, then
//     AvgRound(16, ToNearestAway);
//   - a position re-averaged fill by fill:
//     ROUND((qty·average + addQty·price) / newQty, 16) with truncating
//     products is Add(average, qty) and Add(price, addQty), then
//     AvgRound(16, ToNearestAway).
//
// Sums are bit-identical too, except where a mixed-side sum passes
// through zero mid-stream: udecimal then restarts at precision 0, while
// Total and Weight keep the greatest precision added; those compare equal in
// value.
func TestWeightedAvgUdecimalParity(t *testing.T) {
	if DefaultPrec != 19 {
		t.Skip("udecimal truncates legacy Mul and Div at 19 places")
	}
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x0dec_a1, 0x3e16))

	t.Run("vwap_with_multiplier", func(t *testing.T) {
		mults := [...]Decimal{NewFromInt(1), NewFromInt(100), MustNew(1, -2)}
		zeroCounts := 0
		for stream := range 2_000 {
			mult := mults[stream%len(mults)]
			uMult := accUdec(t, mult)
			neg := rng.IntN(2) == 0
			var avgPrice WeightedAvg
			uSum, uCount := udecimal.Zero, udecimal.Zero
			for range 1 + rng.IntN(24) {
				qty, price := wavgMoneyRow(rng, neg)
				if stream%40 == 0 {
					qty = Decimal{} // a stream of zero-quantity rows keeps a zero count
				}
				uQty := accUdec(t, qty)
				// value := price * multiplier * qty
				uValue := accUdec(t, price).Mul(uMult).Mul(uQty)
				// running sums
				uSum, uCount = uSum.Add(uValue), uCount.Add(uQty)
				avgPrice.AddTotal(price.MustMul(mult).MustMul(qty), qty)
			}
			total, err := avgPrice.Total()
			require.NoError(t, err)
			requireAccUdecIdentical(t, uSum, total, "sum", stream)
			count, err := avgPrice.Weight()
			require.NoError(t, err)
			requireAccUdecIdentical(t, uCount, count, "count", stream)

			// The mean, then the price per unit of the multiplier.
			uCalc := udecimal.Zero
			if !uCount.IsZero() {
				uCalc = uSum.MustDiv(uCount)
			}
			calc, err := avgPrice.Avg()
			if uCount.IsZero() {
				require.Equal(t, ErrDivideByZero, err)
				calc, err = Decimal{}, nil
				zeroCounts++
			}
			require.NoError(t, err)
			requireAccUdecIdentical(t, uCalc, calc, "mean", stream)
			requireAccUdecIdentical(t, uCalc.MustDiv(uMult), calc.MustDiv(mult), "price", stream)
		}
		require.Equal(t, 2_000/40, zeroCounts)
	})

	t.Run("grouped_average", func(t *testing.T) {
		zeroQty := 0
		for stream := range 2_000 {
			side := rng.IntN(2) == 0
			mixed := stream%5 == 0 // groups may net both sides
			var price, local WeightedAvg
			uQty, uQtyPrice, uQtyLocal := udecimal.Zero, udecimal.Zero, udecimal.Zero
			hasPrice, hasLocal := false, false
			n := 1 + rng.IntN(32)
			for i := range n {
				neg := side
				if mixed {
					neg = rng.IntN(2) == 0
				}
				qty, p := wavgMoneyRow(rng, neg)
				if stream%40 == 0 && i == n-1 {
					// The last row nets the group's quantity to zero.
					sum, err := price.Weight()
					require.NoError(t, err)
					qty = sum.Neg()
				}
				l := accRandPrice(rng, 2+uint8(rng.IntN(15)))
				uq := accUdec(t, qty)
				// running quantity
				uQty = uQty.Add(uq)
				if rng.IntN(8) != 0 { // the row has a price
					// qtyPrice += qty * price
					uQtyPrice, hasPrice = uQtyPrice.Add(uq.Mul(accUdec(t, p))), true
					price.Add(p, qty)
				} else {
					price.AddTotal(Decimal{}, qty)
				}
				if rng.IntN(8) != 0 { // the row has a second price
					uQtyLocal, hasLocal = uQtyLocal.Add(uq.Mul(accUdec(t, l))), true
					local.Add(l, qty)
				} else {
					local.AddTotal(Decimal{}, qty)
				}
			}
			for _, g := range []struct {
				name      string
				has       bool
				w         *WeightedAvg
				uQtyPrice udecimal.Decimal
			}{
				{"price", hasPrice, &price, uQtyPrice},
				{"local", hasLocal, &local, uQtyLocal},
			} {
				total, err := g.w.Total()
				require.NoError(t, err)
				weight, err := g.w.Weight()
				require.NoError(t, err)
				if mixed {
					require.Truef(t, accUdec(t, total).Equal(g.uQtyPrice), "%s sum: stream=%d", g.name, stream)
					require.Truef(t, accUdec(t, weight).Equal(uQty), "%s qty: stream=%d", g.name, stream)
				} else {
					requireAccUdecIdentical(t, g.uQtyPrice, total, g.name, "sum", stream)
					requireAccUdecIdentical(t, uQty, weight, g.name, "qty", stream)
				}
				if !g.has {
					continue // no price on any row; nothing to average
				}
				uWant, ok := wavgRoundedTo16(g.uQtyPrice, uQty)
				got, err := g.w.AvgRound(16, ToNearestAway)
				if !ok {
					require.Equal(t, ErrDivideByZero, err)
					zeroQty++
					continue
				}
				require.NoError(t, err)
				requireAccUdecIdentical(t, uWant, got, g.name, "rounded to 16", stream)
			}
		}
		require.Greater(t, zeroQty, 50, "groups whose quantity nets to zero")
	})

	t.Run("running_position", func(t *testing.T) {
		var w WeightedAvg
		for position := range 1_000 {
			neg := rng.IntN(2) == 0
			// Apply from flat: the opening fill's price is the average.
			qty, average := wavgMoneyRow(rng, neg)
			uQty, uAverage := accUdec(t, qty), accUdec(t, average)
			for fill := range 1 + rng.IntN(16) {
				addQty, price := wavgMoneyRow(rng, neg)
				uAddQty, uPrice := accUdec(t, addQty), accUdec(t, price)
				uNewQty := uQty.Add(uAddQty)
				// ROUND((qty*average + addQty*price) / newQty, 16)
				uNext, ok := wavgRoundedTo16(uQty.Mul(uAverage).Add(uAddQty.Mul(uPrice)), uNewQty)
				require.True(t, ok)

				w.Reset()
				w.Add(average, qty)
				w.Add(price, addQty)
				next, err := w.AvgRound(16, ToNearestAway)
				require.NoError(t, err)
				requireAccUdecIdentical(t, uNext, next, "next average", position, fill)
				newQty, err := w.Weight()
				require.NoError(t, err)
				requireAccUdecIdentical(t, uNewQty, newQty, "newQty", position, fill)
				average, uAverage = next, uNext
				qty, uQty = newQty, uNewQty
			}
		}
	})
}

// TestWeightedAvgLegacyRelation pins the relation AvgRound documents:
// whenever Avg keeps more than p digits, Avg().Round(p) is AvgRound(p,
// ToNearestAway) and Avg().Truncate(p) is AvgRound(p, TowardZero), value
// and representation. The streams cover every width, wide numerators and
// degraded quotients, so both sides of the magnitude bound occur.
func TestWeightedAvgLegacyRelation(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x1e6a_c4, 0x9e1a7))
	checked, degraded := 0, 0
	for iter := range 6_000 {
		w := wavgRun(wavgRandStream(rng, iter))
		avg, err := w.Avg()
		if err != nil {
			continue
		}
		for p := range DefaultPrec {
			if !avg.IsZero() && avg.Prec() <= p {
				degraded++ // outside the relation; see TestWeightedAvgLegacyRelationLimits
				continue
			}
			checked++
			rounded, err := w.AvgRound(p, ToNearestAway)
			require.NoError(t, err, "a quotient Avg holds at more than p digits still fits once rounded to p")
			require.Equalf(t, avg.Round(p), rounded, "Round(%d): avg=%v iter=%d", p, avg, iter)
			truncated, err := w.AvgRound(p, TowardZero)
			require.NoError(t, err)
			require.Equalf(t, avg.Truncate(p), truncated, "Truncate(%d): avg=%v iter=%d", p, avg, iter)
		}
	}
	require.Greater(t, checked, 1_500*int(DefaultPrec)) // p ranges over 0..DefaultPrec-1
	require.Greater(t, degraded, 1_000, "too few degraded quotients to exercise the bound")
}

// TestWeightedAvgLegacyRelationLimits pins where the relation stops, with
// quotients built relative to DefaultPrec so every build tag checks them:
// the other modes differ when the exact quotient lies less than
// 10^-DefaultPrec past the boundary they round at, and ToNearestAway differs
// once Avg keeps only p digits.
func TestWeightedAvgLegacyRelationLimits(t *testing.T) {
	t.Parallel()
	ulp := Decimal{coef: u128{lo: 1}, prec: DefaultPrec} // 10^-DefaultPrec
	// mean returns a WeightedAvg of total over weight, and its legacy Avg.
	mean := func(t *testing.T, total Decimal, weight int64) (*WeightedAvg, Decimal) {
		t.Helper()
		w := wavgRun([]wavgRow{{total: true, value: total, weight: NewFromInt(weight)}})
		avg, err := w.Avg()
		require.NoError(t, err)
		return w, avg
	}
	round := func(t *testing.T, w *WeightedAvg, mode RoundingMode) Decimal {
		t.Helper()
		got, err := w.AvgRound(2, mode)
		require.NoError(t, err)
		return got
	}
	d := RequireFromString
	atDefault := func(s string) Decimal { return d(s).MustRescale(DefaultPrec) }

	t.Run("tie_to_even", func(t *testing.T) {
		// (0.375 + ulp)/3 = 0.125 + ulp/3: Avg truncates to the tie 0.125.
		w, avg := mean(t, d("0.375").MustAdd(ulp), 3)
		require.Equal(t, atDefault("0.125"), avg)
		require.Equal(t, d("0.12"), avg.RoundBank(2), "the truncated tie rounds to even")
		require.Equal(t, d("0.13"), round(t, w, ToNearestEven), "the exact quotient is past the tie")
		require.Equal(t, avg.Round(2), round(t, w, ToNearestAway))
		require.Equal(t, avg.Truncate(2), round(t, w, TowardZero))

		// With an odd retained digit both round the tie up.
		w, avg = mean(t, d("0.405").MustAdd(ulp), 3)
		require.Equal(t, avg.RoundBank(2), round(t, w, ToNearestEven))
		require.Equal(t, d("0.14"), round(t, w, ToNearestEven))
	})

	t.Run("directed_modes", func(t *testing.T) {
		// (0.36 + ulp)/3 = 0.12 + ulp/3: every digit Avg keeps past 2 is zero.
		w, avg := mean(t, d("0.36").MustAdd(ulp), 3)
		require.Equal(t, atDefault("0.12"), avg)
		require.Equal(t, d("0.12"), avg.RoundUp(2))
		require.Equal(t, d("0.13"), round(t, w, AwayFromZero))
		require.Equal(t, d("0.12"), avg.RoundCeil(2))
		require.Equal(t, d("0.13"), round(t, w, TowardPositive))
		require.Equal(t, avg.Round(2), round(t, w, ToNearestAway))
		require.Equal(t, avg.Truncate(2), round(t, w, TowardZero))

		w, avg = mean(t, d("0.36").MustAdd(ulp), -3)
		require.Equal(t, atDefault("-0.12"), avg)
		require.Equal(t, d("-0.12"), avg.RoundFloor(2))
		require.Equal(t, d("-0.13"), round(t, w, TowardNegative))
		require.Equal(t, d("-0.12"), avg.RoundUp(2))
		require.Equal(t, d("-0.13"), round(t, w, AwayFromZero))
		require.Equal(t, avg.Round(2), round(t, w, ToNearestAway))
		require.Equal(t, avg.Truncate(2), round(t, w, TowardZero))

		// A full 10^-DefaultPrec past the boundary, Avg keeps it.
		w, avg = mean(t, d("0.36").MustAdd(ulp).MustAdd(ulp).MustAdd(ulp), 3)
		require.Equal(t, round(t, w, AwayFromZero), avg.RoundUp(2))
		require.Equal(t, d("0.13"), avg.RoundUp(2))
	})

	t.Run("degraded_quotient", func(t *testing.T) {
		// q = 4·10^(37-p) + 5·10^-(p+1): q·10^(p+1) = 4·10^38 + 5 ≥ 2^128, so
		// Avg keeps only p digits and drops the 5 that ToNearestAway rounds
		// up. For p = 16 this is q = 4·10^21. The numerator is wide, built
		// from two halves at p+1 places, over a weight of 1.
		p := DefaultPrec - 3
		half := new(big.Int).Mul(big.NewInt(2), bp10(38))
		low := Decimal{coef: exactU128FromBig(half), prec: p + 1}
		high := Decimal{coef: exactU128FromBig(new(big.Int).Add(half, big.NewInt(5))), prec: p + 1}
		w := wavgRun([]wavgRow{
			{total: true, value: low, weight: d("0.5")},
			{total: true, value: high, weight: d("0.5")},
		})
		avg, err := w.Avg()
		require.NoError(t, err)
		kept := new(big.Int).Mul(big.NewInt(4), bp10(37))
		require.Equal(t, Decimal{coef: exactU128FromBig(kept), prec: p}, avg, "Avg degrades to exactly p places")
		require.Equal(t, avg, avg.Round(p), "Round(p) cannot see the dropped digit")
		rounded, err := w.AvgRound(p, ToNearestAway)
		require.NoError(t, err)
		require.Equal(t, Decimal{coef: exactU128FromBig(kept.Add(kept, big.NewInt(1))), prec: p}, rounded)
		truncated, err := w.AvgRound(p, TowardZero)
		require.NoError(t, err)
		require.Equal(t, avg.Truncate(p), truncated, "TowardZero still agrees at Avg's own precision")

		// One place fewer, Avg keeps more than p-1 digits and the relation holds.
		rounded, err = w.AvgRound(p-1, ToNearestAway)
		require.NoError(t, err)
		require.Equal(t, avg.Round(p-1), rounded)
	})
}

// TestWeightedAvgOracle holds every finisher to the exact big.Int/big.Rat
// oracle on random streams of every shape wavgRandStream builds, with
// AvgRound in every mode at every places 0..MaxPrec, plus an invalid places
// and mode, and requires each shape's path to occur often enough.
func TestWeightedAvgOracle(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x0a7_c1e, 0x5eed_04))
	var wideFits, degraded, weightOverflow, zeroWeight, sticky, roundOverflow int
	for iter := range 1_800 {
		rows := wavgRandStream(rng, iter)
		w := wavgRun(rows)
		o := wavgOracleRun(rows)
		places := uint8(rng.IntN(int(MaxPrec) + 2))
		mode := RoundingMode(rng.IntN(int(TowardNegative) + 2))
		requireWavgFinishers(t, w, o, places, mode, rows)
		for p := uint8(0); p <= MaxPrec+1; p++ {
			for m := ToNearestAway; m <= TowardNegative+1; m++ {
				requireWavgAvgRound(t, w, o, p, m, rows)
			}
		}

		_, totalErr := w.Total()
		avg, avgErr := w.Avg()
		_, weightErr := w.Weight()
		_, roundErr := w.AvgRound(MaxPrec, TowardZero)
		switch {
		case weightErr != nil:
			weightOverflow++
		case errors.Is(avgErr, ErrDivideByZero):
			zeroWeight++
		case o.num.sticky():
			sticky++
		case avgErr == nil && totalErr != nil:
			wideFits++
		}
		if avgErr == nil && !avg.IsZero() && avg.Prec() < DefaultPrec {
			degraded++
		}
		if avgErr == nil && errors.Is(roundErr, ErrOverflow) {
			roundOverflow++
		}
	}
	require.Greater(t, wideFits, 100, "wide numerators with a representable mean")
	require.Greater(t, degraded, 100, "means Avg holds below DefaultPrec places")
	require.Greater(t, weightOverflow, 200, "total weights past 128 bits")
	require.Greater(t, zeroWeight, 200, "cancelled weights")
	require.Greater(t, sticky, 200, "sticky numerators over a valid weight")
	require.Greater(t, roundOverflow, 50, "AvgRound results past 128 bits")
}

// wavgPermutations calls visit with every ordering of rows.
func wavgPermutations(rows []wavgRow, visit func([]wavgRow)) {
	work := append([]wavgRow(nil), rows...)
	var permute func(int)
	permute = func(i int) {
		if i == len(work) {
			visit(work)
			return
		}
		for j := i; j < len(work); j++ {
			work[i], work[j] = work[j], work[i]
			permute(i + 1)
			work[i], work[j] = work[j], work[i]
		}
	}
	permute(0)
}

// TestWeightedAvgOrderIndependence streams short random sequences in every
// order: every finisher result, errors included, must be identical.
func TestWeightedAvgOrderIndependence(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x0bde_04, 0x7e1a))
	for iter := range 120 {
		rows := wavgRandRows(rng, 2+rng.IntN(4), [...]int{40, 128}[iter&1])
		places := uint8(rng.IntN(int(MaxPrec) + 1))
		mode := RoundingMode(rng.IntN(int(TowardNegative) + 1))
		ref := wavgRun(rows)
		wantAvg, wantAvgErr := ref.Avg()
		wantRound, wantRoundErr := ref.AvgRound(places, mode)
		wantTotal, wantTotalErr := ref.Total()
		wantWeight, wantWeightErr := ref.Weight()
		wavgPermutations(rows, func(perm []wavgRow) {
			w := wavgRun(perm)
			got, err := w.Avg()
			requireWavgResult(t, "Avg", wantAvg, wantAvgErr, got, err, rows)
			got, err = w.AvgRound(places, mode)
			requireWavgResult(t, "AvgRound", wantRound, wantRoundErr, got, err, rows)
			got, err = w.Total()
			requireWavgResult(t, "Total", wantTotal, wantTotalErr, got, err, rows)
			got, err = w.Weight()
			requireWavgResult(t, "Weight", wantWeight, wantWeightErr, got, err, rows)
		})
	}
}

// TestWeightedAvgMatchesAccumulatorState streams rows through a WeightedAvg
// and through two Accumulators fed the same terms (AddMul or Add for the
// numerator, Add for the weight), and requires identical internal state
// after every row, so wavgAddNoCarry is exactly the case of Accumulator.add
// it inlines. The fixed rows hit each side of it: aligned one-limb terms
// with and without a carry out of the low limb, coarser and finer terms,
// two-limb terms, zero terms, both signs, and a sticky numerator. Random
// and money-shaped streams follow, and every side must occur often.
func TestWeightedAvgMatchesAccumulatorState(t *testing.T) {
	t.Parallel()
	low := Decimal{coef: u128{lo: maxUint64}} // a full low limb at precision 0
	cents, qty := RequireFromString("19.99"), RequireFromString("1.5")
	fixed := []wavgRow{
		{total: true, value: low, weight: low},             // aligned, no carry
		{total: true, value: low, weight: low},             // aligned, carry out of the low limb
		{total: true, value: Decimal{}, weight: low.Neg()}, // zero term at precision 0; negative total
		{value: cents, weight: qty},                        // finer: raises both precisions
		{value: cents, weight: qty},                        // aligned again
		{total: true, value: low, weight: low},             // coarser
		{total: true, value: Decimal{coef: u128{hi: 1, lo: 5}, prec: 3}, weight: Decimal{coef: u128{hi: 1}, prec: 1}}, // two limbs
		{total: true, weight: qty}, // canonical-zero total while the numerator is at 3: declined, a no-op
		{value: wavgMax, weight: wavgMax},
		{value: wavgMax, weight: wavgMax}, // the numerator turns sticky
		{total: true, value: MustNew(-7, -3), weight: MustNew(-5, -1)},
	}
	var hits, carries, others int
	check := func(rows []wavgRow) {
		var w WeightedAvg
		var num, weight Accumulator
		for i, r := range rows {
			probe := w.weight
			switch {
			case wavgAddNoCarry(&probe, r.weight):
				hits++
			case r.weight.prec == w.weight.prec && r.weight.coef.hi == 0:
				carries++
			default:
				others++
			}
			r.apply(&w)
			if r.total {
				num.Add(r.value)
			} else {
				num.AddMul(r.value, r.weight)
			}
			weight.Add(r.weight)
			require.Equalf(t, num, w.num, "numerator after row %d: rows=%+v", i, rows)
			require.Equalf(t, weight, w.weight, "weight after row %d: rows=%+v", i, rows)
		}
	}
	check(fixed)
	require.True(t, wavgRun(fixed).num.overflow, "the fixed rows end with a sticky numerator")
	rng := rand.New(rand.NewPCG(0x5747_e04, 0xfa57))
	for iter := range 1_200 {
		check(wavgRandStream(rng, iter))
		// Money rows at fixed precisions: every row after the first is aligned.
		rows := make([]wavgRow, 1+rng.IntN(24))
		for i := range rows {
			price := newDecimal(u128{lo: 1 + rng.Uint64N(50_000*100)}, false, 2)
			q := newDecimal(u128{lo: 1 + rng.Uint64N(10_000*pow10u64[9])}, rng.IntN(4) == 0, 9)
			rows[i] = wavgRow{total: i%3 == 0, value: price, weight: q}
		}
		check(rows)
		// Full low limbs at one precision: most same-sign adds carry.
		full := make([]wavgRow, 2+rng.IntN(8))
		for i := range full {
			value := newDecimal(u128{lo: 1<<63 | rng.Uint64()}, rng.IntN(2) == 0, 7)
			weight := newDecimal(u128{lo: 1<<63 | rng.Uint64()}, rng.IntN(2) == 0, 7)
			full[i] = wavgRow{total: true, value: value, weight: weight}
		}
		check(full)
	}
	require.Greater(t, hits, 10_000, "aligned no-carry adds")
	require.Greater(t, carries, 100, "aligned adds that carry")
	require.Greater(t, others, 2_000, "adds Accumulator.Add must take")
}

// TestWeightedAvgInlineBudgets pins what Add and AddTotal rely on for speed:
// wavgAddNoCarry fits the default inline budget, so an aligned no-carry row
// costs no call for the weight (nor, in AddTotal, for the numerator).
func TestWeightedAvgInlineBudgets(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("inline budgets are calibrated for 64-bit compiler targets")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; cannot query inlining diagnostics")
	}
	entry, ok := buildInlineReport(t)["wavgAddNoCarry"]
	require.True(t, ok, "wavgAddNoCarry missing from the inlining report")
	require.Truef(t, entry.canInline, "wavgAddNoCarry must fit the default inline budget: cost %d", entry.cost)
}
