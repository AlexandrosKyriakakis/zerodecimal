package zerodecimal

import (
	"errors"
	"math/big"
	"math/bits"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// mulDivByPlaces are the scales the MulDivRoundBy identity suites sweep: both
// ends of 0..MaxPrec, the money and quantity scales between them, and 20,
// one past MaxPrec, for the ErrPrecOutOfRange arm.
var mulDivByPlaces = [...]uint8{0, 1, 2, 9, 16, 19, 20}

// mulDivByPaths are the arms of MulDivRoundBy that mulDivByPath names; every
// sweep that counts them requires each one to have been taken.
var mulDivByPaths = [...]string{"narrow", "inline", "wide_one_limb", "wide_two_limb", "scaled_divisor_fits", "scaled_divisor_wide", "scaled_divisor_wide_pow10"}

// mulDivByPath names the arm of MulDivRoundBy that serves d*e/v at places,
// for a valid v, nonzero operands and places ≤ MaxPrec, by restating its
// dispatch (s is the scale gap places + v.Decimal().Prec() - d.Prec() -
// e.Prec(), c v's coefficient):
//
//   - "narrow": one-limb operands and c, 0 ≤ s ≤ MaxPrec, and d·e·10^s below
//     2^64: MulDivRound's single hardware divide;
//   - "inline": the same operands scaled past one limb, while the high limb
//     n2 of d·e·10^s stays below c-1: the inline reciprocal division;
//   - "wide_one_limb", "wide_two_limb": the outlined arm for any other
//     nonnegative s, by the width of c;
//   - "scaled_divisor_fits": a negative s whose scaled divisor c·10^-s fits
//     128 bits, divided by it as MulDivRound does;
//   - "scaled_divisor_wide": a negative s whose scaled divisor does not,
//     divided by c on v's reciprocal and then by 10^-s;
//   - "scaled_divisor_wide_pow10": the same with ⌊d·e/c⌋ ≥ 2^128, divided by
//     the power of ten first (divRoundPow10By).
func mulDivByPath(d, e Decimal, v *Divisor, places uint8) string {
	c := v.e.coef
	s := int(places) + int(v.e.prec) - int(d.prec) - int(e.prec)
	if s < 0 {
		if mulToU256(c, pow10u128[-s]).isZeroUpper() {
			return "scaled_divisor_fits"
		}
		if !less128(mulToU256(d.coef, e.coef).hi128(), c) {
			return "scaled_divisor_wide_pow10"
		}
		return "scaled_divisor_wide"
	}
	if d.coef.hi|e.coef.hi|c.hi == 0 && s <= int(MaxPrec) {
		ph, p := bits.Mul64(d.coef.lo, e.coef.lo)
		n2, n1, _ := mul128by64to192(u128{hi: ph, lo: p}, pow10u64[s])
		if n2|n1 == 0 {
			return "narrow"
		}
		if n2 < c.lo-1 {
			return "inline"
		}
	}
	if c.hi == 0 {
		return "wide_one_limb"
	}
	return "wide_two_limb"
}

// countMulDivByPath adds the arm one valid call takes to paths. Calls that
// stop before any arm (an invalid Divisor or scale, a zero operand) are not
// counted.
func countMulDivByPath(paths map[string]int, d, e Decimal, v *Divisor, places uint8) {
	if v.Decimal().IsZero() || d.IsZero() || e.IsZero() || places > MaxPrec {
		return
	}
	paths[mulDivByPath(d, e, v, places)]++
}

// requireMulDivByPaths requires every arm to have been counted at least min
// times.
func requireMulDivByPaths(t *testing.T, paths map[string]int, minCount int) {
	t.Helper()
	for _, p := range mulDivByPaths {
		require.GreaterOrEqualf(t, paths[p], minCount, "arm %s taken too rarely: %v", p, paths)
	}
}

// requireMulDivRoundByIdentity requires d.MulDivRoundBy(e, v, places, m) to
// return exactly d.MulDivRound(e, v.Decimal(), places, m) in every rounding
// mode and the first invalid one: the same representation (==, which tells
// 1.5 from 1.50) and the same sentinel. Mismatches alone reach require, which
// keeps the sweeps cheap; TestMulDivRoundByValidation pins the sentinels as
// bare values.
func requireMulDivRoundByIdentity(t *testing.T, d, e Decimal, v *Divisor, places uint8) {
	t.Helper()
	f := v.Decimal()
	for m := ToNearestAway; m <= TowardNegative+1; m++ {
		want, wantErr := d.MulDivRound(e, f, places, m)
		if got, err := d.MulDivRoundBy(e, v, places, m); got != want || !errors.Is(err, wantErr) {
			require.Failf(t, "MulDivRoundBy differs from MulDivRound",
				"d=%s e=%s f=%s places=%d mode=%d: got %s, %v; want %s, %v",
				divisorRepr(d), divisorRepr(e), divisorRepr(f), places, m,
				divisorRepr(got), err, divisorRepr(want), wantErr)
		}
	}
}

// requireMulDivRoundByAllPlaces runs the identity at every mulDivByPlaces
// value. Under -race it checks one scale per triple, picked by the operands
// so a sweep still visits them all (see divisorRaceScale).
func requireMulDivRoundByAllPlaces(t *testing.T, d, e Decimal, v *Divisor) {
	t.Helper()
	places := mulDivByPlaces[:]
	if raceEnabled {
		f := v.Decimal()
		k := (d.coef.lo ^ e.coef.lo ^ f.coef.lo ^ f.coef.hi ^ uint64(d.prec)) % uint64(len(mulDivByPlaces))
		places = mulDivByPlaces[k : k+1]
	}
	for _, p := range places {
		requireMulDivRoundByIdentity(t, d, e, v, p)
	}
}

// requireMulDivRoundByOracle holds d.MulDivRoundBy(e, v, places, mode) to the
// exact big.Rat value d*e/f, for f = v.Decimal(), rounded once in mode:
// ErrDivideByZero for an invalid Divisor, ErrOverflow and Decimal{} exactly
// when the rounded coefficient exceeds 128 bits, and otherwise the oracle's
// coefficient, sign and precision places, with canonical zero.
func requireMulDivRoundByOracle(t *testing.T, d, e Decimal, v *Divisor, places uint8, mode RoundingMode) {
	t.Helper()
	got, err := d.MulDivRoundBy(e, v, places, mode)
	f := v.Decimal()
	if f.IsZero() {
		if !errors.Is(err, ErrDivideByZero) || got != (Decimal{}) {
			require.Failf(t, "an invalid Divisor must fail with ErrDivideByZero", "d=%s e=%s: got %s, %v",
				divisorRepr(d), divisorRepr(e), divisorRepr(got), err)
		}
		return
	}
	exact := new(big.Rat).Mul(exactDecimalRat(d), exactDecimalRat(e))
	exact.Quo(exact, exactDecimalRat(f))
	wantCoef, wantNeg := exactRoundRat(exact, places, mode)
	want, wantErr := Decimal{}, ErrOverflow
	if wantCoef.BitLen() <= 128 {
		want, wantErr = newDecimal(exactU128FromBig(wantCoef), wantNeg, places), nil
	}
	if got != want || !errors.Is(err, wantErr) {
		require.Failf(t, "MulDivRoundBy differs from the exact quotient rounded once",
			"d=%s e=%s f=%s places=%d mode=%d exact=%s: got %s, %v; want %s, %v",
			divisorRepr(d), divisorRepr(e), divisorRepr(f), places, mode, exact.RatString(),
			divisorRepr(got), err, divisorRepr(want), wantErr)
	}
}

// requireMulDivRoundByOracleAllModes runs the oracle in all six modes, on
// both signs of d.
func requireMulDivRoundByOracleAllModes(t *testing.T, d, e Decimal, v *Divisor, places uint8) {
	t.Helper()
	for mode := ToNearestAway; mode <= TowardNegative; mode++ {
		requireMulDivRoundByOracle(t, d, e, v, places, mode)
		requireMulDivRoundByOracle(t, d.Neg(), e, v, places, mode)
	}
}

// TestMulDivRoundByIdentityRandom holds MulDivRoundBy to MulDivRound on 240k
// random triples (d, e and the divisor f), each at every mulDivByPlaces value
// in all six modes plus an invalid one, and to the exact big.Rat oracle at
// one random scale and mode. Half the triples draw coefficient widths uniform
// over 1..128 bits for all three operands, so each arm's widths occur often;
// half come from the boundary-heavy cross-check generator (powers of ten and
// two and their neighbors, the 2^128 ceiling, small integers) with
// edge-biased precisions. Signs are uniform, and zero operands and zero
// divisors are mixed in. Both checks count the arm each call takes and
// require every arm to have been exercised.
func TestMulDivRoundByIdentityRandom(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6d756c64, 0x69766279))
	randPrec := func() uint8 { return uint8(rng.Uint64N(uint64(MaxPrec) + 1)) }
	identityPaths, oraclePaths := map[string]int{}, map[string]int{}
	for i := range divisorRaceScale(240_000) {
		var d, e, f Decimal
		if i%2 == 0 {
			d = shapedDecimal(rng, 128, randPrec())
			e = shapedDecimal(rng, 128, randPrec())
			f = shapedDecimal(rng, 128, randPrec())
		} else {
			d, e, f = randCrossDecimal(rng), randCrossDecimal(rng), randCrossDecimal(rng)
		}
		switch {
		case i%101 == 0:
			d = Decimal{}
		case i%103 == 0:
			e = Decimal{}
		case i%107 == 0:
			f = Decimal{}
		}
		v := newTestDivisor(t, f)
		requireMulDivRoundByAllPlaces(t, d, e, v)
		for _, p := range mulDivByPlaces {
			countMulDivByPath(identityPaths, d, e, v, p)
		}
		places, mode := randPrec(), RoundingMode(rng.Uint64N(uint64(TowardNegative)+1))
		requireMulDivRoundByOracle(t, d, e, v, places, mode)
		countMulDivByPath(oraclePaths, d, e, v, places)
	}
	t.Logf("arms: identity %v; oracle %v", identityPaths, oraclePaths)
	requireMulDivByPaths(t, identityPaths, divisorRaceScale(1_000))
	requireMulDivByPaths(t, oraclePaths, divisorRaceScale(200))
}

