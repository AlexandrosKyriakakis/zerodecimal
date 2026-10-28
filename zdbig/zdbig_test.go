package zdbig

import (
	"math"
	"math/big"
	"math/bits"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/AlexandrosKyriakakis/zerodecimal"
)

var (
	bigOne = big.NewInt(1)
	bigTen = big.NewInt(10)
	two128 = new(big.Int).Lsh(bigOne, 128)
	max128 = new(big.Int).Sub(two128, bigOne)
)

// pow10Big returns 10^n.
func pow10Big(n int) *big.Int {
	return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil)
}

// mustBig parses a decimal integer literal.
func mustBig(tb testing.TB, s string) *big.Int {
	tb.Helper()
	n, ok := new(big.Int).SetString(s, 10)
	require.Truef(tb, ok, "bad integer literal %q", s)
	return n
}

// bigFromHiLo builds hi·2^64 + lo the plain way (SetUint64, Lsh, Or), so the
// oracle shares no code with the limb helpers under test.
func bigFromHiLo(hi, lo uint64) *big.Int {
	n := new(big.Int).SetUint64(hi)
	n.Lsh(n, 64)
	return n.Or(n, new(big.Int).SetUint64(lo))
}

// hiLoOf splits 0 <= n < 2^128 into its two 64-bit halves.
func hiLoOf(n *big.Int) (hi, lo uint64) {
	return new(big.Int).Rsh(n, 64).Uint64(), new(big.Int).And(n, new(big.Int).SetUint64(math.MaxUint64)).Uint64()
}

// signedCoef returns the signed coefficient of d: its exact value scaled up
// by 10^prec.
func signedCoef(d zerodecimal.Decimal) *big.Int {
	neg, hi, lo, _ := d.ToHiLo()
	n := bigFromHiLo(hi, lo)
	if neg {
		n.Neg(n)
	}
	return n
}

// sameValue reports whether coef·10^exp == signed·10^-prec, by cross
// multiplication. Only for small |exp|.
func sameValue(coef *big.Int, exp int, signed *big.Int, prec int) bool {
	shift := exp + prec
	if shift >= 0 {
		return new(big.Int).Mul(coef, pow10Big(shift)).Cmp(signed) == 0
	}
	return coef.Cmp(new(big.Int).Mul(signed, pow10Big(-shift))) == 0
}

// representable reports whether coef·10^exp has an exact Decimal form, from
// first principles and with no knowledge of the code under test: counting the
// trailing decimal zeros of the digits gives the fractional digits the value
// needs, which must be at most MaxPrec, and the integer it becomes at that
// precision must stay below 2^128. Only for small |exp|.
func representable(coef *big.Int, exp int) bool {
	num := new(big.Int).Abs(coef)
	if num.Sign() == 0 {
		return true
	}
	if exp >= 0 {
		return new(big.Int).Mul(num, pow10Big(exp)).Cmp(two128) < 0
	}
	digits := num.String()
	zeros := len(digits) - len(strings.TrimRight(digits, "0"))
	need := max(0, -exp-zeros) // fractional digits of the value
	if need > int(zerodecimal.MaxPrec) {
		return false
	}
	scaled := new(big.Int).Mul(num, pow10Big(need))
	return scaled.Quo(scaled, pow10Big(-exp)).Cmp(two128) < 0
}

// smallExp reports whether exp is small enough for the oracles that have to
// materialize 10^|exp|.
func smallExp(exp int32) bool {
	return exp >= -4000 && exp <= 4000
}

// parseOracle is the strict parser, which folds the exponent into the
// coefficient and canonicalizes (trims trailing fractional zeros), so it
// agrees with FromBigInt on the value and on the error but not on trailing
// zeros: compare against it after Trim. The parser bounds its input at 200
// bytes, so a coefficient of more than 100 digits goes in with its trailing
// zeros moved into the exponent, which is the same number.
func parseOracle(coef *big.Int, exp int32) (zerodecimal.Decimal, error) {
	digits := coef.String()
	if len(digits) <= 100 {
		return zerodecimal.NewFromString(digits + "e" + strconv.FormatInt(int64(exp), 10))
	}
	trimmed := strings.TrimRight(digits, "0")
	zeros := int64(len(digits) - len(trimmed))
	return zerodecimal.NewFromString(trimmed + "e" + strconv.FormatInt(int64(exp)+zeros, 10))
}

