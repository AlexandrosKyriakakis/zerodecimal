package zerodecimal

// Deterministic differential cross-check against shopspring/decimal, whose
// unbounded big.Int arithmetic is the oracle. Every case is fixed-seed and
// checks both directions of the domain boundary: where a result fits the
// 128-bit coefficient the two libraries must agree exactly, and where
// zerodecimal returns ErrOverflow a big.Int computation must prove the exact
// result coefficient at the contracted precision is ≥ 2^128 (an iff oracle,
// so a spurious error fails just as loudly as a wrong value). Documented
// semantic differences — Div's adaptive precision, QuoRem's divisor-alignment
// overflow, rounding places as uint8 — are encoded in the oracles themselves.

import (
	"encoding/binary"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// crossPow10 caches 10^k for every exponent the cross-check oracles rescale
// across (k ≤ 39 = MaxPrec + DefaultPrec + 1). Entries are shared: callers
// must never use one as a mutable receiver.
var crossPow10 = func() [40]*big.Int {
	var tab [40]*big.Int
	for k := range tab {
		tab[k] = pow10Big(k)
	}
	return tab
}()

// bp10 returns the shared cached big.Int value of 10^k.
func bp10(k int) *big.Int {
	return crossPow10[k]
}

// ssFromParts builds the shopspring image of the raw Decimal representation
// (neg ? -1 : +1)·(hi·2^64 + lo)/10^prec: the limbs pack big-endian into 16
// bytes for big.Int.SetBytes and prec becomes the negated exponent.
func ssFromParts(neg bool, hi, lo uint64, prec uint8) decimal.Decimal {
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[:8], hi)
	binary.BigEndian.PutUint64(buf[8:], lo)
	b := new(big.Int).SetBytes(buf[:])
	if neg {
		b.Neg(b)
	}
	return decimal.NewFromBigInt(b, -int32(prec))
}

// ssOf converts d to its exact shopspring oracle image via the raw parts.
func ssOf(d Decimal) decimal.Decimal {
	neg, hi, lo, prec := d.ToHiLo()
	return ssFromParts(neg, hi, lo, prec)
}

// crossVal pairs a zerodecimal value with its precomputed shopspring image so
// the pair sweeps convert each operand exactly once.
type crossVal struct {
	zd Decimal
	ss decimal.Decimal
}

// newCrossVal wraps d with its shopspring oracle image.
func newCrossVal(d Decimal) crossVal {
	return crossVal{zd: d, ss: ssOf(d)}
}

// requireSameValue asserts that the zerodecimal result denotes the same
// number as the shopspring oracle result. Both libraries render canonical
// strings (trailing fractional zeros trimmed), so equality is normally
// byte-for-byte; a residual trailing-zero style difference falls back to
// numeric equality in shopspring arithmetic over the reparsed string before
// failing. ctx is formatted into the failure message only on mismatch.
func requireSameValue(t *testing.T, want decimal.Decimal, got Decimal, ctx ...any) {
	t.Helper()
	wantStr, gotStr := want.String(), got.String()
	if wantStr == gotStr {
		return
	}
	if reparsed, err := decimal.NewFromString(gotStr); err == nil && want.Equal(reparsed) {
		return
	}
	require.Failf(t, "cross-check mismatch",
		"shopspring=%s zerodecimal=%s ctx=%+v", wantStr, gotStr, ctx)
}

// requireResultPrec asserts the result-precision contract shared by the
// arithmetic methods: a zero result is the canonical Decimal{} and any other
// result carries exactly the contracted precision.
func requireResultPrec(t *testing.T, got Decimal, want uint8, ctx ...any) {
	t.Helper()
	if got.IsZero() {
		require.Equalf(t, Decimal{}, got, "zero result must be canonical: ctx=%+v", ctx)
		return
	}
	require.Equalf(t, want, got.Prec(), "result precision: ctx=%+v", ctx)
}

