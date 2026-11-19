package zerodecimal

import (
	"math/big"
	"math/bits"
	"math/rand/v2"
	"os/exec"
	"regexp"
	"strconv"
	"testing"
	"unsafe"

	"github.com/quagmt/udecimal"
	"github.com/stretchr/testify/require"
)

// accOpKind selects the Accumulator method an accOp streams.
type accOpKind uint8

const (
	accAdd accOpKind = iota
	accSub
	accAddMul
)

// accOp is one streamed term: Add(d), Sub(d) or AddMul(d, e).
type accOp struct {
	kind accOpKind
	d, e Decimal
}

func (op accOp) apply(a *Accumulator) {
	switch op.kind {
	case accAdd:
		a.Add(op.d)
	case accSub:
		a.Sub(op.d)
	default:
		a.AddMul(op.d, op.e)
	}
}

// legacyTerm returns the Decimal the legacy chain adds for op: d, -d, or
// d.Mul(e), with Mul's error.
func (op accOp) legacyTerm() (Decimal, error) {
	switch op.kind {
	case accAdd:
		return op.d, nil
	case accSub:
		return op.d.Neg(), nil
	default:
		return op.d.Mul(op.e)
	}
}

func accRun(ops []accOp) *Accumulator {
	a := new(Accumulator)
	for _, op := range ops {
		op.apply(a)
	}
	return a
}

// accOracle is the exact model of an Accumulator after a stream of terms: the
// greatest precision of a nonzero term, and the per-sign totals aligned there.
type accOracle struct {
	prec     uint8
	pos, neg *big.Int
}

// accOracleTerm returns op's exact term as a magnitude, sign and precision.
// An AddMul term is the full product truncated toward zero at
// min(pSum, DefaultPrec) places, the value legacy Mul has, at any width.
func accOracleTerm(op accOp) (mag *big.Int, neg bool, prec uint8) {
	switch op.kind {
	case accAdd:
		return u128ToBig(op.d.coef), op.d.neg, op.d.prec
	case accSub:
		return u128ToBig(op.d.coef), !op.d.neg, op.d.prec
	default:
		mag = new(big.Int).Mul(u128ToBig(op.d.coef), u128ToBig(op.e.coef))
		prec = op.d.prec + op.e.prec
		if prec > DefaultPrec {
			mag.Quo(mag, bp10(int(prec-DefaultPrec)))
			prec = DefaultPrec
		}
		return mag, op.d.neg != op.e.neg, prec
	}
}

func accOracleRun(ops []accOp) accOracle {
	type term struct {
		mag  *big.Int
		neg  bool
		prec uint8
	}
	var terms []term
	o := accOracle{pos: new(big.Int), neg: new(big.Int)}
	for _, op := range ops {
		mag, neg, prec := accOracleTerm(op)
		if mag.Sign() == 0 {
			continue // a zero term is canonical Decimal{}: precision 0
		}
		terms = append(terms, term{mag: mag, neg: neg, prec: prec})
		o.prec = max(o.prec, prec)
	}
	for _, tm := range terms {
		v := new(big.Int).Mul(tm.mag, bp10(int(o.prec-tm.prec)))
		if tm.neg {
			o.neg.Add(o.neg, v)
		} else {
			o.pos.Add(o.pos, v)
		}
	}
	return o
}

// sticky reports whether a same-sign total outgrew the 256-bit width.
func (o accOracle) sticky() bool {
	return o.pos.BitLen() > 256 || o.neg.BitLen() > 256
}

func (o accOracle) total() *big.Int {
	return new(big.Int).Sub(o.pos, o.neg)
}

// requireAccSum holds a.Sum() to the oracle: the exact total at the greatest
// precision, canonical zero, and ErrOverflow exactly when the total needs more
// than 128 bits or a same-sign total more than 256.
func requireAccSum(t *testing.T, a *Accumulator, o accOracle, ctx ...any) {
	t.Helper()
	got, err := a.Sum()
	total := o.total()
	if o.sticky() || total.CmpAbs(mod128big) >= 0 {
		require.Equalf(t, ErrOverflow, err, "sum overflow: ctx=%+v", ctx)
		require.Equal(t, Decimal{}, got)
		return
	}
	require.NoErrorf(t, err, "sum: ctx=%+v", ctx)
	requireAggregateDecimal(t, got, new(big.Int).Abs(total), total.Sign() < 0, o.prec)
}

