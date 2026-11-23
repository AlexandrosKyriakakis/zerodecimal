package zerodecimal

import (
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// checkRoundToMultipleOracle holds d.RoundToMultiple(step, mode) to the exact
// big.Rat quotient d/|step| rounded once to an integer q, in mode and with d's
// sign: the result must be q·|step| at step's precision, ErrDivideByZero for a
// zero step, and ErrOverflow exactly when q·|step| needs a coefficient past
// 128 bits at that precision.
func checkRoundToMultipleOracle(t *testing.T, d, step Decimal, mode RoundingMode) {
	t.Helper()
	got, err := d.RoundToMultiple(step, mode)
	if step.IsZero() {
		require.ErrorIsf(t, err, ErrDivideByZero, "d=%+v mode=%d", d, mode)
		require.Equal(t, Decimal{}, got)
		return
	}
	quo := new(big.Rat).Quo(exactDecimalRat(d), new(big.Rat).Abs(exactDecimalRat(step)))
	q, neg := exactRoundRat(quo, 0, mode)
	coef := q.Mul(q, exactBigFromU128(step.coef))
	if coef.BitLen() > 128 {
		require.ErrorIsf(t, err, ErrOverflow, "d=%+v step=%+v mode=%d want=%s", d, step, mode, coef)
		require.Equalf(t, Decimal{}, got, "overflow must return the zero Decimal: d=%+v step=%+v mode=%d", d, step, mode)
		return
	}
	require.NoErrorf(t, err, "d=%+v step=%+v mode=%d", d, step, mode)
	want := newDecimal(exactU128FromBig(coef), neg, step.prec)
	require.Equalf(t, want, got, "d=%+v step=%+v mode=%d quotient=%s", d, step, mode, quo.RatString())
}

// roundToMultipleSteps are the fixed steps the oracle tests draw from: share,
// cent, nickel and quarter ticks, board quantities of 100, odd steps whose quotients do not
// terminate, a step with trailing zeros, a two-limb step and the finest step,
// 10^-19.
var roundToMultipleSteps = []Decimal{
	MustNew(1, -3),   // 0.001 shares
	MustNew(1, -2),   // a cent
	MustNew(5, -2),   // a nickel
	MustNew(25, -2),  // a quarter tick
	MustNew(125, -3), // 0.125
	MustNew(3, -1),   // 0.3
	MustNew(15, -1),  // 1.5
	NewFromInt(5),
	NewFromInt(7),
	NewFromInt(25), // 25 cents, counted in cents
	NewFromInt(100),
	MustNew(2_500, -2),                 // 25.00
	{coef: u128{hi: 1, lo: 5}},         // 2^64 + 5
	{coef: u128{lo: 1}, prec: MaxPrec}, // 10^-19
}

// TestRoundToMultipleAgainstBigRat runs the oracle over d with coefficient
// widths uniform in 1..128 bits at every precision, against the fixed steps
// and against random ones of every width and precision, with both signs of
// each, so the one-limb path and every branch of the wide one occur often.
func TestRoundToMultipleAgainstBigRat(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x726f756e64, 0x6d756c74))
	widths := [...]int{8, 24, 40, 64, 96, 128}
	randPrec := func() uint8 { return uint8(rng.Uint64N(uint64(MaxPrec) + 1)) }
	for i := range 100_000 {
		// d's width is drawn independently, so fixed and random steps alike
		// meet every width.
		d := shapedDecimal(rng, widths[rng.IntN(len(widths))], randPrec())
		step := shapedDecimal(rng, widths[(i/6)%6], randPrec())
		if i%2 == 0 {
			step = roundToMultipleSteps[(i/2)%len(roundToMultipleSteps)]
		}
		if rng.IntN(2) == 0 {
			step = step.Neg()
		}
		switch {
		case i%41 == 0:
			step = Decimal{}
		case i%43 == 0:
			d = Decimal{}
		}
		checkRoundToMultipleOracle(t, d, step, RoundingMode(rng.Uint64N(uint64(TowardNegative)+1)))
	}
}