// signedCoefAt returns the exact signed integer coefficient of d rescaled to
// precision f: ±coef·10^(f-d.prec).
//
// PRECONDITION: f ≥ d.prec.
func signedCoefAt(d Decimal, f uint8) *big.Int {
	c := new(big.Int).Mul(u128ToBig(d.coef), bp10(int(f-d.prec)))
	if d.neg {
		c.Neg(c)
	}
	return c
}

// checkPair runs every binary differential check on the ordered pair (a, b).
func checkPair(t *testing.T, a, b crossVal) {
	t.Helper()
	checkCmp(t, a, b)
	checkAddSub(t, a, b)
	checkMul(t, a, b)
	checkDiv(t, a, b)
	checkQuoRem(t, a, b)
}

// checkValue runs every unary differential check on v.
func checkValue(t *testing.T, v crossVal) {
	t.Helper()
	checkRounding(t, v)
	checkParseFormat(t, v)
	checkIntPart(t, v)
}

// checkCmp cross-checks Cmp against shopspring and pins the predicate family
// (Equal, LessThan, GreaterThan, and the OrEqual forms) plus antisymmetry to
// the single Cmp result.
func checkCmp(t *testing.T, a, b crossVal) {
	t.Helper()
	got := a.zd.Cmp(b.zd)
	require.Equalf(t, a.ss.Cmp(b.ss), got, "cmp: a=%+v b=%+v", a.zd, b.zd)
	require.Equalf(t, -got, b.zd.Cmp(a.zd), "cmp antisymmetry: a=%+v b=%+v", a.zd, b.zd)
	require.Equalf(t, got == 0, a.zd.Equal(b.zd), "equal vs cmp: a=%+v b=%+v", a.zd, b.zd)
	require.Equalf(t, got < 0, a.zd.LessThan(b.zd), "less_than vs cmp: a=%+v b=%+v", a.zd, b.zd)
	require.Equalf(t, got > 0, a.zd.GreaterThan(b.zd), "greater_than vs cmp: a=%+v b=%+v", a.zd, b.zd)
	require.Equalf(t, got <= 0, a.zd.LessThanOrEqual(b.zd), "less_than_or_equal vs cmp: a=%+v b=%+v", a.zd, b.zd)
	require.Equalf(t, got >= 0, a.zd.GreaterThanOrEqual(b.zd), "greater_than_or_equal vs cmp: a=%+v b=%+v", a.zd, b.zd)
}

// checkAddSub cross-checks Add and Sub with an exact iff overflow oracle: the
// operation must return ErrOverflow precisely when the exact result
// coefficient at the contracted precision max(aPrec, bPrec) is ≥ 2^128, and
// must otherwise match shopspring at exactly that precision.
func checkAddSub(t *testing.T, a, b crossVal) {
	t.Helper()
	f := max(a.zd.prec, b.zd.prec)
	ca, cb := signedCoefAt(a.zd, f), signedCoefAt(b.zd, f)

	exact := new(big.Int).Add(ca, cb)
	got, err := a.zd.Add(b.zd)
	if exact.CmpAbs(mod128big) >= 0 {
		require.ErrorIsf(t, err, ErrOverflow, "add overflow oracle: a=%+v b=%+v", a.zd, b.zd)
	} else {
		require.NoErrorf(t, err, "add: a=%+v b=%+v", a.zd, b.zd)
		requireSameValue(t, a.ss.Add(b.ss), got, "add", a.zd, b.zd)
		requireResultPrec(t, got, f, "add", a.zd, b.zd)
	}

	exact.Sub(ca, cb)
	got, err = a.zd.Sub(b.zd)
	if exact.CmpAbs(mod128big) >= 0 {
		require.ErrorIsf(t, err, ErrOverflow, "sub overflow oracle: a=%+v b=%+v", a.zd, b.zd)
	} else {
		require.NoErrorf(t, err, "sub: a=%+v b=%+v", a.zd, b.zd)
		requireSameValue(t, a.ss.Sub(b.ss), got, "sub", a.zd, b.zd)
		requireResultPrec(t, got, f, "sub", a.zd, b.zd)
	}
}

