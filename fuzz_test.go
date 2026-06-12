//go:build fuzz

package zerodecimal

// Differential fuzzing against two independent oracles, compiled only under
// the fuzz tag (go test -tags=fuzz -run='^$' -fuzz='^FuzzName$').
// shopspring/decimal is the unbounded semantic oracle: where a result fits
// the 128-bit coefficient the libraries must agree exactly, and where
// zerodecimal returns ErrOverflow a big.Int computation must prove the exact
// coefficient at the contracted precision is ≥ 2^128 — an iff oracle, so a
// spurious error fails as loudly as a wrong value. quagmt/udecimal is the
// second oracle for Add/Sub/Mul: it shares the (neg, hi, lo, prec)
// representation bit for bit and its internal big.Int fallback keeps its
// answers exact past 128 bits, so on every zerodecimal success the canonical
// strings must match. Every target is total over its input space — the
// fuzzer must never be able to panic the library — and the oracle plumbing
// reuses the cross-check helpers from crosscheck_test.go, which compile under
// all build tags.

import (
	"errors"
	"math"
	"math/big"
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/quagmt/udecimal"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// fuzzSeed is one raw (neg, hi, lo, prec) corpus quadruple — the exact wire
// shape every pairwise target fuzzes over.
type fuzzSeed struct {
	neg  bool
	hi   uint64
	lo   uint64
	prec uint8
}

// fuzzCorpus seeds every target: udecimal's eighteen fuzz quadruples verbatim
// (the shared-representation baseline), then zerodecimal's boundary
// additions — the 2^128-1 coefficient at full and near-full precision, the
// 2^64 limb boundary with both neighbors, 2^127, 10^19 and 10^38 (the one-
// and two-limb power-of-ten extremes), a sign-boundary fraction, and a
// near-max two-limb coefficient at prec 18.
var fuzzCorpus = []fuzzSeed{
	{false, 0, 0, 0},
	{false, 1, 0, 0},
	{false, 1234567890123456789, 0, 0},
	{true, 1, 0, 0},
	{false, 1123, 0, 3},
	{true, 1123, 0, 3},
	{false, 123123, 0, 6},
	{true, 123123, 0, 6},
	{false, 123456789123456789, 1234567890123456789, 9},
	{true, 123456789123456789, 1234567890123456789, 9},
	{false, 0, 1234567890123456789, 19},
	{true, 0, 1234567890123456789, 19},
	{false, 0, 1, 19},
	{true, 0, 1, 19},
	{false, math.MaxUint64, math.MaxUint64, 0},
	{false, math.MaxUint64, math.MaxUint64, 10},
	{true, math.MaxUint64, math.MaxUint64, 0},
	{true, math.MaxUint64, math.MaxUint64, 10},
	{false, math.MaxUint64, math.MaxUint64, 19},
	{false, 0, math.MaxUint64, 0},
	{false, 1, 0, 0},
	{false, 1, 1, 0},
	{false, 1 << 63, 0, 0},
	{false, 0, 1<<63 | 1, 7},
	{false, 0x4B3B4CA85A86C47A, 0x098A224000000000, 0}, // 10^38
	{false, 0, 10_000_000_000_000_000_000, 0},          // 10^19
	{true, math.MaxUint64, math.MaxUint64, 1},
	{false, math.MaxUint64 / 2, math.MaxUint64, 18},
}

// fuzzPairs seeds f with the full corpus cross product, so every boundary
// shape meets every other on both operand sides.
func fuzzPairs(f *testing.F) {
	for _, a := range fuzzCorpus {
		for _, b := range fuzzCorpus {
			f.Add(a.neg, a.hi, a.lo, a.prec, b.neg, b.hi, b.lo, b.prec)
		}
	}
}

// fuzzPairsPlaces is fuzzPairs with a trailing rounding-places byte drawn
// from a fixed-seed generator — seed corpora must be identical run to run.
func fuzzPairsPlaces(f *testing.F) {
	rng := rand.New(rand.NewPCG(0xF0CC5EED, 0x9DACE5))
	for _, a := range fuzzCorpus {
		for _, b := range fuzzCorpus {
			f.Add(a.neg, a.hi, a.lo, a.prec, b.neg, b.hi, b.lo, b.prec, uint8(rng.Uint64N(20)))
		}
	}
}

// fuzzDecimal builds the canonical operand for one fuzzed quadruple. prec
// reduces modulo 20 into the valid 0..MaxPrec range so every mutated byte
// yields a constructible operand instead of a skipped input.
func fuzzDecimal(t *testing.T, neg bool, hi, lo uint64, prec uint8) Decimal {
	t.Helper()
	return mustHiLo(t, neg, hi, lo, prec%20)
}

// fuzzOperands builds both operands of a pairwise target.
func fuzzOperands(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) (Decimal, Decimal) {
	t.Helper()
	return fuzzDecimal(t, aneg, ahi, alo, aprec), fuzzDecimal(t, bneg, bhi, blo, bprec)
}

// fuzzProduct widens the value space beyond raw quadruples: the pair product
// when it fits, otherwise the first operand. The fallback keeps the target
// total — an overflowing product is proven exact by FuzzMul's oracle and must
// not abort coverage here.
func fuzzProduct(t *testing.T, a, b Decimal) Decimal {
	t.Helper()
	c, err := a.Mul(b)
	if err != nil {
		require.ErrorIsf(t, err, ErrOverflow, "mul can fail only with overflow: a=%+v b=%+v", a, b)
		return a
	}
	return c
}

// udecOf mirrors d into udecimal's bit-compatible representation. It cannot
// fail: both libraries share the 128-bit coefficient and the 0..19 precision
// range.
func udecOf(t *testing.T, d Decimal) udecimal.Decimal {
	t.Helper()
	neg, hi, lo, prec := d.ToHiLo()
	u, err := udecimal.NewFromHiLo(neg, hi, lo, prec)
	require.NoErrorf(t, err, "udecimal must accept every zerodecimal representation: d=%+v", d)
	return u
}

// FuzzParseRoundTrip checks the canonical formatting/parsing fixed point on
// products of fuzzed operands (the multiply widens the string space far
// beyond raw quadruples): String must reparse to the same value and the same
// string, and shopspring must read the canonical output as the same number.
func FuzzParseRoundTrip(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		c := fuzzProduct(t, a, b)
		s := c.String()
		reparsed, err := NewFromString(s)
		require.NoErrorf(t, err, "canonical output must reparse: %q", s)
		require.Equalf(t, s, reparsed.String(), "string fixed point: %q", s)
		require.Zerof(t, c.Cmp(reparsed), "reparse preserves the value: c=%+v", c)
		ssParsed, ssErr := decimal.NewFromString(s)
		require.NoErrorf(t, ssErr, "shopspring must parse canonical output %q", s)
		require.Truef(t, ssOf(c).Equal(ssParsed), "shopspring reparse value: c=%+v s=%q", c, s)
	})
}