// requireMaxPrecision pins the representation FromBigInt chooses. Like New it
// never trims a coefficient that fits (the precision is -exp when that is at
// most MaxPrec), and when stripping is forced (a precision past MaxPrec, or a
// coefficient past 128 bits) it strips only as many factors of ten as needed.
// Both say the same thing: the precision is the largest in 0..min(-exp,
// MaxPrec) at which the coefficient still fits 128 bits.
func requireMaxPrecision(t *testing.T, got zerodecimal.Decimal, exp int32) {
	t.Helper()
	if got.IsZero() {
		require.Equal(t, zerodecimal.Decimal{}, got)
		return
	}
	limit := uint8(0)
	if exp < 0 {
		limit = uint8(min(int64(zerodecimal.MaxPrec), -int64(exp)))
	}
	require.LessOrEqualf(t, got.Prec(), limit, "precision above min(-exp, MaxPrec): %+v", got)
	if got.Prec() < limit {
		_, hi, lo, _ := got.ToHiLo()
		bigger := new(big.Int).Mul(bigFromHiLo(hi, lo), bigTen)
		require.GreaterOrEqualf(t, bigger.Cmp(two128), 0,
			"%+v has precision %d < %d but one more digit would still fit: over-stripped", got, got.Prec(), limit)
	}
}

// checkFromBigInt holds FromBigInt(coef, exp) to three independent oracles:
//   - the strict parser on "<coef>e<exp>": the same value (after Trim, see
//     parseOracle) or the same sentinel;
//   - for small |exp|, plain big.Int arithmetic: success exactly when the
//     value has a Decimal form, and then exactly that value;
//   - requireMaxPrecision for the representation.
//
// It also checks that FromBigInt leaves its argument alone.
func checkFromBigInt(t *testing.T, coef *big.Int, exp int32) {
	t.Helper()
	before := new(big.Int).Set(coef)
	got, err := FromBigInt(coef, exp)
	require.Zerof(t, coef.Cmp(before), "FromBigInt modified its argument: %s became %s", before, coef)

	want, wantErr := parseOracle(coef, exp)
	if wantErr != nil {
		require.ErrorIsf(t, err, wantErr, "coef=%s exp=%d", coef, exp)
		require.Equalf(t, zerodecimal.Decimal{}, got, "coef=%s exp=%d", coef, exp)
		if smallExp(exp) {
			require.Falsef(t, representable(coef, int(exp)), "coef=%s exp=%d is representable, yet %v", coef, exp, err)
		}
		return
	}
	require.NoErrorf(t, err, "coef=%s exp=%d", coef, exp)
	require.Equalf(t, want, got.Trim(), "coef=%s exp=%d got=%+v", coef, exp, got)
	if smallExp(exp) {
		require.Truef(t, representable(coef, int(exp)), "coef=%s exp=%d is not representable, yet got %+v", coef, exp, got)
		require.Truef(t, sameValue(coef, int(exp), signedCoef(got), int(got.Prec())),
			"coef=%s exp=%d got=%+v: wrong value", coef, exp, got)
	}
	requireMaxPrecision(t, got, exp)
}

// randMagnitude returns a random non-negative integer of exactly bitLen bits
// (zero for bitLen 0).
func randMagnitude(rng *rand.Rand, bitLen int) *big.Int {
	if bitLen == 0 {
		return new(big.Int)
	}
	buf := make([]byte, (bitLen+7)/8)
	for i := range buf {
		buf[i] = byte(rng.Uint64())
	}
	n := new(big.Int).SetBytes(buf)
	n.Rsh(n, uint(len(buf)*8-bitLen))
	return n.SetBit(n, bitLen-1, 1)
}

// randSign negates n half the time.
func randSign(rng *rand.Rand, n *big.Int) *big.Int {
	if rng.IntN(2) == 0 {
		n.Neg(n)
	}
	return n
}

// randCoef returns a signed coefficient of 0..200 bits that is, a third of
// the time, also a multiple of up to 10^45: random numbers almost never end
// in a zero, and trailing zeros are what stripping needs.
func randCoef(rng *rand.Rand) *big.Int {
	n := randMagnitude(rng, rng.IntN(201))
	if rng.IntN(3) == 0 {
		n.Mul(n, pow10Big(rng.IntN(46)))
	}
	return randSign(rng, n)
}

// extremeExps are the exponents beyond -40..40 that the tests mix in: the
// int32 limits and their neighbors, and a few large ones.
var extremeExps = [...]int32{
	math.MinInt32, math.MinInt32 + 1, -1 << 20, -4096, -100, 100, 4096, 1 << 20, math.MaxInt32 - 1, math.MaxInt32,
}

// randExp returns an exponent in -40..40, or an extreme one now and then.
func randExp(rng *rand.Rand) int32 {
	if rng.IntN(8) == 0 {
		return extremeExps[rng.IntN(len(extremeExps))]
	}
	return int32(rng.IntN(81)) - 40
}