// roundToMultipleReps returns v as a Decimal at the least precision that holds
// it exactly and, below MaxPrec, also at MaxPrec. A representation whose
// coefficient exceeds 128 bits is left out, and so is every one when v needs
// more than MaxPrec fractional digits.
func roundToMultipleReps(v *big.Rat) []Decimal {
	neg := v.Sign() < 0
	abs := new(big.Rat).Abs(v)
	for p := 0; p <= int(MaxPrec); p++ {
		scaled := new(big.Rat).Mul(abs, new(big.Rat).SetInt(exactPow10(p)))
		if !scaled.IsInt() {
			continue
		}
		var reps []Decimal
		if c := scaled.Num(); c.BitLen() <= 128 {
			reps = append(reps, newDecimal(exactU128FromBig(c), neg, uint8(p)))
		}
		if c := new(big.Int).Mul(scaled.Num(), exactPow10(int(MaxPrec)-p)); p < int(MaxPrec) && c.BitLen() <= 128 {
			reps = append(reps, newDecimal(exactU128FromBig(c), neg, MaxPrec))
		}
		return reps
	}
	return nil
}

// TestRoundToMultipleNearTies runs the oracle in every mode, for both signs, on
// values exactly on a multiple of step, exactly on the midpoint between two
// multiples, and one 10^-19 unit to either side of each: the inputs a quotient
// truncated before rounding would get wrong. Multiples span 1..100-bit
// quotients, so ties land on both the one-limb and the wide path.
func TestRoundToMultipleNearTies(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x74696573, 0x6d756c74))
	unit := new(big.Rat).SetFrac(big.NewInt(1), exactPow10(int(MaxPrec)))
	half := big.NewRat(1, 2)
	steps := append([]Decimal(nil), roundToMultipleSteps...)
	for range 24 {
		steps = append(steps, shapedDecimal(rng, 1+rng.IntN(80), uint8(rng.Uint64N(uint64(MaxPrec)+1))).Abs())
	}
	ties := 0
	for _, step := range steps {
		s := exactDecimalRat(step)
		for range 30 {
			k := exactBigFromU128(shapedCoef(rng, 1+rng.IntN(100)))
			k.Sub(k, big.NewInt(1)) // k = 0 occurs too
			for _, midpoint := range []bool{false, true} {
				base := new(big.Rat).Mul(new(big.Rat).SetInt(k), s)
				if midpoint {
					base.Add(base, new(big.Rat).Mul(s, half))
				}
				for _, off := range []int64{-1, 0, 1} {
					v := new(big.Rat).Add(base, new(big.Rat).Mul(unit, big.NewRat(off, 1)))
					for _, d := range roundToMultipleReps(v) {
						if midpoint && off == 0 {
							ties++
						}
						for mode := ToNearestAway; mode <= TowardNegative; mode++ {
							checkRoundToMultipleOracle(t, d, step, mode)
							checkRoundToMultipleOracle(t, d.Neg(), step.Neg(), mode)
						}
					}
				}
			}
		}
	}
	require.Greater(t, ties, 1500, "too few exact midpoints were representable; the tie cases went untested")
	t.Logf("%d exact midpoints across %d steps", ties, len(steps))
}

// TestRoundToMultipleScalingBoundaries runs the oracle where the one-limb path
// hands over to the wide one, for every scale gap k: a dividend d·10^k and a
// divisor |step|·10^k just inside and just past 64 bits. At the divisor edge,
// d sits on, beside and halfway to |step|.
func TestRoundToMultipleScalingBoundaries(t *testing.T) {
	t.Parallel()
	check := func(d, step Decimal) {
		for mode := ToNearestAway; mode <= TowardNegative; mode++ {
			checkRoundToMultipleOracle(t, d, step, mode)
			checkRoundToMultipleOracle(t, d.Neg(), step, mode)
		}
	}
	for k := uint8(1); k <= MaxPrec; k++ {
		edge := maxUint64 / pow10u64[k] // the largest c whose c·10^k fits 64 bits
		for _, c := range []uint64{edge - 1, edge, edge + 1} {
			if c == 0 {
				continue // edge is 1 at k = MaxPrec
			}
			// The dividend d·10^k: d at precision 0, step at precision k.
			for _, s := range []uint64{1, 2, 3, 7, 25, edge} {
				check(newDecimal(u128{lo: c}, false, 0), newDecimal(u128{lo: s}, false, k))
			}
			// The divisor |step|·10^k: step at precision 0, d at precision k
			// around step/2 and step.
			den := new(big.Int).Mul(new(big.Int).SetUint64(c), exactPow10(int(k)))
			halfDen := new(big.Int).Rsh(den, 1)
			for _, n := range []*big.Int{
				big.NewInt(1),
				new(big.Int).Sub(halfDen, big.NewInt(1)), halfDen, new(big.Int).Add(halfDen, big.NewInt(1)),
				new(big.Int).Sub(den, big.NewInt(1)), den, new(big.Int).Add(den, big.NewInt(1)),
				new(big.Int).SetUint64(maxUint64),
			} {
				check(newDecimal(exactU128FromBig(n), false, k), newDecimal(u128{lo: c}, false, 0))
			}
		}
	}
}