// FuzzParseString throws raw strings at both parser modes. Neither may
// panic. A strict success must round-trip to an identical canonical
// representation and agree with shopspring on the value; shopspring failures
// on our successes are tolerated only where its int32 exponent gives out,
// which our saturating parser reaches solely for exact zeros. Our stricter
// rejections (bare dots, ".5", "1.", over-long input, >MaxPrec fractional
// positions) carry no shopspring assertion. A truncating success must equal
// the strict parse of its own canonical output, and trunc may never reject
// what strict accepted.
func FuzzParseString(f *testing.F) {
	for _, s := range []string{
		"0", "-0", "+1", "1.5", "-1.5", "1.500", "0.0000000000000000001",
		"34028236692093846346.3374607431768211455",
		"-34028236692093846346.3374607431768211455",
		"340282366920938463463374607431768211455",
		"340282366920938463463374607431768211456",
		"1.23e4", "1E-7", "1e19", "9e38", "0e99999999999", "1e-99999999999",
		"", "-", ".", "1..2", " 1", "NaN", "Inf", "1.", ".1", "00012.3400",
		"123456789012345678901234567890123456789012345678901234567890",
		"0.000000000000000000000000000000000000000000000000000000000001",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		d, err := NewFromString(raw)
		if err == nil {
			s := d.String()
			again, err2 := NewFromString(s)
			require.NoErrorf(t, err2, "canonical output must reparse: %q from %q", s, raw)
			require.Equalf(t, d, again, "canonical round trip is representation-exact: %q", raw)
			ssV, ssErr := decimal.NewFromString(raw)
			switch {
			case ssErr != nil:
				// shopspring's int32 exponent rejects the huge exponents our
				// saturating parser folds into an exact zero.
				require.Truef(t, d.IsZero(), "shopspring rejected %q yet we parsed nonzero %+v", raw, d)
			case d.IsZero() || ssV.IsZero():
				// Compare zeroness directly: shopspring's Equal rescales by
				// 10^|expDiff| in big.Int, which explodes on zeros carrying a
				// huge-but-int32 exponent like "0e900190000". Nonzero values
				// with such exponents never reach here — our parser already
				// rejected them as ErrOverflow or ErrPrecOutOfRange.
				require.Equalf(t, d.IsZero(), ssV.IsZero(), "zeroness vs shopspring: %q -> %s vs %s", raw, d, ssV)
			default:
				require.Truef(t, ssV.Equal(ssOf(d)), "parse value vs shopspring: %q -> %s vs %s", raw, d, ssV)
			}
		}
		dt, terr := NewFromStringTrunc(raw)
		if err == nil {
			// Truncation only relaxes precision and length limits, so a strict
			// success must survive it byte for byte.
			require.NoErrorf(t, terr, "trunc parse must accept strict-accepted %q", raw)
			require.Equalf(t, d, dt, "trunc parse must equal strict parse: %q", raw)
		}
		if terr == nil {
			s := dt.String()
			strict, serr := NewFromString(s)
			require.NoErrorf(t, serr, "trunc output must strict-reparse: %q from %q", s, raw)
			require.Equalf(t, dt, strict, "trunc round trip is representation-exact: %q", raw)
		}
	})
}

