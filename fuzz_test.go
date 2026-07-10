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
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
	"time"

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

// fuzzTriples seeds f with corpus pairs plus a third quadruple chosen by a
// rotating index, so every boundary shape appears in every operand slot
// without cubing the seed count.
func fuzzTriples(f *testing.F) {
	for i, a := range fuzzCorpus {
		for j, b := range fuzzCorpus {
			c := fuzzCorpus[(i+j)%len(fuzzCorpus)]
			f.Add(a.neg, a.hi, a.lo, a.prec, b.neg, b.hi, b.lo, b.prec, c.neg, c.hi, c.lo, c.prec)
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

// fuzzPanicValue runs fn and returns the recovered panic value, nil when fn
// returned normally. Library panics always carry a sentinel error, never nil,
// so nil is unambiguous.
func fuzzPanicValue(fn func()) (pv any) {
	defer func() { pv = recover() }()
	fn()
	return nil
}

// requireTwin asserts the Must-twin contract: the twin panicked exactly when
// the erroring form failed, and the panic value carries that same sentinel.
func requireTwin(t *testing.T, name string, wantErr error, pv any, ctx ...any) {
	t.Helper()
	if wantErr == nil {
		require.Nilf(t, pv, "%s: must twin panicked on success: pv=%v ctx=%+v", name, pv, ctx)
		return
	}
	require.NotNilf(t, pv, "%s: must twin must panic on %v: ctx=%+v", name, wantErr, ctx)
	perr, ok := pv.(error)
	require.Truef(t, ok, "%s: panic value must be an error, got %T: ctx=%+v", name, pv, ctx)
	require.ErrorIsf(t, perr, wantErr, "%s: panic value must carry the twin's sentinel: ctx=%+v", name, ctx)
}

// requireParseSentinel asserts err is one of the bare parse sentinels the
// string decoders document.
func requireParseSentinel(t *testing.T, err error, ctx ...any) {
	t.Helper()
	for _, s := range []error{ErrEmptyString, ErrMaxStrLen, ErrInvalidFormat, ErrOverflow, ErrPrecOutOfRange} {
		if errors.Is(err, s) {
			return
		}
	}
	require.Failf(t, "unexpected parse error", "err=%v ctx=%+v", err, ctx)
}

// requireParsedValue cross-checks a successful parse of raw against
// shopspring with FuzzParseString's documented tolerances: shopspring's int32
// exponent may reject the huge exponents our saturating parser folds into
// exact zeros, and zeros carrying huge-but-int32 exponents compare by
// zeroness so shopspring's Equal never rescales by an astronomic 10^diff.
func requireParsedValue(t *testing.T, raw string, d Decimal, ctx ...any) {
	t.Helper()
	ssV, ssErr := decimal.NewFromString(raw)
	switch {
	case ssErr != nil:
		require.Truef(t, d.IsZero(), "shopspring rejected %q yet we parsed nonzero %+v ctx=%+v", raw, d, ctx)
	case d.IsZero() || ssV.IsZero():
		require.Equalf(t, d.IsZero(), ssV.IsZero(), "zeroness vs shopspring: %q -> %s vs %s ctx=%+v", raw, d, ssV, ctx)
	default:
		require.Truef(t, ssV.Equal(ssOf(d)), "parse value vs shopspring: %q -> %s vs %s ctx=%+v", raw, d, ssV, ctx)
	}
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
		"35000000000000000000e20",
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

// FuzzParseGeneralAgree pins the parser's specialized paths — the one-limb
// fast path and the plain-literal long path — to parseGeneral, the
// full-grammar reference: every input must yield the identical
// (Decimal, error) pair through parseCore and through forcedGeneral, in both
// strict and truncating modes and for both element types. This is the
// differential gate for path duplication: any divergence in an
// accept/reject/value decision fails before the oracle suites ever see it.
func FuzzParseGeneralAgree(f *testing.F) {
	for _, s := range parseAgreeSeeds {
		f.Add(s)
	}
	for _, s := range []string{
		"1x", "1e", "1e+", "1e5x", "1e40", "1e-45",
		"40000000000000000000e20",
		"0." + strings.Repeat("9", 59),
		strings.Repeat("9", 45) + "e2",
		strings.Repeat("9", 39) + "000000" + ".5",
		"34028236692093846346337460743176821145000.5",
		strings.Repeat("1", maxParseLen+1),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		for _, trunc := range []bool{false, true} {
			want, wantErr := forcedGeneral(raw, trunc)
			got, gotErr := parseCore(raw, trunc)
			require.Equalf(t, wantErr, gotErr, "error vs parseGeneral: %q trunc=%v", raw, trunc)
			require.Equalf(t, want, got, "value vs parseGeneral: %q trunc=%v", raw, trunc)
			gotB, gotBErr := parseCore([]byte(raw), trunc)
			require.Equalf(t, wantErr, gotBErr, "[]byte error vs parseGeneral: %q trunc=%v", raw, trunc)
			require.Equalf(t, want, gotB, "[]byte value vs parseGeneral: %q trunc=%v", raw, trunc)
			pb, pbErr := ParseBytes([]byte(raw))
			if trunc {
				pb, pbErr = ParseBytesTrunc([]byte(raw))
			}
			require.Equalf(t, wantErr, pbErr, "ParseBytes error vs parseGeneral: %q trunc=%v", raw, trunc)
			require.Equalf(t, want, pb, "ParseBytes value vs parseGeneral: %q trunc=%v", raw, trunc)
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
// integer-quotient iff overflow oracle (a wide aligned divisor instead proves
// a zero quotient), shopspring's precision-0 remainder on success, the
// dividend's sign, and the contracted remainder precision.
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
		if new(big.Int).Quo(num, den).Cmp(mod128big) >= 0 {
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
		aCopy, bCopy := a, b
		require.Zerof(t, a.Cmp(aCopy), "cmp self must be zero: a=%+v", a)
		require.Zerof(t, b.Cmp(bCopy), "cmp self must be zero: b=%+v", b)
		require.Truef(t, a.Equal(aCopy), "equal self must hold: a=%+v", a)
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
// rounded value. Places range over 0..45, past MaxPrec, so paddings longer
// than one zeroRun block are exercised while the rendering stays well under
// the parser's length limit for the reparse leg.
func FuzzStringFixed(f *testing.F) {
	fuzzPairsPlaces(f)
	f.Add(false, uint64(0), uint64(5), uint8(1), false, uint64(0), uint64(2), uint8(0), uint8(40))
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8, pc uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		c := fuzzProduct(t, a, b)
		// Full uint8 range, not pc%20: places past 32+prec are the only inputs
		// that drive AppendFixed's whole-block zeroRun padding loop, and
		// shopspring's StringFixed(int32) matches byte for byte at every places
		// up to 255.
		places := pc
		got := c.StringFixed(places)
		require.Equalf(t, ssOf(c).StringFixed(int32(places)), got, "string_fixed: c=%+v places=%d", c, places)
		// Trunc mode: the fixed padding can push the written coefficient past
		// 39 significant digits, which strict parsing deliberately rejects;
		// only padding zeros are ever dropped, so the value stays exact. Past
		// maxParseLen the whole-block padding overruns the parser's documented
		// input cap — the single reason a fixed output fails to reparse — so
		// assert the exact sentinel there, leaving a formatting defect to still
		// fail loudly.
		reparsed, err := NewFromStringTrunc(got)
		if len(got) > maxParseLen {
			require.ErrorIsf(t, err, ErrMaxStrLen, "over-long fixed output rejects only on length: %q", got)
			return
		}
		require.NoErrorf(t, err, "string_fixed output must trunc-reparse: %q", got)
		require.Zerof(t, c.Round(places).Cmp(reparsed), "string_fixed reparse: c=%+v places=%d", c, places)
	})
}

// FuzzTrim checks the canonicalizer on raw representations (trailing
// fractional zeros intact, exactly what Trim exists to strip): the trimmed
// value must equal shopspring's reading of the original, trimming must be
// idempotent, a trimmed nonzero coefficient behind a nonzero precision must
// not divide by ten, and representations of the same number must trim to the
// identical — ==-comparable — Decimal.
func FuzzTrim(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		for _, d := range []Decimal{a, b, fuzzProduct(t, a, b)} {
			got := d.Trim()
			require.Truef(t, ssOf(d).Equal(ssOf(got)), "trim preserves the value: d=%+v got=%+v", d, got)
			require.Equalf(t, got, got.Trim(), "trim idempotence: d=%+v", d)
			if got.IsZero() {
				require.Equalf(t, Decimal{}, got, "trim zero must be canonical: d=%+v", d)
			} else if got.Prec() > 0 {
				_, r := divmod128Pow10(got.coef, 1)
				require.NotZerof(t, r, "trim must reach the minimal precision: d=%+v got=%+v", d, got)
			}
			if d.Prec() < MaxPrec {
				if widened, over := mul128by64(d.coef, 10); over == 0 {
					e := newDecimal(widened, d.neg, d.Prec()+1)
					require.Equalf(t, got, e.Trim(), "equal values must trim to one representation: d=%+v e=%+v", d, e)
				}
			}
		}
	})
}

// FuzzRescale checks both rescaling directions at every fuzzed prec 0..19
// plus the out-of-range row at 20: lowering must be bit-identical to
// RoundBank, raising must preserve the value at exactly the requested
// precision with an iff big.Int overflow oracle, and prec > MaxPrec must be
// ErrPrecOutOfRange.
func FuzzRescale(f *testing.F) {
	fuzzPairsPlaces(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8, pc uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		prec := pc % 21 // 0..20: one past MaxPrec covers the range guard
		for _, d := range []Decimal{a, b, fuzzProduct(t, a, b)} {
			got, err := d.Rescale(prec)
			switch {
			case prec > MaxPrec:
				require.ErrorIsf(t, err, ErrPrecOutOfRange, "prec %d is out of range: d=%+v", prec, d)
			case prec < d.Prec():
				require.NoErrorf(t, err, "rescale lower: d=%+v prec=%d", d, prec)
				require.Equalf(t, d.RoundBank(prec), got, "rescale lowering must equal RoundBank: d=%+v prec=%d", d, prec)
			default:
				exact := new(big.Int).Mul(u128ToBig(d.coef), bp10(int(prec-d.Prec())))
				if exact.Cmp(mod128big) >= 0 {
					require.ErrorIsf(t, err, ErrOverflow, "rescale raise overflow oracle: d=%+v prec=%d", d, prec)
					continue
				}
				require.NoErrorf(t, err, "rescale raise: d=%+v prec=%d", d, prec)
				require.Truef(t, ssOf(d).Equal(ssOf(got)), "raising preserves the value: d=%+v got=%+v", d, got)
				requireResultPrec(t, got, prec, "rescale_raise", d, prec)
			}
		}
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
		0x1p55, 1.5e-19,
	} {
		f.Add(v)
	}
	// Dragonbox trigger set: bit patterns landing on the shorter-interval,
	// tie-parity, and round-up arms of dboxShortest64, plus every power of two
	// in the guarded domain (the mant == 2^52 closer-left-endpoint branch).
	for _, b := range []uint64{
		0x435d1c47aedaaacb, 0x431eb7fcd82760ed, 0xc35587d2a7851bef,
		0xc1acd9f551180278, 0x41f27cc6f3875d04, 0x3c04951aa42655d9,
		0xc3ea3b9393f93f33, 0x3c00000000000000, 0x3c10000000000000,
		0x3e60000000000000, 0x4350000000000000, 0x4580000000000000,
		0x435f35b70dbc0e24, 0xc36f5111960b7b8c, 0x43f751d1932c8114,
	} {
		f.Add(math.Float64frombits(b))
	}
	for n := -63; n <= 127; n++ {
		f.Add(math.Ldexp(1, n))
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
			a.Truncate(places), a.Trim(), b.Trim(),
		}
		if c, err := a.Add(b); err == nil {
			results = append(results, c)
		}
		if c, err := a.Rescale(places); err == nil {
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

// FuzzFloat32 mirrors FuzzFloat64's domain contract for NewFromFloat32: NaN
// and infinities are ErrInvalidFloat, any other rejection is a documented
// domain guard, and every success equals shopspring's reading of the float's
// shortest 32-bit 'f'-form — the exact digits the constructor generates.
func FuzzFloat32(f *testing.F) {
	for _, v := range []float32{
		0, float32(math.Copysign(0, -1)), 1, -1, 0.1, -0.1, 1.5,
		math.MaxFloat32, -math.MaxFloat32, math.SmallestNonzeroFloat32,
		float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1)),
		1e19, -1e19, 3.4e38, 1e-19, 1e-20, -123.456, 1.5e-19,
	} {
		f.Add(v)
	}
	// Dragonbox trigger set for the 32-bit core: bit patterns landing on the
	// shorter-interval, tie-parity, and round-up arms of dboxShortest32, plus
	// every float32 power of two in the guarded domain (the mant == 2^23
	// closer-left-endpoint branch).
	for _, b := range []uint32{
		0x20000000, 0x20800000, 0x4c000000, 0x4c800000, 0x6b000000,
		0x5f3f164f, 0x392907a0, 0x3f71f8cb, 0x4c330f1d, 0x4d49461f,
		0x49c6e2d1,
	} {
		f.Add(math.Float32frombits(b))
	}
	for n := -63; n <= 127; n++ {
		f.Add(float32(math.Ldexp(1, n)))
	}
	f.Fuzz(func(t *testing.T, v float32) {
		d, err := NewFromFloat32(v)
		v64 := float64(v)
		if math.IsNaN(v64) || math.IsInf(v64, 0) {
			require.ErrorIsf(t, err, ErrInvalidFloat, "non-finite input: %v", v)
			return
		}
		if err != nil {
			require.Truef(t, errors.Is(err, ErrOverflow) || errors.Is(err, ErrPrecOutOfRange),
				"only the domain guards may reject a finite float: %g -> %v", v, err)
			return
		}
		ssV, ssErr := decimal.NewFromString(strconv.FormatFloat(v64, 'f', -1, 32))
		require.NoErrorf(t, ssErr, "shopspring must parse the shortest form of %g", v)
		require.Truef(t, ssV.Equal(ssOf(d)), "float32 value vs shopspring: %g -> %s vs %s", v, d, ssV)
	})
}

// FuzzMustTwins pins every Must method to its erroring twin: the Must form
// panics exactly when the twin errors, the panic value carries the twin's
// sentinel (errors.Is), and successful results are representation-identical.
// The corpus already crosses the overflow rows (max×max) and the zero-divisor
// rows, so both panic arms replay from seeds alone.
func FuzzMustTwins(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		for _, op := range []struct {
			name string
			op   func(Decimal, Decimal) (Decimal, error)
			must func(Decimal, Decimal) Decimal
		}{
			{"add", Decimal.Add, Decimal.MustAdd},
			{"sub", Decimal.Sub, Decimal.MustSub},
			{"mul", Decimal.Mul, Decimal.MustMul},
			{"div", Decimal.Div, Decimal.MustDiv},
			{"mod", Decimal.Mod, Decimal.MustMod},
		} {
			want, wantErr := op.op(a, b)
			var got Decimal
			pv := fuzzPanicValue(func() { got = op.must(a, b) })
			requireTwin(t, op.name, wantErr, pv, a, b)
			if wantErr == nil {
				require.Equalf(t, want, got, "%s: must twin result: a=%+v b=%+v", op.name, a, b)
			}
		}
		wantQ, wantR, wantErr := a.QuoRem(b)
		var gotQ, gotR Decimal
		pv := fuzzPanicValue(func() { gotQ, gotR = a.MustQuoRem(b) })
		requireTwin(t, "quo_rem", wantErr, pv, a, b)
		if wantErr == nil {
			require.Equalf(t, wantQ, gotQ, "quo_rem: must twin quotient: a=%+v b=%+v", a, b)
			require.Equalf(t, wantR, gotR, "quo_rem: must twin remainder: a=%+v b=%+v", a, b)
		}
	})
}

// FuzzAggregates cross-checks the variadic helpers over three operands.
// Min/Max must return one of their inputs verbatim, bound every input, and
// match shopspring's fold. Sum uses one exact signed coefficient at the
// greatest input precision: ErrOverflow is determined only by that final
// coefficient, so arbitrary-width partial sums may cancel. Avg divides the
// same wide total before narrowing and is checked at its adaptive precision.
// MustSum/MustAvg follow the Must-twin contract.
func FuzzAggregates(f *testing.F) {
	fuzzTriples(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8, cneg bool, chi, clo uint64, cprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		c := fuzzDecimal(t, cneg, chi, clo, cprec)
		xs := []Decimal{a, b, c}

		gotMin, gotMax := Min(a, b, c), Max(a, b, c)
		require.Truef(t, gotMin == a || gotMin == b || gotMin == c, "min must return one of its inputs: %+v xs=%+v", gotMin, xs)
		require.Truef(t, gotMax == a || gotMax == b || gotMax == c, "max must return one of its inputs: %+v xs=%+v", gotMax, xs)
		for _, x := range xs {
			require.LessOrEqualf(t, gotMin.Cmp(x), 0, "min bounds every input: min=%+v x=%+v", gotMin, x)
			require.GreaterOrEqualf(t, gotMax.Cmp(x), 0, "max bounds every input: max=%+v x=%+v", gotMax, x)
		}
		require.Truef(t, decimal.Min(ssOf(a), ssOf(b), ssOf(c)).Equal(ssOf(gotMin)), "min vs shopspring: xs=%+v", xs)
		require.Truef(t, decimal.Max(ssOf(a), ssOf(b), ssOf(c)).Equal(ssOf(gotMax)), "max vs shopspring: xs=%+v", xs)

		// Final-result oracle: all operands align once to the greatest input
		// precision and sum in arbitrary width. Only the final magnitude may
		// trigger ErrOverflow.
		sumBig, sumPrec := aggregateOracleTotal(xs)
		sumOverflow := sumBig.BitLen() > 128
		gotSum, sumErr := Sum(a, b, c)
		var mustSum Decimal
		pv := fuzzPanicValue(func() { mustSum = MustSum(a, b, c) })
		requireTwin(t, "must_sum", sumErr, pv, xs)
		if sumOverflow {
			require.ErrorIsf(t, sumErr, ErrOverflow, "sum overflow oracle: xs=%+v", xs)
		} else {
			require.NoErrorf(t, sumErr, "sum: xs=%+v", xs)
			require.Equalf(t, gotSum, mustSum, "must_sum result: xs=%+v", xs)
			requireSameValue(t, ssOf(a).Add(ssOf(b)).Add(ssOf(c)), gotSum, "sum", xs)
			requireResultPrec(t, gotSum, sumPrec, "sum", xs)
		}

		gotAvg, avgErr := Avg(a, b, c)
		var mustAvg Decimal
		pv = fuzzPanicValue(func() { mustAvg = MustAvg(a, b, c) })
		requireTwin(t, "must_avg", avgErr, pv, xs)
		require.NoErrorf(t, avgErr, "avg: xs=%+v", xs)
		require.Equalf(t, gotAvg, mustAvg, "must_avg result: xs=%+v", xs)
		requireAggregateAvgOracle(t, xs)
		wantAvg, _ := ssOf(a).Add(ssOf(b)).Add(ssOf(c)).QuoRem(decimal.NewFromInt(3), int32(gotAvg.prec))
		require.Truef(t, wantAvg.Equal(ssOf(gotAvg)), "avg vs shopspring truncation: xs=%+v", xs)
	})
}

// FuzzConstructors cross-checks the remaining constructors and accessors
// against shopspring and big.Int. New shares decimal.New's exact signature
// and semantics, with iff oracles for both error arms; NewFromHiLo takes the
// RAW precision byte so the prec > MaxPrec rejection is reachable; IntPart is
// checked against the big.Int truncation with exact int64 bounds (MinInt64
// round-trips); the Must/Require twins follow the twin contract.
func FuzzConstructors(f *testing.F) {
	f.Add(int64(0), int32(0), uint64(0), uint64(0), uint8(0), uint64(0))
	f.Add(int64(1), int32(39), uint64(0), uint64(0), uint8(0), uint64(0))
	f.Add(int64(5), int32(38), uint64(0), uint64(1), uint8(20), uint64(1))
	f.Add(int64(1), int32(38), uint64(math.MaxUint64), uint64(math.MaxUint64), uint8(19), uint64(2))
	f.Add(int64(1), int32(-25), uint64(0), uint64(100), uint8(2), uint64(3))
	f.Add(int64(1000000), int32(-25), uint64(0), uint64(0), uint8(0), uint64(0))
	f.Add(int64(math.MinInt64), int32(0), uint64(0), uint64(1)<<63, uint8(0), uint64(1))
	f.Add(int64(1), int32(math.MinInt32), uint64(1), uint64(0), uint8(0), uint64(0))
	f.Add(int64(-7), int32(-3), uint64(123), uint64(456), uint8(7), uint64(1))
	f.Add(int64(math.MaxInt64), int32(2), uint64(0), uint64(math.MaxUint64), uint8(19), uint64(0))
	f.Add(int64(2), int32(0), uint64(0), uint64(math.MaxUint64), uint8(0), uint64(1))
	f.Add(int64(3), int32(0), uint64(0), uint64(1)<<63, uint8(0), uint64(2))
	f.Fuzz(func(t *testing.T, v int64, exp int32, hi, lo uint64, prec uint8, u uint64) {
		got, err := New(v, exp)
		var mustGot Decimal
		pv := fuzzPanicValue(func() { mustGot = MustNew(v, exp) })
		requireTwin(t, "must_new", err, pv, v, exp)
		if err == nil {
			require.Equalf(t, got, mustGot, "must_new result: v=%d exp=%d", v, exp)
		}
		mag := uint64(v)
		if v < 0 {
			mag = -mag
		}
		switch {
		case v == 0:
			require.NoErrorf(t, err, "new zero: exp=%d", exp)
			require.Equalf(t, Decimal{}, got, "New(0, exp) is canonical zero: exp=%d", exp)
		case exp >= 0:
			// ErrOverflow iff |v|*10^exp >= 2^128; exp > 38 always overflows
			// since 10^39 > 2^128.
			overflow := exp > 38
			if !overflow {
				exact := new(big.Int).Mul(new(big.Int).SetUint64(mag), bp10(int(exp)))
				overflow = exact.Cmp(mod128big) >= 0
			}
			if overflow {
				require.ErrorIsf(t, err, ErrOverflow, "new overflow oracle: v=%d exp=%d", v, exp)
				break
			}
			require.NoErrorf(t, err, "new: v=%d exp=%d", v, exp)
			require.Truef(t, decimal.New(v, exp).Equal(ssOf(got)), "new vs shopspring: v=%d exp=%d -> %s", v, exp, got)
			requireResultPrec(t, got, 0, "new", v, exp)
		default:
			// ErrPrecOutOfRange iff the scale still exceeds MaxPrec after the
			// documented exact stripping of trailing ten-factors from value.
			scale := -int64(exp)
			for scale > int64(MaxPrec) && mag%10 == 0 {
				mag /= 10
				scale--
			}
			if scale > int64(MaxPrec) {
				require.ErrorIsf(t, err, ErrPrecOutOfRange, "new precision oracle: v=%d exp=%d", v, exp)
				break
			}
			require.NoErrorf(t, err, "new: v=%d exp=%d", v, exp)
			require.Truef(t, decimal.New(v, exp).Equal(ssOf(got)), "new vs shopspring: v=%d exp=%d -> %s", v, exp, got)
			requireResultPrec(t, got, uint8(scale), "new", v, exp)
		}

		i32 := int32(v)
		d32 := NewFromInt32(i32)
		require.Truef(t, decimal.NewFromInt32(i32).Equal(ssOf(d32)), "new_from_int32 vs shopspring: %d -> %s", i32, d32)
		requireResultPrec(t, d32, 0, "new_from_int32", i32)
		du := NewFromUint64(u)
		require.Truef(t, decimal.NewFromUint64(u).Equal(ssOf(du)), "new_from_uint64 vs shopspring: %d -> %s", u, du)
		requireResultPrec(t, du, 0, "new_from_uint64", u)

		neg := u&1 == 1
		dh, hErr := NewFromHiLo(neg, hi, lo, prec)
		if prec > MaxPrec {
			require.ErrorIsf(t, hErr, ErrPrecOutOfRange, "new_from_hi_lo precision oracle: prec=%d", prec)
			require.Equalf(t, Decimal{}, dh, "new_from_hi_lo error result: prec=%d", prec)
		} else {
			require.NoErrorf(t, hErr, "new_from_hi_lo: hi=%d lo=%d prec=%d", hi, lo, prec)
			require.Truef(t, ssFromParts(neg, hi, lo, prec).Equal(ssOf(dh)),
				"new_from_hi_lo vs shopspring: neg=%v hi=%d lo=%d prec=%d", neg, hi, lo, prec)
		}

		d := fuzzDecimal(t, neg, hi, lo, prec)
		ipBig := new(big.Int).Quo(u128ToBig(d.coef), bp10(int(d.prec)))
		if d.neg {
			ipBig.Neg(ipBig)
		}
		ip, ipErr := d.IntPart()
		if ipBig.IsInt64() {
			require.NoErrorf(t, ipErr, "int_part: d=%+v", d)
			require.Equalf(t, ipBig.Int64(), ip, "int_part vs big.Int: d=%+v", d)
		} else {
			require.ErrorIsf(t, ipErr, ErrIntPartOverflow, "int_part overflow oracle: d=%+v", d)
		}
		require.Equalf(t, ssOf(d).Sign() == 1, d.IsPositive(), "is_positive vs shopspring sign: d=%+v", d)

		for _, s := range []string{d.String(), d.String() + "!"} {
			want, wantErr := NewFromString(s)
			var gotS Decimal
			pv = fuzzPanicValue(func() { gotS = RequireFromString(s) })
			requireTwin(t, "require_from_string", wantErr, pv, s)
			if wantErr == nil {
				require.Equalf(t, want, gotS, "require_from_string result: %q", s)
			}
		}
		fv := float64(v)
		wantF, wantFErr := NewFromFloat(fv)
		var gotF Decimal
		pv = fuzzPanicValue(func() { gotF = RequireFromFloat(fv) })
		requireTwin(t, "require_from_float", wantFErr, pv, fv)
		if wantFErr == nil {
			require.Equalf(t, wantF, gotF, "require_from_float result: %g", fv)
		}
		pv = fuzzPanicValue(func() { RequireFromFloat(math.NaN()) })
		requireTwin(t, "require_from_float_nan", ErrInvalidFloat, pv)
	})
}

// FuzzTextCodec checks the text marshalers on raw representations and the
// widened product: MarshalText and AppendText emit exactly the canonical
// String bytes (the corpus zero row replays the cached-string arm, the large
// rows the render arm), AppendText preserves its prefix, shopspring reads the
// text as the same value, and UnmarshalText round-trips it to a value-equal,
// string-identical Decimal.
func FuzzTextCodec(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		for _, d := range []Decimal{a, b, fuzzProduct(t, a, b)} {
			text, err := d.MarshalText()
			require.NoErrorf(t, err, "marshal text: d=%+v", d)
			require.Equalf(t, d.String(), string(text), "marshal_text must equal String: d=%+v", d)
			appended, err := d.AppendText(nil)
			require.NoErrorf(t, err, "append text: d=%+v", d)
			require.Equalf(t, string(text), string(appended), "append_text(nil) must equal marshal_text: d=%+v", d)
			prefixed, err := d.AppendText([]byte("x:"))
			require.NoErrorf(t, err, "append text with prefix: d=%+v", d)
			require.Equalf(t, "x:"+string(text), string(prefixed), "append_text must preserve its prefix: d=%+v", d)
			ssV, ssErr := decimal.NewFromString(string(text))
			require.NoErrorf(t, ssErr, "shopspring must parse the text form %q", text)
			require.Truef(t, ssV.Equal(ssOf(d)), "text value vs shopspring: d=%+v text=%q", d, text)
			var e Decimal
			require.NoErrorf(t, e.UnmarshalText(text), "unmarshal own output %q", text)
			require.Zerof(t, d.Cmp(e), "text round trip preserves the value: d=%+v e=%+v", d, e)
			require.Equalf(t, string(text), e.String(), "text round trip is a string fixed point: d=%+v", d)
		}
	})
}