// requireAccDiv holds a.Div(den) to Div's legacy contract on the exact total:
// the quotient truncated at the greatest p ≤ DefaultPrec whose coefficient fits
// 128 bits, after the zero-divisor and sticky-overflow checks.
func requireAccDiv(t *testing.T, a *Accumulator, o accOracle, den Decimal, ctx ...any) {
	t.Helper()
	got, err := a.Div(den)
	switch {
	case den.IsZero():
		require.Equalf(t, ErrDivideByZero, err, "div by zero: ctx=%+v", ctx)
		require.Equal(t, Decimal{}, got)
		return
	case o.sticky():
		require.Equalf(t, ErrOverflow, err, "sticky div: ctx=%+v", ctx)
		require.Equal(t, Decimal{}, got)
		return
	}
	total := o.total()
	neg := (total.Sign() < 0) != den.neg
	// The coefficient at p places is |total|·10^(p+dp) / (den·10^tp).
	num := new(big.Int).Mul(new(big.Int).Abs(total), bp10(int(den.prec)))
	dn := new(big.Int).Mul(u128ToBig(den.coef), bp10(int(o.prec)))
	for p := int(DefaultPrec); p >= 0; p-- {
		q := new(big.Int).Quo(new(big.Int).Mul(num, bp10(p)), dn)
		if q.BitLen() <= 128 {
			require.NoErrorf(t, err, "div: ctx=%+v", ctx)
			requireAggregateDecimal(t, got, q, neg, uint8(p))
			return
		}
	}
	require.Equalf(t, ErrOverflow, err, "div overflow: ctx=%+v", ctx)
	require.Equal(t, Decimal{}, got)
}

// requireAccDivRound holds a.DivRound to the exact quotient rounded once, after
// DivRound's validation order and the sticky-overflow check.
func requireAccDivRound(t *testing.T, a *Accumulator, o accOracle, den Decimal, places uint8, mode RoundingMode, ctx ...any) {
	t.Helper()
	got, err := a.DivRound(den, places, mode)
	var wantErr error
	switch {
	case places > MaxPrec:
		wantErr = ErrPrecOutOfRange
	case !mode.valid():
		wantErr = ErrInvalidRoundingMode
	case den.IsZero():
		wantErr = ErrDivideByZero
	case o.sticky():
		wantErr = ErrOverflow
	}
	if wantErr != nil {
		require.Equalf(t, wantErr, err, "divround validation: places=%d mode=%d ctx=%+v", places, mode, ctx)
		require.Equal(t, Decimal{}, got)
		return
	}
	num := new(big.Int).Mul(o.total(), bp10(int(den.prec)))
	if den.neg {
		num.Neg(num)
	}
	exact := new(big.Rat).SetFrac(num, new(big.Int).Mul(u128ToBig(den.coef), bp10(int(o.prec))))
	coef, neg := exactRoundRat(exact, places, mode)
	if coef.BitLen() > 128 {
		require.Equalf(t, ErrOverflow, err, "divround overflow: places=%d mode=%d ctx=%+v", places, mode, ctx)
		require.Equal(t, Decimal{}, got)
		return
	}
	require.NoErrorf(t, err, "divround: places=%d mode=%d ctx=%+v", places, mode, ctx)
	requireAggregateDecimal(t, got, coef, neg, places)
}

// requireAccFinishers runs every finisher against the oracle, and pins Div and
// DivRound to Sum followed by the Decimal method wherever Sum succeeds.
func requireAccFinishers(t *testing.T, a *Accumulator, o accOracle, den Decimal, places uint8, mode RoundingMode, ctx ...any) {
	t.Helper()
	requireAccSum(t, a, o, ctx...)
	requireAccDiv(t, a, o, den, ctx...)
	requireAccDivRound(t, a, o, den, places, mode, ctx...)
	sum, err := a.Sum()
	if err != nil {
		return
	}
	want, wantErr := sum.Div(den)
	got, gotErr := a.Div(den)
	require.Equalf(t, wantErr, gotErr, "Div vs Sum().Div: ctx=%+v", ctx)
	require.Equalf(t, want, got, "Div vs Sum().Div: ctx=%+v", ctx)
	want, wantErr = sum.DivRound(den, places, mode)
	got, gotErr = a.DivRound(den, places, mode)
	require.Equalf(t, wantErr, gotErr, "DivRound vs Sum().DivRound: ctx=%+v", ctx)
	require.Equalf(t, want, got, "DivRound vs Sum().DivRound: ctx=%+v", ctx)
}

