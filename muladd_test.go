package zerodecimal

import (
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// checkMulAddOracle holds a.MulAddRound(b, c, places, mode) to the exact
// big.Rat value a*b + c rounded once: the same coefficient, sign and precision,
// or ErrOverflow exactly when the rounded coefficient exceeds 128 bits.
func checkMulAddOracle(t *testing.T, a, b, c Decimal, places uint8, mode RoundingMode) {
	t.Helper()
	got, err := a.MulAddRound(b, c, places, mode)
	exact := new(big.Rat).Mul(exactDecimalRat(a), exactDecimalRat(b))
	exact.Add(exact, exactDecimalRat(c))
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

// TestMulAddRoundAgainstBigRat runs the oracle over coefficient widths uniform
// in 1..128 bits for every operand, so both the one-limb path and every
// alignment, sign and overflow case of the wide path occur often.
func TestMulAddRoundAgainstBigRat(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6d756c61, 0x646472))
	widths := [...]int{8, 24, 40, 64, 96, 128}
	randPrec := func() uint8 { return uint8(rng.Uint64N(uint64(MaxPrec) + 1)) }
	for i := range 80_000 {
		a := shapedDecimal(rng, widths[i%6], randPrec())
		b := shapedDecimal(rng, widths[(i/6)%6], randPrec())
		c := shapedDecimal(rng, widths[(i/36)%6], randPrec())
		if i%17 == 0 {
			c = Decimal{}
		}
		if i%29 == 0 {
			a = Decimal{}
		}
		checkMulAddOracle(t, a, b, c, randPrec(), RoundingMode(rng.Uint64N(uint64(TowardNegative)+1)))
	}
}

// TestMulAddRoundMoneyShapes runs the oracle on the shapes the one-limb path
// exists for — quantities at 0..9 places, prices at 0..4, fees at 0..3,
// cents out — with small coefficients, where ties are frequent.
func TestMulAddRoundMoneyShapes(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6d6f6e6579, 0x73686170))
	for range 100_000 {
		qty := newDecimal(u128{lo: 1 + rng.Uint64N(1<<uint(1+rng.IntN(40)))}, rng.IntN(2) == 0, uint8(rng.IntN(10)))
		price := newDecimal(u128{lo: 1 + rng.Uint64N(1<<uint(1+rng.IntN(30)))}, false, uint8(rng.IntN(5)))
		fee := newDecimal(u128{lo: rng.Uint64N(1 << uint(1+rng.IntN(20)))}, rng.IntN(4) == 0, uint8(rng.IntN(4)))
		places := uint8(rng.IntN(5))
		mode := RoundingMode(rng.Uint64N(uint64(TowardNegative) + 1))
		checkMulAddOracle(t, qty, price, fee, places, mode)
	}
}

// TestMulAddRoundTiesAndCancellation pins exact ties (decided by the mode),
// sums that cancel to canonical zero, sign changes across the addend, and an
// addend at a higher precision than the product.
func TestMulAddRoundTiesAndCancellation(t *testing.T) {
	t.Parallel()
	rs := RequireFromString
	cases := []struct {
		name    string
		a, b, c Decimal
		places  uint8
	}{
		{"tie_even_down", rs("0.5"), rs("0.5"), rs("0.125"), 2}, // 0.375 -> 0.38 even / away
		{"tie_odd_up", rs("1.5"), rs("0.1"), rs("0.005"), 2},    // 0.155
		{"tie_through_addend", rs("2.5"), rs("1"), rs("0"), 0},  // 2.5
		{"negative_tie", rs("-2.5"), rs("1"), rs("0"), 0},       // -2.5
		{"cancel_to_zero", rs("1.25"), rs("4"), rs("-5"), 2},    // 0
		{"cancel_wide", rs("12345678901234567890.5"), rs("2"), rs("-24691357802469135781"), 0},
		{"sign_flip", rs("0.1"), rs("0.2"), rs("-0.03"), 1}, // -0.01 -> -0.0 / -0.1
		{"addend_finer", rs("3"), rs("7"), rs("0.0000000000000000005"), 18},
		{"product_finer", rs("0.0000000001"), rs("0.0000000005"), rs("1"), 19},
		{"exact_scale_up", rs("12"), rs("3"), rs("1.5"), 4},
		{"zero_product", Decimal{}, rs("3"), rs("-1.005"), 2},
		{"zero_addend", rs("1.005"), rs("1"), Decimal{}, 2},
		{"all_zero", Decimal{}, Decimal{}, Decimal{}, 3},
	}
	for _, tc := range cases {
		for mode := ToNearestAway; mode <= TowardNegative; mode++ {
			t.Run(tc.name, func(t *testing.T) {
				checkMulAddOracle(t, tc.a, tc.b, tc.c, tc.places, mode)
				checkMulAddOracle(t, tc.a.Neg(), tc.b, tc.c.Neg(), tc.places, mode)
			})
		}
	}
	got, err := RequireFromString("1.25").MulAddRound(NewFromInt(4), NewFromInt(-5), 2, ToNearestEven)
	require.NoError(t, err)
	require.Equal(t, Decimal{}, got, "cancellation must return canonical zero")
}