// TestFromBigIntOracle runs the oracles over random coefficients of 0..200
// bits in both signs against exponents -40..40 and the int32 extremes.
func TestFromBigIntOracle(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x7a646269, 0x6f7261636c65))
	for range 80_000 {
		checkFromBigInt(t, randCoef(rng), randExp(rng))
	}
}

// TestFromBigIntNearStrip aims at the interesting boundary: coefficients
// r·10^k whose exponent lands within a few places of -k, so that the value
// flips between representable, too precise and too large as the exponent
// moves, and the strip (or the lack of one) decides each case.
func TestFromBigIntNearStrip(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x7a646269, 0x7374726970))
	for range 80_000 {
		k := rng.IntN(60)
		coef := randSign(rng, new(big.Int).Mul(randMagnitude(rng, 1+rng.IntN(150)), pow10Big(k)))
		checkFromBigInt(t, coef, int32(-(k + rng.IntN(34) - 10)))
	}
}

// TestFromBigIntLarge runs the oracles on coefficients with up to thousands of
// trailing zeros (r·10^n for a random r of up to 140 bits) at exponents around
// -n, so that the decimal-digit arithmetic of the wide path sees long digit
// strings on both sides of every decision: representable, too precise, too
// large.
func TestFromBigIntLarge(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6c61726765, 0x7a646269))
	for _, n := range []int{50, 120, 200, 1_000, 3_000} {
		for range 4_000 {
			coef := randSign(rng, new(big.Int).Mul(randMagnitude(rng, 1+rng.IntN(140)), pow10Big(n)))
			checkFromBigInt(t, coef, int32(-(n + rng.IntN(71) - 30)))
		}
	}
}

// TestFromBigIntBoundaries crosses the width, limb and power-of-ten
// boundaries of the 128-bit range with every exponent in -70..70 and the
// extremes, in both signs.
func TestFromBigIntBoundaries(t *testing.T) {
	t.Parallel()
	pow64 := new(big.Int).Lsh(bigOne, 64)
	coefs := []*big.Int{
		big.NewInt(0), big.NewInt(1), big.NewInt(9), big.NewInt(10), big.NewInt(11), big.NewInt(100),
		new(big.Int).SetUint64(math.MaxUint64), pow64, new(big.Int).Add(pow64, bigOne),
		new(big.Int).Lsh(bigOne, 127), new(big.Int).Sub(max128, bigOne), max128, two128, new(big.Int).Add(two128, bigOne),
		pow10Big(19), pow10Big(20), pow10Big(38), new(big.Int).Mul(big.NewInt(3), pow10Big(38)),
		new(big.Int).Mul(big.NewInt(4), pow10Big(38)), pow10Big(39), pow10Big(45), pow10Big(80),
		new(big.Int).Mul(max128, bigTen), new(big.Int).Mul(max128, pow10Big(19)), new(big.Int).Mul(max128, pow10Big(25)),
		new(big.Int).Mul(two128, pow10Big(5)), new(big.Int).Mul(new(big.Int).Add(two128, bigOne), pow10Big(5)),
	}
	exps := []int32{math.MinInt32, math.MinInt32 + 1, -1 << 20, math.MaxInt32 - 1, math.MaxInt32}
	for e := int32(-70); e <= 70; e++ {
		exps = append(exps, e)
	}
	for _, c := range coefs {
		for _, e := range exps {
			checkFromBigInt(t, c, e)
			checkFromBigInt(t, new(big.Int).Neg(c), e)
		}
	}
}