// accRandDecimal returns a Decimal with a coefficient width uniform over
// 1..maxBits, any precision and sign, and canonical zero one time in twelve.
func accRandDecimal(rng *rand.Rand, maxBits int) Decimal {
	if rng.IntN(12) == 0 {
		return Decimal{}
	}
	return shapedDecimal(rng, maxBits, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
}

func accRandOps(rng *rand.Rand, n, maxBits int, withMul bool) []accOp {
	ops := make([]accOp, n)
	for i := range ops {
		kind := accOpKind(rng.IntN(2))
		if withMul && rng.IntN(3) == 0 {
			kind = accAddMul
		}
		ops[i] = accOp{kind: kind, d: accRandDecimal(rng, maxBits), e: accRandDecimal(rng, maxBits)}
	}
	return ops
}

func TestAccumulatorZeroValueAndReset(t *testing.T) {
	t.Parallel()
	var a Accumulator
	sum, err := a.Sum()
	require.NoError(t, err)
	require.Equal(t, Decimal{}, sum, "an empty sum is canonical zero")
	q, err := a.Div(NewFromInt(3))
	require.NoError(t, err)
	require.Equal(t, Decimal{}, q)
	q, err = a.DivRound(NewFromInt(3), 2, ToNearestEven)
	require.NoError(t, err)
	require.Equal(t, Decimal{}, q)
	_, err = a.Div(Decimal{})
	require.Equal(t, ErrDivideByZero, err, "an empty sum still rejects a zero divisor")

	a.Add(RequireFromString("1.25"))
	a.Sub(RequireFromString("0.5"))
	a.AddMul(RequireFromString("0.1"), NewFromInt(3))
	sum, err = a.Sum()
	require.NoError(t, err)
	require.Equal(t, RequireFromString("1.05"), sum)
	a.Reset()
	require.Equal(t, Accumulator{}, a, "Reset restores the zero value")

	maxValue := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	a.AddMul(maxValue, maxValue)
	a.AddMul(maxValue, maxValue)
	_, err = a.Sum()
	require.Equal(t, ErrOverflow, err)
	a.Reset()
	require.Equal(t, Accumulator{}, a, "Reset clears the sticky overflow")
	a.Add(One)
	sum, err = a.Sum()
	require.NoError(t, err)
	require.Equal(t, One, sum)
}

// TestAccumulatorZeroTermsKeepPrecision pins that a zero term, including an
// AddMul product that truncates to zero, adds precision 0 like Decimal{}.
func TestAccumulatorZeroTermsKeepPrecision(t *testing.T) {
	t.Parallel()
	var a Accumulator
	a.Add(RequireFromString("1.5"))
	a.Add(Decimal{})
	a.Sub(Decimal{})
	a.AddMul(Decimal{}, RequireFromString("0.0000000000000000001"))
	a.AddMul(RequireFromString("0.0000000000000000001"), RequireFromString("0.1")) // 1e-20 truncates to 0
	sum, err := a.Sum()
	require.NoError(t, err)
	require.Equal(t, RequireFromString("1.5"), sum)

	product := RequireFromString("0.0000000000000000001").MustMul(RequireFromString("0.1"))
	want, err := Sum(RequireFromString("1.5"), Decimal{}, product)
	require.NoError(t, err)
	require.Equal(t, want, sum, "representation-identical to Sum over the same terms")
}

// TestAccumulatorSumMatchesChains checks Add/Sub streams against the MustAdd
// chain (equal value whenever it succeeds) and the variadic Sum over the same
// terms (identical representation and error), plus the exact oracle.
func TestAccumulatorSumMatchesChains(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0xacc0_5eed, 0x5_c4a1))
	widths := [...]int{20, 40, 64, 96, 128}
	chainOK := 0
	for iter := range 4_000 {
		ops := accRandOps(rng, rng.IntN(65), widths[iter%len(widths)], false)
		a := accRun(ops)
		o := accOracleRun(ops)
		requireAccSum(t, a, o, ops)

		got, gotErr := a.Sum()
		terms := []Decimal{{}}
		chain := Decimal{}
		var chainErr error
		for _, op := range ops {
			term, _ := op.legacyTerm()
			terms = append(terms, term)
			if chainErr == nil {
				chain, chainErr = chain.Add(term)
			}
		}
		want, wantErr := Sum(terms[0], terms[1:]...)
		require.Equalf(t, wantErr, gotErr, "variadic Sum error: ops=%+v", ops)
		require.Equalf(t, want, got, "variadic Sum representation: ops=%+v", ops)
		if chainErr == nil {
			chainOK++
			require.NoError(t, gotErr)
			require.Truef(t, chain.Equal(got), "chain value: chain=%v got=%v ops=%+v", chain, got, ops)
		}
	}
	require.Greater(t, chainOK, 1_000, "too few successful chains to pin the equality")
}