// checkMul cross-checks Mul, whose result truncates toward zero at precision
// min(aPrec+bPrec, DefaultPrec): success must match shopspring's exact
// product truncated there, and ErrOverflow must occur precisely when the
// truncated coefficient is ≥ 2^128.
func checkMul(t *testing.T, a, b crossVal) {
	t.Helper()
	pSum := int(a.zd.prec) + int(b.zd.prec)
	rp := min(pSum, int(DefaultPrec))
	exact := new(big.Int).Mul(u128ToBig(a.zd.coef), u128ToBig(b.zd.coef))
	if pSum > rp {
		exact.Quo(exact, bp10(pSum-rp))
	}
	got, err := a.zd.Mul(b.zd)
	if exact.Cmp(mod128big) >= 0 {
		require.ErrorIsf(t, err, ErrOverflow, "mul overflow oracle: a=%+v b=%+v", a.zd, b.zd)
		return
	}
	require.NoErrorf(t, err, "mul: a=%+v b=%+v", a.zd, b.zd)
	requireSameValue(t, a.ss.Mul(b.ss).Truncate(int32(rp)), got, "mul", a.zd, b.zd)
	requireResultPrec(t, got, uint8(rp), "mul", a.zd, b.zd)
}

// checkDiv cross-checks Div's adaptive-precision truncated quotient.
// shopspring's QuoRem at the precision Div chose is exactly the truncated
// quotient at that precision, so the values must match; a big.Int oracle then
// proves the choice maximal — one more fractional digit would push the
// quotient coefficient to ≥ 2^128 — and proves every ErrOverflow exact (even
// the integer quotient does not fit). The maximality check is skipped for
// zero quotients, whose canonical form erases the precision it was computed
// at (a zero coefficient fits at every precision).
func checkDiv(t *testing.T, a, b crossVal) {
	t.Helper()
	q, err := a.zd.Div(b.zd)
	if b.zd.IsZero() {
		require.ErrorIsf(t, err, ErrDivideByZero, "div by zero: a=%+v", a.zd)
		return
	}
	// Magnitude quotient at precision p is ⌊coefA·10^(p+bPrec) / (coefB·10^aPrec)⌋.
	num := new(big.Int).Mul(u128ToBig(a.zd.coef), bp10(int(b.zd.prec)))
	den := new(big.Int).Mul(u128ToBig(b.zd.coef), bp10(int(a.zd.prec)))
	if new(big.Int).Quo(num, den).Cmp(mod128big) >= 0 {
		require.ErrorIsf(t, err, ErrOverflow, "div overflow oracle: a=%+v b=%+v", a.zd, b.zd)
		return
	}
	require.NoErrorf(t, err, "div: a=%+v b=%+v", a.zd, b.zd)
	p := q.Prec()
	require.LessOrEqualf(t, p, DefaultPrec, "div precision bound: a=%+v b=%+v", a.zd, b.zd)
	ssQ, _ := a.ss.QuoRem(b.ss, int32(p))
	requireSameValue(t, ssQ, q, "div", a.zd, b.zd)
	if !q.IsZero() && p < DefaultPrec {
		next := new(big.Int).Quo(num.Mul(num, bp10(int(p)+1)), den)
		require.GreaterOrEqualf(t, next.Cmp(mod128big), 0,
			"div must keep the largest precision that fits: prec %d chosen but %d still fits: a=%+v b=%+v",
			p, p+1, a.zd, b.zd)
	}
}

