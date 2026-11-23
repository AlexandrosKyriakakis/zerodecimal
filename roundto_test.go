package zerodecimal

import (
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roundToNamedMethods pairs each RoundingMode with the named rounding method
// that RoundTo must reproduce representation for representation, and with the
// mode name roundModeOracle dispatches on. TowardZero appears twice because
// RoundDown and Truncate are one rounding under two names.
var roundToNamedMethods = [...]struct {
	mode   RoundingMode
	method string
	oracle string
	apply  func(Decimal, uint8) Decimal
}{
	{ToNearestAway, "Round", "round", Decimal.Round},
	{ToNearestEven, "RoundBank", "bank", Decimal.RoundBank},
	{AwayFromZero, "RoundUp", "up", Decimal.RoundUp},
	{TowardZero, "RoundDown", "down", Decimal.RoundDown},
	{TowardZero, "Truncate", "down", Decimal.Truncate},
	{TowardPositive, "RoundCeil", "ceil", Decimal.RoundCeil},
	{TowardNegative, "RoundFloor", "floor", Decimal.RoundFloor},
}

// roundToTie is a decimal whose digits below places sit just under, exactly
// on, or just over the half-way point of that rounding.
type roundToTie struct {
	d      Decimal
	places uint8
}

// roundToTies constructs, for every precision and every dropped-digit count
// k = 1..prec, coefficients q·10^k + 10^k/2 + delta with delta in {-1, 0, +1}:
// the three decisions around a tie. The kept quotients q cover the carry
// chains (9, 99), the one-limb to two-limb coefficient boundary, the top of
// the 128-bit range, and random widths, so both limb paths of every core meet
// ties of every k and of both parities. Each coefficient is built for both
// signs.
func roundToTies(t *testing.T) []roundToTie {
	t.Helper()
	rng := rand.New(rand.NewPCG(0x80A1, 0x7013))
	one := big.NewInt(1)
	limit64 := new(big.Int).Lsh(one, 64)
	limit128 := new(big.Int).Lsh(one, 128)

	var ties []roundToTie
	for prec := uint8(1); prec <= MaxPrec; prec++ {
		for k := uint8(1); k <= prec; k++ {
			unit := pow10Big(int(k))
			half := new(big.Int).Rsh(unit, 1) // 10^k is even for k ≥ 1
			// The largest q whose tie still fits 64 bits, and the largest
			// that fits 128 bits.
			q64 := new(big.Int).Div(new(big.Int).Sub(limit64, new(big.Int).Add(half, one)), unit)
			qMax := new(big.Int).Div(new(big.Int).Sub(limit128, new(big.Int).Add(half, one)), unit)

			qs := []*big.Int{
				big.NewInt(0), big.NewInt(1), big.NewInt(2), big.NewInt(3),
				big.NewInt(9), big.NewInt(10), big.NewInt(99), big.NewInt(100),
				q64, new(big.Int).Add(q64, one),
				qMax, new(big.Int).Sub(qMax, one),
			}
			span := new(big.Int).Add(qMax, one)
			for range 4 {
				q := u128ToBig(randShapedU128(rng))
				q.Rsh(q, uint(rng.IntN(128)))
				qs = append(qs, q.Mod(q, span))
			}

			for _, q := range qs {
				for delta := int64(-1); delta <= 1; delta++ {
					coef := new(big.Int).Mul(q, unit)
					coef.Add(coef, half)
					coef.Add(coef, big.NewInt(delta))
					if coef.Sign() <= 0 || coef.Cmp(limit128) >= 0 {
						continue
					}
					for _, neg := range []bool{false, true} {
						ties = append(ties, roundToTie{
							d:      mustFromBig(t, neg, coef, prec),
							places: prec - k,
						})
					}
				}
			}
		}
	}
	return ties
}

// TestRoundToTiesCoverEveryK guards the generator the two oracle tests rest
// on: for every dropped-digit count k = 1..MaxPrec it must produce exact ties
// with a one-limb and with a two-limb coefficient, each with an even and with
// an odd kept quotient, and in both signs.
func TestRoundToTiesCoverEveryK(t *testing.T) {
	type bucket struct {
		k       uint8
		twoLimb bool
		odd     bool
		neg     bool
	}
	seen := map[bucket]int{}
	for _, tie := range roundToTies(t) {
		neg, hi, lo, prec := tie.d.ToHiLo()
		k := prec - tie.places
		unit := pow10Big(int(k))
		q, r := new(big.Int).QuoRem(u128ToBig(u128{hi: hi, lo: lo}), unit, new(big.Int))
		if r.Cmp(new(big.Int).Rsh(unit, 1)) != 0 {
			continue // just below or just above the tie
		}
		seen[bucket{k: k, twoLimb: hi != 0, odd: q.Bit(0) == 1, neg: neg}]++
	}
	for k := uint8(1); k <= MaxPrec; k++ {
		for _, twoLimb := range []bool{false, true} {
			for _, odd := range []bool{false, true} {
				for _, neg := range []bool{false, true} {
					b := bucket{k: k, twoLimb: twoLimb, odd: odd, neg: neg}
					assert.Positive(t, seen[b], "no exact tie for %+v", b)
				}
			}
		}
	}
}

// checkRoundToShape asserts the representation contract shared by every mode:
// places at or above d's precision returns d untouched, a zero result is the
// canonical Decimal{}, and anything else carries exactly places digits.
func checkRoundToShape(t *testing.T, d, got Decimal, places uint8, method string) {
	t.Helper()
	switch {
	case places >= d.Prec():
		require.Equal(t, d, got, "%s of %+v at %d places must return d unchanged", method, d, places)
	case got.IsZero():
		require.Equal(t, Decimal{}, got, "%s of %+v at %d places: zero must be canonical", method, d, places)
	default:
		require.Equal(t, places, got.Prec(), "%s of %+v at %d places", method, d, places)
	}
}

// TestRoundToMatchesNamedMethods pins the headline contract: for every mode,
// every places value 0..25 (past MaxPrec, so the no-op arm is covered) and a
// large population of ties and random values, RoundTo returns the very same
// Decimal, bit for bit, as the named method of that mode.
func TestRoundToMatchesNamedMethods(t *testing.T) {
	var values []Decimal
	for _, tie := range roundToTies(t) {
		values = append(values, tie.d)
	}
	rng := rand.New(rand.NewPCG(0x80A2, 0x7014))
	for range 20_000 {
		values = append(values, randDecimal(rng))
	}
	// Representations a random draw rarely lands on: canonical zero, integers,
	// the largest coefficient at every precision, and untrimmed zeros.
	values = append(values, Decimal{}, NewFromInt(1), NewFromInt(-1), NewFromInt(42))
	for prec := range MaxPrec + 1 {
		values = append(
			values,
			mustHiLo(t, false, maxUint64, maxUint64, prec),
			mustHiLo(t, true, maxUint64, maxUint64, prec),
			mustHiLo(t, false, 0, 100, prec),
			mustHiLo(t, true, 0, 5, prec),
		)
	}

	for _, d := range values {
		for places := range uint8(26) {
			for _, m := range roundToNamedMethods {
				got, err := d.RoundTo(places, m.mode)
				want := m.apply(d, places)
				if err != nil || got != want {
					require.Fail(t, "RoundTo diverges from "+m.method,
						"d=%+v places=%d mode=%d: RoundTo=%+v err=%v, %s=%+v",
						d, places, m.mode, got, err, m.method, want)
				}
			}
		}
	}
}

// TestRoundToMatchesOracle checks RoundTo against exact big.Rat arithmetic,
// independent of the library's divmod cores that the named methods share with
// it: at the places of every constructed tie, and at a random places for a
// population of random values.
func TestRoundToMatchesOracle(t *testing.T) {
	check := func(d Decimal, places uint8) {
		t.Helper()
		for _, m := range roundToNamedMethods {
			got, err := d.RoundTo(places, m.mode)
			want := roundModeOracle(d, places, m.oracle)
			if err != nil || want.Cmp(decimalToRat(got)) != 0 {
				require.Fail(t, "RoundTo mismatch",
					"%s of %+v at %d places: want %s, got %+v err=%v", m.oracle, d, places, want, got, err)
			}
			checkRoundToShape(t, d, got, places, "RoundTo")
		}
	}

	for _, tie := range roundToTies(t) {
		check(tie.d, tie.places)
	}
	rng := rand.New(rand.NewPCG(0x80A3, 0x7015))
	for range 10_000 {
		check(randDecimal(rng), uint8(rng.Uint64N(26)))
	}
}

// TestRoundToTieTables pins literal expectations for the six modes, one row
// per value, so the contract is readable without trusting any other rounding
// code. Each row lists the result in RoundingMode order: ToNearestAway,
// ToNearestEven, AwayFromZero, TowardZero, TowardPositive, TowardNegative.
// The first three rows are the README's rounding table.
func TestRoundToTieTables(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		places uint8
		want   [6]string
	}{
		{"pos_2_5_to_int", "2.5", 0, [6]string{"3", "2", "3", "2", "3", "2"}},
		{"pos_3_5_to_int", "3.5", 0, [6]string{"4", "4", "4", "3", "4", "3"}},
		{"neg_2_5_to_int", "-2.5", 0, [6]string{"-3", "-2", "-3", "-2", "-2", "-3"}},
		{"neg_0_5_to_int", "-0.5", 0, [6]string{"-1", "0", "-1", "0", "0", "-1"}},
		{"pos_0_05_to_1", "0.05", 1, [6]string{"0.1", "0", "0.1", "0", "0.1", "0"}},
		{"neg_0_15_to_1", "-0.15", 1, [6]string{"-0.2", "-0.2", "-0.2", "-0.1", "-0.1", "-0.2"}},
		{"neg_0_25_to_1", "-0.25", 1, [6]string{"-0.3", "-0.2", "-0.3", "-0.2", "-0.2", "-0.3"}},
		{"pos_below_tie", "1.2349", 2, [6]string{"1.23", "1.23", "1.24", "1.23", "1.24", "1.23"}},
		{"neg_below_tie", "-1.2349", 2, [6]string{"-1.23", "-1.23", "-1.24", "-1.23", "-1.23", "-1.24"}},
		{"pos_above_tie", "1.2351", 2, [6]string{"1.24", "1.24", "1.24", "1.23", "1.24", "1.23"}},
		{"neg_above_tie", "-1.2351", 2, [6]string{"-1.24", "-1.24", "-1.24", "-1.23", "-1.23", "-1.24"}},
		{"carry_chain", "9.9999999999999999995", 18, [6]string{
			"10", "10", "10", "9.999999999999999999", "10", "9.999999999999999999",
		}},
		{"exact_value_is_unchanged", "1.25", 2, [6]string{"1.25", "1.25", "1.25", "1.25", "1.25", "1.25"}},
	}
	modes := [6]RoundingMode{ToNearestAway, ToNearestEven, AwayFromZero, TowardZero, TowardPositive, TowardNegative}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString(tc.in)
			for i, mode := range modes {
				got, err := d.RoundTo(tc.places, mode)
				require.NoError(t, err, "mode %d", mode)
				assert.Equal(t, tc.want[i], got.String(), "mode %d", mode)
				checkRoundToShape(t, d, got, tc.places, "RoundTo")
			}
		})
	}
}