// FuzzCodecGarbage feeds arbitrary bytes to every text-shaped decoder. For
// Decimal, a JSON null is an explicit ErrJSONNull rejection and every
// rejection leaves the receiver unchanged; for NullDecimal, null/empty clears
// to the invalid zero, decode errors leave it unchanged while Scan errors
// clear it, and every acceptance marks it valid, round-trips through its own
// marshaler, and agrees with shopspring.
func FuzzCodecGarbage(f *testing.F) {
	for _, s := range []string{
		"null", `"null"`, `"1.5"`, `"1\u002e5"`, `"\u002d1\u0045\u002b2"`, `"1\uD800"`,
		"1.5", "", "x", `"1.5`, `"15x`, "-0.000", "1e40",
		"0.00000000000000000001", strings.Repeat("1", maxParseLen+1),
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		marker := MustNew(424242, -2)

		d := marker
		err := d.UnmarshalJSON(raw)
		switch {
		case string(raw) == "null":
			require.ErrorIsf(t, err, ErrJSONNull, "json null must fail closed")
			require.Equalf(t, marker, d, "rejected json null must leave the receiver unchanged")
		case err != nil:
			requireParseSentinel(t, err, "unmarshal_json", raw)
			require.Equalf(t, marker, d, "failed unmarshal_json must leave the receiver unchanged: %q", raw)
		default:
			// Quoted inputs parse their decoded JSON string, not the encoded
			// source bytes. Unquoted successes use the strict decimal token as-is.
			semantic := string(raw)
			if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
				require.NoErrorf(t, json.Unmarshal(raw, &semantic), "accepted quoted JSON must be valid: %q", raw)
			}
			requireParsedValue(t, semantic, d, "unmarshal_json", raw)
		}

		dt := marker
		terr := dt.UnmarshalText(raw)
		if terr != nil {
			requireParseSentinel(t, terr, "unmarshal_text", raw)
			require.Equalf(t, marker, dt, "failed unmarshal_text must leave the receiver unchanged: %q", raw)
		} else {
			requireParsedValue(t, string(raw), dt, "unmarshal_text", raw)
		}

		n := NewNullDecimal(marker)
		require.Truef(t, n.Valid, "new_null_decimal must be valid")
		nerr := n.UnmarshalJSON(raw)
		switch {
		case string(raw) == "null":
			require.NoErrorf(t, nerr, "null decimal json null")
			require.Equalf(t, NullDecimal{}, n, "json null must clear to the invalid zero")
		case nerr != nil:
			require.Equalf(t, err, nerr, "null decimal must mirror Decimal.UnmarshalJSON's error: %q", raw)
			require.Equalf(t, NewNullDecimal(marker), n, "failed unmarshal_json must leave n unchanged: %q", raw)
		default:
			require.Truef(t, n.Valid, "accepted json must mark n valid: %q", raw)
			require.Equalf(t, d, n.Decimal, "null decimal must decode like Decimal: %q", raw)
			data, mErr := n.MarshalJSON()
			require.NoErrorf(t, mErr, "null decimal marshal_json: %+v", n)
			var n2 NullDecimal
			require.NoErrorf(t, n2.UnmarshalJSON(data), "null decimal json round trip: %q", data)
			require.Equalf(t, NewNullDecimal(n.Decimal), n2, "null decimal json round trip: %q", data)
		}

		nt := NewNullDecimal(marker)
		nterr := nt.UnmarshalText(raw)
		switch {
		case len(raw) == 0:
			require.NoErrorf(t, nterr, "null decimal empty text")
			require.Equalf(t, NullDecimal{}, nt, "empty text must clear to the invalid zero")
		case nterr != nil:
			require.Equalf(t, terr, nterr, "null decimal must mirror Decimal.UnmarshalText's error: %q", raw)
			require.Equalf(t, NewNullDecimal(marker), nt, "failed unmarshal_text must leave n unchanged: %q", raw)
		default:
			require.Truef(t, nt.Valid, "accepted text must mark n valid: %q", raw)
			require.Equalf(t, dt, nt.Decimal, "null decimal must decode text like Decimal: %q", raw)
			data, mErr := nt.MarshalText()
			require.NoErrorf(t, mErr, "null decimal marshal_text: %+v", nt)
			require.Equalf(t, nt.Decimal.String(), string(data), "null decimal text is the canonical string: %+v", nt)
			v, vErr := nt.Value()
			require.NoErrorf(t, vErr, "null decimal value: %+v", nt)
			require.Equalf(t, nt.Decimal.String(), v, "valid null decimal drives Decimal.Value: %+v", nt)
		}

		for _, src := range []any{string(raw), append([]byte(nil), raw...)} {
			ns := NewNullDecimal(marker)
			serr := ns.Scan(src)
			if serr != nil {
				requireParseSentinel(t, serr, "null_scan", raw)
				require.Equalf(t, NullDecimal{}, ns, "failed scan must clear n: %q", raw)
				continue
			}
			require.Truef(t, ns.Valid, "accepted scan must mark n valid: %q", raw)
			requireParsedValue(t, string(raw), ns.Decimal, "null_scan", raw)
		}

		var inv NullDecimal
		data, mErr := inv.MarshalJSON()
		require.NoError(t, mErr, "invalid null decimal marshal_json")
		require.Equal(t, jsonNull, string(data), "invalid null decimal renders the json null literal")
		data, mErr = inv.MarshalText()
		require.NoError(t, mErr, "invalid null decimal marshal_text")
		require.Empty(t, data, "invalid null decimal renders the empty string")
		v, vErr := inv.Value()
		require.NoError(t, vErr, "invalid null decimal value")
		require.Nil(t, v, "invalid null decimal drives SQL NULL")
		cleared := NewNullDecimal(marker)
		require.NoError(t, cleared.Scan(nil), "scanning SQL NULL into a null decimal")
		require.Equal(t, NullDecimal{}, cleared, "SQL NULL must clear to the invalid zero")
	})
}