// TestAccumulatorAddMulMatchesMulChain checks streams mixing AddMul with Add
// and Sub against MustMul + MustAdd chains and the variadic Sum over the
// products, whenever every Mul succeeds, plus the exact oracle everywhere.
func TestAccumulatorAddMulMatchesMulChain(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0xadd_3a1, 0x0c4a1))
	widths := [...]int{16, 32, 48, 64, 96, 128}
	mulOK, chainOK := 0, 0
	for iter := range 4_000 {
		ops := accRandOps(rng, rng.IntN(33), widths[iter%len(widths)], true)
		a := accRun(ops)
		o := accOracleRun(ops)
		requireAccSum(t, a, o, ops)

		terms := []Decimal{{}}
		chain := Decimal{}
		var mulErr, chainErr error
		for _, op := range ops {
			term, err := op.legacyTerm()
			if err != nil {
				mulErr = err
				break
			}
			terms = append(terms, term)
			if chainErr == nil {
				chain, chainErr = chain.Add(term)
			}
		}
		if mulErr != nil {
			continue
		}
		mulOK++
		got, gotErr := a.Sum()
		want, wantErr := Sum(terms[0], terms[1:]...)
		require.Equalf(t, wantErr, gotErr, "Sum over products error: ops=%+v", ops)
		require.Equalf(t, want, got, "Sum over products representation: ops=%+v", ops)
		if chainErr == nil {
			chainOK++
			require.Truef(t, chain.Equal(got), "chain value: chain=%v got=%v ops=%+v", chain, got, ops)
		}
	}
	require.Greater(t, mulOK, 1_000)
	require.Greater(t, chainOK, 500)
}

// TestAccumulatorMoneyShapes streams money-shaped inputs: quantities at 0..9
// places, prices at 2..16 places, and multipliers 1, 100 and 0.01, with the
// MustMul + MustAdd chain as the reference for every finisher.
func TestAccumulatorMoneyShapes(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6d6f_6e65, 0x79acc))
	mults := [...]Decimal{NewFromInt(1), NewFromInt(100), MustNew(1, -2)}
	for range 3_000 {
		var notional, qtyAcc Accumulator
		chain, qtyChain := Decimal{}, Decimal{}
		var ops []accOp
		mult := mults[rng.IntN(len(mults))]
		side := rng.IntN(2) == 0
		for range 1 + rng.IntN(24) {
			qtyPrec := uint8(rng.IntN(10))
			qty := newDecimal(u128{lo: 1 + rng.Uint64N(10_000*pow10u64[qtyPrec])}, side, qtyPrec)
			price := accRandPrice(rng, 2+uint8(rng.IntN(15)))
			pm := price.MustMul(mult)
			notional.AddMul(pm, qty)
			qtyAcc.Add(qty)
			ops = append(ops, accOp{kind: accAddMul, d: pm, e: qty})
			chain = chain.MustAdd(pm.MustMul(qty))
			qtyChain = qtyChain.MustAdd(qty)
		}
		o := accOracleRun(ops)
		den, err := qtyAcc.Sum()
		require.NoError(t, err)
		require.True(t, qtyChain.Equal(den))
		places := uint8(rng.IntN(int(MaxPrec) + 1))
		mode := RoundingMode(rng.IntN(int(TowardNegative) + 1))
		requireAccFinishers(t, &notional, o, den, places, mode, ops)

		sum, err := notional.Sum()
		require.NoError(t, err)
		require.True(t, chain.Equal(sum))
		got, err := notional.Div(den)
		require.NoError(t, err)
		require.Equal(t, chain.MustDiv(qtyChain), got, "legacy VWAP chain")
	}
}

// accRandPrice returns a positive price below 50 000 at prec places.
func accRandPrice(rng *rand.Rand, prec uint8) Decimal {
	const maxUnits = 50_000
	if prec <= 14 {
		return newDecimal(u128{lo: 1 + rng.Uint64N(maxUnits*pow10u64[prec])}, false, prec)
	}
	head := 1 + rng.Uint64N(maxUnits*pow10u64[14])
	scale := pow10u64[prec-14]
	hi, lo := bits.Mul64(head, scale)
	lo, carry := bits.Add64(lo, rng.Uint64N(scale), 0)
	return newDecimal(u128{hi: hi + carry, lo: lo}, false, prec)
}