// TestMulAddRoundRange pins the overflow boundaries and the claim that no
// intermediate overflows on its own: a product past 2^256 once aligned to a
// finer addend, a same-sign sum carrying out of 256 bits, a 256-bit product
// that an opposite-sign addend brings back into range, and results that just
// fit or just overflow 128 bits.
func TestMulAddRoundRange(t *testing.T) {
	t.Parallel()
	maxCoef := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	maxInt := maxCoef  // 2^128-1 at precision 0
	maxFrac := maxCoef // the same coefficient at precision 19
	maxFrac.prec = MaxPrec
	tiny := Decimal{coef: u128{lo: 1}, prec: MaxPrec}
	cases := []struct {
		name    string
		a, b, c Decimal
		places  uint8
	}{
		{"max_times_one", maxInt, NewFromInt(1), Decimal{}, 0},
		{"max_plus_one_overflows", maxInt, NewFromInt(1), NewFromInt(1), 0},
		{"max_minus_one", maxInt, NewFromInt(1), NewFromInt(-1), 0},
		{"product_past_2_256_aligned_to_addend", maxInt, maxInt, tiny, 0},
		{"sum_carries_past_2_256", maxFrac, maxFrac, maxFrac, 0},
		{"wide_product_cancelled", maxInt, NewFromInt(2), maxInt.Neg(), 0},
		{"wide_product_rounded_down", maxFrac, maxFrac, Decimal{}, 0},
		{"scale_up_overflows", maxInt, NewFromInt(1), Decimal{}, 1},
		{"scale_up_fits", Decimal{coef: u128{hi: maxUint64 / 10}}, NewFromInt(1), Decimal{}, 1},
	}
	// Two cases whose 256-bit wrap would land in range if unchecked. First, a
	// product that passes 2^256 by less than 2^127·10^19 when aligned to an
	// addend at 19 places: (2^127)·⌈2^129/10^19⌋·10^19.
	pow19 := exactPow10(int(MaxPrec))
	two129 := new(big.Int).Lsh(big.NewInt(1), 129)
	near := new(big.Int).Add(two129, new(big.Int).Sub(pow19, big.NewInt(1)))
	near.Quo(near, pow19)
	cases = append(cases, struct {
		name    string
		a, b, c Decimal
		places  uint8
	}{"product_wraps_when_aligned", Decimal{coef: exactU128FromBig(near)}, Decimal{coef: u128{hi: 1 << 63}}, tiny, 0})
	// Second, a same-sign sum that passes 2^256 by less than 10^38: a 38-place
	// product plus an integer addend scaled by 10^38 to just past 2^256.
	pa := new(big.Int).Sub(exactMaxBig(), new(big.Int).Lsh(big.NewInt(1), 72))
	prod := new(big.Int).Mul(pa, exactMaxBig())
	pow38 := exactPow10(2 * int(MaxPrec))
	gap := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), prod)
	fc := new(big.Int).Add(gap, new(big.Int).Sub(pow38, big.NewInt(1)))
	fc.Quo(fc, pow38)
	cases = append(cases, struct {
		name    string
		a, b, c Decimal
		places  uint8
	}{"sum_wraps_just_past_2_256", Decimal{coef: exactU128FromBig(pa), prec: MaxPrec}, Decimal{coef: maxCoef.coef, prec: MaxPrec}, Decimal{coef: exactU128FromBig(fc)}, 0})
	for _, tc := range cases {
		for mode := ToNearestAway; mode <= TowardNegative; mode++ {
			t.Run(tc.name, func(t *testing.T) {
				checkMulAddOracle(t, tc.a, tc.b, tc.c, tc.places, mode)
			})
		}
	}
}

