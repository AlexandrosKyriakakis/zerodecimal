package zerodecimal

import (
	"errors"
	"math/big"
	"math/rand/v2"
	"os/exec"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// Div by a divisor whose coefficient is 1 or 100 takes a rescale fast path
// (pow10_operand.go), and mulSlow multiplies a one-limb factor inline
// (arith.go). Their results must be bit-identical to the general paths,
// errors and Div's adaptive precision included, under every build tag. The
// tests below hold both operations to big.Int restatements of their
// contracts, compared field for field (canonical zero included), and to the
// shopspring cross-check oracles in crosscheck_test.go.

// pow10RefDiv restates Div's contract with big.Int: the quotient truncated toward
// zero at the largest p ≤ DefaultPrec whose coefficient fits 128 bits,
// ErrOverflow when even p = 0 does not fit.
func pow10RefDiv(d, e Decimal) (Decimal, error) {
	if e.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	num := u128ToBig(d.coef)
	den := new(big.Int).Mul(u128ToBig(e.coef), bp10(int(d.prec)))
	for p := int(DefaultPrec); p >= 0; p-- {
		q := new(big.Int).Mul(num, bp10(p+int(e.prec)))
		q.Quo(q, den)
		if q.Cmp(mod128big) < 0 {
			return newDecimal(exactU128FromBig(q), d.neg != e.neg, uint8(p)), nil
		}
	}
	return Decimal{}, ErrOverflow
}

// pow10RefMul restates Mul's contract with big.Int: the exact product truncated
// toward zero at min(d.prec+e.prec, DefaultPrec), ErrOverflow iff that
// coefficient does not fit 128 bits.
func pow10RefMul(d, e Decimal) (Decimal, error) {
	pSum := int(d.prec) + int(e.prec)
	rp := min(pSum, int(DefaultPrec))
	q := new(big.Int).Mul(u128ToBig(d.coef), u128ToBig(e.coef))
	q.Quo(q, bp10(pSum-rp))
	if q.Cmp(mod128big) >= 0 {
		return Decimal{}, ErrOverflow
	}
	return newDecimal(exactU128FromBig(q), d.neg != e.neg, uint8(rp)), nil
}

// pow10RequireSame asserts that got and gotErr match the reference result:
// the same sentinel and the zero Decimal on failure, otherwise the identical
// representation (coefficient, sign and precision).
func pow10RequireSame(t *testing.T, op string, d, e, want Decimal, wantErr error, got Decimal, gotErr error) {
	t.Helper()
	if wantErr != nil {
		if !errors.Is(gotErr, wantErr) || got != (Decimal{}) {
			require.Failf(t, op+" error mismatch", "d=%+v e=%+v: want %v, got %+v, %v", d, e, wantErr, got, gotErr)
		}
		return
	}
	if gotErr != nil || got != want {
		require.Failf(t, op+" result mismatch", "d=%+v e=%+v: want %+v, got %+v, %v", d, e, want, got, gotErr)
	}
}

// pow10CheckDiv runs Div on (d, e), compares it with pow10RefDiv and
// returns the reference result.
func pow10CheckDiv(t *testing.T, d, e Decimal) (Decimal, error) {
	t.Helper()
	want, wantErr := pow10RefDiv(d, e)
	got, gotErr := d.Div(e)
	pow10RequireSame(t, "div", d, e, want, wantErr, got, gotErr)
	return want, wantErr
}

// pow10CheckMul runs Mul on (d, e) and compares it with pow10RefMul.
func pow10CheckMul(t *testing.T, d, e Decimal) {
	t.Helper()
	want, wantErr := pow10RefMul(d, e)
	got, gotErr := d.Mul(e)
	pow10RequireSame(t, "mul", d, e, want, wantErr, got, gotErr)
}

// pow10EdgeCoefs returns dividend coefficients on the edges of Div's
// power-of-ten fast path for a divisor 10^j at precision ePrec and a dividend
// at precision dPrec. With g = DefaultPrec + ePrec - j - dPrec the quotient
// coefficient at DefaultPrec is ⌊c·10^g⌋: for g ≥ 0 the edge is the largest c
// whose scaled coefficient fits 128 bits and its neighbours (one more and Div
// must degrade); for g < 0 it is the truncation boundary 10^-g and the
// largest coefficient.
func pow10EdgeCoefs(j int, ePrec, dPrec uint8) []u128 {
	maxCoef := new(big.Int).Sub(mod128big, big.NewInt(1))
	g := int(DefaultPrec) + int(ePrec) - j - int(dPrec)
	var base []*big.Int
	if g >= 0 {
		c0 := new(big.Int).Quo(maxCoef, bp10(g))
		base = []*big.Int{c0, new(big.Int).Sub(c0, big.NewInt(1)), new(big.Int).Add(c0, big.NewInt(1))}
	} else {
		k := bp10(-g)
		base = []*big.Int{k, new(big.Int).Sub(k, big.NewInt(1)), new(big.Int).Add(k, big.NewInt(1)), maxCoef}
	}
	coefs := make([]u128, 0, len(base))
	for _, c := range base {
		if c.Sign() > 0 && c.Cmp(maxCoef) <= 0 {
			coefs = append(coefs, exactU128FromBig(c))
		}
	}
	return coefs
}

// TestDivPow10DivisorAgainstReference drives Div through every power-of-ten
// divisor 10^j/10^ePrec with j, ePrec ∈ 0..MaxPrec and both signs. Dividends
// sit on the fast path's degradation edge at every precision, cover fixed
// one- and two-limb shapes, and are drawn from randCrossDecimal; every result
// must match pow10RefDiv exactly, and a sample also runs the shopspring checkDiv.
// The dividends must reach every outcome the general path owns: a degraded
// precision, ErrOverflow, and a quotient truncated to zero.
func TestDivPow10DivisorAgainstReference(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x9047_0d1f, 0x5ca1e))
	var degraded, overflowed, zeroed int
	check := func(d, e Decimal) {
		t.Helper()
		want, err := pow10CheckDiv(t, d, e)
		switch {
		case err != nil:
			overflowed++
		case want.IsZero():
			zeroed++
		case want.Prec() < DefaultPrec:
			degraded++
		}
	}
	fixed := []u128{
		{lo: 1},
		{lo: 7},
		{lo: 12345},
		{lo: pow10u64[19] - 1},
		{lo: maxUint64},
		{hi: 1},
		{hi: 1, lo: 1},
		pow10u128[20], pow10u128[38],
		{hi: maxUint64, lo: maxUint64},
	}
	for j := range int(MaxPrec) + 1 {
		for ePrec := range MaxPrec + 1 {
			for _, eNeg := range []bool{false, true} {
				e := newDecimal(u128{lo: pow10u64[j]}, eNeg, ePrec)
				for dPrec := range MaxPrec + 1 {
					for i, c := range pow10EdgeCoefs(j, ePrec, dPrec) {
						check(newDecimal(c, i%2 == 1, dPrec), e)
					}
				}
				for i, c := range fixed {
					for _, dPrec := range []uint8{0, 2, 9, MaxPrec} {
						check(newDecimal(c, i%2 == 0, dPrec), e)
					}
				}
				ce := newCrossVal(e)
				for i := range 24 {
					d := randCrossDecimal(rng)
					check(d, e)
					if i%4 == 0 {
						checkDiv(t, newCrossVal(d), ce)
					}
				}
			}
		}
	}
	require.Positive(t, degraded, "dividends must reach the degradation path")
	require.Positive(t, overflowed, "dividends must reach ErrOverflow")
	require.Positive(t, zeroed, "dividends must truncate to zero")
}

