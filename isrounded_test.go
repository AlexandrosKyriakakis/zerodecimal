package zerodecimal

import (
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// isRoundedOracle reports whether d has no nonzero digit past places
// fractional digits, from big.Int arithmetic alone: the coefficient must be a
// multiple of 10^(prec-places). It shares no code with the library's
// reciprocal-division core.
func isRoundedOracle(d Decimal, places uint8) bool {
	if places >= d.Prec() {
		return true
	}
	rem := new(big.Int).Rem(u128ToBig(d.coef), pow10Big(int(d.Prec()-places)))
	return rem.Sign() == 0
}

// fracDigitsOracle returns the least f in 0..Prec such that d·10^f is an
// integer, taken straight from the definition with big.Rat. Zero is the
// canonical Decimal{}, whose f = 0 the loop returns at once.
func fracDigitsOracle(d Decimal) uint8 {
	r := decimalToRat(d)
	for f := range int(d.Prec()) + 1 {
		if new(big.Rat).Mul(r, new(big.Rat).SetInt(pow10Big(f))).IsInt() {
			return uint8(f)
		}
	}
	panic("fracDigitsOracle: d·10^Prec is always an integer")
}

// isRoundedShapes are the values the allocation gate and the benchmarks run
// over: every limb count, every kind of precision overhang, and the zero and
// 2^128-1 extremes. The names say how many trailing fractional zeros the
// stored representation carries and whether the value fits two places.
var isRoundedShapes = []struct {
	name string
	d    Decimal
}{
	{"zero", Decimal{}},
	{"integer", NewFromInt(42)},
	{"prec2", RequireFromString("12.34")},
	{"prec2_trailing_zero", RequireFromString("12.3").MustRescale(2)},
	{"one_limb_prec19_no_zeros", RequireFromString("1.2345678901234567891")},
	{"one_limb_prec19_cents", RequireFromString("1.25").MustRescale(19)},
	{"one_limb_prec19_whole", NewFromInt(1).MustRescale(19)},
	{"two_limb_prec19_no_zeros", RequireFromString("1234.5678901234567891234")},
	{"two_limb_prec19_cents", RequireFromString("1234.56").MustRescale(19)},
	{"two_limb_prec19_four_digits", RequireFromString("1234.5678").MustRescale(19)},
	{"two_limb_prec19_whole", NewFromInt(12345).MustRescale(19)},
	{"negative_two_limb_prec19_cents", RequireFromString("-1234.56").MustRescale(19)},
	{"max_coef_prec19", Decimal{coef: u128{hi: maxUint64, lo: maxUint64}, prec: 19}},
}

// isRoundedPlaces are the places arguments every identity sweep covers: every
// value that can be below, at or above a precision (0..MaxPrec+2, the last two
// already past every precision), then the uint8 maximum.
var isRoundedPlaces = func() []uint8 {
	ps := make([]uint8, 0, 23)
	for p := range MaxPrec + 3 {
		ps = append(ps, p)
	}
	return append(ps, 255)
}()

// isRoundedCoefs returns the coefficients the identity sweeps run over,
// without duplicates. They aim at the places where a divisibility test can go
// wrong:
//   - the limb and 2^128 boundaries, 10^k for every k that fits 128 bits, and
//     the neighbours of each (an exact multiple and the values just off it);
//   - m·10^z for every z in 0..19 over many mantissas, which have exactly z
//     trailing zeros whenever m is not a multiple of ten;
//   - 2^a·5^b: divisible by 10^min(a,b) but with a binary or a quinary excess
//     that a test of only one of the two prime factors would mistake for more;
//   - two-limb values whose low limb alone looks like a multiple of 10^j,
//     because 2^64 mod 10 = 6 means the low limb says nothing there;
//   - shaped random coefficients of every bit width.
func isRoundedCoefs(t *testing.T, rng *rand.Rand) []u128 {
	t.Helper()
	seen := map[u128]bool{}
	var coefs []u128
	addPlain := func(c u128) {
		if !seen[c] {
			seen[c] = true
			coefs = append(coefs, c)
		}
	}
	// add also takes the values just below and above c.
	add := func(c u128) {
		addPlain(c)
		if below, borrow := sub128(c, u128{lo: 1}); borrow == 0 {
			addPlain(below)
		}
		if above, carry := add128(c, u128{lo: 1}); carry == 0 {
			addPlain(above)
		}
	}
	fits := func(b *big.Int) (u128, bool) {
		if b.BitLen() > 128 {
			return u128{}, false
		}
		return bigToU128(t, b), true
	}

	for _, tc := range boundary128 {
		add(tc.v)
	}
	for k := range 39 {
		add(pow10u128[k])
	}

	// Fixed mantissas get their neighbours too; the random ones only the exact
	// multiple, since their neighbours are just more random coefficients.
	fixed := []*big.Int{
		big.NewInt(1), big.NewInt(3), big.NewInt(7), big.NewInt(9), big.NewInt(11),
		big.NewInt(99), big.NewInt(123456789), big.NewInt(5), big.NewInt(25),
		new(big.Int).SetUint64(maxUint64), // 2^64-1
		new(big.Int).Lsh(big.NewInt(1), 64),
		new(big.Int).Lsh(big.NewInt(1), 100),
	}
	var random []*big.Int
	for range 8 {
		m := u128ToBig(shapedCoef(rng, 128))
		if new(big.Int).Rem(m, big.NewInt(10)).Sign() == 0 {
			m.Add(m, big.NewInt(1)) // keep the trailing zero count exactly z
		}
		random = append(random, m)
	}
	for z := range 20 {
		for _, m := range fixed {
			if c, ok := fits(new(big.Int).Mul(m, pow10Big(z))); ok {
				add(c)
			}
		}
		for _, m := range random {
			if c, ok := fits(new(big.Int).Mul(m, pow10Big(z))); ok {
				addPlain(c)
			}
		}
	}

	for _, a := range []int{0, 1, 3, 10, 19, 25, 40, 64, 100} {
		for _, b := range []int{0, 1, 3, 10, 19, 25, 40, 55} {
			c := new(big.Int).Lsh(big.NewInt(1), uint(a))
			if v, ok := fits(c.Mul(c, new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(b)), nil))); ok {
				add(v)
			}
		}
	}

	for j := range 20 {
		for _, hi := range []uint64{1, 2, 5, 1 << 63, maxUint64, rng.Uint64()} {
			addPlain(u128{hi: hi, lo: pow10u64[j]})
			addPlain(u128{hi: hi, lo: 7 * pow10u64[j%19]})
			addPlain(u128{hi: hi, lo: 0})
		}
	}

	for range 100 {
		addPlain(shapedCoef(rng, 128))
	}
	return coefs
}