// TestFromBigIntEdges pins the contract on named cases with literal
// expectations: the edge cases (nil, zero, 2^128-1, 2^128, a strippable
// 10^45 at exp -40, MinInt32) and the representation FromBigInt picks.
func TestFromBigIntEdges(t *testing.T) {
	t.Parallel()
	maxS := max128.String()
	tests := []struct {
		name    string
		coef    string
		exp     int32
		want    string // signed coefficient of the result
		prec    uint8
		wantErr error
	}{
		// zero, for every exponent
		{name: "zero_exp_0", coef: "0", exp: 0, want: "0"},
		{name: "zero_exp_positive", coef: "0", exp: 7, want: "0"},
		{name: "zero_exp_negative", coef: "0", exp: -7, want: "0"},
		{name: "zero_exp_max", coef: "0", exp: math.MaxInt32, want: "0"},
		{name: "zero_exp_min", coef: "0", exp: math.MinInt32, want: "0"},

		// the 128-bit boundary at exp 0
		{name: "max128", coef: maxS, exp: 0, want: maxS},
		{name: "negative_max128", coef: "-" + maxS, exp: 0, want: "-" + maxS},
		{name: "two128_overflows", coef: two128.String(), exp: 0, wantErr: zerodecimal.ErrOverflow},
		{name: "negative_two128_overflows", coef: "-" + two128.String(), exp: 0, wantErr: zerodecimal.ErrOverflow},
		{name: "min_int64", coef: "-9223372036854775808", exp: 0, want: "-9223372036854775808"},

		// a negative exp is the precision, trailing zeros kept
		{name: "keeps_trailing_zeros", coef: "150", exp: -2, want: "150", prec: 2},
		{name: "negative_keeps_trailing_zeros", coef: "-150", exp: -2, want: "-150", prec: 2},
		{name: "max128_prec1", coef: maxS, exp: -1, want: maxS, prec: 1},
		{name: "max128_prec19", coef: maxS, exp: -19, want: maxS, prec: 19},
		{name: "max128_prec20_odd", coef: maxS, exp: -20, wantErr: zerodecimal.ErrPrecOutOfRange},
		{name: "two128_prec1_not_strippable", coef: two128.String(), exp: -1, wantErr: zerodecimal.ErrOverflow},

		// -exp > MaxPrec: strip factors of ten while the value stays exact
		{name: "strip_one", coef: pow10Big(38).String(), exp: -20, want: pow10Big(37).String(), prec: 19},
		{name: "strip_two_divisions", coef: pow10Big(38).String(), exp: -57, want: "1", prec: 19},
		{name: "strip_more_than_the_digits", coef: pow10Big(38).String(), exp: -58, wantErr: zerodecimal.ErrPrecOutOfRange},
		{name: "strip_inexact", coef: "12345678901234567890123456789", exp: -25, wantErr: zerodecimal.ErrPrecOutOfRange},
		{name: "strip_one_zero_short", coef: "1200000", exp: -25, wantErr: zerodecimal.ErrPrecOutOfRange},
		{name: "strip_exactly_enough", coef: "12000000", exp: -25, want: "12", prec: 19},

		// past 128 bits: only stripping can help
		{name: "wide_1e45_exp_-40", coef: pow10Big(45).String(), exp: -40, want: pow10Big(24).String(), prec: 19},
		{name: "wide_1e45_exp_-10_minimal_strip", coef: pow10Big(45).String(), exp: -10, want: pow10Big(38).String(), prec: 3},
		{name: "wide_1e45_exp_-5_value_too_large", coef: pow10Big(45).String(), exp: -5, wantErr: zerodecimal.ErrOverflow},
		{name: "wide_1e45_exp_-45", coef: pow10Big(45).String(), exp: -45, want: pow10Big(19).String(), prec: 19},
		{name: "wide_1e45_exp_-64", coef: pow10Big(45).String(), exp: -64, want: "1", prec: 19},
		{name: "wide_1e45_exp_-65", coef: pow10Big(45).String(), exp: -65, wantErr: zerodecimal.ErrPrecOutOfRange},
		{name: "wide_1e45_exp_min", coef: pow10Big(45).String(), exp: math.MinInt32, wantErr: zerodecimal.ErrPrecOutOfRange},
		{name: "wide_negative", coef: "-" + pow10Big(45).String(), exp: -40, want: "-" + pow10Big(24).String(), prec: 19},
		{name: "wide_odd", coef: new(big.Int).Add(pow10Big(45), bigOne).String(), exp: -40, wantErr: zerodecimal.ErrOverflow},
		{name: "wide_even_not_a_multiple_of_ten", coef: new(big.Int).Add(pow10Big(45), big.NewInt(2)).String(), exp: -40, wantErr: zerodecimal.ErrOverflow},
		{name: "wide_not_enough_zeros", coef: "123456789012345678901234567890123456789012000", exp: -40, wantErr: zerodecimal.ErrOverflow},
		{name: "wide_odd_min_exp", coef: "123456789012345678901234567890123456789012345", exp: math.MinInt32, wantErr: zerodecimal.ErrOverflow},
		{name: "wide_fits_at_prec_0", coef: new(big.Int).Mul(max128, pow10Big(5)).String(), exp: -5, want: maxS},
		{name: "wide_one_place_short", coef: new(big.Int).Mul(max128, pow10Big(5)).String(), exp: -4, wantErr: zerodecimal.ErrOverflow},
		{name: "wide_exp_0", coef: pow10Big(45).String(), exp: 0, wantErr: zerodecimal.ErrOverflow},
		{name: "wide_exp_positive", coef: pow10Big(45).String(), exp: 3, wantErr: zerodecimal.ErrOverflow},
		{name: "wide_exp_max", coef: pow10Big(45).String(), exp: math.MaxInt32, wantErr: zerodecimal.ErrOverflow},

		// a positive exp scales the coefficient
		{name: "scale_up", coef: "12", exp: 3, want: "12000"},
		{name: "scale_up_negative", coef: "-12", exp: 3, want: "-12000"},
		{name: "scale_up_to_1e38", coef: "1", exp: 38, want: pow10Big(38).String()},
		{name: "scale_up_to_3e38", coef: "3", exp: 38, want: new(big.Int).Mul(big.NewInt(3), pow10Big(38)).String()},
		{name: "scale_up_past_max", coef: "4", exp: 38, wantErr: zerodecimal.ErrOverflow},
		{name: "scale_up_exp_39", coef: "1", exp: 39, wantErr: zerodecimal.ErrOverflow},
		{name: "scale_up_exp_max", coef: "1", exp: math.MaxInt32, wantErr: zerodecimal.ErrOverflow},
		{name: "scale_up_max128", coef: maxS, exp: 1, wantErr: zerodecimal.ErrOverflow},
		{name: "scale_up_2e64_by_1e19", coef: new(big.Int).Lsh(bigOne, 64).String(), exp: 19, want: new(big.Int).Mul(new(big.Int).Lsh(bigOne, 64), pow10Big(19)).String()},
		{name: "scale_up_2e64_by_1e20", coef: new(big.Int).Lsh(bigOne, 64).String(), exp: 20, wantErr: zerodecimal.ErrOverflow},

		// nothing is ever rounded
		{name: "min_exp", coef: "1", exp: math.MinInt32, wantErr: zerodecimal.ErrPrecOutOfRange},
		{name: "min_exp_plus_one", coef: "-1", exp: math.MinInt32 + 1, wantErr: zerodecimal.ErrPrecOutOfRange},
		{name: "one_place_past_max_prec", coef: "1", exp: -20, wantErr: zerodecimal.ErrPrecOutOfRange},
		{name: "tiny_but_exact", coef: "10", exp: -20, want: "1", prec: 19},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coef := mustBig(t, tc.coef)
			got, err := FromBigInt(coef, tc.exp)
			if tc.wantErr != nil {
				// The sentinels come back bare, never wrapped, exactly as from
				// New and the arithmetic.
				require.Same(t, tc.wantErr, err, "the sentinel must come back bare")
				require.Equal(t, zerodecimal.Decimal{}, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.want, signedCoef(got).String())
				require.Equal(t, tc.prec, got.Prec())
			}
			checkFromBigInt(t, coef, tc.exp) // and the same case through the oracles
		})
	}
}