// TestAccumulatorRandomFinishers checks Sum, Div and DivRound against the
// exact oracle on random streams, half of them biased to same-sign 128-bit
// terms so the total often passes 2^128 while the quotient still fits.
func TestAccumulatorRandomFinishers(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0xf1_7a11, 0xd1_5e5))
	widths := [...]int{24, 64, 100, 128}
	wideTotals, wideFits := 0, 0
	for iter := range 6_000 {
		var ops []accOp
		if iter&1 == 0 {
			ops = accRandOps(rng, rng.IntN(17), widths[iter%len(widths)], true)
		} else {
			// Same-sign terms of 120..128 bits: wide totals by construction.
			neg := rng.IntN(2) == 0
			for range 2 + rng.IntN(6) {
				d := newDecimal(shapedCoef(rng, 128), neg, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
				d.coef.hi |= 1 << 56
				ops = append(ops, accOp{kind: accAdd, d: d})
			}
		}
		a := accRun(ops)
		o := accOracleRun(ops)
		den := accRandDecimal(rng, widths[(iter/4)%len(widths)])
		places := uint8(rng.IntN(int(MaxPrec) + 2))             // MaxPrec+1 checks validation
		mode := RoundingMode(rng.IntN(int(TowardNegative) + 2)) // TowardNegative+1 too
		requireAccFinishers(t, a, o, den, places, mode, ops, den)
		if !o.sticky() && o.total().CmpAbs(mod128big) >= 0 {
			wideTotals++
			if _, err := a.Div(den); err == nil {
				wideFits++
			}
		}
	}
	require.Greater(t, wideTotals, 1_000, "too few totals past 128 bits")
	require.Greater(t, wideFits, 300, "too few wide totals with a representable quotient")
}

// TestAccumulatorWideDivision pins totals past 2^128 whose quotient fits:
// Div degrades precision exactly like Decimal.Div, and DivRound rounds the
// exact quotient in every mode.
func TestAccumulatorWideDivision(t *testing.T) {
	t.Parallel()
	maxValue := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	tiny := Decimal{coef: u128{lo: 1}, prec: MaxPrec}
	cases := []struct {
		name string
		ops  []accOp
		den  Decimal
	}{
		{"three_max_over_four", []accOp{{kind: accAdd, d: maxValue}, {kind: accAdd, d: maxValue}, {kind: accAdd, d: maxValue}}, NewFromInt(4)},
		{"three_max_over_minus_four", []accOp{{kind: accAdd, d: maxValue}, {kind: accAdd, d: maxValue}, {kind: accAdd, d: maxValue}}, NewFromInt(-4)},
		{"negative_max_plus_tiny_over_1e20", []accOp{{kind: accSub, d: maxValue}, {kind: accSub, d: tiny}}, MustNew(1, 20)},
		{"max_plus_tiny_over_fraction", []accOp{{kind: accAdd, d: maxValue}, {kind: accAdd, d: tiny}}, Decimal{coef: maxValue.coef, prec: MaxPrec}},
		{"wide_product_over_max", []accOp{{kind: accAddMul, d: maxValue, e: NewFromInt(3)}}, maxValue},
		{"wide_product_over_three", []accOp{{kind: accAddMul, d: maxValue, e: NewFromInt(2)}}, NewFromInt(3)},
		{"wide_quotient_overflows", []accOp{{kind: accAddMul, d: maxValue, e: NewFromInt(2)}}, MustNew(3, -1)},
		{"quotient_never_fits", []accOp{{kind: accAddMul, d: maxValue, e: maxValue}}, NewFromInt(7)},
		// (2^128-1)+0.5: at 0 places the truncated quotient is the largest
		// coefficient, and rounding the half up carries past it.
		{"rounding_carries_past_max", []accOp{{kind: accAdd, d: maxValue}, {kind: accAdd, d: MustNew(5, -1)}}, One},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := accRun(tc.ops)
			o := accOracleRun(tc.ops)
			require.GreaterOrEqual(t, o.total().CmpAbs(mod128big), 0, "the total must pass 128 bits")
			_, err := a.Sum()
			require.Equal(t, ErrOverflow, err)
			requireAccDiv(t, a, o, tc.den)
			for places := uint8(0); places <= MaxPrec; places++ {
				for mode := ToNearestAway; mode <= TowardNegative; mode++ {
					requireAccDivRound(t, a, o, tc.den, places, mode)
				}
			}
		})
	}

	// 3·(2^128-1)/4 = 3·2^126 - 3/4: Div truncates at precision 0, and the
	// remainder 1/4 decides each rounding mode.
	a := accRun(cases[0].ops)
	got, err := a.Div(NewFromInt(4))
	require.NoError(t, err)
	want := Decimal{coef: u128{hi: 3 << 62, lo: 0}}
	want.coef, _ = sub128(want.coef, u128{lo: 1})
	require.Equal(t, want, got)
	up, err := a.DivRound(NewFromInt(4), 0, AwayFromZero)
	require.NoError(t, err)
	require.Equal(t, Decimal{coef: u128{hi: 3 << 62}}, up)
	near, err := a.DivRound(NewFromInt(4), 0, ToNearestAway)
	require.NoError(t, err)
	require.Equal(t, want, near)

	carry := accRun(cases[len(cases)-1].ops)
	_, err = carry.DivRound(One, 0, ToNearestAway)
	require.Equal(t, ErrOverflow, err, "the rounding step itself overflows")
	down, err := carry.DivRound(One, 0, TowardZero)
	require.NoError(t, err)
	require.Equal(t, maxValue, down)
}