// FuzzAdd cross-checks Add with the exact iff overflow oracle — ErrOverflow
// precisely when the exact signed coefficient at max(aPrec, bPrec) is
// ≥ 2^128 — and on success requires the shopspring value, the contracted
// result precision, and udecimal's exact string.
func FuzzAdd(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		fp := max(a.prec, b.prec)
		exact := new(big.Int).Add(signedCoefAt(a, fp), signedCoefAt(b, fp))
		got, err := a.Add(b)
		if exact.CmpAbs(mod128big) >= 0 {
			require.ErrorIsf(t, err, ErrOverflow, "add overflow oracle: a=%+v b=%+v", a, b)
			return
		}
		require.NoErrorf(t, err, "add: a=%+v b=%+v", a, b)
		requireSameValue(t, ssOf(a).Add(ssOf(b)), got, "add", a, b)
		requireResultPrec(t, got, fp, "add", a, b)
		require.Equalf(t, udecOf(t, a).Add(udecOf(t, b)).String(), got.String(),
			"udecimal add oracle: a=%+v b=%+v", a, b)
	})
}

// FuzzSub is FuzzAdd's oracle pattern for Sub: iff overflow on the exact
// difference coefficient, shopspring value and precision on success, and
// udecimal's exact string as the second oracle.
func FuzzSub(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		fp := max(a.prec, b.prec)
		exact := new(big.Int).Sub(signedCoefAt(a, fp), signedCoefAt(b, fp))
		got, err := a.Sub(b)
		if exact.CmpAbs(mod128big) >= 0 {
			require.ErrorIsf(t, err, ErrOverflow, "sub overflow oracle: a=%+v b=%+v", a, b)
			return
		}
		require.NoErrorf(t, err, "sub: a=%+v b=%+v", a, b)
		requireSameValue(t, ssOf(a).Sub(ssOf(b)), got, "sub", a, b)
		requireResultPrec(t, got, fp, "sub", a, b)
		require.Equalf(t, udecOf(t, a).Sub(udecOf(t, b)).String(), got.String(),
			"udecimal sub oracle: a=%+v b=%+v", a, b)
	})
}