// TestRoundToMultipleEdges pins hand-computed results: a zero step or d, a d
// already on a multiple (it takes step's precision either way), a step coarser
// or finer than d, every mode's direction for both signs, a step whose sign is
// ignored, the finest step 10^-19, a round-up that must carry across the
// 64-bit limb, and the range: quotients past 2^128, a product past 128 bits,
// and ties whose rounding direction alone decides overflow.
func TestRoundToMultipleEdges(t *testing.T) {
	t.Parallel()
	maxInt := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	twoTo64 := Decimal{coef: u128{hi: 1}}
	// (7·2^128 - 2)/10 over 0.7 is 2^128 - 2/7: the truncated quotient is the
	// largest 128-bit value with a remainder above half.
	pastMax := Decimal{coef: u128{hi: 0xb333333333333333, lo: 0x3333333333333333}}
	// 2^65 - 1/2 over a step of 1 takes the reciprocal arm (a two-limb d, a
	// 10^-p step): the truncated quotient 2^65 - 1 has a low limb of all ones,
	// so rounding it up must carry into the high limb.
	limbCarry := RequireFromString("36893488147419103231.5")
	tick := MustNew(25, -2)
	five := NewFromInt(5)
	finest := MustNew(1, -19)
	cases := []struct {
		name    string
		d, step Decimal
		mode    RoundingMode
		want    Decimal
		err     error
	}{
		{"zero_step", MustNew(15, -1), Decimal{}, ToNearestEven, Decimal{}, ErrDivideByZero},
		{"zero_d_zero_step", Decimal{}, Decimal{}, TowardZero, Decimal{}, ErrDivideByZero},
		{"zero_d", Decimal{}, tick, AwayFromZero, Decimal{}, nil},
		{"zero_d_negative_step", Decimal{}, tick.Neg(), TowardNegative, Decimal{}, nil},
		{"multiple_gains_step_precision", MustNew(15, -1), tick, AwayFromZero, MustNew(150, -2), nil},
		{"multiple_drops_trailing_zero", MustNew(150, -2), MustNew(5, -1), AwayFromZero, MustNew(15, -1), nil},
		{"multiple_negative_block", NewFromInt(-300), NewFromInt(100), TowardNegative, NewFromInt(-300), nil},
		{"tick_nearest", MustNew(10_137, -2), tick, ToNearestEven, MustNew(10_125, -2), nil},
		{"tick_up", MustNew(10_137, -2), tick, TowardPositive, MustNew(10_150, -2), nil},
		{"finer_d_takes_step_precision", MustNew(333_333_333, -7), MustNew(1, -3), TowardZero, MustNew(33_333, -3), nil},
		{"coarser_d_takes_step_precision", NewFromInt(7), MustNew(1, -3), ToNearestEven, MustNew(7_000, -3), nil},
		{"coarser_step_nearest", MustNew(73, -1), five, ToNearestEven, five, nil},
		{"coarser_step_tie_to_even", MustNew(125, -1), five, ToNearestEven, NewFromInt(10), nil},
		{"coarser_step_tie_away", MustNew(125, -1), five, ToNearestAway, NewFromInt(15), nil},
		{"coarser_step_toward_zero", MustNew(1, -1), five, TowardZero, Decimal{}, nil},
		{"coarser_step_away_from_zero", MustNew(1, -1), five, AwayFromZero, five, nil},
		{"positive_toward_negative", MustNew(73, -1), five, TowardNegative, five, nil},
		{"negative_toward_positive", MustNew(-73, -1), five, TowardPositive, five.Neg(), nil},
		{"negative_toward_negative", MustNew(-73, -1), five, TowardNegative, NewFromInt(-10), nil},
		{"negative_toward_zero", MustNew(-73, -1), five, TowardZero, five.Neg(), nil},
		{"negative_away_from_zero", MustNew(-1, -1), five, AwayFromZero, five.Neg(), nil},
		{"negative_tie_to_even", MustNew(-125, -1), five, ToNearestEven, NewFromInt(-10), nil},
		{"negative_tie_away", MustNew(-125, -1), five, ToNearestAway, NewFromInt(-15), nil},
		{"negative_rounds_to_canonical_zero", MustNew(-1, -1), five, TowardPositive, Decimal{}, nil},
		{"step_sign_ignored", MustNew(73, -1), five.Neg(), TowardPositive, NewFromInt(10), nil},
		{"finest_step_keeps_d", MustNew(1_234_567_890_123_456_789, -19), finest, AwayFromZero, MustNew(1_234_567_890_123_456_789, -19), nil},
		{"finest_step_scales_d", NewFromInt(1), finest, TowardZero, Decimal{coef: u128{lo: pow10u64[MaxPrec]}, prec: MaxPrec}, nil},
		{"tiny_d_huge_step_away", finest, Decimal{coef: u128{hi: 1 << 63}}, AwayFromZero, Decimal{coef: u128{hi: 1 << 63}}, nil},
		{"tiny_d_huge_step_nearest", finest, Decimal{coef: u128{hi: 1 << 63}}, ToNearestAway, Decimal{}, nil},
		{"max_by_one", maxInt, NewFromInt(1), ToNearestEven, maxInt, nil},
		{"max_by_finest_overflows", maxInt, finest, TowardZero, Decimal{}, ErrOverflow},
		{"max_by_one_and_a_half_overflows", maxInt, MustNew(15, -1), TowardZero, Decimal{}, ErrOverflow},
		{"max_by_two_tie_away_overflows", maxInt, NewFromInt(2), ToNearestAway, Decimal{}, ErrOverflow},
		{"max_by_two_tie_to_even_overflows", maxInt, NewFromInt(2), ToNearestEven, Decimal{}, ErrOverflow},
		{"max_by_two_toward_zero", maxInt, NewFromInt(2), TowardZero, Decimal{coef: u128{hi: maxUint64, lo: maxUint64 - 1}}, nil},
		{"max_by_two_limb_step_nearest_overflows", maxInt, twoTo64, ToNearestEven, Decimal{}, ErrOverflow},
		{"max_by_two_limb_step_toward_zero", maxInt, twoTo64, TowardZero, Decimal{coef: u128{hi: maxUint64}}, nil},
		{"quotient_rounds_past_2_128", pastMax, MustNew(7, -1), ToNearestEven, Decimal{}, ErrOverflow},
		{"quotient_truncates_below_2_128_overflows", pastMax, MustNew(7, -1), TowardZero, Decimal{}, ErrOverflow},
		{"round_up_carries_across_limbs_tie_away", limbCarry, NewFromInt(1), ToNearestAway, RequireFromString("36893488147419103232"), nil},
		{"round_up_carries_across_limbs_tie_to_even", limbCarry, NewFromInt(1), ToNearestEven, RequireFromString("36893488147419103232"), nil},
		{"round_up_carries_across_limbs_away", limbCarry, NewFromInt(1), AwayFromZero, RequireFromString("36893488147419103232"), nil},
		{"truncation_keeps_all_ones_low_limb", limbCarry, NewFromInt(1), TowardZero, RequireFromString("36893488147419103231"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.d.RoundToMultiple(tc.step, tc.mode)
			require.Equal(t, tc.err, err, "errors are bare sentinels")
			require.Equal(t, tc.want, got)
			checkRoundToMultipleOracle(t, tc.d, tc.step, tc.mode)
		})
	}
}