// checkQuoRem cross-checks QuoRem and Mod against shopspring's T-division at
// precision 0 (identical convention: q truncated toward zero, r signed like
// the dividend). ErrOverflow must occur precisely when the integer quotient
// magnitude is ≥ 2^128 or the divisor aligned to max(aPrec, bPrec) is — the
// documented divisor-alignment contract. Successful results must match
// shopspring's q and r and satisfy d = q·e + r in shopspring arithmetic.
func checkQuoRem(t *testing.T, a, b crossVal) {
	t.Helper()
	q, r, err := a.zd.QuoRem(b.zd)
	m, merr := a.zd.Mod(b.zd)
	if b.zd.IsZero() {
		require.ErrorIsf(t, err, ErrDivideByZero, "quorem by zero: a=%+v", a.zd)
		require.ErrorIsf(t, merr, ErrDivideByZero, "mod by zero: a=%+v", a.zd)
		return
	}
	f := max(a.zd.prec, b.zd.prec)
	num := new(big.Int).Mul(u128ToBig(a.zd.coef), bp10(int(f-a.zd.prec)))
	den := new(big.Int).Mul(u128ToBig(b.zd.coef), bp10(int(f-b.zd.prec)))
	if den.Cmp(mod128big) >= 0 || new(big.Int).Quo(num, den).Cmp(mod128big) >= 0 {
		require.ErrorIsf(t, err, ErrOverflow, "quorem overflow oracle: a=%+v b=%+v", a.zd, b.zd)
		require.ErrorIsf(t, merr, ErrOverflow, "mod overflow oracle: a=%+v b=%+v", a.zd, b.zd)
		return
	}
	require.NoErrorf(t, err, "quorem: a=%+v b=%+v", a.zd, b.zd)
	require.NoErrorf(t, merr, "mod: a=%+v b=%+v", a.zd, b.zd)
	ssQ, ssR := a.ss.QuoRem(b.ss, 0)
	requireSameValue(t, ssQ, q, "quorem_q", a.zd, b.zd)
	requireSameValue(t, ssR, r, "quorem_r", a.zd, b.zd)
	require.Equalf(t, r, m, "mod must return the quorem remainder: a=%+v b=%+v", a.zd, b.zd)
	require.Truef(t, a.ss.Equal(ssOf(q).Mul(b.ss).Add(ssOf(r))),
		"quorem identity d == q·e + r: a=%+v b=%+v q=%+v r=%+v", a.zd, b.zd, q, r)
	requireResultPrec(t, q, 0, "quorem_q", a.zd, b.zd)
	requireResultPrec(t, r, f, "quorem_r", a.zd, b.zd)
}

// crossRoundOp pairs a zerodecimal rounding method with the shopspring method
// of identical documented semantics.
type crossRoundOp struct {
	name string
	zd   func(Decimal, uint8) Decimal
	ss   func(decimal.Decimal, int32) decimal.Decimal
}

// crossRoundOps lists every rounding mode both libraries implement. The
// round/round_bank pair pins the half-away-from-zero versus ties-to-even tie
// behavior against shopspring exactly; round_up/round_down are the
// away-from-zero/toward-zero modes in both libraries (NOT ceil/floor).
var crossRoundOps = []crossRoundOp{
	{name: "round", zd: Decimal.Round, ss: decimal.Decimal.Round},
	{name: "round_bank", zd: Decimal.RoundBank, ss: decimal.Decimal.RoundBank},
	{name: "round_up", zd: Decimal.RoundUp, ss: decimal.Decimal.RoundUp},
	{name: "round_down", zd: Decimal.RoundDown, ss: decimal.Decimal.RoundDown},
	{name: "round_ceil", zd: Decimal.RoundCeil, ss: decimal.Decimal.RoundCeil},
	{name: "round_floor", zd: Decimal.RoundFloor, ss: decimal.Decimal.RoundFloor},
	{name: "truncate", zd: Decimal.Truncate, ss: decimal.Decimal.Truncate},
}

// crossRoundPlaces are the fractional-digit counts every rounding mode is
// checked at; 18 sits one digit inside MaxPrec, where full-precision values
// put their tie digit exactly on the boundary.
var crossRoundPlaces = []uint8{0, 1, 5, 18}

// checkRounding cross-checks the full rounding family plus Floor and Ceil at
// every place count in crossRoundPlaces.
func checkRounding(t *testing.T, v crossVal) {
	t.Helper()
	for _, op := range crossRoundOps {
		for _, places := range crossRoundPlaces {
			requireSameValue(t, op.ss(v.ss, int32(places)), op.zd(v.zd, places), op.name, places, v.zd)
		}
	}
	requireSameValue(t, v.ss.Floor(), v.zd.Floor(), "floor", v.zd)
	requireSameValue(t, v.ss.Ceil(), v.zd.Ceil(), "ceil", v.zd)
}