// TestMulDivRoundByShares runs the identity and the oracle on pro-rata
// shares, amount × part / whole with part ≤ whole at one precision, the
// money shape: small wholes make exact half-cent ties frequent, and the 19
// place results scale the product past one limb into the inline reciprocal.
func TestMulDivRoundByShares(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x7368617265, 0x6279))
	paths := map[string]int{}
	for range divisorRaceScale(100_000) {
		qp := uint8(rng.IntN(10))
		whole := 1 + rng.Uint64N(1<<uint(1+rng.IntN(40)))
		part := 1 + rng.Uint64N(whole)
		amount := newDecimal(u128{lo: rng.Uint64N(1 << uint(1+rng.IntN(40)))}, rng.IntN(2) == 0, uint8(rng.IntN(5)))
		v := newTestDivisor(t, newDecimal(u128{lo: whole}, rng.IntN(5) == 0, qp))
		e := newDecimal(u128{lo: part}, false, qp)
		places := [...]uint8{2, 2, 9, 19}[rng.IntN(4)]
		requireMulDivRoundByIdentity(t, amount, e, v, places)
		requireMulDivRoundByOracle(t, amount, e, v, places, RoundingMode(rng.Uint64N(uint64(TowardNegative)+1)))
		countMulDivByPath(paths, amount, e, v, places)
	}
	require.Positive(t, paths["narrow"], "shares must reach the hardware-divide arm: %v", paths)
	require.Positive(t, paths["inline"], "shares must reach the inline reciprocal arm: %v", paths)
}

