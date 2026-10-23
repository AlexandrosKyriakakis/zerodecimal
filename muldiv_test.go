package zerodecimal

import (
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// checkMulDivOracle holds a.MulDivRound(b, c, places, mode) to the exact
// big.Rat value a*b/c rounded once: the same coefficient, sign and precision,
// ErrDivideByZero for a zero c, and ErrOverflow exactly when the rounded
// coefficient exceeds 128 bits.
func checkMulDivOracle(t *testing.T, a, b, c Decimal, places uint8, mode RoundingMode) {
	t.Helper()
	got, err := a.MulDivRound(b, c, places, mode)
	if c.IsZero() {
		require.ErrorIsf(t, err, ErrDivideByZero, "a=%+v b=%+v", a, b)
		return
	}
	exact := new(big.Rat).Mul(exactDecimalRat(a), exactDecimalRat(b))
	exact.Quo(exact, exactDecimalRat(c))
	wantCoef, wantNeg := exactRoundRat(exact, places, mode)
	if wantCoef.BitLen() > 128 {
		require.ErrorIsf(t, err, ErrOverflow, "a=%+v b=%+v c=%+v places=%d mode=%d", a, b, c, places, mode)
		require.Equal(t, Decimal{}, got)
		return
	}
	require.NoErrorf(t, err, "a=%+v b=%+v c=%+v places=%d mode=%d", a, b, c, places, mode)
	want := newDecimal(exactU128FromBig(wantCoef), wantNeg, places)
	require.Equalf(t, want, got, "a=%+v b=%+v c=%+v places=%d mode=%d exact=%s", a, b, c, places, mode, exact.RatString())
}

// TestMulDivRoundAgainstBigRat runs the oracle over coefficient widths
// uniform in 1..128 bits for every operand and every precision, so the
// one-limb path, both scalings and all three divisions occur often.
func TestMulDivRoundAgainstBigRat(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6d756c64, 0x6976))
	widths := [...]int{8, 24, 40, 64, 96, 128}
	randPrec := func() uint8 { return uint8(rng.Uint64N(uint64(MaxPrec) + 1)) }
	for i := range 80_000 {
		a := shapedDecimal(rng, widths[i%6], randPrec())
		b := shapedDecimal(rng, widths[(i/6)%6], randPrec())
		c := shapedDecimal(rng, widths[(i/36)%6], randPrec())
		switch {
		case i%31 == 0:
			c = Decimal{}
		case i%37 == 0:
			a = Decimal{}
		}
		checkMulDivOracle(t, a, b, c, randPrec(), RoundingMode(rng.Uint64N(uint64(TowardNegative)+1)))
	}
}

// TestMulDivRoundShares runs the oracle on pro-rata shares, amount × part /
// whole with part ≤ whole at one precision, the shape the one-limb path
// exists for; small wholes make exact half-cent ties frequent.
func TestMulDivRoundShares(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x7368617265, 0x73))
	for range 100_000 {
		qp := uint8(rng.IntN(10))
		whole := 1 + rng.Uint64N(1<<uint(1+rng.IntN(40)))
		part := 1 + rng.Uint64N(whole)
		amount := newDecimal(u128{lo: rng.Uint64N(1 << uint(1+rng.IntN(40)))}, rng.IntN(2) == 0, uint8(rng.IntN(5)))
		mode := RoundingMode(rng.Uint64N(uint64(TowardNegative) + 1))
		checkMulDivOracle(t, amount, newDecimal(u128{lo: part}, false, qp), newDecimal(u128{lo: whole}, rng.IntN(5) == 0, qp), 2, mode)
	}
}