// TestIsRoundedFracDigitsIdentities is the exhaustive sweep: every precision
// 0..MaxPrec against every coefficient shape, both signs, every places value,
// holding both methods to the two identities the API promises
//
//	d.IsRounded(p) == d.Equal(d.Truncate(p))
//	d.FracDigits() == d.Trim().Prec()
//
// and to independent big.Int/big.Rat oracles, and the two methods to each
// other: d fits p places exactly when it needs at most p of them.
func TestIsRoundedFracDigitsIdentities(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x15720ded, 0xf2ac0d19))
	coefs := isRoundedCoefs(t, rng)
	for prec := range MaxPrec + 1 {
		for i, c := range coefs {
			// Neither method reads the sign, so alternating it across the sweep
			// covers both without doubling the cost of the big.Int oracles.
			d := newDecimal(c, (i+int(prec))%2 == 1, prec)

			fd := d.FracDigits()
			if want := d.Trim().Prec(); fd != want {
				require.Failf(t, "FracDigits != Trim().Prec()",
					"d=%+v: got %d, want %d", d, fd, want)
			}
			if want := fracDigitsOracle(d); fd != want {
				require.Failf(t, "FracDigits != definition",
					"d=%+v: got %d, want %d", d, fd, want)
			}

			for _, places := range isRoundedPlaces {
				got := d.IsRounded(places)
				if want := d.Equal(d.Truncate(places)); got != want {
					require.Failf(t, "IsRounded != Equal(Truncate)",
						"d=%+v places=%d: got %v, want %v", d, places, got, want)
				}
				if want := isRoundedOracle(d, places); got != want {
					require.Failf(t, "IsRounded != oracle",
						"d=%+v places=%d: got %v, want %v", d, places, got, want)
				}
				if want := fd <= places; got != want {
					require.Failf(t, "IsRounded != (FracDigits <= places)",
						"d=%+v places=%d fd=%d: got %v", d, places, fd, got)
				}
			}
		}
	}
}