// TestMulDivRoundByInlineBoundary pins the edges of the inline reciprocal
// arm. It divides the scaled product {n2,n1,n0} by a one-limb c only while
// n2 < c-1, which keeps the quotient below 2^128-2^64 so that rounding it up
// cannot overflow; n2 = c-1 (a quotient that may round past 2^128-1) and
// n2 ≥ c (one past 128 bits) take the outlined arm. Inside, a high quotient
// digit of zero (n2 = 0 and n1 < c) skips a division step. For random
// one-limb products scaled by 10^s it sets c to n2-1 .. n2+3 and, when n2 is
// zero, to n1-1 .. n1+1, and checks the arm each c takes, the identity in
// every mode, and the oracle.
func TestMulDivRoundByInlineBoundary(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x696e6c696e65, 0x62))
	arms := map[string]int{}
	digitSkips := map[bool]int{} // inline cases by whether the high digit step runs
	for i := range divisorRaceScale(3_000) {
		s := uint8(rng.IntN(int(MaxPrec) + 1))
		d := u128{lo: rng.Uint64() | 1<<63}
		e := u128{lo: rng.Uint64()>>uint(rng.IntN(40)) | 1}
		ph, p := bits.Mul64(d.lo, e.lo)
		n2, n1, _ := mul128by64to192(u128{hi: ph, lo: p}, pow10u64[s])
		if n2|n1 == 0 {
			continue
		}
		cs := []uint64{n2 - 1, n2, n2 + 1, n2 + 2, n2 + 3}
		if n2 == 0 {
			cs = append(cs, n1-1, n1, n1+1)
		}
		for _, c := range cs {
			if c == 0 || c+1 == 0 {
				continue
			}
			dd := newDecimal(d, i%2 == 0, 0)
			ee := newDecimal(e, false, 0)
			v := newTestDivisor(t, newDecimal(u128{lo: c}, i%3 == 0, 0))
			want := "wide_one_limb"
			if n2 < c-1 {
				want = "inline"
				digitSkips[n2 == 0 && n1 < c]++
			}
			require.Equal(t, want, mulDivByPath(dd, ee, v, s), "n2=%d n1=%d c=%d", n2, n1, c)
			arms[want]++
			requireMulDivRoundByIdentity(t, dd, ee, v, s)
			requireMulDivRoundByOracleAllModes(t, dd, ee, v, s)
		}
	}
	require.Positive(t, arms["inline"], "%v", arms)
	require.Positive(t, arms["wide_one_limb"], "%v", arms)
	require.Positive(t, digitSkips[true], "the inline arm must skip a zero high digit")
	require.Positive(t, digitSkips[false], "the inline arm must divide both digits")
}