// TestRoundToNearMaxCoefficient pins the family's infallibility argument
// through RoundTo: the largest coefficient never overflows when a step up
// follows the division, in either sign and at any dropped-digit count.
func TestRoundToNearMaxCoefficient(t *testing.T) {
	pos := mustHiLo(t, false, maxUint64, maxUint64, 19)
	neg := mustHiLo(t, true, maxUint64, maxUint64, 19)
	tests := []struct {
		name   string
		d      Decimal
		places uint8
		mode   RoundingMode
		want   string
	}{
		{"away_to_int", pos, 0, AwayFromZero, "34028236692093846347"},
		{"away_to_18", pos, 18, AwayFromZero, "34028236692093846346.337460743176821146"},
		{"nearest_away_stays_down", pos, 0, ToNearestAway, "34028236692093846346"},
		{"toward_positive_steps_up", pos, 0, TowardPositive, "34028236692093846347"},
		{"toward_negative_truncates", pos, 0, TowardNegative, "34028236692093846346"},
		{"negative_away_to_int", neg, 0, AwayFromZero, "-34028236692093846347"},
		{"negative_toward_positive_truncates", neg, 0, TowardPositive, "-34028236692093846346"},
		{"negative_toward_negative_steps_down", neg, 0, TowardNegative, "-34028236692093846347"},
		{"negative_nearest_even_stays_down", neg, 0, ToNearestEven, "-34028236692093846346"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.d.RoundTo(tc.places, tc.mode)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.String())
		})
	}
}