// TestIsRoundedMatchesEveryRoundingMode pins the money idiom this API
// replaces. Rounding a value that has no digit past places changes nothing,
// and rounding one that has moves it to a different multiple of 10^-places in
// every mode, so x.Equal(x.<Round>(places)) is IsRounded for all of them.
func TestIsRoundedMatchesEveryRoundingMode(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x15720ded, 0x70de5))
	modes := append([]struct {
		name  string
		apply func(Decimal, uint8) Decimal
	}{{"truncate", Decimal.Truncate}}, roundingModes...)
	for range 10_000 {
		d := randCrossDecimal(rng)
		for places := range MaxPrec + 1 {
			got := d.IsRounded(places)
			for _, m := range modes {
				if want := d.Equal(m.apply(d, places)); got != want {
					require.Failf(t, "IsRounded != Equal(rounded)",
						"mode %s d=%+v places=%d: got %v, want %v", m.name, d, places, got, want)
				}
			}
		}
	}
}

// TestIsRoundedFracDigitsRepresentationInvariant holds both methods to being
// properties of the number, not of its representation: widening d with
// trailing zeros (Rescale up), trimming it, or negating it must not change
// either answer. Rescale and Trim are existing, separately tested code.
func TestIsRoundedFracDigitsRepresentationInvariant(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x15720ded, 0x1a5a41a7))
	for range 15_000 {
		d := randCrossDecimal(rng)
		forms := []Decimal{d, d.Neg(), d.Abs(), d.Trim()}
		for prec := d.Prec(); prec <= MaxPrec; prec++ {
			if wide, err := d.Rescale(prec); err == nil {
				forms = append(forms, wide)
			}
		}
		wantFD := d.FracDigits()
		for _, e := range forms {
			if got := e.FracDigits(); got != wantFD {
				require.Failf(t, "FracDigits is not representation-invariant",
					"d=%+v e=%+v: got %d, want %d", d, e, got, wantFD)
			}
			for _, places := range isRoundedPlaces {
				if got, want := e.IsRounded(places), d.IsRounded(places); got != want {
					require.Failf(t, "IsRounded is not representation-invariant",
						"d=%+v e=%+v places=%d: got %v, want %v", d, e, places, got, want)
				}
			}
		}
		if got := d.Trim().FracDigits(); got != d.Trim().Prec() {
			require.Failf(t, "a trimmed value's FracDigits is its precision",
				"d=%+v trimmed=%+v: FracDigits %d", d, d.Trim(), got)
		}
	}
}