// TestFromBigIntNil pins the nil coefficient: canonical zero, any exponent.
func TestFromBigIntNil(t *testing.T) {
	t.Parallel()
	for _, exp := range []int32{0, 1, -1, 38, 39, -19, -20, math.MaxInt32, math.MinInt32} {
		got, err := FromBigInt(nil, exp)
		require.NoError(t, err)
		require.Equal(t, zerodecimal.Decimal{}, got)
	}
}

// TestFromBigIntAgreesWithNew: for coefficients that fit an int64, FromBigInt
// is New, error for error and representation for representation.
func TestFromBigIntAgreesWithNew(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6e6577, 0x7a646269))
	values := []int64{0, 1, -1, 7, 10, -10, 100, 1_000, 12_345, math.MaxInt64, math.MinInt64, math.MinInt64 + 1, math.MaxInt64 - 1}
	for range 5_000 {
		values = append(values, int64(rng.Uint64())>>rng.IntN(64))
	}
	exps := []int32{
		math.MinInt32, math.MinInt32 + 1, -64, -41, -40, -39, -38, -20, -19, -18, -2, -1, 0, 1, 2, 18, 19, 20, 37, 38, 39, 40,
		math.MaxInt32 - 1, math.MaxInt32,
	}
	for _, v := range values {
		for _, e := range exps {
			want, wantErr := zerodecimal.New(v, e)
			got, err := FromBigInt(big.NewInt(v), e)
			require.Equalf(t, wantErr, err, "v=%d exp=%d", v, e)
			require.Equalf(t, want, got, "v=%d exp=%d", v, e)
		}
	}
}