// FuzzMul cross-checks Mul against shopspring's exact product truncated at
// min(aPrec+bPrec, DefaultPrec), with the iff overflow oracle on the
// truncated coefficient. udecimal truncates at its own fixed 19-digit
// default, so its string oracle applies only on default-precision builds.
func FuzzMul(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		pSum := int(a.prec) + int(b.prec)
		rp := min(pSum, int(DefaultPrec))
		exact := new(big.Int).Mul(u128ToBig(a.coef), u128ToBig(b.coef))
		if pSum > rp {
			exact.Quo(exact, bp10(pSum-rp))
		}
		got, err := a.Mul(b)
		if exact.Cmp(mod128big) >= 0 {
			require.ErrorIsf(t, err, ErrOverflow, "mul overflow oracle: a=%+v b=%+v", a, b)
			return
		}
		require.NoErrorf(t, err, "mul: a=%+v b=%+v", a, b)
		requireSameValue(t, ssOf(a).Mul(ssOf(b)).Truncate(int32(rp)), got, "mul", a, b)
		requireResultPrec(t, got, uint8(rp), "mul", a, b)
		if DefaultPrec == 19 {
			require.Equalf(t, udecOf(t, a).Mul(udecOf(t, b)).String(), got.String(),
				"udecimal mul oracle: a=%+v b=%+v", a, b)
		}
	})
}

// FuzzDiv runs the full adaptive-precision division oracle from the
// cross-check suite: ErrDivideByZero on zero divisors, exact equality with
// shopspring's truncated quotient at the precision Div chose, a big.Int proof
// that the chosen precision is maximal (one more digit would push the
// coefficient to ≥ 2^128), and an exact proof behind every ErrOverflow.
func FuzzDiv(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		checkDiv(t, newCrossVal(a), newCrossVal(b))
	})
}

// FuzzQuoRem runs the cross-check T-division oracle — q and r exact against
// shopspring's QuoRem at precision 0, the d == q·e + r identity in shopspring
// arithmetic, the divisor-alignment overflow proof — plus the strict
// remainder magnitude bound |r| < |b|.
func FuzzQuoRem(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		av, bv := newCrossVal(a), newCrossVal(b)
		checkQuoRem(t, av, bv)
		if _, r, err := a.QuoRem(b); err == nil {
			require.Truef(t, ssOf(r).Abs().LessThan(bv.ss.Abs()),
				"remainder magnitude bound |r| < |b|: a=%+v b=%+v r=%+v", a, b, r)
		}
	})
}

// FuzzMod cross-checks Mod standalone: ErrDivideByZero on zero divisors, the
// divisor-alignment iff overflow oracle, shopspring's precision-0 remainder
// on success, the dividend's sign, and the contracted remainder precision.
func FuzzMod(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		m, err := a.Mod(b)
		if b.IsZero() {
			require.ErrorIsf(t, err, ErrDivideByZero, "mod by zero: a=%+v", a)
			return
		}
		fp := max(a.prec, b.prec)
		num := new(big.Int).Mul(u128ToBig(a.coef), bp10(int(fp-a.prec)))
		den := new(big.Int).Mul(u128ToBig(b.coef), bp10(int(fp-b.prec)))
		if den.Cmp(mod128big) >= 0 || new(big.Int).Quo(num, den).Cmp(mod128big) >= 0 {
			require.ErrorIsf(t, err, ErrOverflow, "mod overflow oracle: a=%+v b=%+v", a, b)
			return
		}
		require.NoErrorf(t, err, "mod: a=%+v b=%+v", a, b)
		_, ssR := ssOf(a).QuoRem(ssOf(b), 0)
		requireSameValue(t, ssR, m, "mod", a, b)
		require.Truef(t, m.IsZero() || m.IsNegative() == a.IsNegative(),
			"remainder sign follows the dividend: a=%+v m=%+v", a, m)
		requireResultPrec(t, m, fp, "mod", a, b)
	})
}

// FuzzCmp cross-checks Cmp against shopspring plus the structural laws the
// predicate family must satisfy: antisymmetry, Equal/LessThan/GreaterThan and
// the OrEqual forms pinned to the single Cmp result, and reflexivity.
func FuzzCmp(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		checkCmp(t, newCrossVal(a), newCrossVal(b))
		require.Zerof(t, a.Cmp(a), "cmp self must be zero: a=%+v", a)
		require.Zerof(t, b.Cmp(b), "cmp self must be zero: b=%+v", b)
		require.Truef(t, a.Equal(a), "equal self: a=%+v", a)
	})
}