// TestAccumulatorOverflowSemantics pins the overflow contract: partial sums
// past 2^128 that cancel succeed where the chain fails, a final total past
// 2^128 fails, and a same-sign total of 2^256 or more fails for every order of
// the same terms, while 2^256-1 still fits.
func TestAccumulatorOverflowSemantics(t *testing.T) {
	t.Parallel()
	maxValue := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	maxNeg := maxValue.Neg()
	maxMinus7 := Decimal{coef: u128{hi: maxUint64, lo: maxUint64 - 7}}
	tiny := Decimal{coef: u128{lo: 1}, prec: MaxPrec}

	t.Run("partial_sums_cancel", func(t *testing.T) {
		ops := []accOp{{kind: accAdd, d: maxValue}, {kind: accAdd, d: maxValue}, {kind: accSub, d: maxValue}}
		_, err := maxValue.Add(maxValue)
		require.Equal(t, ErrOverflow, err, "the chain fails mid-stream")
		got, err := accRun(ops).Sum()
		require.NoError(t, err)
		require.Equal(t, maxValue, got)
		neg, err := accRun([]accOp{{kind: accSub, d: maxValue}, {kind: accSub, d: maxValue}, {kind: accAdd, d: maxValue}}).Sum()
		require.NoError(t, err)
		require.Equal(t, maxNeg, neg)
	})

	t.Run("final_total_overflows", func(t *testing.T) {
		_, err := accRun([]accOp{{kind: accAdd, d: maxValue}, {kind: accAdd, d: One}}).Sum()
		require.Equal(t, ErrOverflow, err)
		_, err = accRun([]accOp{{kind: accAdd, d: maxValue}, {kind: accAdd, d: tiny}}).Sum()
		require.Equal(t, ErrOverflow, err, "the total does not fit at the greatest precision")
	})

	permutationCases := []struct {
		name      string
		ops       []accOp
		sticky    bool
		wantTotal Decimal
	}{
		// (2^128-1)^2 is 2^256-2^129+1, so adding 2·(2^128-1) reaches exactly
		// 2^256-1 on each side; the sides differ by 7.
		{"width_boundary_fits", []accOp{
			{kind: accAddMul, d: maxValue, e: maxValue},
			{kind: accAdd, d: maxValue},
			{kind: accAdd, d: maxValue},
			{kind: accAddMul, d: maxValue, e: maxNeg},
			{kind: accSub, d: maxValue},
			{kind: accSub, d: maxMinus7},
		}, false, NewFromInt(7)},
		{"width_boundary_plus_one", []accOp{
			{kind: accAddMul, d: maxValue, e: maxValue},
			{kind: accAdd, d: maxValue},
			{kind: accAdd, d: maxValue},
			{kind: accAddMul, d: maxValue, e: maxNeg},
			{kind: accSub, d: maxValue},
			{kind: accSub, d: maxMinus7},
			{kind: accAdd, d: One},
		}, true, Decimal{}},
		// The side reaching 2^256 would wrap to 0 and leave a representable
		// -1: an Add of One (carry out of bit 63) or of max (carry out of bit
		// 127) crosses the width in some orders, the product in others.
		{"aligned_add_crosses_width", []accOp{
			{kind: accAddMul, d: maxValue, e: maxValue},
			{kind: accAdd, d: maxValue},
			{kind: accAdd, d: maxValue},
			{kind: accAdd, d: One},
			{kind: accSub, d: One},
		}, true, Decimal{}},
		// (2^128-1)^2 at 9 places plus 2^100 aligned by 10^9 passes 2^256
		// through the coarser-term path in some orders.
		{"coarser_add_crosses_width", []accOp{
			{kind: accAddMul, d: Decimal{coef: maxValue.coef, prec: 9}, e: maxValue},
			{kind: accAdd, d: Decimal{coef: u128{hi: 1 << 36}}},
			{kind: accSub, d: One},
		}, true, Decimal{}},
		{"two_max_products", []accOp{
			{kind: accAddMul, d: maxValue, e: maxValue},
			{kind: accAddMul, d: maxNeg, e: maxNeg},
			{kind: accSub, d: maxValue},
			{kind: accAdd, d: tiny},
		}, true, Decimal{}},
		{"precision_raise_overflows", []accOp{
			{kind: accAddMul, d: maxValue, e: maxValue},
			{kind: accAdd, d: tiny},
			{kind: accSub, d: One},
		}, true, Decimal{}},
		{"sides_cancel_after_overflow", []accOp{
			{kind: accAddMul, d: maxValue, e: maxValue},
			{kind: accAddMul, d: maxValue, e: maxValue},
			{kind: accAddMul, d: maxValue, e: maxNeg},
			{kind: accAddMul, d: maxNeg, e: maxValue},
		}, true, Decimal{}},
	}
	for _, tc := range permutationCases {
		t.Run(tc.name, func(t *testing.T) {
			o := accOracleRun(tc.ops)
			require.Equal(t, tc.sticky, o.sticky(), "fixture must sit on the intended side of 2^256")
			count := 0
			accPermutations(tc.ops, func(ops []accOp) {
				count++
				a := accRun(ops)
				got, err := a.Sum()
				if tc.sticky {
					require.Equal(t, ErrOverflow, err)
				} else {
					require.NoError(t, err)
					require.True(t, tc.wantTotal.Equal(got))
				}
				requireAccFinishers(t, a, o, NewFromInt(3), 2, ToNearestEven, ops)
			})
			want := 1
			for i := 2; i <= len(tc.ops); i++ {
				want *= i
			}
			require.Equal(t, want, count, "every permutation must be exercised")
		})
	}
}