// TestRoundToMultipleValidation pins the validation order: the mode first,
// then a zero step, both ahead of a zero d and of a d already on a multiple.
func TestRoundToMultipleValidation(t *testing.T) {
	t.Parallel()
	one, three := NewFromInt(1), NewFromInt(3)
	_, err := one.RoundToMultiple(Decimal{}, RoundingMode(6))
	require.Equal(t, ErrInvalidRoundingMode, err, "the mode is validated first")
	_, err = Decimal{}.RoundToMultiple(Decimal{}, RoundingMode(255))
	require.Equal(t, ErrInvalidRoundingMode, err, "even for a zero d")
	_, err = three.RoundToMultiple(three, RoundingMode(6))
	require.Equal(t, ErrInvalidRoundingMode, err, "even when d is already a multiple")
	_, err = one.RoundToMultiple(Decimal{}, ToNearestEven)
	require.Equal(t, ErrDivideByZero, err, "then a zero step")
	got, err := Decimal{}.RoundToMultiple(Decimal{}, TowardZero)
	require.Equal(t, ErrDivideByZero, err, "a zero d still needs a step")
	require.Equal(t, Decimal{}, got)
	got, err = Decimal{}.RoundToMultiple(three.Neg(), TowardNegative)
	require.NoError(t, err)
	require.Equal(t, Decimal{}, got)
}