// TestFromBigIntBareSentinels: errors are the package sentinels themselves,
// on the 128-bit path and on the wide path.
func TestFromBigIntBareSentinels(t *testing.T) {
	t.Parallel()
	tests := []struct {
		coef *big.Int
		exp  int32
		want error
	}{
		{big.NewInt(1), 39, zerodecimal.ErrOverflow},
		{max128, 1, zerodecimal.ErrOverflow},
		{two128, 0, zerodecimal.ErrOverflow},
		{pow10Big(45), -5, zerodecimal.ErrOverflow},
		{big.NewInt(1), -20, zerodecimal.ErrPrecOutOfRange},
		{pow10Big(38), -58, zerodecimal.ErrPrecOutOfRange},
		{pow10Big(45), -65, zerodecimal.ErrPrecOutOfRange},
	}
	for _, tc := range tests {
		_, err := FromBigInt(tc.coef, tc.exp)
		require.Samef(t, tc.want, err, "FromBigInt(%s, %d) must return the bare sentinel", tc.coef, tc.exp)
	}
}

// TestRoundTrip: random Decimals through ToBigInt and back come out equal,
// representation included, and ToBigInt reports exactly -prec.
func TestRoundTrip(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x726f756e64, 0x74726970))
	for i := range 100_000 {
		// widths spread evenly over 0..128 bits so that one-limb, two-limb
		// and full-width coefficients all occur
		width := rng.IntN(129)
		if i%13 == 0 {
			width = 128
		}
		hi, lo := hiLoOf(randMagnitude(rng, width))
		prec := uint8(rng.IntN(int(zerodecimal.MaxPrec) + 1))
		d, err := zerodecimal.NewFromHiLo(rng.IntN(2) == 0, hi, lo, prec)
		require.NoError(t, err)

		coef, exp := ToBigInt(d)
		require.Equal(t, -int32(d.Prec()), exp)
		require.Zerof(t, signedCoef(d).Cmp(coef), "d=%+v coef=%s", d, coef)

		back, err := FromBigInt(coef, exp)
		require.NoErrorf(t, err, "d=%+v", d)
		require.Equal(t, d, back)
	}
}

// TestRoundTripFromBigInt: whatever FromBigInt accepts ToBigInt turns back
// into the same value (not necessarily the same pair: a stripped coefficient
// comes back without its zeros).
func TestRoundTripFromBigInt(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x62616b, 0x7a646269))
	for range 40_000 {
		coef, exp := randCoef(rng), randExp(rng)
		d, err := FromBigInt(coef, exp)
		if err != nil {
			continue
		}
		c2, e2 := ToBigInt(d)
		require.LessOrEqual(t, e2, int32(0))
		if smallExp(exp) {
			require.Truef(t, sameValue(coef, int(exp), c2, int(-e2)), "coef=%s exp=%d -> %s, %d", coef, exp, c2, e2)
		}
		d2, err := FromBigInt(c2, e2)
		require.NoError(t, err)
		require.Equal(t, d, d2)
	}
}

// TestToBigInt pins ToBigInt on named values.
func TestToBigInt(t *testing.T) {
	t.Parallel()
	maxNeg, err := zerodecimal.NewFromHiLo(true, math.MaxUint64, math.MaxUint64, 19)
	require.NoError(t, err)
	tests := []struct {
		name string
		d    zerodecimal.Decimal
		coef string
		exp  int32
	}{
		{name: "zero", d: zerodecimal.Decimal{}, coef: "0", exp: 0},
		{name: "integer", d: zerodecimal.NewFromInt(-5), coef: "-5", exp: 0},
		{name: "keeps_trailing_zeros", d: zerodecimal.MustNew(1_500, -3), coef: "1500", exp: -3},
		{name: "fraction", d: zerodecimal.RequireFromString("-0.000001"), coef: "-1", exp: -6},
		{name: "one_limb_max", d: zerodecimal.NewFromUint64(math.MaxUint64), coef: "18446744073709551615", exp: 0},
		{name: "two_limbs", d: zerodecimal.RequireFromString("18446744073709551616"), coef: "18446744073709551616", exp: 0},
		{name: "max_negative_prec19", d: maxNeg, coef: "-" + max128.String(), exp: -19},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			coef, exp := ToBigInt(tc.d)
			require.NotNil(t, coef)
			require.Equal(t, tc.coef, coef.String())
			require.Equal(t, tc.exp, exp)
			// The result is normalized like any other big.Int.
			_, hi, lo, _ := tc.d.ToHiLo()
			wantBits := bits.Len64(lo)
			if hi != 0 {
				wantBits = 64 + bits.Len64(hi)
			}
			require.Equal(t, wantBits, coef.BitLen())
			require.Equal(t, tc.d.Sign(), coef.Sign())
		})
	}
}