func accPermutations(ops []accOp, visit func([]accOp)) {
	work := append([]accOp(nil), ops...)
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

// TestAccumulatorOrderIndependence streams random short sequences in every
// order: all finisher results, errors included, must be identical.
func TestAccumulatorOrderIndependence(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x0bde_5eed, 0x9e47))
	for iter := range 150 {
		ops := accRandOps(rng, 2+rng.IntN(4), [...]int{40, 128}[iter&1], true)
		den := accRandDecimal(rng, 64)
		places := uint8(rng.IntN(int(MaxPrec) + 1))
		mode := RoundingMode(rng.IntN(int(TowardNegative) + 1))
		ref := accRun(ops)
		wantSum, wantSumErr := ref.Sum()
		wantDiv, wantDivErr := ref.Div(den)
		wantRound, wantRoundErr := ref.DivRound(den, places, mode)
		accPermutations(ops, func(perm []accOp) {
			a := accRun(perm)
			sum, err := a.Sum()
			require.Equal(t, wantSumErr, err)
			require.Equal(t, wantSum, sum)
			div, err := a.Div(den)
			require.Equal(t, wantDivErr, err)
			require.Equal(t, wantDiv, div)
			round, err := a.DivRound(den, places, mode)
			require.Equal(t, wantRoundErr, err)
			require.Equal(t, wantRound, round)
		})
	}
}

// TestAccumulatorValidationOrder pins DivRound's places, mode, zero-divisor
// order, and the sticky overflow after all three, with bare sentinels.
func TestAccumulatorValidationOrder(t *testing.T) {
	t.Parallel()
	maxValue := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	var sticky Accumulator
	sticky.AddMul(maxValue, maxValue)
	sticky.AddMul(maxValue, maxValue)
	for _, a := range []*Accumulator{new(Accumulator), &sticky} {
		_, err := a.DivRound(Decimal{}, MaxPrec+1, RoundingMode(99))
		require.Equal(t, ErrPrecOutOfRange, err)
		_, err = a.DivRound(Decimal{}, MaxPrec, RoundingMode(99))
		require.Equal(t, ErrInvalidRoundingMode, err)
		_, err = a.DivRound(Decimal{}, MaxPrec, TowardZero)
		require.Equal(t, ErrDivideByZero, err)
		_, err = a.Div(Decimal{})
		require.Equal(t, ErrDivideByZero, err)
	}
	_, err := sticky.DivRound(One, 2, TowardZero)
	require.Equal(t, ErrOverflow, err)
	_, err = sticky.Div(One)
	require.Equal(t, ErrOverflow, err)
	_, err = sticky.Sum()
	require.Equal(t, ErrOverflow, err)
}

// accUdec mirrors d into udecimal through the shared (neg, hi, lo, prec)
// representation.
func accUdec(t *testing.T, d Decimal) udecimal.Decimal {
	t.Helper()
	neg, hi, lo, prec := d.ToHiLo()
	u, err := udecimal.NewFromHiLo(neg, hi, lo, prec)
	require.NoError(t, err)
	return u
}

// requireAccUdecIdentical asserts got carries udecimal's exact representation.
func requireAccUdecIdentical(t *testing.T, want udecimal.Decimal, got Decimal, ctx ...any) {
	t.Helper()
	neg, hi, lo, prec, ok := want.ToHiLo()
	require.Truef(t, ok, "udecimal result left 128 bits: %s ctx=%+v", want, ctx)
	gotNeg, gotHi, gotLo, gotPrec := got.ToHiLo()
	require.Equalf(t, [4]uint64{accBoolU64(neg), hi, lo, uint64(prec)}, [4]uint64{accBoolU64(gotNeg), gotHi, gotLo, uint64(gotPrec)},
		"udecimal=%s zerodecimal=%s ctx=%+v", want, got, ctx)
}