// TestMulDivRoundWideDenominator pins divRoundPow10By, the path for a scaled
// denominator v·10^t past 128 bits, on every branch of its half comparison:
// 2·r2 - v ≥ 1, = 0 (with and without a low remainder r1), = -1 (with r1
// below, on and above 10^t/2) and ≤ -2, for one- and two-limb v and for t on
// both sides of MaxPrec.
func TestMulDivRoundWideDenominator(t *testing.T) {
	t.Parallel()
	divisors := []*big.Int{
		big.NewInt(3), big.NewInt(4), big.NewInt(5), big.NewInt(1001), big.NewInt(1_000_000),
		new(big.Int).SetUint64(maxUint64),
		new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1)),
		new(big.Int).Lsh(big.NewInt(3), 100),
	}
	two128 := new(big.Int).Lsh(big.NewInt(1), 128)
	branches := map[string]int{}
	for t10 := 1; t10 <= 2*int(MaxPrec); t10++ {
		pow := exactPow10(t10)
		half := new(big.Int).Rsh(pow, 1)
		for _, v := range divisors {
			den := new(big.Int).Mul(v, pow)
			if den.Cmp(two128) < 0 {
				continue // divRoundWide's territory, covered by the random tests
			}
			vh := new(big.Int).Rsh(v, 1)
			for _, r2 := range []*big.Int{big.NewInt(0), new(big.Int).Sub(vh, big.NewInt(1)), vh, new(big.Int).Add(vh, big.NewInt(1)), new(big.Int).Sub(v, big.NewInt(1))} {
				if r2.Sign() < 0 || r2.Cmp(v) >= 0 {
					continue
				}
				for _, k := range []int64{0, 1, 2} {
					w := new(big.Int).Add(new(big.Int).Mul(big.NewInt(k), v), r2)
					for _, r1 := range []*big.Int{big.NewInt(0), big.NewInt(1), new(big.Int).Sub(half, big.NewInt(1)), half, new(big.Int).Add(half, big.NewInt(1)), new(big.Int).Sub(pow, big.NewInt(1))} {
						num := new(big.Int).Add(new(big.Int).Mul(w, pow), r1)
						if num.Sign() == 0 || num.BitLen() > 128 {
							continue
						}
						// a·1/v with a at precision t10 split over a and b: the
						// result coefficient is num / (v·10^t10) at places 0.
						ap := uint8(min(t10, int(MaxPrec)))
						a := newDecimal(exactU128FromBig(num), false, ap)
						b := newDecimal(u128{lo: 1}, false, uint8(t10)-ap)
						c := newDecimal(exactU128FromBig(v), false, 0)
						for mode := ToNearestAway; mode <= TowardNegative; mode++ {
							checkMulDivOracle(t, a, b, c, 0, mode)
							checkMulDivOracle(t, a.Neg(), b, c, 0, mode)
						}
						// Classify the case by the half-comparison branch it takes.
						delta := new(big.Int).Sub(new(big.Int).Lsh(r2, 1), v)
						switch {
						case delta.Cmp(big.NewInt(1)) >= 0:
							branches["2r2-v>=1"]++
						case delta.Sign() == 0 && r1.Sign() == 0:
							branches["2r2-v=0,r1=0"]++
						case delta.Sign() == 0:
							branches["2r2-v=0,r1>0"]++
						case delta.Cmp(big.NewInt(-1)) == 0:
							branches["2r2-v=-1,2r1"+[]string{"<", "=", ">"}[new(big.Int).Lsh(r1, 1).Cmp(pow)+1]+"10^t"]++
						default:
							branches["2r2-v<=-2"]++
						}
					}
				}
			}
		}
	}
	for _, b := range []string{"2r2-v>=1", "2r2-v=0,r1=0", "2r2-v=0,r1>0", "2r2-v=-1,2r1<10^t", "2r2-v=-1,2r1=10^t", "2r2-v=-1,2r1>10^t", "2r2-v<=-2"} {
		require.Positivef(t, branches[b], "branch %s never taken: %v", b, branches)
	}
}