func TestIsRoundedTables(t *testing.T) {
	t.Parallel()
	maxPrec19 := mustHiLo(t, false, maxUint64, maxUint64, 19)
	one25 := RequireFromString("1.25").MustRescale(19)
	cents1234 := RequireFromString("1234.56").MustRescale(19)
	subCent1234 := RequireFromString("1234.5678901234567891").MustRescale(19)
	tests := []struct {
		name   string
		d      Decimal
		places uint8
		want   bool
	}{
		// Money: the "does it fit cents" check, x.Equal(x.RoundBank(2)).
		{"cents_at_2", RequireFromString("12.34"), 2, true},
		{"sub_cent_at_2", RequireFromString("12.345"), 2, false},
		{"sub_cent_at_3", RequireFromString("12.345"), 3, true},
		{"tiny_sub_cent_at_2", RequireFromString("0.0100000000000000001"), 2, false},
		{"negative_sub_cent", RequireFromString("-12.345"), 2, false},
		{"negative_cents", RequireFromString("-12.34"), 2, true},
		// Arithmetic keeps trailing zeros, so the stored precision overstates
		// what the value needs: 1.2500000000000000000 is a whole number of
		// cents (one-limb coefficient), and so is 1234.5600000000000000000
		// (two limbs).
		{"stored_prec_19_cents_one_limb", one25, 2, true},
		{"stored_prec_19_cents_one_limb_to_1", one25, 1, false},
		{"stored_prec_19_cents_two_limb", cents1234, 2, true},
		{"stored_prec_19_cents_two_limb_to_1", cents1234, 1, false},
		{"stored_prec_19_sub_cent_two_limb", subCent1234, 2, false},
		{"stored_prec_19_sub_cent_two_limb_exact", subCent1234, 16, true},
		{"stored_prec_19_sub_cent_two_limb_short", subCent1234, 15, false},
		{"integer_at_0", NewFromInt(42), 0, true},
		{"integer_wide_prec_at_0", NewFromInt(10).MustRescale(19), 0, true}, // 10^20: two limbs
		{"half_at_0", RequireFromString("0.5"), 0, false},
		{"negative_half_at_0", RequireFromString("-0.5"), 0, false},
		{"smallest_unit_at_18", RequireFromString("0.0000000000000000001"), 18, false},
		{"smallest_unit_at_19", RequireFromString("0.0000000000000000001"), 19, true},
		{"max_coef_at_18", maxPrec19, 18, false},
		{"max_coef_at_19", maxPrec19, 19, true},
		{"max_coef_at_255", maxPrec19, 255, true},
		// Zero is whole at every places, including through its canonical
		// precision of 0.
		{"zero_at_0", Decimal{}, 0, true},
		{"zero_at_19", Decimal{}, 19, true},
		{"zero_at_255", Decimal{}, 255, true},
		// 2^64 has a zero low limb: only the high limb makes it nonzero.
		{"two_to_the_64_at_prec_1", mustHiLo(t, false, 1, 0, 1), 0, false},
		{"two_to_the_64_at_prec_19", mustHiLo(t, false, 1, 0, 19), 18, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.d.IsRounded(tc.places))
			require.Equal(t, tc.d.Equal(tc.d.Truncate(tc.places)), tc.d.IsRounded(tc.places),
				"documented identity")
		})
	}
}

func TestFracDigitsTables(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		d    Decimal
		want uint8
	}{
		{"zero", Decimal{}, 0},
		{"integer", NewFromInt(42), 0},
		{"integer_with_trailing_zeros", NewFromInt(1500), 0},
		{"parsed_fraction", RequireFromString("1.5"), 1},
		{"cents", RequireFromString("12.34"), 2},
		{"negative_cents", RequireFromString("-12.34"), 2},
		// Stored precision 3, but one significant fractional digit: the whole
		// point of FracDigits over Prec.
		{"trailing_zeros_kept_by_arithmetic", mustHiLo(t, false, 0, 1500, 3), 1},
		{"sum_keeps_stored_precision", RequireFromString("0.75").MustAdd(RequireFromString("0.75")), 1},
		{"all_fraction_zeros", mustHiLo(t, false, 0, 1000, 3), 0},
		{"more_zeros_than_precision", mustHiLo(t, false, 0, 50_000, 2), 0},
		{"smallest_unit", RequireFromString("0.0000000000000000001"), 19},
		{"prec_19_of_one", mustHiLo(t, false, 0, 10_000_000_000_000_000_000, 19), 0},
		{"prec_19_of_one_and_a_half", mustHiLo(t, false, 0, 15_000_000_000_000_000_000, 19), 1},
		{"max_coef_no_zeros", mustHiLo(t, false, maxUint64, maxUint64, 19), 19},
		{"negative_max_coef", mustHiLo(t, true, maxUint64, maxUint64, 19), 19},
		// 10^20 at precision 19 is 10: the two-limb coefficient sheds its
		// whole fractional part. 1234.5678 at precision 19 keeps four digits.
		{"two_limb_integer", mustHiLo(t, false, 5, 0x6BC75E2D63100000, 19), 0},
		{"two_limb_four_digits", RequireFromString("1234.5678").MustRescale(19), 4},
		{"two_limb_negative_four_digits", RequireFromString("-1234.5678").MustRescale(19), 4},
		// 2^64 has a zero low limb but no trailing decimal zero.
		{"two_to_the_64_at_prec_19", mustHiLo(t, false, 1, 0, 19), 19},
		{"two_to_the_64_at_prec_1", mustHiLo(t, false, 1, 0, 1), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.d.FracDigits())
			require.Equal(t, tc.d.Trim().Prec(), tc.d.FracDigits(), "documented identity")
			require.Equal(t, fracDigitsOracle(tc.d), tc.d.FracDigits(), "definition")
		})
	}
}