// TestRoundToLowersPrecisionEvenWhenExact pins that rounding to fewer places
// than d carries rescales d even when no digit is lost, exactly like the
// named methods: 1.50 at precision 2 becomes 1.5 at precision 1.
func TestRoundToLowersPrecisionEvenWhenExact(t *testing.T) {
	for _, m := range roundToNamedMethods {
		got, err := mustHiLo(t, false, 0, 150, 2).RoundTo(1, m.mode)
		require.NoError(t, err, m.method)
		assert.Equal(t, Decimal{coef: u128{lo: 15}, prec: 1}, got, m.method)

		got, err = mustHiLo(t, true, 0, 500, 2).RoundTo(0, m.mode)
		require.NoError(t, err, m.method)
		assert.Equal(t, Decimal{coef: u128{lo: 5}, neg: true}, got, m.method)
	}
}

// TestRoundToPlacesAtOrAbovePrecIsNoOp covers the shortcut in every mode:
// places at or above the precision, up to the whole uint8 range, returns d
// itself with no error, untrimmed zeros included.
func TestRoundToPlacesAtOrAbovePrecIsNoOp(t *testing.T) {
	tests := []struct {
		name string
		d    Decimal
	}{
		{"price", RequireFromString("-1234.5678")},
		{"untrimmed_zeros", mustHiLo(t, false, 0, 150, 2)},
		{"max_prec", mustHiLo(t, false, maxUint64, maxUint64, 19)},
		{"integer", NewFromInt(42)},
		{"zero", Decimal{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, m := range roundToNamedMethods {
				// Every precision is at most MaxPrec, so each places value
				// below is at or above it.
				for _, places := range []uint8{tc.d.Prec(), tc.d.Prec() + 1, MaxPrec, MaxPrec + 1, 200, 255} {
					got, err := tc.d.RoundTo(places, m.mode)
					require.NoError(t, err, "%s at places %d", m.method, places)
					require.Equal(t, tc.d, got, "%s at places %d must return d unchanged", m.method, places)
				}
			}
		})
	}
}