// TestMulDivRoundByRoundPastMaxQuotient pins the outlined arm's one overflow
// that is not a fits failure: a truncated quotient of exactly 2^128-1 with a
// nonzero remainder, which fits but overflows when the mode rounds it up.
// For one- and two-limb divisors c it searches the scale gaps s for a
// two-limb d with d·10^s strictly between (2^128-1)·c and 2^128·c (e = 1),
// as TestDivisorRoundPastMaxQuotient does for DivRoundBy, then checks every
// mode against MulDivRound at places = MaxPrec.
func TestMulDivRoundByRoundPastMaxQuotient(t *testing.T) {
	t.Parallel()
	maxQ := new(big.Int).Sub(mod128big, big.NewInt(1))
	found := map[bool]int{} // by two-limb divisor
	divisors := []u128{{lo: 3}, {lo: 7}, {lo: 99}, {lo: 999}, {lo: 1_000_003}, {hi: 1, lo: 1}, {hi: 3}, {hi: 5, lo: 12345}, {hi: 0x8000, lo: 99}}
	one := NewFromInt(1)
	for _, c := range divisors {
		cb := u128ToBig(c)
		low := new(big.Int).Mul(maxQ, cb) // d·10^s = low + r, 0 < r < c
		for _, cp := range []uint8{0, MaxPrec} {
			v := newTestDivisor(t, newDecimal(c, false, cp))
			// places = MaxPrec gives d.Prec() = MaxPrec + cp - s.
			for s := max(1, int(cp)); s <= int(MaxPrec)+int(cp); s++ {
				// The least multiple of 10^s above low, if it is still below
				// low + c, divides to the coefficient that lands there.
				coef := new(big.Int).Add(low, bp10(s))
				coef.Quo(coef, bp10(s))
				num := new(big.Int).Mul(coef, bp10(s))
				if r := new(big.Int).Sub(num, low); r.Sign() <= 0 || r.Cmp(cb) >= 0 || coef.Cmp(mod128big) >= 0 {
					continue
				}
				found[c.hi != 0]++
				d := newDecimal(bigToU128(t, coef), s%2 == 0, MaxPrec+cp-uint8(s))
				require.Contains(t, []string{"wide_one_limb", "wide_two_limb"}, mulDivByPath(d, one, v, MaxPrec))
				requireMulDivRoundByIdentity(t, d, one, v, MaxPrec)
				requireMulDivRoundByOracleAllModes(t, d, one, v, MaxPrec)
				got, err := d.MulDivRoundBy(one, v, MaxPrec, AwayFromZero)
				require.ErrorIs(t, err, ErrOverflow, "rounding 2^128-1 up must overflow: c=%+v s=%d", c, s)
				require.Equal(t, Decimal{}, got)
				got, err = d.MulDivRoundBy(one, v, MaxPrec, TowardZero)
				require.NoError(t, err)
				require.Equal(t, u128{hi: maxUint64, lo: maxUint64}, got.coef)
			}
		}
	}
	require.Positive(t, found[false], "the search must construct a one-limb divisor case")
	require.Positive(t, found[true], "the search must construct a two-limb divisor case")
}