// fuzzRoundOracle is the shared body of the rounding-mode targets: the value
// under test is the widened pair product and the zerodecimal mode must match
// the shopspring method with the same documented semantics at every fuzzed
// places 0..19.
func fuzzRoundOracle(f *testing.F, name string, zdOp func(Decimal, uint8) Decimal, ssOp func(decimal.Decimal, int32) decimal.Decimal) {
	fuzzPairsPlaces(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8, pc uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		c := fuzzProduct(t, a, b)
		places := pc % 20
		requireSameValue(t, ssOp(ssOf(c), int32(places)), zdOp(c, places), name, places, c)
	})
}

// FuzzRound cross-checks Round against shopspring's Round — both round half
// away from zero.
func FuzzRound(f *testing.F) {
	fuzzRoundOracle(f, "round", Decimal.Round, decimal.Decimal.Round)
}

// FuzzRoundBank cross-checks RoundBank against shopspring's RoundBank — both
// round ties to even.
func FuzzRoundBank(f *testing.F) {
	fuzzRoundOracle(f, "round_bank", Decimal.RoundBank, decimal.Decimal.RoundBank)
}

// FuzzRoundUp cross-checks RoundUp against shopspring's RoundUp — both step
// any nonzero remainder away from zero (NOT toward +∞).
func FuzzRoundUp(f *testing.F) {
	fuzzRoundOracle(f, "round_up", Decimal.RoundUp, decimal.Decimal.RoundUp)
}

// FuzzRoundDown cross-checks RoundDown against shopspring's RoundDown — both
// drop the excess digits toward zero.
func FuzzRoundDown(f *testing.F) {
	fuzzRoundOracle(f, "round_down", Decimal.RoundDown, decimal.Decimal.RoundDown)
}

// FuzzRoundCeil cross-checks RoundCeil against shopspring's RoundCeil — both
// round toward +∞.
func FuzzRoundCeil(f *testing.F) {
	fuzzRoundOracle(f, "round_ceil", Decimal.RoundCeil, decimal.Decimal.RoundCeil)
}

// FuzzRoundFloor cross-checks RoundFloor against shopspring's RoundFloor —
// both round toward -∞.
func FuzzRoundFloor(f *testing.F) {
	fuzzRoundOracle(f, "round_floor", Decimal.RoundFloor, decimal.Decimal.RoundFloor)
}

// FuzzTruncate cross-checks Truncate against shopspring's Truncate — both
// drop digits toward zero, identical to the RoundDown pair by construction.
func FuzzTruncate(f *testing.F) {
	fuzzRoundOracle(f, "truncate", Decimal.Truncate, decimal.Decimal.Truncate)
}

// FuzzStringFixed pins StringFixed to shopspring's StringFixed byte for
// byte — both round half away from zero and zero-pad to exactly places
// fractional digits — and requires the fixed rendering to reparse to the
// rounded value.
func FuzzStringFixed(f *testing.F) {
	fuzzPairsPlaces(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8, pc uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		c := fuzzProduct(t, a, b)
		places := pc % 20
		got := c.StringFixed(places)
		require.Equalf(t, ssOf(c).StringFixed(int32(places)), got, "string_fixed: c=%+v places=%d", c, places)
		// Trunc mode: the fixed padding can push the written coefficient past
		// 39 significant digits, which strict parsing deliberately rejects;
		// only padding zeros are ever dropped, so the value stays exact.
		reparsed, err := NewFromStringTrunc(got)
		require.NoErrorf(t, err, "string_fixed output must trunc-reparse: %q", got)
		require.Zerof(t, c.Round(places).Cmp(reparsed), "string_fixed reparse: c=%+v places=%d", c, places)
	})
}