// checkParseFormat cross-checks the formatting/parsing round trip in both
// directions: the zerodecimal canonical string must parse to the same number
// in shopspring, the shopspring canonical string must parse back through
// NewFromString (every value here is within zerodecimal's domain), and both
// canonical renderings must agree byte-for-byte.
func checkParseFormat(t *testing.T, v crossVal) {
	t.Helper()
	zdStr := v.zd.String()
	ssParsed, err := decimal.NewFromString(zdStr)
	require.NoErrorf(t, err, "shopspring must parse the canonical string %q", zdStr)
	require.Truef(t, v.ss.Equal(ssParsed), "shopspring reparse of %q: want %s", zdStr, v.ss.String())

	ssStr := v.ss.String()
	zdParsed, err := NewFromString(ssStr)
	require.NoErrorf(t, err, "NewFromString must parse the shopspring string %q", ssStr)
	require.Zerof(t, v.zd.Cmp(zdParsed), "zerodecimal reparse of %q: d=%+v", ssStr, v.zd)
	require.Equalf(t, zdStr, ssStr, "canonical renderings must agree: d=%+v", v.zd)
}

// checkIntPart cross-checks IntPart: when the truncated integer part fits
// int64 it must equal both the big.Int oracle and shopspring's IntPart;
// otherwise ErrIntPartOverflow is required (shopspring silently wraps there,
// so the oracle alone decides).
func checkIntPart(t *testing.T, v crossVal) {
	t.Helper()
	mag := new(big.Int).Quo(u128ToBig(v.zd.coef), bp10(int(v.zd.prec)))
	if v.zd.neg {
		mag.Neg(mag)
	}
	got, err := v.zd.IntPart()
	if !mag.IsInt64() {
		require.ErrorIsf(t, err, ErrIntPartOverflow, "int_part overflow oracle: d=%+v", v.zd)
		return
	}
	require.NoErrorf(t, err, "int_part: d=%+v", v.zd)
	require.Equalf(t, mag.Int64(), got, "int_part vs big.Int oracle: d=%+v", v.zd)
	require.Equalf(t, v.ss.IntPart(), got, "int_part vs shopspring: d=%+v", v.zd)
}

// crossBoundaryValues builds the deterministic boundary value set: every
// boundary coefficient at each boundary precision and sign, deduplicated (all
// sign and precision variants of a zero coefficient collapse to the one
// canonical zero). The coefficients sit on decimal-digit, limb, and 2^128
// boundaries; precisions cover both extremes, their neighbors, and a midpoint.
func crossBoundaryValues(t *testing.T) []crossVal {
	t.Helper()
	coefs := []u128{
		{},                     // zero
		{lo: 1},                // smallest unit
		{lo: 5},                // half of the first carry
		{lo: 9},                // last single digit
		{lo: 10},               // first carry
		{lo: 99},               // two-digit ceiling
		{lo: 100},              //
		{lo: pow10u64[9]},      // 10^9
		{lo: pow10u64[18]},     // 10^18
		{lo: 5 * pow10u64[18]}, // 5·10^18: tie numerator at MaxPrec
		{lo: pow10u64[19] - 1}, // all-nines single limb
		{lo: pow10u64[19]},     // 10^19
		{lo: 1 << 63},          // 2^63
		{lo: maxUint64},        // 2^64 - 1
		{hi: 1},                // 2^64
		{hi: 1, lo: 1},         // 2^64 + 1
		pow10u128[20],          // 10^20
		pow10u128[38],          // 10^38
		{hi: 1 << 63},          // 2^127
		{hi: 0x1999999999999999, lo: 0x9999999999999999}, // ⌊(2^128-1)/10⌋: ×10 just overflows
		{hi: maxUint64, lo: maxUint64},                   // 2^128 - 1
	}
	precs := []uint8{0, 1, 2, 9, 18, 19}
	seen := make(map[Decimal]struct{})
	vals := make([]crossVal, 0, 2*len(coefs)*len(precs))
	for _, c := range coefs {
		for _, p := range precs {
			for _, neg := range []bool{false, true} {
				d := mustHiLo(t, neg, c.hi, c.lo, p)
				if _, dup := seen[d]; dup {
					continue
				}
				seen[d] = struct{}{}
				vals = append(vals, newCrossVal(d))
			}
		}
	}
	return vals
}