// FuzzSQLScan drives Decimal.Scan and NullDecimal.Scan across every driver
// source type the switch dispatches on, plus the unsupported ones. Successes
// must equal shopspring's reading of the same source; ErrScanNil covers nil;
// unsupported types (bool, time.Time, any struct) error wrapping ErrScanType.
// Every error leaves a Decimal receiver unchanged and clears a NullDecimal.
func FuzzSQLScan(f *testing.F) {
	for sel := range uint8(11) {
		f.Add(sel, int64(-5), 1.5, []byte("1.25"))
	}
	f.Add(uint8(0), int64(math.MinInt64), math.NaN(), []byte("xyz"))
	f.Add(uint8(1), int64(-1), 0.5, []byte("1e40"))
	f.Add(uint8(6), int64(1), math.NaN(), []byte("0"))
	f.Add(uint8(6), int64(1), math.Inf(-1), []byte("0"))
	f.Add(uint8(6), int64(1), 1e-30, []byte("0"))
	f.Add(uint8(6), int64(1), 0x1p128, []byte("0"))
	f.Add(uint8(6), int64(1), -1e300, []byte("0"))
	f.Fuzz(func(t *testing.T, sel uint8, i int64, fv float64, s []byte) {
		var src any
		switch sel % 11 {
		case 0:
			src = string(s)
		case 1:
			src = append([]byte(nil), s...)
		case 2:
			src = i
		case 3:
			src = int32(i)
		case 4:
			src = int(i)
		case 5:
			src = uint64(i)
		case 6:
			src = fv
		case 7:
			src = true
		case 8:
			src = time.Time{}
		case 9:
			src = nil
		case 10:
			src = struct{}{}
		}
		marker := MustNew(31337, -3)
		d := marker
		err := d.Scan(src)
		switch v := src.(type) {
		case string:
			if err != nil {
				requireParseSentinel(t, err, "scan_string", v)
			} else {
				requireParsedValue(t, v, d, "scan_string")
			}
		case []byte:
			if err != nil {
				requireParseSentinel(t, err, "scan_bytes", v)
			} else {
				requireParsedValue(t, string(v), d, "scan_bytes")
			}
		case int64:
			require.NoErrorf(t, err, "scan int64 %d", v)
			require.Truef(t, decimal.NewFromInt(v).Equal(ssOf(d)), "scan int64 vs shopspring: %d -> %s", v, d)
			requireResultPrec(t, d, 0, "scan_int64", v)
		case int32:
			require.NoErrorf(t, err, "scan int32 %d", v)
			require.Truef(t, decimal.NewFromInt32(v).Equal(ssOf(d)), "scan int32 vs shopspring: %d -> %s", v, d)
		case int:
			require.NoErrorf(t, err, "scan int %d", v)
			require.Truef(t, decimal.NewFromInt(int64(v)).Equal(ssOf(d)), "scan int vs shopspring: %d -> %s", v, d)
		case uint64:
			require.NoErrorf(t, err, "scan uint64 %d", v)
			require.Truef(t, decimal.NewFromUint64(v).Equal(ssOf(d)), "scan uint64 vs shopspring: %d -> %s", v, d)
		case float64:
			switch {
			case math.IsNaN(v) || math.IsInf(v, 0):
				require.ErrorIsf(t, err, ErrInvalidFloat, "scan non-finite float: %v", v)
			case err != nil:
				require.Truef(t, errors.Is(err, ErrOverflow) || errors.Is(err, ErrPrecOutOfRange),
					"only the domain guards may reject a finite float: %g -> %v", v, err)
			default:
				ssV, ssErr := decimal.NewFromString(strconv.FormatFloat(v, 'f', -1, 64))
				require.NoErrorf(t, ssErr, "shopspring must parse the shortest form of %g", v)
				require.Truef(t, ssV.Equal(ssOf(d)), "scan float vs shopspring: %g -> %s", v, d)
			}
		case bool, time.Time, struct{}:
			require.ErrorIsf(t, err, ErrScanType, "unsupported source type must wrap ErrScanType: %T", src)
		case nil:
			require.ErrorIsf(t, err, ErrScanNil, "nil source must be ErrScanNil")
		}
		if err != nil {
			require.Equalf(t, marker, d, "failed scan must leave the receiver unchanged: src=%#v", src)
		}

		nd := NewNullDecimal(marker)
		nerr := nd.Scan(src)
		if src == nil {
			require.NoErrorf(t, nerr, "null decimal scan of SQL NULL")
			require.Equalf(t, NullDecimal{}, nd, "SQL NULL must clear to the invalid zero")
			return
		}
		if err != nil {
			require.Errorf(t, nerr, "null decimal scan must mirror Decimal.Scan's failure: src=%#v", src)
			require.Equalf(t, NullDecimal{}, nd, "failed scan must clear the null decimal: src=%#v", src)
			return
		}
		require.NoErrorf(t, nerr, "null decimal scan: src=%#v", src)
		require.Equalf(t, NewNullDecimal(d), nd, "null decimal scan must match Decimal.Scan: src=%#v", src)
	})
}