// TestMulDivRoundByScaledDivisor pins the arm for a scaled divisor c·10^t
// past 128 bits. It divides num by c on v's reciprocal, num = w·c + r2, and
// w by 10^t, w = q·10^t + r1, then compares 2·r1 with 10^t and lets r2 > 0
// tip an exact half. A one-coefficient num (e = 1) is below 2^128 ≤ c·10^t,
// so q is 0 and num = r1·c + r2; r1 can reach 10^t/2 only when c·10^t <
// 2^129, so for every t the divisors include the least c past 2^128/10^t and
// its successor, beside fixed one- and two-limb divisors. The test builds num
// from r1 (0, 1, just below, on and above 10^t/2, and 10^t-1) and r2 (0, 1,
// about c/2 and c-1) and requires every outcome: 2·r1 below, on or above
// 10^t, each with r2 zero and nonzero. Ties with an odd and an even nonzero
// quotient, d·e = 3·c·10^t/2 and 5·c·10^t/2, pin the parity, and products
// whose ⌊num/c⌋ reaches 2^128, which divide by the power of ten first
// (divRoundPow10By), are pinned too.
func TestMulDivRoundByScaledDivisor(t *testing.T) {
	t.Parallel()
	fixed := []*big.Int{
		big.NewInt(3), big.NewInt(4), big.NewInt(5), big.NewInt(1001), big.NewInt(1_000_000),
		new(big.Int).SetUint64(maxUint64),
		new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(1)),
		new(big.Int).Lsh(big.NewInt(3), 100),
	}
	two128 := new(big.Int).Lsh(big.NewInt(1), 128)
	branches := map[string]int{}
	widths := map[bool]int{} // by two-limb divisor
	ties := map[bool]int{}   // nonzero quotient ties by odd quotient
	step := 1
	if raceEnabled {
		step = 5 // see divisorRaceScale
	}
	for t10 := 1; t10 <= 2*int(MaxPrec); t10 += step {
		pow := exactPow10(t10)
		half := new(big.Int).Rsh(pow, 1)
		// The precisions of a one-coefficient num and of b = 1 sum to t10.
		ap := uint8(min(t10, int(MaxPrec)))
		bp := uint8(t10) - ap
		least := new(big.Int).Add(two128, new(big.Int).Sub(pow, big.NewInt(1)))
		least.Quo(least, pow) // ⌈2^128/10^t⌉
		for _, cb := range append([]*big.Int{least, new(big.Int).Add(least, big.NewInt(1))}, fixed...) {
			if new(big.Int).Mul(cb, pow).Cmp(two128) < 0 {
				continue // scaled_divisor_fits, covered by the random tests
			}
			v := newTestDivisor(t, newDecimal(bigToU128(t, cb), false, 0))
			for _, r2 := range []*big.Int{big.NewInt(0), big.NewInt(1), new(big.Int).Rsh(cb, 1), new(big.Int).Sub(cb, big.NewInt(1))} {
				for _, r1 := range []*big.Int{big.NewInt(0), big.NewInt(1), new(big.Int).Sub(half, big.NewInt(1)), half, new(big.Int).Add(half, big.NewInt(1)), new(big.Int).Sub(pow, big.NewInt(1))} {
					num := new(big.Int).Mul(r1, cb)
					num.Add(num, r2)
					if num.Sign() == 0 || num.BitLen() > 128 {
						continue
					}
					// num/(c·10^t) at places 0: a at precision ap times 1 at bp.
					a := newDecimal(bigToU128(t, num), false, ap)
					b := newDecimal(u128{lo: 1}, false, bp)
					require.Equal(t, "scaled_divisor_wide", mulDivByPath(a, b, v, 0))
					requireMulDivRoundByIdentity(t, a, b, v, 0)
					requireMulDivRoundByOracleAllModes(t, a, b, v, 0)
					widths[v.e.coef.hi != 0]++
					branches["2r1"+[]string{"<", "=", ">"}[new(big.Int).Lsh(r1, 1).Cmp(pow)+1]+"10^t,r2"+[]string{"=0", ">0"}[r2.Sign()]]++
				}
			}
			// k·c·10^t/2 over c·10^t is the tie k/2: q = 1 for k = 3, 2 for 5.
			hd := new(big.Int).Mul(cb, half)
			if hd.BitLen() > 128 {
				continue
			}
			for _, k := range []uint64{3, 5} {
				a := newDecimal(bigToU128(t, hd), false, ap)
				b := newDecimal(u128{lo: k}, false, bp)
				require.Equal(t, "scaled_divisor_wide", mulDivByPath(a, b, v, 0))
				requireMulDivRoundByIdentity(t, a, b, v, 0)
				requireMulDivRoundByOracleAllModes(t, a, b, v, 0)
				got, err := a.MulDivRoundBy(b, v, 0, ToNearestEven)
				require.NoError(t, err)
				require.Equal(t, newDecimal(u128{lo: 2}, false, 0), got, "k=%d: ties round to the even quotient", k)
				ties[k == 3]++
			}
		}
	}
	for _, r1 := range []string{"<", "=", ">"} {
		for _, r2 := range []string{"=0", ">0"} {
			b := "2r1" + r1 + "10^t,r2" + r2
			require.Positivef(t, branches[b], "branch %s never taken: %v", b, branches)
		}
	}
	require.Positive(t, widths[false], "one-limb divisors must reach the arm")
	require.Positive(t, widths[true], "two-limb divisors must reach the arm")
	require.Positive(t, ties[true], "a tie with an odd quotient must be pinned")
	require.Positive(t, ties[false], "a tie with an even quotient must be pinned")

	// ⌊d·e/c⌋ ≥ 2^128: operands of at least 2^126 at precisions 19 and
	// 10..19 (t = 29..38 at places 0), over divisors with c·10^t ≥ 2^128.
	rng := rand.New(rand.NewPCG(0x7363616c6564, 0x77))
	wide := 0
	for i := range divisorRaceScale(2_000) {
		c := []u128{{lo: 7}, {lo: 1001}, {hi: 1, lo: 1}, {hi: 3 << 36}}[i%4]
		ep := []uint8{MaxPrec, 15, 10}[i%3]
		d := newDecimal(u128{hi: rng.Uint64() | 1<<62, lo: rng.Uint64()}, i%2 == 0, MaxPrec)
		e := newDecimal(u128{hi: rng.Uint64() | 1<<62, lo: rng.Uint64()}, i%5 == 0, ep)
		v := newTestDivisor(t, newDecimal(c, i%7 == 0, 0))
		if mulDivByPath(d, e, v, 0) != "scaled_divisor_wide_pow10" {
			continue // c·10^t < 2^128: scaled_divisor_fits
		}
		wide++
		requireMulDivRoundByIdentity(t, d, e, v, 0)
		requireMulDivRoundByOracleAllModes(t, d, e, v, 0)
	}
	require.Greater(t, wide, divisorRaceScale(2_000)/4, "too few products took the power-of-ten-first fallback")
}