// TestDivPow10DivisorExamples pins readable cases of each fast-path shape:
// multiplying rescales (g ≥ 0, one- and two-limb 10^g), a negative divisor,
// the two-limb prec-19 coefficient pow10TwoLimb19 at g = 0 and g < 0 (a
// truncating rescale), a quotient that truncates to canonical zero, and the
// fall-through to the degradation and overflow the general path owns. The
// powers of ten isPow10Divisor does not admit (1000, 10^19) pin the general
// path for contrast. Values depend on DefaultPrec, so each case is checked
// against pow10RefDiv; the readable strings that hold under every build tag
// are pinned as well.
func TestDivPow10DivisorExamples(t *testing.T) {
	t.Parallel()
	maxInt := mustHiLo(t, false, maxUint64, maxUint64, 0)
	tests := []struct {
		name    string
		d, e    Decimal
		want    string // "" when the value depends on DefaultPrec
		general bool   // the divisor is a power of ten isPow10Divisor does not admit
	}{
		{name: "cents_by_100", d: RequireFromString("123.45"), e: NewFromInt(100), want: "1.2345"},
		{name: "price_by_1", d: RequireFromString("98.7654"), e: NewFromInt(1), want: "98.7654"},
		{name: "price_by_cent", d: RequireFromString("1.5"), e: RequireFromString("0.01"), want: "150"},
		{name: "strike_by_1000_general_path", d: NewFromInt(-125000), e: NewFromInt(1000), want: "-125", general: true},
		{name: "negative_divisor", d: RequireFromString("2.5"), e: NewFromInt(-100), want: "-0.025"},
		{name: "one_by_ten_pow_neg19", d: NewFromInt(1), e: RequireFromString("0.0000000000000000001"), want: "10000000000000000000"},
		{name: "two_limb_prec19_by_100", d: pow10TwoLimb19, e: NewFromInt(100)},
		{name: "two_limb_prec19_by_1", d: pow10TwoLimb19, e: NewFromInt(1)},
		{name: "quantum_by_100_truncates_to_zero", d: RequireFromString("0.0000000000000000001"), e: NewFromInt(100), want: "0"},
		{name: "max_by_1_degrades", d: maxInt, e: NewFromInt(1), want: maxInt.String()},
		{name: "max_by_cent_overflows", d: maxInt, e: RequireFromString("0.01")},
		{name: "max_prec19_by_ten_pow_19_general_path", d: mustHiLo(t, true, maxUint64, maxUint64, 19), e: NewFromUint64(pow10u64[19]), general: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, !tc.general, isPow10Divisor(tc.e.coef), "fast-path admission of divisor %v", tc.e)
			_, _ = pow10CheckDiv(t, tc.d, tc.e)
			if tc.want != "" {
				got, err := tc.d.Div(tc.e)
				require.NoError(t, err)
				require.Equal(t, tc.want, got.String())
			}
		})
	}
}