// FuzzDivInternals differentially tests the outlined division cores against
// math/big: divU256Pow10 with its iff 2^128 overflow contract across all
// three k regimes (k == 0, table range, and the wide 10^19 peel), the
// full-width divmodU256Pow10Wide quotient and remainder, div256by64 with the zero-divisor
// and quotient-overflow contracts plus the exact remainder, div256by128 on
// precondition-satisfying operands (seed rows land on the trial-digit clamp
// and the double-overshoot correction of div3by2), and divCoefAt —
// trunc(|d|/|e|·10^p) with the fits-in-128 iff — across both signs of its
// scale gap, including the scaled-divisor one-limb, two-limb, and
// overflows-to-exact-zero arms.
func FuzzDivInternals(f *testing.F) {
	f.Add(uint64(0), uint64(0), uint64(0), uint64(0), uint64(0), uint8(0), uint8(0))
	f.Add(^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0), uint64(3), uint8(38), uint8(19))
	f.Add(uint64(12345), uint64(0), uint64(0), uint64(0), uint64(7), uint8(1), uint8(5))
	f.Add(uint64(0), uint64(0), ^uint64(0), uint64(0), uint64(5), uint8(10), uint8(3))
	f.Add(uint64(0), uint64(0), uint64(0), uint64(1), uint64(0), uint8(0), uint8(0))
	f.Add(uint64(9), uint64(8), uint64(0), uint64(0), uint64(4), uint8(39), uint8(2))
	f.Add(uint64(1), uint64(2), uint64(100), uint64(0), uint64(9), uint8(2), uint8(1))
	f.Add(^uint64(0), ^uint64(0), uint64(1), uint64(0), uint64(0), uint8(19), uint8(0))
	f.Add(^uint64(0), ^uint64(0), uint64(0), uint64(1), uint64(0), uint8(1), uint8(0))
	f.Add(^uint64(0), ^uint64(0), uint64(0), uint64(1)<<63, uint64(0), uint8(19), uint8(0))
	f.Add(^uint64(0), ^uint64(0), uint64(0), uint64(1), uint64(2), uint8(0), uint8(19))
	f.Add(uint64(0), uint64(0), uint64(10_000_000_000_000_000_000), uint64(0), uint64(1), uint8(20), uint8(0))
	f.Add(uint64(5), uint64(7), uint64(3), uint64(1)<<63, uint64(1)<<63, uint8(3), uint8(4))
	f.Add(uint64(0xfffffffffffd4829), uint64(0x52465abe7d4bc0d2), uint64(0xe49fe68a4c730be6), uint64(0x7fffffffffffe8ef), uint64(0x800000000000010b), uint8(5), uint8(6))
	f.Add(uint64(0), uint64(0), uint64(7), uint64(0), uint64(7), uint8(0), uint8(0))
	f.Fuzz(func(t *testing.T, d0, d1, d2, d3, v uint64, k, p uint8) {
		u := u256{d0: d0, d1: d1, d2: d2, d3: d3}
		ub := u256ToBig(u)

		kq := k % 39
		q, err := divU256Pow10(u, kq)
		wantQ := new(big.Int).Quo(ub, bp10(int(kq)))
		if wantQ.Cmp(mod128big) >= 0 {
			require.ErrorIsf(t, err, ErrOverflow, "divU256Pow10 overflow oracle: u=%+v k=%d", u, kq)
		} else {
			require.NoErrorf(t, err, "divU256Pow10: u=%+v k=%d", u, kq)
			require.Truef(t, q.eq(bigToU128(t, wantQ)), "divU256Pow10 vs big.Int: u=%+v k=%d got=%+v want=%s", u, kq, q, wantQ)
		}

		kw := 1 + k%19
		w, wr := divmodU256Pow10Wide(u, kw)
		wantWideQ, wantWideR := new(big.Int).QuoRem(ub, bp10(int(kw)), new(big.Int))
		require.Zerof(t, u256ToBig(w).Cmp(wantWideQ),
			"divmodU256Pow10Wide quotient vs big.Int: u=%+v k=%d got=%+v", u, kw, w)
		require.Equalf(t, wantWideR.Uint64(), wr,
			"divmodU256Pow10Wide remainder vs big.Int: u=%+v k=%d", u, kw)

		q64, r64, err := div256by64(u, v)
		if v == 0 {
			require.ErrorIsf(t, err, ErrDivideByZero, "div256by64 zero divisor: u=%+v", u)
		} else {
			vb := new(big.Int).SetUint64(v)
			wantQ := new(big.Int).Quo(ub, vb)
			if wantQ.Cmp(mod128big) >= 0 {
				require.ErrorIsf(t, err, ErrOverflow, "div256by64 overflow oracle: u=%+v v=%d", u, v)
			} else {
				require.NoErrorf(t, err, "div256by64: u=%+v v=%d", u, v)
				require.Truef(t, q64.eq(bigToU128(t, wantQ)), "div256by64 quotient vs big.Int: u=%+v v=%d got=%+v want=%s", u, v, q64, wantQ)
				require.Equalf(t, new(big.Int).Mod(ub, vb).Uint64(), r64, "div256by64 remainder vs big.Int: u=%+v v=%d", u, v)
			}
		}

		vv := u128{hi: v, lo: d0}
		if v != 0 && less128(u.hi128(), vv) {
			q128, r128 := div256by128(u, vv)
			vb := u128ToBig(vv)
			require.Truef(t, q128.eq(bigToU128(t, new(big.Int).Quo(ub, vb))),
				"div256by128 quotient vs big.Int: u=%+v v=%+v got=%+v", u, vv, q128)
			require.Truef(t, r128.eq(bigToU128(t, new(big.Int).Rem(ub, vb))),
				"div256by128 remainder vs big.Int: u=%+v v=%+v got=%+v", u, vv, r128)
		}

		dd := fuzzDecimal(t, false, d1, d0, k)
		ee := fuzzDecimal(t, false, d3, d2, uint8(v%20))
		if ee.IsZero() {
			return
		}
		pp := int(p) % 20
		num := new(big.Int).Mul(u128ToBig(dd.coef), bp10(pp+int(ee.prec)))
		den := new(big.Int).Mul(u128ToBig(ee.coef), bp10(int(dd.prec)))
		want := num.Quo(num, den)
		gotQ, ok := divCoefAt(dd, ee, pp)
		if want.Cmp(mod128big) >= 0 {
			require.Falsef(t, ok, "divCoefAt fits oracle: d=%+v e=%+v p=%d want=%s", dd, ee, pp, want)
			return
		}
		require.Truef(t, ok, "divCoefAt must fit: d=%+v e=%+v p=%d want=%s", dd, ee, pp, want)
		require.Truef(t, gotQ.eq(bigToU128(t, want)), "divCoefAt vs big.Int: d=%+v e=%+v p=%d got=%+v want=%s", dd, ee, pp, gotQ, want)
	})
}