// TestMulDivRoundByRange runs MulDivRound's range cases through
// MulDivRoundBy: overflow on both scalings (a product past 2^256 once scaled
// up, including a first 10^19 step that would wrap back into range if left
// unchecked), quotients that just fit or just overflow, and tiny quotients of
// huge denominators that round to canonical zero or to one unit.
func TestMulDivRoundByRange(t *testing.T) {
	t.Parallel()
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	maxInt := Decimal{coef: maxCoef}
	two := NewFromInt(2)
	pow19 := exactPow10(int(MaxPrec))
	ceil := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 191), new(big.Int).Sub(pow19, big.NewInt(1)))
	ceil.Quo(ceil, pow19)
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
		{"one_limb_scaled_past_2_128", Decimal{coef: u128{lo: maxUint64}}, Decimal{coef: u128{lo: maxUint64}}, NewFromInt(3), 19},
		{"tiny_over_huge", Decimal{coef: u128{lo: 1}, prec: MaxPrec}, Decimal{coef: u128{lo: 1}, prec: MaxPrec}, maxInt, 0},
		{"tiny_over_huge_places", Decimal{coef: u128{lo: 7}, prec: MaxPrec}, Decimal{coef: u128{lo: 3}, prec: MaxPrec}, Decimal{coef: u128{lo: 9}}, MaxPrec},
		{"tiny_over_one_limb", Decimal{coef: u128{lo: 1}, prec: MaxPrec}, Decimal{coef: u128{lo: 1}, prec: MaxPrec}, NewFromInt(3), MaxPrec},
		// A product whose first 10^MaxPrec scaling step (s = 38) passes 2^256
		// by less than 2^65·10^19: unchecked, the wrapped value would survive
		// the second step and divide into range.
		{"first_scaling_step_wraps", Decimal{coef: exactU128FromBig(ceil)}, Decimal{coef: u128{hi: 2}}, Decimal{coef: maxCoef, prec: MaxPrec}, MaxPrec},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, cneg := range []bool{false, true} {
				v := newTestDivisor(t, newDecimal(tc.c.coef, cneg, tc.c.prec))
				requireMulDivRoundByIdentity(t, tc.a, tc.b, v, tc.places)
				requireMulDivRoundByIdentity(t, tc.a.Neg(), tc.b, v, tc.places)
				requireMulDivRoundByOracleAllModes(t, tc.a, tc.b, v, tc.places)
			}
		})
	}
}