// randCrossCoef returns a coefficient biased toward the carry and overflow
// boundaries: powers of ten and two and their ±2 neighborhoods, the 2^128
// ceiling, small integers, and shaped uniform filler.
func randCrossCoef(rng *rand.Rand) u128 {
	switch rng.IntN(8) {
	case 0: // 10^k neighborhood, single limb (k ≤ 19)
		return nudge(rng, u128{lo: pow10u64[rng.IntN(20)]})
	case 1: // 10^k neighborhood, two limbs (k = 20..38)
		return nudge(rng, pow10u128[20+rng.IntN(19)])
	case 2: // 2^k neighborhood
		bit := uint(rng.IntN(128))
		if bit < 64 {
			return nudge(rng, u128{lo: 1 << bit})
		}
		return nudge(rng, u128{hi: 1 << (bit - 64)})
	case 3: // top of the coefficient range
		return nudge(rng, u128{hi: maxUint64, lo: maxUint64})
	case 4: // small integers: carry chains in the low digits
		return u128{lo: rng.Uint64N(1000)}
	case 5: // single-limb uniform
		return u128{lo: rng.Uint64()}
	default:
		return randShapedU128(rng)
	}
}

// nudge moves c by a delta in [-2, 2] wrapping mod 2^128, landing the value
// just below, exactly on, or just above the boundary it started from.
func nudge(rng *rand.Rand, c u128) u128 {
	delta := u128{lo: rng.Uint64N(3)}
	if rng.Uint64()&1 == 0 {
		r, _ := sub128(c, delta)
		return r
	}
	r, _ := add128(c, delta)
	return r
}

// randCrossPrec returns a precision biased toward 0 and the MaxPrec edge,
// where alignment and truncation paths change shape.
func randCrossPrec(rng *rand.Rand) uint8 {
	switch rng.IntN(6) {
	case 0:
		return 0
	case 1:
		return MaxPrec
	case 2:
		return MaxPrec - 1
	default:
		return uint8(rng.Uint64N(uint64(MaxPrec) + 1))
	}
}

// randCrossDecimal returns a canonical Decimal with boundary-biased
// coefficient and precision and a uniform sign.
func randCrossDecimal(rng *rand.Rand) Decimal {
	return newDecimal(randCrossCoef(rng), rng.Uint64()&1 == 1, randCrossPrec(rng))
}

func TestCrossCheckOraclePacking(t *testing.T) {
	tests := []struct {
		name string
		neg  bool
		hi   uint64
		lo   uint64
		prec uint8
		want string
	}{
		{name: "zero", want: "0"},
		{name: "negative_zero_collapses", neg: true, want: "0"},
		{name: "lo_limb_with_fraction", lo: 12345, prec: 2, want: "123.45"},
		{name: "hi_limb_weight_is_2_pow_64", hi: 1, want: "18446744073709551616"},
		{name: "max_coef_full_prec", hi: maxUint64, lo: maxUint64, prec: 19, want: "34028236692093846346.3374607431768211455"},
		{name: "negative_fraction", neg: true, lo: 15, prec: 1, want: "-1.5"},
		{name: "trailing_zeros_trim", lo: 1500, prec: 3, want: "1.5"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ss := ssFromParts(tc.neg, tc.hi, tc.lo, tc.prec)
			require.Equal(t, tc.want, ss.String())
			zd := mustHiLo(t, tc.neg, tc.hi, tc.lo, tc.prec)
			require.Equal(t, tc.want, zd.String())
		})
	}
}

func TestCrossCheckBoundaryPairs(t *testing.T) {
	vals := crossBoundaryValues(t)
	for _, a := range vals {
		for _, b := range vals {
			checkPair(t, a, b)
		}
	}
}

func TestCrossCheckBoundaryValues(t *testing.T) {
	for _, v := range crossBoundaryValues(t) {
		checkValue(t, v)
	}
}