// TestRoundToMultipleAgreesWithDivRound pins the documented mode semantics: q
// is d.DivRound(|step|, 0, mode), so the result is q·|step| at step's
// precision, and ErrOverflow exactly when DivRound overflows or that product
// needs more than 128 bits.
func TestRoundToMultipleAgreesWithDivRound(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x646976, 0x726f756e64))
	for i := range 20_000 {
		d := shapedDecimal(rng, 1+rng.IntN(128), uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		step := shapedDecimal(rng, 1+rng.IntN(128), uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		if i%2 == 0 {
			step = roundToMultipleSteps[(i/2)%len(roundToMultipleSteps)].Neg()
		}
		for mode := ToNearestAway; mode <= TowardNegative; mode++ {
			got, err := d.RoundToMultiple(step, mode)
			q, qErr := d.DivRound(step.Abs(), 0, mode)
			if qErr != nil {
				require.Equal(t, ErrOverflow, qErr)
				require.Equalf(t, ErrOverflow, err, "d=%+v step=%+v mode=%d", d, step, mode)
				continue
			}
			coef := new(big.Int).Mul(exactBigFromU128(q.coef), exactBigFromU128(step.coef))
			if coef.BitLen() > 128 {
				require.Equalf(t, ErrOverflow, err, "d=%+v step=%+v mode=%d", d, step, mode)
				continue
			}
			require.NoErrorf(t, err, "d=%+v step=%+v mode=%d", d, step, mode)
			require.Equalf(t, newDecimal(exactU128FromBig(coef), q.neg, step.prec), got, "d=%+v step=%+v mode=%d", d, step, mode)
		}
	}
}

// TestRoundToMultiplePow10StepIsRounding pins a step of 10^-p below d's
// precision to the named rounding method for each mode at p places, which
// crosscheck_test.go holds to shopspring: the same representation, on the
// one-limb path and on the reciprocal arm of the wide path (two-limb d).
func TestRoundToMultiplePow10StepIsRounding(t *testing.T) {
	t.Parallel()
	named := map[RoundingMode]func(Decimal, uint8) Decimal{
		ToNearestAway:  Decimal.Round,
		ToNearestEven:  Decimal.RoundBank,
		AwayFromZero:   Decimal.RoundUp,
		TowardZero:     Decimal.RoundDown,
		TowardPositive: Decimal.RoundCeil,
		TowardNegative: Decimal.RoundFloor,
	}
	rng := rand.New(rand.NewPCG(0x706f77, 0x3130))
	for i := range 20_000 {
		dp := 1 + uint8(rng.Uint64N(uint64(MaxPrec)))
		d := shapedDecimal(rng, []int{32, 64, 96, 128}[i%4], dp)
		p := uint8(rng.Uint64N(uint64(dp)))
		step := newDecimal(u128{lo: 1}, rng.IntN(2) == 0, p)
		for mode, round := range named {
			got, err := d.RoundToMultiple(step, mode)
			require.NoErrorf(t, err, "d=%+v p=%d mode=%d", d, p, mode)
			require.Equalf(t, round(d, p), got, "d=%+v p=%d mode=%d", d, p, mode)
		}
	}
}