// TestToBigIntIsAnOrdinaryBigInt: the result belongs to the caller. It can be
// mutated and grown without touching any other result, and each call returns
// a distinct integer.
func TestToBigIntIsAnOrdinaryBigInt(t *testing.T) {
	t.Parallel()
	d := zerodecimal.MustNew(-123_456_789, -4)
	a, _ := ToBigInt(d)
	b, _ := ToBigInt(d)
	require.NotSame(t, a, b)

	a.Mul(a, pow10Big(60)) // far past the limbs ToBigInt allocated
	a.Add(a, bigOne)
	want := new(big.Int).Mul(big.NewInt(-123_456_789), pow10Big(60))
	require.Zero(t, a.Cmp(want.Add(want, bigOne)))
	require.Equal(t, "-123456789", b.String())
	c, _ := ToBigInt(d)
	require.Equal(t, "-123456789", c.String())

	// and an integer ToBigInt returned is a valid operand after mutation
	b.Neg(b)
	got, err := FromBigInt(b, -4)
	require.NoError(t, err)
	require.Equal(t, d.Neg(), got)
}

// limbsOf splits n into little-endian limbs of limbBits bits with plain
// big.Int shifts and masks, without leading zero limbs: the decomposition a
// platform with that big.Word width reports from Bits(). limbBits must not
// exceed the host's word size.
func limbsOf(n *big.Int, limbBits int) []big.Word {
	mask := new(big.Int).Sub(new(big.Int).Lsh(bigOne, uint(limbBits)), bigOne)
	rest := new(big.Int).Set(n)
	var limbs []big.Word
	for rest.Sign() != 0 {
		limbs = append(limbs, big.Word(new(big.Int).And(rest, mask).Uint64()))
		rest.Rsh(rest, uint(limbBits))
	}
	return limbs
}

// TestLimbHelpers forces the decomposition a 32-bit platform produces (four
// limbs of 32 bits instead of two of 64) through the helpers behind FromBigInt
// and ToBigInt, on any host, and checks both widths against big.Int. (A host
// with 32-bit words can only run the 32-bit rows.)
func TestLimbHelpers(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6c696d6273, 0x7a646269))
	for _, limbBits := range []int{32, 64} {
		if limbBits > bits.UintSize {
			continue
		}
		for i := range 30_000 {
			mag := randMagnitude(rng, rng.IntN(129))
			if i%11 == 0 {
				mag = new(big.Int).Set(max128)
			}
			hi, lo := hiLoOf(mag)
			limbs := limbsOf(mag, limbBits)

			// u128FromLimbs reads the limbs a big.Int of that width reports...
			gotHi, gotLo, ok := u128FromLimbs(limbs, limbBits)
			require.Truef(t, ok, "limbBits=%d mag=%s", limbBits, mag)
			require.Equalf(t, [2]uint64{hi, lo}, [2]uint64{gotHi, gotLo}, "limbBits=%d mag=%s", limbBits, mag)
			// ...tolerating zero limbs past the top, which a normalized slice
			// never has...
			padded := append(append([]big.Word(nil), limbs...), 0, 0, 0, 0, 0)
			gotHi, gotLo, ok = u128FromLimbs(padded, limbBits)
			require.True(t, ok)
			require.Equal(t, [2]uint64{hi, lo}, [2]uint64{gotHi, gotLo})
			// ...and refusing a value with any bit at or above 2^128.
			over := new(big.Int).Add(mag, two128)
			_, _, ok = u128FromLimbs(limbsOf(over, limbBits), limbBits)
			require.Falsef(t, ok, "limbBits=%d mag+2^128=%s", limbBits, over)
			top := new(big.Int).Lsh(bigOne, uint(128+rng.IntN(300)))
			_, _, ok = u128FromLimbs(limbsOf(top, limbBits), limbBits)
			require.False(t, ok)

			// putLimbs writes the limbs back, each in range, all of them: the
			// significant ones equal what big.Int would report, the rest are zero.
			var buf [4]big.Word
			dst := buf[:128/limbBits]
			putLimbs(dst, limbBits, hi, lo)
			for _, w := range dst {
				require.Zero(t, uint64(w)>>limbBits, "limb wider than %d bits", limbBits)
			}
			significant := dst
			for len(significant) > 0 && significant[len(significant)-1] == 0 {
				significant = significant[:len(significant)-1]
			}
			require.Lenf(t, significant, len(limbs), "limbBits=%d mag=%s", limbBits, mag)
			if len(limbs) > 0 {
				require.Equalf(t, limbs, significant, "limbBits=%d mag=%s", limbBits, mag)
			}
		}
	}
}