// TestMulDivRoundRange pins overflow on both scalings: a product that passes
// 2^256 once scaled up (the quotient then exceeds 2^128 for any divisor),
// quotients that just fit or just overflow, and a tiny quotient of a huge
// denominator that only the rounding mode lifts to one unit.
func TestMulDivRoundRange(t *testing.T) {
	t.Parallel()
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	maxInt := Decimal{coef: maxCoef}
	two := NewFromInt(2)
	cases := []struct {
		name    string
		a, b, c Decimal
		places  uint8
	}{
		{"max_over_one", maxInt, NewFromInt(1), NewFromInt(1), 0},
		{"max_squared_over_max", maxInt, maxInt, maxInt, 0},
		{"max_squared_over_max_scaled_up", maxInt, maxInt, maxInt, 1},
		{"max_squared_over_half_max", maxInt, maxInt, Decimal{coef: u128{hi: maxUint64 >> 1, lo: maxUint64}}, 0},
		{"max_times_two_over_two", maxInt, two, two, 0},
		{"product_past_2_256_scaled", maxInt, maxInt, maxInt, 19},
		{"one_limb_scaled_past_2_64", Decimal{coef: u128{lo: maxUint64}}, NewFromInt(1), NewFromInt(3), 19},
		{"tiny_over_huge", Decimal{coef: u128{lo: 1}, prec: MaxPrec}, Decimal{coef: u128{lo: 1}, prec: MaxPrec}, maxInt, 0},
		{"tiny_over_huge_places", Decimal{coef: u128{lo: 7}, prec: MaxPrec}, Decimal{coef: u128{lo: 3}, prec: MaxPrec}, Decimal{coef: u128{lo: 9}}, MaxPrec},
	}
	// A product whose first 10^MaxPrec scaling step (s = 38) passes 2^256 by
	// less than 2^65·10^19: unchecked, the wrapped value would survive the
	// second step and divide into range. 2^65·⌈2^191/10^19⌉ is just past
	// 2^256/10^19.
	pow19 := exactPow10(int(MaxPrec))
	ceil := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 191), new(big.Int).Sub(pow19, big.NewInt(1)))
	ceil.Quo(ceil, pow19)
	cases = append(cases, struct {
		name    string
		a, b, c Decimal
		places  uint8
	}{"first_scaling_step_wraps", Decimal{coef: exactU128FromBig(ceil)}, Decimal{coef: u128{hi: 2}}, Decimal{coef: maxCoef, prec: MaxPrec}, MaxPrec})
	for _, tc := range cases {
		for mode := ToNearestAway; mode <= TowardNegative; mode++ {
			t.Run(tc.name, func(t *testing.T) {
				checkMulDivOracle(t, tc.a, tc.b, tc.c, tc.places, mode)
				checkMulDivOracle(t, tc.a.Neg(), tc.b, tc.c, tc.places, mode)
			})
		}
	}
}

func TestMulDivRoundValidation(t *testing.T) {
	t.Parallel()
	one := NewFromInt(1)
	_, err := one.MulDivRound(one, Decimal{}, MaxPrec+1, RoundingMode(255))
	require.ErrorIs(t, err, ErrPrecOutOfRange, "places is validated first")
	_, err = one.MulDivRound(one, Decimal{}, 2, RoundingMode(255))
	require.ErrorIs(t, err, ErrInvalidRoundingMode, "then the mode")
	_, err = one.MulDivRound(one, Decimal{}, 2, ToNearestEven)
	require.ErrorIs(t, err, ErrDivideByZero, "then the divisor")
	_, err = Decimal{}.MulDivRound(one, Decimal{}, 2, ToNearestEven)
	require.ErrorIs(t, err, ErrDivideByZero, "a zero product still needs a divisor")
	got, err := Decimal{}.MulDivRound(one, NewFromInt(-3), 2, ToNearestEven)
	require.NoError(t, err)
	require.Equal(t, Decimal{}, got)
}