func TestMulAddRoundValidation(t *testing.T) {
	t.Parallel()
	one := NewFromInt(1)
	_, err := one.MulAddRound(one, one, MaxPrec+1, RoundingMode(255))
	require.ErrorIs(t, err, ErrPrecOutOfRange, "places is validated before mode")
	_, err = one.MulAddRound(one, one, 2, RoundingMode(255))
	require.ErrorIs(t, err, ErrInvalidRoundingMode)
	_, err = Decimal{}.MulAddRound(Decimal{}, Decimal{}, 2, TowardNegative+1)
	require.ErrorIs(t, err, ErrInvalidRoundingMode, "zero operands still validate the mode")
}

// TestMulAddRoundMatchesLegacyChain pins the documented migration property:
// with d.Prec()+e.Prec() ≤ DefaultPrec the legacy product is exact, so
// wherever d.Mul(e).Add(f) succeeds, rounding it in the corresponding mode
// gives MulAddRound's value.
func TestMulAddRoundMatchesLegacyChain(t *testing.T) {
	t.Parallel()
	legacy := map[RoundingMode]func(Decimal, uint8) Decimal{
		ToNearestAway:  Decimal.Round,
		ToNearestEven:  Decimal.RoundBank,
		AwayFromZero:   Decimal.RoundUp,
		TowardZero:     Decimal.RoundDown,
		TowardPositive: Decimal.RoundCeil,
		TowardNegative: Decimal.RoundFloor,
	}
	rng := rand.New(rand.NewPCG(0x6c656761, 0x6379))
	checked := 0
	for range 60_000 {
		dp := uint8(rng.Uint64N(uint64(DefaultPrec) + 1))
		ep := uint8(rng.Uint64N(uint64(DefaultPrec-dp) + 1))
		a := shapedDecimal(rng, 1+rng.IntN(90), dp)
		b := shapedDecimal(rng, 1+rng.IntN(90), ep)
		c := shapedDecimal(rng, 1+rng.IntN(127), uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		p, err := a.Mul(b)
		if err != nil {
			continue
		}
		s, err := p.Add(c)
		if err != nil {
			continue
		}
		places := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		mode := RoundingMode(rng.Uint64N(uint64(TowardNegative) + 1))
		want := legacy[mode](s, places)
		got, err := a.MulAddRound(b, c, places, mode)
		if _, scaleErr := want.Rescale(places); scaleErr != nil {
			// The legacy value fits at its own precision but not at places,
			// where MulAddRound's result must carry it.
			require.ErrorIsf(t, err, ErrOverflow, "a=%+v b=%+v c=%+v places=%d", a, b, c, places)
			continue
		}
		require.NoErrorf(t, err, "a=%+v b=%+v c=%+v places=%d", a, b, c, places)
		require.Truef(t, got.Equal(want), "a=%v b=%v c=%v places=%d mode=%d: MulAddRound %v, legacy %v", a, b, c, places, mode, got, want)
		checked++
	}
	require.Greater(t, checked, 30_000, "legacy chain rarely succeeded; the property went untested")
}

func TestMulAddRoundAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	ops := []struct {
		name string
		fn   func()
	}{
		{"one_limb", func() { mulAddSink, errMulAdd = mulAddQty.MulAddRound(mulAddPrice, mulAddFee, 2, ToNearestEven) }},
		{"wide", func() { mulAddSink, errMulAdd = mulAddWideA.MulAddRound(mulAddWideB, mulAddFee, 2, ToNearestEven) }},
		{"overflow", func() { mulAddSink, errMulAdd = mulAddMax.MulAddRound(mulAddMax, mulAddMax, 0, ToNearestEven) }},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			requireAllocs(t, 0, op.fn)
		})
	}
}