func accBoolU64(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

// TestAccumulatorUdecimalParity replays running sums with udecimal, which
// never fails, and requires bit-identical results:
//   - a VWAP: Σ price·mult·qty over Σ qty, finished by Div;
//   - a weighted average price: Σ qty·price over Σ qty, rounded to 16
//     places, which is Div then RoundHAZ(16), as DivRound(…, 16,
//     ToNearestAway).
func TestAccumulatorUdecimalParity(t *testing.T) {
	if DefaultPrec != 19 {
		t.Skip("udecimal truncates legacy Mul and Div at 19 places")
	}
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x0dec_1a1, 0x9a41))
	mults := [...]Decimal{NewFromInt(1), NewFromInt(100), MustNew(1, -2)}
	for stream := range 3_000 {
		mult := mults[stream%len(mults)]
		side := rng.IntN(2) == 0
		mixed := stream%5 == 0 // groups may net both sides
		var notional, qtyAcc, qtyPrice Accumulator
		uSum, uCount, uQtyPrice := udecimal.Zero, udecimal.Zero, udecimal.Zero
		for range 1 + rng.IntN(32) {
			neg := side
			if mixed {
				neg = rng.IntN(2) == 0
			}
			qty := newDecimal(u128{lo: 1 + rng.Uint64N(1e14)}, neg, 9)
			price := accRandPrice(rng, 9+uint8(rng.IntN(8)))
			uQty, uPrice := accUdec(t, qty), accUdec(t, price)

			// value := price * multiplier * qty
			uSum = uSum.Add(uPrice.Mul(accUdec(t, mult)).Mul(uQty))
			uCount = uCount.Add(uQty)
			notional.AddMul(price.MustMul(mult), qty)
			qtyAcc.Add(qty)

			// qtyPrice += qty * price
			uQtyPrice = uQtyPrice.Add(uQty.Mul(uPrice))
			qtyPrice.AddMul(qty, price)
		}
		sum, err := notional.Sum()
		require.NoError(t, err)
		requireAccUdecIdentical(t, uSum, sum, "notional", stream)
		den, err := qtyAcc.Sum()
		require.NoError(t, err)
		requireAccUdecIdentical(t, uCount, den, "qty", stream)
		qp, err := qtyPrice.Sum()
		require.NoError(t, err)
		requireAccUdecIdentical(t, uQtyPrice, qp, "qty·price", stream)

		uAvg, uErr := uSum.Div(uCount)
		avg, err := notional.Div(den)
		uRounded, uRoundErr := uQtyPrice.Div(uCount)
		rounded, roundErr := qtyPrice.DivRound(den, 16, ToNearestAway)
		if uCount.IsZero() {
			require.ErrorIs(t, uErr, udecimal.ErrDivideByZero)
			require.Equal(t, ErrDivideByZero, err)
			require.ErrorIs(t, uRoundErr, udecimal.ErrDivideByZero)
			require.Equal(t, ErrDivideByZero, roundErr)
			continue
		}
		require.NoError(t, uErr)
		require.NoError(t, err)
		requireAccUdecIdentical(t, uAvg, avg, "VWAP", stream)
		require.NoError(t, uRoundErr)
		require.NoError(t, roundErr)
		requireAccUdecIdentical(t, uRounded.RoundHAZ(16), rounded, "rounded to 16", stream)
	}
}

func TestAccumulatorLayout(t *testing.T) {
	t.Parallel()
	var want uintptr
	switch unsafe.Alignof(uint64(0)) {
	case 4:
		want = 68
	case 8:
		want = 72
	default:
		t.Fatalf("unsupported uint64 alignment: %d", unsafe.Alignof(uint64(0)))
	}
	require.Equal(t, want, unsafe.Sizeof(Accumulator{}), "Accumulator size changed; update its doc comment")
}

// TestAccumulatorInlineBudgets pins the per-term call shape: Add and Sub are
// thin wrappers that inline into the caller's loop, leaving one call into the
// shared body per term.
func TestAccumulatorInlineBudgets(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("inline budgets are calibrated for 64-bit compiler targets")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; cannot query inlining diagnostics")
	}
	out, err := exec.Command("go", "build", "-gcflags=-m=2", ".").CombinedOutput()
	require.NoError(t, err, "go build -gcflags=-m=2: %s", out)
	for _, fn := range []string{`\(\*Accumulator\)\.Add`, `\(\*Accumulator\)\.Sub`} {
		re := regexp.MustCompile(`: can inline ` + fn + ` with cost (\d+) `)
		m := re.FindSubmatch(out)
		require.NotNilf(t, m, "%s must fit the default inline budget", fn)
	}
}