// TestMulOneLimbFactorAgainstReference multiplies one-limb factors — every
// coefficient-1 operand (±1, ±0.1, …, ±10^-19), the options multiplier 100, a
// small quantity and the one-limb extremes — at every precision by one- and
// two-limb operands at every precision, in both operand orders. That reaches
// both mulSlow arms the inline one-limb multiply serves (pSum ≤ DefaultPrec,
// and the rescale past it) plus their overflow edges, against pow10RefMul and
// a sample against the shopspring checkMul.
func TestMulOneLimbFactorAgainstReference(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x0001_c0ef, 0x3a11))
	factors := []uint64{1, 100, 7, pow10u64[19], maxUint64}
	coefs := []u128{
		{hi: 1},
		{hi: 1, lo: 1},
		pow10u128[20], pow10u128[38],
		{hi: 1 << 63},
		{hi: maxUint64, lo: maxUint64},
		{lo: 1},
		{lo: 7},
		{lo: pow10u64[19]},
		{lo: maxUint64},
	}
	var overflowed int
	for fi, f := range factors {
		for uPrec := range MaxPrec + 1 {
			u := newDecimal(u128{lo: f}, (fi+int(uPrec))%2 == 1, uPrec)
			cu := newCrossVal(u)
			for xPrec := range MaxPrec + 1 {
				for i, c := range coefs {
					x := newDecimal(c, i%2 == 0, xPrec)
					pow10CheckMul(t, u, x)
					pow10CheckMul(t, x, u)
					if _, err := pow10RefMul(u, x); err != nil {
						overflowed++
					}
				}
			}
			for i := range 8 {
				x := newDecimal(shapedCoef(rng, 128), rng.Uint64()&1 == 1, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
				if i%2 == 0 {
					x = randCrossDecimal(rng)
				}
				pow10CheckMul(t, u, x)
				pow10CheckMul(t, x, u)
				if i%4 == 0 {
					cx := newCrossVal(x)
					checkMul(t, cu, cx)
					checkMul(t, cx, cu)
				}
			}
		}
	}
	require.Positive(t, overflowed, "factors must reach ErrOverflow")
}