func TestCrossCheckRandomPairs(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xC405C4EC, 0x0DDBA111))
	for range 30_000 {
		a := newCrossVal(randCrossDecimal(rng))
		b := randCrossDecimal(rng)
		if rng.IntN(4) == 0 {
			// Same-precision bias keeps the aligned fast paths well covered.
			b = newDecimal(b.coef, b.neg, a.zd.prec)
		}
		checkPair(t, a, newCrossVal(b))
	}
}

func TestCrossCheckRandomValues(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x0AC1E5, 0x5A1735))
	for range 8_000 {
		checkValue(t, newCrossVal(randCrossDecimal(rng)))
	}
}

func TestCrossCheckEqualValueRepresentations(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xE0A111, 0x9EC5))
	for range 8_000 {
		c := randShaped64(rng)
		p := uint8(rng.Uint64N(uint64(MaxPrec)))       // 0..18
		k := uint8(1 + rng.Uint64N(uint64(MaxPrec-p))) // 1..19-p
		scaled, over := mul128by64(u128{lo: c}, pow10u64[k&31])
		require.Zero(t, over, "c < 2^64 and k ≤ 19 keep c·10^k under 2^128")
		neg := rng.Uint64()&1 == 1
		a := newCrossVal(newDecimal(u128{lo: c}, neg, p))
		b := newCrossVal(newDecimal(scaled, neg, p+k))
		require.Truef(t, a.zd.Equal(b.zd), "equal values across precisions: a=%+v b=%+v", a.zd, b.zd)
		checkPair(t, a, b)
		checkPair(t, b, a)
		// The opposite-sign twin exercises exact cancellation across precisions.
		checkPair(t, a, newCrossVal(b.zd.Neg()))
	}
}

// TestCrossCheckRegressionPairs is the permanent named home for minimized
// cross-check cases: structural boundary pins plus any pair a differential
// sweep ever flagged. Every case runs the full binary and unary check set in
// both operand orders.
func TestCrossCheckRegressionPairs(t *testing.T) {
	type part struct {
		neg  bool
		hi   uint64
		lo   uint64
		prec uint8
	}
	tests := []struct {
		name string
		a, b part
	}{
		{name: "round_tie_positive_half", a: part{lo: 25, prec: 1}, b: part{lo: 35, prec: 1}},
		{name: "round_tie_negative_half", a: part{neg: true, lo: 25, prec: 1}, b: part{neg: true, lo: 5, prec: 1}},
		{name: "round_tie_below_one_at_max_prec", a: part{lo: pow10u64[19] - 5, prec: 19}, b: part{lo: 5, prec: 19}},
		{name: "div_adaptive_precision_degrades", a: part{hi: maxUint64, lo: maxUint64}, b: part{lo: 3}},
		{name: "div_integer_quotient_overflows", a: part{hi: maxUint64, lo: maxUint64}, b: part{lo: 1, prec: 19}},
		{name: "quorem_divisor_alignment_overflows", a: part{lo: 1, prec: 19}, b: part{hi: maxUint64, lo: maxUint64}},
		{name: "mul_truncates_beyond_default_prec", a: part{lo: pow10u64[19] - 1, prec: 19}, b: part{lo: pow10u64[19] - 1, prec: 19}},
		{name: "add_carry_across_limb_boundary", a: part{lo: maxUint64}, b: part{lo: 1}},
		{name: "sub_exact_cancellation_across_prec", a: part{lo: 15, prec: 1}, b: part{neg: true, lo: 150, prec: 2}},
		{name: "equal_value_different_prec", a: part{lo: 15, prec: 1}, b: part{lo: 1500, prec: 3}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newCrossVal(mustHiLo(t, tc.a.neg, tc.a.hi, tc.a.lo, tc.a.prec))
			b := newCrossVal(mustHiLo(t, tc.b.neg, tc.b.hi, tc.b.lo, tc.b.prec))
			checkPair(t, a, b)
			checkPair(t, b, a)
			checkValue(t, a)
			checkValue(t, b)
		})
	}
}