// mulDivByTie returns operands whose exact quotient d*e/f is the half-unit
// tie k/2 at places, for an odd k = dk·ek: d = dk, e = h·ek and the divisor
// f = 2·10^s·h with the scale gap s ≥ 0, or d = dk, e = h·ek·10^t at
// precision t and f = 2·h, a negative gap -t. ok is false when a coefficient
// does not fit 128 bits.
func mulDivByTie(dk, ek, h u128, gap int) (d, e, f Decimal, places uint8, ok bool) {
	hb, ekb := u128ToBig(h), u128ToBig(ek)
	fb := new(big.Int).Lsh(hb, 1)
	eb := new(big.Int).Mul(hb, ekb)
	var ep uint8
	if gap >= 0 {
		places = uint8(gap)
		fb.Mul(fb, exactPow10(gap))
	} else {
		ep = uint8(-gap)
		eb.Mul(eb, exactPow10(-gap))
	}
	if fb.BitLen() > 128 || eb.BitLen() > 128 {
		return Decimal{}, Decimal{}, Decimal{}, 0, false
	}
	return newDecimal(dk, false, 0), newDecimal(exactU128FromBig(eb), false, ep), newDecimal(exactU128FromBig(fb), false, 0), places, true
}

// TestMulDivRoundByTies pins exact half-unit ties, their neighbors one unit
// of the divisor away, and the zero-quotient tie 1/2 (which rounds to
// canonical zero or one unit) on every arm: narrow and inline products, the
// inline arm with one and two quotient digits, both widths of the outlined
// arm, and a negative gap whose scaled divisor fits 128 bits. Ties of a
// scaled divisor past 128 bits are TestMulDivRoundByScaledDivisor's
// "2r1=10^t,r2=0" branch.
func TestMulDivRoundByTies(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x746965, 0x73))
	odd := func(maxBits int) u128 {
		c := shapedCoef(rng, maxBits)
		c.lo |= 1
		return c
	}
	arms := map[string]int{}
	for i := range divisorRaceScale(20_000) {
		var dk, ek, h u128
		gap := rng.IntN(int(MaxPrec)+1) - rng.IntN(int(MaxPrec)+1)
		switch i % 5 {
		case 0: // a zero quotient: 1/2
			dk, ek, h = u128{lo: 1}, u128{lo: 1}, shapedCoef(rng, 40)
		case 1: // narrow or inline products
			dk, ek, h = odd(32), odd(16), shapedCoef(rng, 20)
		case 2: // inline with a quotient past one limb
			dk, ek, h = odd(64), odd(64), u128{lo: 1}
			gap = 0
		case 3: // a two-limb d: the outlined arm
			dk, ek, h = odd(128), u128{lo: 1}, shapedCoef(rng, 60)
		default: // a two-limb divisor
			dk, ek, h = odd(40), odd(8), u128{hi: 1 + rng.Uint64N(1<<20), lo: rng.Uint64()}
		}
		d, e, f, places, ok := mulDivByTie(dk, ek, h, gap)
		if !ok {
			continue
		}
		neg := i%2 == 0
		for _, c := range []u128{f.coef, inc128(f.coef)} {
			v := newTestDivisor(t, newDecimal(c, neg, f.prec))
			if !v.e.coef.isZero() {
				arms[mulDivByPath(d, e, v, places)]++
			}
			requireMulDivRoundByIdentity(t, d, e, v, places)
			requireMulDivRoundByOracleAllModes(t, d, e, v, places)
		}
		if dk == (u128{lo: 1}) && ek == (u128{lo: 1}) {
			// 1/2 rounds to canonical zero in ToNearestEven and TowardZero,
			// whatever the sign, and to one unit away from zero otherwise.
			v := newTestDivisor(t, newDecimal(f.coef, neg, f.prec))
			for _, mode := range []RoundingMode{ToNearestEven, TowardZero} {
				got, err := d.MulDivRoundBy(e, v, places, mode)
				require.NoError(t, err)
				require.Equal(t, Decimal{}, got, "a zero quotient must be canonical")
			}
			got, err := d.MulDivRoundBy(e, v, places, ToNearestAway)
			require.NoError(t, err)
			require.Equal(t, newDecimal(u128{lo: 1}, neg, places), got)
		}
	}
	for _, arm := range []string{"narrow", "inline", "wide_one_limb", "wide_two_limb", "scaled_divisor_fits"} {
		require.Positivef(t, arms[arm], "no tie reached arm %s: %v", arm, arms)
	}
}