// FuzzJSONRoundTrip checks MarshalJSON → UnmarshalJSON value identity and
// that the payload is exactly one quoted canonical literal whose unquoted
// body shopspring reads as the same number.
func FuzzJSONRoundTrip(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		c := fuzzProduct(t, a, b)
		data, err := c.MarshalJSON()
		require.NoErrorf(t, err, "marshal json: c=%+v", c)
		var e Decimal
		require.NoErrorf(t, e.UnmarshalJSON(data), "unmarshal own output %q", data)
		require.Truef(t, c.Equal(e), "json round trip: c=%+v e=%+v", c, e)
		require.GreaterOrEqualf(t, len(data), 3, "payload is at least one quoted digit: %q", data)
		require.Equalf(t, byte('"'), data[0], "payload must be quoted: %q", data)
		require.Equalf(t, byte('"'), data[len(data)-1], "payload must be quoted: %q", data)
		ssParsed, ssErr := decimal.NewFromString(string(data[1 : len(data)-1]))
		require.NoErrorf(t, ssErr, "shopspring must parse the unquoted payload %q", data)
		require.Truef(t, ssOf(c).Equal(ssParsed), "json payload value: c=%+v data=%q", c, data)
	})
}

// FuzzBinaryRoundTrip checks the binary codec on raw representations
// (trailing fractional zeros intact, where the wire format must be exact):
// MarshalBinary → UnmarshalBinary reproduces the identical struct — operands
// are zero-normalized at construction — and AppendBinary(nil) emits the same
// bytes as MarshalBinary.
func FuzzBinaryRoundTrip(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		for _, d := range []Decimal{a, b, fuzzProduct(t, a, b)} {
			data, err := d.MarshalBinary()
			require.NoErrorf(t, err, "marshal binary: d=%+v", d)
			appended, err := d.AppendBinary(nil)
			require.NoErrorf(t, err, "append binary: d=%+v", d)
			require.Equalf(t, data, appended, "append_binary must equal marshal_binary: d=%+v", d)
			var e Decimal
			require.NoErrorf(t, e.UnmarshalBinary(data), "unmarshal own encoding: d=%+v", d)
			require.Equalf(t, d, e, "binary round trip is representation-exact: d=%+v", d)
		}
	})
}

// FuzzBinaryGarbage feeds arbitrary bytes to UnmarshalBinary: it must never
// panic, every rejection is the bare ErrInvalidBinaryData sentinel leaving
// the receiver unchanged, and every accepted payload re-marshals to an
// encoding that decodes back to the same Decimal.
func FuzzBinaryGarbage(f *testing.F) {
	for _, s := range fuzzCorpus {
		if d, err := NewFromHiLo(s.neg, s.hi, s.lo, s.prec); err == nil {
			if data, err := d.MarshalBinary(); err == nil {
				f.Add(data)
			}
		}
	}
	for _, raw := range [][]byte{
		{},
		{0x00},
		{0x00, 0x00},
		{0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 0}, // canonical zero
		{0x04, 0x00, 0, 0, 0, 0, 0, 0, 0, 1}, // reserved flag bit set
		{0x00, 0x14, 0, 0, 0, 0, 0, 0, 0, 1}, // prec 20 > MaxPrec
		{0x02, 0x00, 0, 0, 0, 0, 0, 0, 0, 1}, // hi flag without hi limb bytes
		{0x00, 0x00, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 1},                                 // 18 bytes without hi flag
		{0x02, 0x00, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0},                                 // hi flag with zero hi limb
		{0x03, 0x13, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255}, // -max at prec 19
	} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		var d Decimal
		if err := d.UnmarshalBinary(raw); err != nil {
			require.ErrorIsf(t, err, ErrInvalidBinaryData, "decode rejects only with the sentinel: % x", raw)
			require.Equalf(t, Decimal{}, d, "failed decode must leave the receiver unchanged: % x", raw)
			return
		}
		require.LessOrEqualf(t, d.Prec(), MaxPrec, "decoded precision must be valid: % x", raw)
		data, err := d.MarshalBinary()
		require.NoErrorf(t, err, "re-marshal accepted input: % x", raw)
		var e Decimal
		require.NoErrorf(t, e.UnmarshalBinary(data), "re-marshaled encoding must decode: % x", raw)
		require.Equalf(t, d, e, "accepted input must re-marshal stably: % x", raw)
	})
}