// TestMulDivRoundLegacyChain pins the documented relation to the legacy
// chain: with an exact product and a DefaultPrec-digit quotient, Mul then Div
// then the matching rounding agrees in ToNearestAway and TowardZero, and in
// the other modes differs only within 10^-DefaultPrec past the boundary the
// mode rounds away from.
func TestMulDivRoundLegacyChain(t *testing.T) {
	t.Parallel()
	legacy := map[RoundingMode]func(Decimal, uint8) Decimal{
		ToNearestAway:  Decimal.Round,
		ToNearestEven:  Decimal.RoundBank,
		AwayFromZero:   Decimal.RoundUp,
		TowardZero:     Decimal.RoundDown,
		TowardPositive: Decimal.RoundCeil,
		TowardNegative: Decimal.RoundFloor,
	}
	ulp := new(big.Rat).SetFrac(big.NewInt(1), exactPow10(int(DefaultPrec)))
	rng := rand.New(rand.NewPCG(0x6c65676163, 0x79))
	checked, differed := 0, 0
	check := func(a, b, c Decimal, places uint8, mode RoundingMode) {
		p, err := a.Mul(b)
		if err != nil {
			return
		}
		quo, err := p.Div(c)
		if err != nil || quo.Prec() != DefaultPrec || places >= DefaultPrec {
			return
		}
		got, err := a.MulDivRound(b, c, places, mode)
		require.NoErrorf(t, err, "a=%v b=%v c=%v", a, b, c)
		want := legacy[mode](quo, places)
		checked++
		if got.Equal(want) {
			return
		}
		differed++
		require.NotContainsf(t, []RoundingMode{ToNearestAway, TowardZero}, mode,
			"a=%v b=%v c=%v places=%d mode=%d: MulDivRound %v, legacy %v", a, b, c, places, mode, got, want)
		// The exact quotient lies less than one DefaultPrec unit past the
		// boundary the truncated quotient landed on.
		exact := new(big.Rat).Mul(exactDecimalRat(a), exactDecimalRat(b))
		exact.Quo(exact, exactDecimalRat(c))
		exact.Abs(exact)
		landed := new(big.Rat).Abs(exactDecimalRat(quo))
		past := new(big.Rat).Sub(exact, landed)
		require.Truef(t, past.Sign() > 0 && past.Cmp(ulp) < 0,
			"a=%v b=%v c=%v places=%d mode=%d: differs %s past the truncated quotient", a, b, c, places, mode, past.FloatString(25))
	}
	for range 100_000 {
		dp := uint8(rng.Uint64N(uint64(DefaultPrec) + 1))
		ep := uint8(rng.Uint64N(uint64(DefaultPrec-dp) + 1))
		a := shapedDecimal(rng, 1+rng.IntN(60), dp)
		b := shapedDecimal(rng, 1+rng.IntN(60), ep)
		c := shapedDecimal(rng, 1+rng.IntN(70), uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		check(a, b, c, uint8(rng.Uint64N(uint64(DefaultPrec))), RoundingMode(rng.Uint64N(uint64(TowardNegative)+1)))
	}
	// Both documented windows, built to land on them: (T·7 + 1)/7 at
	// DefaultPrec places is T plus 1/7 of a unit, which Div truncates to T.
	// T = 0.005 is a tie: RoundBank keeps 0.00, the exact value rounds to 0.01.
	// T = 0.01 is a multiple of 10^-2: RoundUp keeps 0.01, the exact 0.02.
	differedBefore := differed
	for _, tie := range []int64{5, 10} {
		tc := new(big.Int).Mul(big.NewInt(tie), exactPow10(int(DefaultPrec)-3))
		tc.Mul(tc, big.NewInt(7))
		tc.Add(tc, big.NewInt(1))
		a := newDecimal(exactU128FromBig(tc), false, DefaultPrec)
		for mode := ToNearestAway; mode <= TowardNegative; mode++ {
			check(a, NewFromInt(1), NewFromInt(7), 2, mode)
		}
	}
	require.Greater(t, differed-differedBefore, 1, "the constructed boundary cases must differ")
	require.Greater(t, checked, 50_000, "legacy chain rarely succeeded; the property went untested")
	t.Logf("%d legacy comparisons, %d differed (all within one DefaultPrec unit)", checked, differed)
}

func TestMulDivRoundAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	ops := []struct {
		name string
		fn   func()
	}{
		{"share", func() { mulDivSink, errMulDiv = mulDivAmount.MulDivRound(mulDivPart, mulDivWhole, 2, ToNearestEven) }},
		{"wide", func() { mulDivSink, errMulDiv = mulDivWideA.MulDivRound(mulDivWideB, mulDivWhole, 2, ToNearestEven) }},
		{"wide_denominator", func() { mulDivSink, errMulDiv = mulDivTiny.MulDivRound(mulDivTiny, mulDivHuge, 0, AwayFromZero) }},
		{"divide_by_zero", func() { mulDivSink, errMulDiv = mulDivAmount.MulDivRound(mulDivPart, mulDivZero, 2, ToNearestEven) }},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			requireAllocs(t, 0, op.fn)
		})
	}
}