// TestMulDivRoundByEdgeCases crosses the edge coefficients (every one-limb
// power of ten, both sides of 2^64, 2^127, 10^38, the maximum, a few odd
// values) as divisors at precisions 0 and 19 with edge operands d and e at
// several precisions and alternating signs, through the identity at every
// mulDivByPlaces value.
func TestMulDivRoundByEdgeCases(t *testing.T) {
	t.Parallel()
	coefs := divisorEdgeCoefs()
	for ci, fc := range coefs {
		for _, fp := range []uint8{0, MaxPrec} {
			v := newTestDivisor(t, newDecimal(fc, ci%2 == 1, fp))
			for di, dc := range coefs {
				for _, dp := range []uint8{0, 9, MaxPrec} {
					d := newDecimal(dc, (di+int(dp))%2 == 1, dp)
					for ei, ec := range coefs {
						if raceEnabled && (ci+di+ei)%20 != 0 {
							continue // see divisorRaceScale
						}
						for _, ep := range []uint8{0, MaxPrec} {
							requireMulDivRoundByAllPlaces(t, d, newDecimal(ec, ei%3 == 1, ep), v)
						}
					}
				}
			}
		}
	}
}

// TestMulDivRoundByValidation pins MulDivRound's validation order through
// MulDivRoundBy: places, then mode, then a zero or nil Divisor, then a zero
// operand; and that every error, on each arm that can fail, is the bare
// sentinel itself.
func TestMulDivRoundByValidation(t *testing.T) {
	t.Parallel()
	three := MustNewDivisor(NewFromInt(3))
	tiny := MustNewDivisor(newDecimal(u128{lo: 1}, false, MaxPrec))
	var zero Divisor
	one := NewFromInt(1)
	maxInt := newDecimal(u128{hi: maxUint64, lo: maxUint64}, false, 0)
	tests := []struct {
		name    string
		d, e    Decimal
		v       *Divisor
		places  uint8
		mode    RoundingMode
		wantErr error
	}{
		{name: "places_before_mode_and_divisor", d: one, e: one, v: nil, places: MaxPrec + 1, mode: TowardNegative + 1, wantErr: ErrPrecOutOfRange},
		{name: "places_before_zero_divisor", d: one, e: one, v: &zero, places: 255, mode: ToNearestEven, wantErr: ErrPrecOutOfRange},
		{name: "mode_before_nil_divisor", d: one, e: one, v: nil, places: 2, mode: TowardNegative + 1, wantErr: ErrInvalidRoundingMode},
		{name: "mode_before_zero_divisor", d: one, e: one, v: &zero, places: 2, mode: 255, wantErr: ErrInvalidRoundingMode},
		{name: "nil_divisor", d: one, e: one, v: nil, places: 2, mode: ToNearestEven, wantErr: ErrDivideByZero},
		{name: "zero_divisor", d: one, e: one, v: &zero, places: 2, mode: ToNearestEven, wantErr: ErrDivideByZero},
		{name: "nil_divisor_before_zero_operand", d: Decimal{}, e: one, v: nil, places: 2, mode: ToNearestEven, wantErr: ErrDivideByZero},
		{name: "zero_divisor_before_zero_operand", d: one, e: Decimal{}, v: &zero, places: 0, mode: TowardZero, wantErr: ErrDivideByZero},
		{name: "zero_d", d: Decimal{}, e: one, v: &three, places: 2, mode: ToNearestEven},
		{name: "zero_e", d: one, e: Decimal{}, v: &three, places: 2, mode: ToNearestEven},
		{name: "valid", d: one, e: one, v: &three, places: 2, mode: ToNearestEven},
		{name: "overflow_inline_bound", d: newDecimal(u128{lo: maxUint64}, false, 0), e: newDecimal(u128{lo: maxUint64}, false, 0), v: &three, places: 19, mode: TowardZero, wantErr: ErrOverflow},
		{name: "overflow_scaled_product", d: maxInt, e: maxInt, v: &three, places: 19, mode: TowardZero, wantErr: ErrOverflow},
		{name: "overflow_quotient", d: maxInt, e: one, v: &tiny, places: 0, mode: TowardZero, wantErr: ErrOverflow},
		{name: "overflow_round_up", d: mulDivByAllocPastMax, e: one, v: &mulDivByAllocSeven, places: 1, mode: AwayFromZero, wantErr: ErrOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.d.MulDivRoundBy(tc.e, tc.v, tc.places, tc.mode)
			want, wantErr := tc.d.MulDivRound(tc.e, tc.v.Decimal(), tc.places, tc.mode)
			require.Equal(t, tc.wantErr, wantErr, "MulDivRound's own result")
			require.Equal(t, tc.wantErr, err, "the bare sentinel")
			require.Equal(t, want, got)
			if tc.wantErr != nil {
				require.Equal(t, Decimal{}, got)
			}
		})
	}
}