// TestLimbHelpersNativeWidth: on the host's own word size the helpers agree
// with what big.Int reports, including zero and values past the range. (On
// linux/386 this is the 32-bit path itself.)
func TestLimbHelpersNativeWidth(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6e6174697665, 0x7a646269))
	for range 20_000 {
		mag := randMagnitude(rng, rng.IntN(200))
		hi, lo, ok := u128FromLimbs(mag.Bits(), bits.UintSize)
		require.Equal(t, mag.BitLen() <= 128, ok, "mag=%s", mag)
		if ok {
			require.Zero(t, bigFromHiLo(hi, lo).Cmp(mag))
		}
	}
	hi, lo, ok := u128FromLimbs(new(big.Int).Bits(), bits.UintSize)
	require.True(t, ok)
	require.Zero(t, hi|lo)
}

// TestPow10Bits: pow10Bits(scale) is a safe bound, 10^scale < 2^bits, and a
// tight one, at most one bit beyond the exact length of 10^scale.
func TestPow10Bits(t *testing.T) {
	t.Parallel()
	for scale := range 4_000 {
		exact := int64(pow10Big(scale).BitLen()) // 10^scale < 2^exact
		got := pow10Bits(int64(scale))
		require.GreaterOrEqual(t, got, exact, "scale=%d: bound %d below the %d bits 10^scale has", scale, got, exact)
		require.LessOrEqual(t, got, exact+1, "scale=%d: bound %d is loose", scale, got)
	}
	// At the int32 extreme the exact length is out of reach; the bound must
	// still be ahead of log2(10)·scale, within the ten or so bits that
	// rounding the constant up costs there, and the arithmetic must not wrap.
	scale := int64(math.MaxInt32) + 1
	require.Greater(t, float64(pow10Bits(scale)), float64(scale)*math.Log2(10))
	require.Less(t, float64(pow10Bits(scale)), float64(scale)*math.Log2(10)+16)
}

// TestMultipleOf10 holds the limb-sum test for divisibility by ten to big.Int,
// on the limb widths of 32- and 64-bit platforms (both bases are 1 mod 5), for
// numbers of every size. Random numbers are a multiple of ten one time in
// ten, so multiples of ten, of five and of two are made common.
func TestMultipleOf10(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6d756c74, 0x3130))
	five := big.NewInt(5)
	check := func(limbBits int, n *big.Int) {
		t.Helper()
		want := new(big.Int).Mod(n, bigTen).Sign() == 0
		require.Equalf(t, want, multipleOf10(limbsOf(n, limbBits)), "limbBits=%d n=%s", limbBits, n)
	}
	for _, limbBits := range []int{32, 64} {
		if limbBits > bits.UintSize {
			continue
		}
		for v := range int64(300) { // small values, zero included (no limbs at all)
			check(limbBits, big.NewInt(v))
		}
		for k := range uint(300) { // 2^k is even and never a multiple of five; 10^k and 5^k are the other extremes
			check(limbBits, new(big.Int).Lsh(bigOne, k))
			check(limbBits, pow10Big(int(k)))
			check(limbBits, new(big.Int).Exp(five, big.NewInt(int64(k)), nil))
			check(limbBits, new(big.Int).Mul(new(big.Int).Lsh(bigOne, k), five))
		}
		for i := range 40_000 {
			n := randMagnitude(rng, 1+rng.IntN(400))
			switch i % 4 {
			case 0:
				n.Mul(n, bigTen)
			case 1:
				n.Mul(n, five)
			case 2:
				n.Lsh(n, uint(1+rng.IntN(70)))
			}
			check(limbBits, n)
		}
	}
	require.True(t, multipleOf10(nil), "an empty limb slice is zero")
	require.True(t, multipleOf10([]big.Word{0}), "so is a zero limb")
}

// TestDecimalFromDigits pins the helper behind the last step of the wide
// path: digits the parser takes become the Decimal, and anything it refuses,
// or a precision it cannot hold, comes back as an error, never as a silent
// zero. (FromBigInt never hands it such input today; this is what a change to
// the parser's contract would hit.)
func TestDecimalFromDigits(t *testing.T) {
	t.Parallel()
	got, err := decimalFromDigits(true, max128.String(), 7)
	require.NoError(t, err)
	require.Equal(t, "-"+max128.String(), signedCoef(got).String())
	require.Equal(t, uint8(7), got.Prec())

	for _, bad := range []string{"", "x", "12e", "1.", ".5", strings.Repeat("1", 201), two128.String()} {
		refused, refusedErr := decimalFromDigits(false, bad, 3)
		require.Errorf(t, refusedErr, "digits %q", bad)
		require.Equalf(t, zerodecimal.Decimal{}, refused, "digits %q", bad)
	}
	got, err = decimalFromDigits(false, "1", zerodecimal.MaxPrec+1)
	require.ErrorIs(t, err, zerodecimal.ErrPrecOutOfRange)
	require.Equal(t, zerodecimal.Decimal{}, got)
}