// TestFracDigitsEveryTrailingZeroCount builds m·10^z at every precision so
// each (precision, zero count) pair of 0..MaxPrec × 0..MaxPrec occurs with
// the digit just above the zeros nonzero, one- and two-limb alike. The answer
// is prec-min(z, prec): the zeros past the decimal point are the only ones
// that count.
func TestFracDigitsEveryTrailingZeroCount(t *testing.T) {
	t.Parallel()
	// None of these ends in a zero digit, so m·10^z has exactly z of them.
	for _, m := range []uint64{1, 7, 9, 12_345, 1_000_000_007, 1<<63 + 1, maxUint64} {
		for z := range MaxPrec + 1 {
			coef, overflow := mul128by64(u128{lo: m}, pow10u64[z])
			require.Zero(t, overflow, "m=%d z=%d", m, z)
			// The same digits shifted left by 19 more zeros: a coefficient in
			// the second limb (when it still fits 128 bits).
			wide, wideOverflow := mul128by64(coef, pow10u64[MaxPrec])
			for prec := range MaxPrec + 1 {
				d := newDecimal(coef, false, prec)
				want := prec - min(z, prec)
				if got := d.FracDigits(); got != want {
					require.Failf(t, "FracDigits",
						"m=%d z=%d prec=%d: got %d, want %d", m, z, prec, got, want)
				}
				if wideOverflow != 0 {
					continue
				}
				d = newDecimal(wide, false, prec)
				want = prec - min(z+MaxPrec, prec)
				if got := d.FracDigits(); got != want {
					require.Failf(t, "FracDigits (past one limb)",
						"m=%d z=%d prec=%d: got %d, want %d", m, z+MaxPrec, prec, got, want)
				}
			}
		}
	}
}