// FuzzSQLRoundTrip checks Value → Scan identity: the driver value is always
// the canonical string, and scanning it back — as string and as []byte —
// yields an equal Decimal.
func FuzzSQLRoundTrip(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		for _, d := range []Decimal{a, b, fuzzProduct(t, a, b)} {
			v, err := d.Value()
			require.NoErrorf(t, err, "value: d=%+v", d)
			s, ok := v.(string)
			require.Truef(t, ok, "driver value must be a string: d=%+v got %T", d, v)
			require.Equalf(t, d.String(), s, "driver value must be the canonical string: d=%+v", d)
			var e Decimal
			require.NoErrorf(t, e.Scan(v), "scan string: d=%+v", d)
			require.Truef(t, d.Equal(e), "sql round trip via string: d=%+v e=%+v", d, e)
			var eb Decimal
			require.NoErrorf(t, eb.Scan([]byte(s)), "scan bytes: d=%+v", d)
			require.Truef(t, d.Equal(eb), "sql round trip via bytes: d=%+v eb=%+v", d, eb)
		}
	})
}

// FuzzFloat64 checks the NewFromFloat domain contract: NaN and infinities
// are ErrInvalidFloat, every other rejection is one of the documented domain
// guards (ErrOverflow past 2^128, ErrPrecOutOfRange below the smallest unit
// or past MaxPrec fractional digits), and every success equals shopspring's
// reading of the float's shortest 'f'-form — the exact same digits the
// constructor parses.
func FuzzFloat64(f *testing.F) {
	for _, v := range []float64{
		0, math.Copysign(0, -1), 1, -1, 0.1, -0.1, 1.5,
		math.MaxFloat64, -math.MaxFloat64, math.SmallestNonzeroFloat64,
		math.NaN(), math.Inf(1), math.Inf(-1),
		1e19, -1e19, 0x1p127, 0x1p128, 3.4e38, 1e-19, 1e-20, -123.456,
	} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, v float64) {
		d, err := NewFromFloat(v)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			require.ErrorIsf(t, err, ErrInvalidFloat, "non-finite input: %v", v)
			return
		}
		if err != nil {
			require.Truef(t, errors.Is(err, ErrOverflow) || errors.Is(err, ErrPrecOutOfRange),
				"only the domain guards may reject a finite float: %g -> %v", v, err)
			return
		}
		ssV, ssErr := decimal.NewFromString(strconv.FormatFloat(v, 'f', -1, 64))
		require.NoErrorf(t, ssErr, "shopspring must parse the shortest form of %g", v)
		require.Truef(t, ssV.Equal(ssOf(d)), "float value vs shopspring: %g -> %s vs %s", v, d, ssV)
	})
}

// FuzzInvariants checks the structural contract on every Decimal any
// operation produces: a zero value is exactly the canonical Decimal{},
// precision never exceeds MaxPrec, and the canonical string reparses to the
// same value and the same string. Operation errors are ignored here — their
// exactness is each dedicated target's job; only produced values matter.
func FuzzInvariants(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		places := b.Prec()
		results := []Decimal{
			a, b, a.Neg(), b.Neg(), a.Abs(), b.Abs(),
			a.Floor(), a.Ceil(), b.Floor(), b.Ceil(),
			a.Round(places), a.RoundBank(places), a.RoundUp(places),
			a.RoundDown(places), a.RoundCeil(places), a.RoundFloor(places),
			a.Truncate(places),
		}
		if c, err := a.Add(b); err == nil {
			results = append(results, c)
		}
		if c, err := a.Sub(b); err == nil {
			results = append(results, c)
		}
		if c, err := a.Mul(b); err == nil {
			results = append(results, c)
		}
		if c, err := a.Div(b); err == nil {
			results = append(results, c)
		}
		if q, r, err := a.QuoRem(b); err == nil {
			results = append(results, q, r)
		}
		if m, err := a.Mod(b); err == nil {
			results = append(results, m)
		}
		for _, d := range results {
			if d.Sign() == 0 {
				require.Equalf(t, Decimal{}, d, "zero must be canonical: d=%+v a=%+v b=%+v", d, a, b)
			}
			require.LessOrEqualf(t, d.Prec(), MaxPrec, "precision bound: d=%+v a=%+v b=%+v", d, a, b)
			s := d.String()
			reparsed, err := NewFromString(s)
			require.NoErrorf(t, err, "canonical string must reparse: %q from d=%+v", s, d)
			require.Zerof(t, d.Cmp(reparsed), "reparse preserves the value: %q d=%+v", s, d)
			require.Equalf(t, s, reparsed.String(), "string fixed point: %q d=%+v", s, d)
		}
	})
}