// TestDivPow10CoefAllExponents holds divPow10Coef to Div's contract for every
// 10^j, j ≤ MaxPrec, not only the 1 and 100 isPow10Divisor admits: on success
// it is the quotient coefficient at DefaultPrec, and it fails exactly when
// that coefficient does not fit 128 bits.
func TestDivPow10CoefAllExponents(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0xd1f0_c0ef, 0x77))
	for j := range int(MaxPrec) + 1 {
		for ePrec := range MaxPrec + 1 {
			e := newDecimal(u128{lo: pow10u64[j]}, false, ePrec)
			for dPrec := range MaxPrec + 1 {
				coefs := pow10EdgeCoefs(j, ePrec, dPrec)
				for range 4 {
					coefs = append(coefs, randCrossCoef(rng))
				}
				for _, c := range coefs {
					if c.isZero() {
						continue
					}
					d := newDecimal(c, false, dPrec)
					num := new(big.Int).Mul(u128ToBig(c), bp10(int(DefaultPrec)+int(ePrec)))
					want := num.Quo(num, new(big.Int).Mul(u128ToBig(e.coef), bp10(int(dPrec))))
					got, ok := divPow10Coef(d.coef, dPrec, ePrec, pow10u64[j])
					if want.Cmp(mod128big) >= 0 {
						require.Falsef(t, ok, "j=%d d=%+v e=%+v: quotient %s does not fit", j, d, e, want)
						continue
					}
					require.Truef(t, ok, "j=%d d=%+v e=%+v: quotient %s fits", j, d, e, want)
					if u128ToBig(got).Cmp(want) != 0 {
						require.Failf(t, "divPow10Coef mismatch", "j=%d d=%+v e=%+v: got %s, want %s", j, d, e, u128ToBig(got), want)
					}
				}
			}
		}
	}
}

// TestIsPow10Divisor pins the predicate to exactly the coefficients 1 and 100.
func TestIsPow10Divisor(t *testing.T) {
	for _, c := range []u128{{lo: 1}, {lo: 100}} {
		require.True(t, isPow10Divisor(c), "%+v", c)
	}
	for _, c := range []u128{
		{},
		{lo: 10},
		{lo: 1000},
		{lo: 2},
		{lo: 99},
		{lo: 101},
		{hi: 1, lo: 1},
		{hi: 1, lo: 100},
		{lo: pow10u64[19]},
		pow10u128[20],
	} {
		require.False(t, isPow10Divisor(c), "%+v", c)
	}
}

// TestPow10BenchmarkOperandShapes pins the shapes BenchmarkPow10Operand's
// rows are named after, so a literal that parses to a different precision or
// limb count cannot silently benchmark another path.
func TestPow10BenchmarkOperandShapes(t *testing.T) {
	require.Equal(t, MaxPrec, pow10TwoLimb19.Prec())
	require.NotZero(t, pow10TwoLimb19.coef.hi, "two-limb coefficient")
	require.Equal(t, uint8(9), pow10OneLimb9.Prec())
	require.Zero(t, pow10OneLimb9.coef.hi, "one-limb coefficient")
	require.Equal(t, MaxPrec, pow10OneLimb19.Prec())
	require.Zero(t, pow10OneLimb19.coef.hi, "one-limb coefficient")
	for _, dz := range pow10Divisors {
		_, _, lo, _ := dz.e.ToHiLo()
		require.Contains(t, pow10u64[:MaxPrec+1], lo, "divisor %s must have a power-of-ten coefficient", dz.name)
	}
	for _, dz := range pow10GuardDivisors {
		_, hi, lo, _ := dz.e.ToHiLo()
		require.False(t, hi == 0 && slices.Contains(pow10u64[:MaxPrec+1], lo),
			"guard divisor %s must not have a power-of-ten coefficient", dz.name)
	}
}

// TestPow10OperandInlineBudgets pins the inlining the fast paths rely on.
// isPow10Divisor must inline into Div, so a divisor outside it pays only two
// compares and never a call. mul128by64 and mul128by64to192 must inline into
// divCoefAt, mulSlow and divPow10Coef: those arms exist to avoid
// mulToU256's call frame, and an outlined helper would bring a frame back.
// Mul's own body is untouched (TestArithRoundingInlineBudgets pins it).
func TestPow10OperandInlineBudgets(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("inline budgets are calibrated for 64-bit compiler targets")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; cannot query inlining diagnostics")
	}
	report := buildInlineReport(t)
	for _, fn := range []string{"isPow10Divisor", "mul128by64", "mul128by64to192"} {
		entry, ok := report[fn]
		require.True(t, ok, "%s missing from -m=2 inline report", fn)
		require.True(t, entry.canInline, "%s must fit the default inline budget, got cost %d", fn, entry.cost)
	}
}