// TestRoundToNegativeToZeroIsCanonical pins the canonical-zero invariant:
// a negative value that rounds to zero loses its sign and its precision.
func TestRoundToNegativeToZeroIsCanonical(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		places uint8
		mode   RoundingMode
	}{
		{"nearest_away_neg_0_4", "-0.4", 0, ToNearestAway},
		{"nearest_even_neg_0_5", "-0.5", 0, ToNearestEven},
		{"toward_zero_neg_0_9", "-0.9", 0, TowardZero},
		{"toward_positive_neg_0_9", "-0.9", 0, TowardPositive},
		{"toward_zero_neg_0_05", "-0.05", 1, TowardZero},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RequireFromString(tc.in).RoundTo(tc.places, tc.mode)
			require.NoError(t, err)
			require.Equal(t, Decimal{}, got, "a negative value rounding to zero must lose the sign")
			assert.Equal(t, "0", got.String())
			assert.False(t, got.IsNegative())
		})
	}
}

// TestRoundToInvalidMode pins the error contract: every value past
// TowardNegative, up to the top of uint8 (6..255), is ErrInvalidRoundingMode
// with the zero Decimal, and MustRoundTo panics with that same sentinel. Each
// mode is tried at a spread of places values (0, 2, d.Prec(), MaxPrec,
// MaxPrec+1 and 255), not at every one: the last four are at or above the
// precision, where the no-op shortcut would otherwise hide the bad mode.
func TestRoundToInvalidMode(t *testing.T) {
	tests := []struct {
		name string
		d    Decimal
	}{
		{"zero", Decimal{}},
		{"integer", NewFromInt(42)},
		{"price", RequireFromString("-1234.5678")},
		{"max_prec", mustHiLo(t, false, maxUint64, maxUint64, 19)},
		{"negative_max_prec", mustHiLo(t, true, maxUint64, maxUint64, 19)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for mode := TowardNegative + 1; mode != 0; mode++ { // 6..255, then the uint8 wrap ends the loop
				for _, places := range []uint8{0, 2, tc.d.Prec(), MaxPrec, MaxPrec + 1, 255} {
					got, err := tc.d.RoundTo(places, mode)
					require.ErrorIs(t, err, ErrInvalidRoundingMode, "mode %d places %d", mode, places)
					require.Equal(t, Decimal{}, got, "mode %d places %d: failure must return the zero Decimal", mode, places)
					require.PanicsWithValue(t, ErrInvalidRoundingMode, func() {
						_ = tc.d.MustRoundTo(places, mode)
					}, "mode %d places %d", mode, places)
				}
			}
		})
	}
}

// TestRoundToMustTwin pins the twin contract on valid modes: MustRoundTo
// returns what RoundTo returns and never panics.
func TestRoundToMustTwin(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x80A4, 0x7016))
	for range 5_000 {
		d := randDecimal(rng)
		places := uint8(rng.Uint64N(26))
		for _, m := range roundToNamedMethods {
			want, err := d.RoundTo(places, m.mode)
			require.NoError(t, err)
			require.NotPanics(t, func() {
				require.Equal(t, want, d.MustRoundTo(places, m.mode), "%s of %+v at %d places", m.method, d, places)
			})
		}
	}
}

// TestRoundToValidatesModeBeforePlaces pins the argument order shared with
// MulRound and DivRound: with both a no-op places and a bad mode the mode
// wins; with a valid mode the same call succeeds untouched.
func TestRoundToValidatesModeBeforePlaces(t *testing.T) {
	d := RequireFromString("1.25")
	_, err := d.RoundTo(MaxPrec, TowardNegative+1)
	require.ErrorIs(t, err, ErrInvalidRoundingMode)

	got, err := d.RoundTo(MaxPrec, TowardNegative)
	require.NoError(t, err)
	require.Equal(t, d, got)
}