// TestFracDigitsLadderConstants recomputes the ten magic constants of
// fracDigitsCore's ladder from their definitions, so none of them is trusted
// by inspection: fracInv5pS is the inverse of 5^s modulo 2^64 and fracMaxQuoS is
// ⌊(2^64-1)/10^s⌋. It also pins tenToMaxPrec to 10^MaxPrec and to the normalized
// divisor of the reciprocal tables, which modTenToMaxPrec relies on.
func TestFracDigitsLadderConstants(t *testing.T) {
	t.Parallel()
	mod64 := new(big.Int).Lsh(big.NewInt(1), 64)
	for _, tc := range []struct {
		s              int
		inv, maxQuoGot uint64
	}{
		{16, fracInv5p16, fracMaxQuo16},
		{8, fracInv5p8, fracMaxQuo8},
		{4, fracInv5p4, fracMaxQuo4},
		{2, fracInv5p2, fracMaxQuo2},
		{1, fracInv5p1, fracMaxQuo1},
	} {
		pow5 := new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(tc.s)), nil)
		wantInv := new(big.Int).ModInverse(pow5, mod64)
		require.Equal(t, wantInv.Uint64(), tc.inv, "inverse of 5^%d mod 2^64", tc.s)
		maxQuo := new(big.Int).Quo(new(big.Int).Sub(mod64, big.NewInt(1)), pow10Big(tc.s))
		require.Equal(t, maxQuo.Uint64(), tc.maxQuoGot, "floor((2^64-1)/10^%d)", tc.s)
	}

	require.Equal(t, pow10u64[MaxPrec], uint64(tenToMaxPrec))
	require.Equal(t, pow10u64[MaxPrec], pow10Tab[MaxPrec].dn, "10^MaxPrec is already normalized")
	require.Zero(t, pow10Tab[MaxPrec].s, "so its normalization shift is zero")
	require.True(t, tenToMaxPrec > 1<<63, "top bit set: a valid div2by1 divisor as is")
}

// TestFracDigitsStep holds the divisibility-and-quotient step to its contract
// directly: for every s in the ladder and a spread of x, the second result is
// exactly "10^s divides x" and, when it does, the first is exactly x/10^s.
func TestFracDigitsStep(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x15720ded, 0x9ad4b0))
	steps := []struct {
		s        int
		inv, max uint64
	}{
		{16, fracInv5p16, fracMaxQuo16},
		{8, fracInv5p8, fracMaxQuo8},
		{4, fracInv5p4, fracMaxQuo4},
		{2, fracInv5p2, fracMaxQuo2},
		{1, fracInv5p1, fracMaxQuo1},
	}
	for _, st := range steps {
		div := pow10u64[st.s]
		check := func(x uint64) {
			q, ok := fracDigitsStep(x, st.inv, st.max, st.s)
			if want := x%div == 0; ok != want {
				require.Failf(t, "divisibility", "x=%d s=%d: got %v, want %v", x, st.s, ok, want)
			}
			if ok && q != x/div {
				require.Failf(t, "quotient", "x=%d s=%d: got %d, want %d", x, st.s, q, x/div)
			}
		}
		for _, x := range []uint64{0, 1, div - 1, div, div + 1, 2 * div, 5 * div, maxUint64, maxUint64 - maxUint64%div, maxUint64/div*div - div} {
			check(x)
		}
		for range 200_000 {
			x := randShaped64(rng)
			check(x)
			check(x / div * div) // a multiple, so the true branch is common
			check(x/div*div + 1) // and just past one
		}
	}
}

// TestModTenToMaxPrec checks the one-step reduction against big.Int: the high limb
// below, at, around and above 10^19 (where the folded first division flips
// from quotient 0 to 1), the extremes of both limbs, and random shaped values.
func TestModTenToMaxPrec(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x15720ded, 0x10d16175))
	check := func(u u128) {
		want := new(big.Int).Rem(u128ToBig(u), pow10Big(int(MaxPrec))).Uint64()
		if got := modTenToMaxPrec(u); got != want {
			require.Failf(t, "modTenToMaxPrec", "u=%+v: got %d, want %d", u, got, want)
		}
	}
	his := []uint64{
		0, 1, tenToMaxPrec - 2, tenToMaxPrec - 1, tenToMaxPrec, tenToMaxPrec + 1,
		1 << 63, maxUint64 - 1, maxUint64,
	}
	los := []uint64{0, 1, tenToMaxPrec - 1, tenToMaxPrec, 1 << 63, maxUint64 - 1, maxUint64}
	for _, hi := range his {
		for _, lo := range los {
			check(u128{hi: hi, lo: lo})
		}
	}
	for _, tc := range boundary128 {
		check(tc.v)
	}
	for range 60_000 {
		check(randShapedU128(rng))
		check(u128{hi: tenToMaxPrec - 2 + rng.Uint64N(5), lo: rng.Uint64()})
	}
}
