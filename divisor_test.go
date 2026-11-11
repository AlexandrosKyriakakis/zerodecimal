package zerodecimal

import (
	"errors"
	"fmt"
	"math/big"
	"math/bits"
	"math/rand/v2"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// divisorPlaces are the DivRoundBy scales the identity suites sweep: both
// ends of 0..MaxPrec, the money and quantity scales between them, and 20,
// one past MaxPrec, for the ErrPrecOutOfRange arm.
var divisorPlaces = [...]uint8{0, 1, 2, 4, 9, 16, 18, 19, 20}

// divisorRaceScale shrinks a sweep under -race. make test runs the package
// with the race detector and atomic coverage, which slows these
// pure-arithmetic sweeps about 250-fold; there they are a smoke test beside
// TestDivisorConcurrentUse, the actual race proof. The full sweeps run in a
// plain go test and in every build-tag configuration (make test-tags).
func divisorRaceScale(n int) int {
	if raceEnabled {
		return max(1, n/20)
	}
	return n
}

// divisorRepr formats d's raw representation. Results are compared with ==,
// which tells 1.5 from 1.50; String would hide exactly that difference.
func divisorRepr(d Decimal) string {
	neg, hi, lo, prec := d.ToHiLo()
	return fmt.Sprintf("{neg:%t hi:%#x lo:%#x prec:%d}", neg, hi, lo, prec)
}

// requireDivByIdentity requires d.DivBy(v) to return exactly d.Div of v's
// Decimal: the same representation (==) and the same sentinel. Mismatches
// alone reach require, which keeps the hot sweeps cheap.
func requireDivByIdentity(t *testing.T, d Decimal, v *Divisor) {
	t.Helper()
	e := v.Decimal()
	want, wantErr := d.Div(e)
	if got, err := d.DivBy(v); got != want || !errors.Is(err, wantErr) {
		require.Failf(t, "DivBy differs from Div", "d=%s e=%s: got %s, %v; want %s, %v",
			divisorRepr(d), divisorRepr(e), divisorRepr(got), err, divisorRepr(want), wantErr)
	}
}

// requireDivRoundByIdentity requires d.DivRoundBy(v, places, m) to return
// exactly d.DivRound of v's Decimal in every rounding mode and the first
// invalid one.
func requireDivRoundByIdentity(t *testing.T, d Decimal, v *Divisor, places uint8) {
	t.Helper()
	e := v.Decimal()
	for m := ToNearestAway; m <= TowardNegative+1; m++ {
		want, wantErr := d.DivRound(e, places, m)
		if got, err := d.DivRoundBy(v, places, m); got != want || !errors.Is(err, wantErr) {
			require.Failf(t, "DivRoundBy differs from DivRound", "d=%s e=%s places=%d mode=%d: got %s, %v; want %s, %v",
				divisorRepr(d), divisorRepr(e), places, m, divisorRepr(got), err, divisorRepr(want), wantErr)
		}
	}
}

// requireDivisorIdentity runs both identities for one operand pair, DivRoundBy
// at every divisorPlaces value. Under -race it checks one scale per pair,
// picked by the operands so a sweep still visits them all (see
// divisorRaceScale).
func requireDivisorIdentity(t *testing.T, d Decimal, v *Divisor) {
	t.Helper()
	requireDivByIdentity(t, d, v)
	places := divisorPlaces[:]
	if raceEnabled {
		e := v.Decimal()
		k := (d.coef.lo ^ e.coef.lo ^ e.coef.hi ^ uint64(d.prec)) % uint64(len(divisorPlaces))
		places = divisorPlaces[k : k+1]
	}
	for _, p := range places {
		requireDivRoundByIdentity(t, d, v, p)
	}
}

// newTestDivisor returns NewDivisor(e) for a test divisor: an invalid Divisor
// exactly when e is zero, as NewDivisor documents.
func newTestDivisor(t *testing.T, e Decimal) *Divisor {
	t.Helper()
	v, err := NewDivisor(e)
	switch {
	case e.IsZero() && (!errors.Is(err, ErrDivideByZero) || v != Divisor{}):
		require.Failf(t, "NewDivisor(0) must fail with the zero Divisor", "got %+v, %v", v, err)
	case !e.IsZero() && (err != nil || v.Decimal() != e):
		require.Failf(t, "Decimal must return the divisor exactly as given", "e=%s: got %s, %v",
			divisorRepr(e), divisorRepr(v.Decimal()), err)
	}
	return &v
}

// TestDivisorIdentityRandom holds DivBy and DivRoundBy to Div and DivRound on
// 240k random operand pairs: half with coefficient widths uniform over 1..128
// bits for both operands, so every numerator width and both divisor arms
// occur often, and half from the boundary-heavy cross-check generator
// (powers of ten and two and their neighbors, the 2^128 ceiling, small
// integers). Precisions are uniform or edge-biased over 0..MaxPrec and signs
// uniform; zero operands are mixed in. Div depends on DefaultPrec, so this
// runs under every precision build tag (make test-tags).
func TestDivisorIdentityRandom(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0xd1715012, 0x1de7))
	randPrec := func() uint8 { return uint8(rng.Uint64N(uint64(MaxPrec) + 1)) }
	for i := range divisorRaceScale(240_000) {
		var d, e Decimal
		if i%2 == 0 {
			d = shapedDecimal(rng, 128, randPrec())
			e = shapedDecimal(rng, 128, randPrec())
		} else {
			d = randCrossDecimal(rng)
			e = randCrossDecimal(rng)
		}
		switch {
		case i%101 == 0:
			d = Decimal{}
		case i%103 == 0:
			e = Decimal{}
		}
		requireDivisorIdentity(t, d, newTestDivisor(t, e))
	}
}

// divisorEdgeCoefs are the divisor and dividend coefficients the edge suite
// crosses: every one-limb power of ten (1 included), both sides of the
// one/two-limb boundary 2^64, 2^127, 10^38, the maximum coefficient, and a
// few odd values.
func divisorEdgeCoefs() []u128 {
	coefs := []u128{
		{lo: 3},
		{lo: 7},
		{lo: 1<<63 + 1},
		{lo: maxUint64},
		{hi: 1},
		{hi: 1, lo: 1},
		{hi: 1 << 63},
		{hi: 1<<63 - 1, lo: maxUint64},
		pow10u128[38],
		{hi: maxUint64, lo: maxUint64},
	}
	for k := range 20 {
		coefs = append(coefs, u128{lo: pow10u64[k]})
	}
	return coefs
}

// TestDivisorEdgeCases crosses the edge coefficients as divisors (±, at
// precisions 0 and 19) with the edge dividends: zero, the maximum coefficient
// at every precision, and every other edge coefficient at precisions 0, 1, 9,
// 18 and 19, with alternating signs.
func TestDivisorEdgeCases(t *testing.T) {
	t.Parallel()
	coefs := divisorEdgeCoefs()
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	for _, ec := range coefs {
		for _, ep := range []uint8{0, MaxPrec} {
			for _, eneg := range []bool{false, true} {
				v := newTestDivisor(t, newDecimal(ec, eneg, ep))
				requireDivisorIdentity(t, Decimal{}, v)
				for dp := range MaxPrec + 1 {
					requireDivisorIdentity(t, newDecimal(maxCoef, dp%2 == 1, dp), v)
				}
				for i, dc := range coefs {
					for _, dp := range []uint8{0, 1, 9, MaxPrec - 1, MaxPrec} {
						requireDivisorIdentity(t, newDecimal(dc, (i+int(dp))%2 == 1, dp), v)
					}
				}
			}
		}
	}
}

// TestDivisorQuotientBoundary pins the 2^128 quotient boundary, where DivBy
// hands off to Div's degradation path and DivRoundBy to DivRound's
// ErrOverflow. For each divisor c and scale gap f it builds the dividend
// coefficients ⌊Q·c/10^f⌋ - 1 .. + 1 for Q = 2^128 - 1 and 2^128, so the
// scaled quotient lands just below, on and just above the boundary, and
// checks each operation at the dividend precision that gives it gap f:
// DefaultPrec + e.Prec() - f for DivBy, places + e.Prec() - f for DivRoundBy.
func TestDivisorQuotientBoundary(t *testing.T) {
	t.Parallel()
	targets := []*big.Int{new(big.Int).Sub(mod128big, big.NewInt(1)), mod128big}
	divisors := []u128{{lo: 1}, {lo: 3}, {lo: 10}, {lo: 1_000_000_007}, {lo: maxUint64}, {hi: 1}, {hi: 5, lo: 12345}, {hi: maxUint64, lo: maxUint64}}
	step := 1
	if raceEnabled {
		step = 5 // see divisorRaceScale
	}
	for _, c := range divisors {
		cb := u128ToBig(c)
		for _, ep := range []uint8{0, 9, MaxPrec} {
			v := newTestDivisor(t, newDecimal(c, false, ep))
			for f := 1; f <= 2*int(MaxPrec); f += step {
				for _, q := range targets {
					base := new(big.Int).Mul(q, cb)
					base.Quo(base, bp10(f))
					for delta := int64(-1); delta <= 1; delta++ {
						coef := new(big.Int).Add(base, big.NewInt(delta))
						if coef.Sign() <= 0 || coef.Cmp(mod128big) >= 0 {
							continue
						}
						dc := bigToU128(t, coef)
						if dp := int(DefaultPrec) + int(ep) - f; dp >= 0 && dp <= int(MaxPrec) {
							requireDivByIdentity(t, newDecimal(dc, delta == 0, uint8(dp)), v)
						}
						for places := range MaxPrec + 1 {
							if dp := int(places) + int(ep) - f; dp >= 0 && dp <= int(MaxPrec) {
								requireDivRoundByIdentity(t, newDecimal(dc, delta < 0, uint8(dp)), v, places)
							}
						}
					}
				}
			}
		}
	}
}

// TestDivisorRoundPastMaxQuotient pins DivRoundBy's one overflow that is not
// a fits failure: a truncated quotient of exactly 2^128 - 1 with a nonzero
// remainder, which fits but overflows when the mode rounds it up. For one-limb
// divisors (the inline path) and two-limb ones (divRoundByWide) it searches
// the scale gaps f for a dividend coefficient whose d.coef·10^f / c lies
// strictly between 2^128 - 1 and 2^128, then checks every mode against
// DivRound at places = MaxPrec.
func TestDivisorRoundPastMaxQuotient(t *testing.T) {
	t.Parallel()
	maxQ := new(big.Int).Sub(mod128big, big.NewInt(1))
	found := map[bool]int{} // by two-limb divisor
	divisors := []u128{{lo: 3}, {lo: 7}, {lo: 99}, {lo: 999}, {lo: 1_000_003}, {hi: 1, lo: 1}, {hi: 3}, {hi: 5, lo: 12345}, {hi: 0x8000, lo: 99}}
	for _, c := range divisors {
		cb := u128ToBig(c)
		low := new(big.Int).Mul(maxQ, cb) // num = low + r, 0 < r < c
		for _, ep := range []uint8{0, MaxPrec} {
			v := newTestDivisor(t, newDecimal(c, false, ep))
			// places = MaxPrec gives d.Prec() = MaxPrec + ep - f.
			for f := max(1, int(ep)); f <= int(MaxPrec)+int(ep); f++ {
				// The least multiple of 10^f above low, if it is still below
				// low + c, divides to the coefficient that lands there.
				coef := new(big.Int).Add(low, bp10(f))
				coef.Quo(coef, bp10(f))
				num := new(big.Int).Mul(coef, bp10(f))
				if r := new(big.Int).Sub(num, low); r.Sign() <= 0 || r.Cmp(cb) >= 0 || coef.Cmp(mod128big) >= 0 {
					continue
				}
				found[c.hi != 0]++
				d := newDecimal(bigToU128(t, coef), f%2 == 0, MaxPrec+ep-uint8(f))
				requireDivRoundByIdentity(t, d, v, MaxPrec)
				got, err := d.DivRoundBy(v, MaxPrec, AwayFromZero)
				require.ErrorIs(t, err, ErrOverflow, "rounding 2^128-1 up must overflow: c=%+v f=%d", c, f)
				require.Equal(t, Decimal{}, got)
				got, err = d.DivRoundBy(v, MaxPrec, TowardZero)
				require.NoError(t, err)
				require.Equal(t, u128{hi: maxUint64, lo: maxUint64}, got.coef)
			}
		}
	}
	require.Positive(t, found[false], "the search must construct a one-limb divisor case")
	require.Positive(t, found[true], "the search must construct a two-limb divisor case")
}

// TestDivisorValidationOrder pins DivRoundBy's argument validation to
// DivRound's order: places, then mode, then a zero or nil Divisor, then a
// zero dividend; and DivBy's to Div's: the divisor before the dividend.
func TestDivisorValidationOrder(t *testing.T) {
	t.Parallel()
	three := MustNewDivisor(NewFromInt(3))
	var zero Divisor
	one := NewFromInt(1)
	tests := []struct {
		name    string
		d       Decimal
		v       *Divisor
		places  uint8
		mode    RoundingMode
		wantErr error
	}{
		{name: "places_before_mode_and_divisor", d: one, v: nil, places: MaxPrec + 1, mode: TowardNegative + 1, wantErr: ErrPrecOutOfRange},
		{name: "places_before_divisor", d: one, v: &zero, places: 255, mode: ToNearestEven, wantErr: ErrPrecOutOfRange},
		{name: "mode_before_nil_divisor", d: one, v: nil, places: 2, mode: TowardNegative + 1, wantErr: ErrInvalidRoundingMode},
		{name: "mode_before_zero_divisor", d: one, v: &zero, places: 2, mode: 255, wantErr: ErrInvalidRoundingMode},
		{name: "nil_divisor", d: one, v: nil, places: 2, mode: ToNearestEven, wantErr: ErrDivideByZero},
		{name: "zero_divisor", d: one, v: &zero, places: 2, mode: ToNearestEven, wantErr: ErrDivideByZero},
		{name: "nil_divisor_before_zero_dividend", d: Decimal{}, v: nil, places: 2, mode: ToNearestEven, wantErr: ErrDivideByZero},
		{name: "zero_divisor_before_zero_dividend", d: Decimal{}, v: &zero, places: 0, mode: TowardZero, wantErr: ErrDivideByZero},
		{name: "zero_dividend", d: Decimal{}, v: &three, places: 2, mode: ToNearestEven},
		{name: "valid", d: one, v: &three, places: 2, mode: ToNearestEven},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.d.DivRoundBy(tc.v, tc.places, tc.mode)
			want, wantErr := tc.d.DivRound(tc.v.Decimal(), tc.places, tc.mode)
			require.ErrorIs(t, wantErr, tc.wantErr, "DivRound's own order")
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, Decimal{}, got)
			}
			require.Equal(t, want, got)

			if tc.places <= MaxPrec && tc.mode.valid() {
				got, err := tc.d.DivBy(tc.v)
				want, wantErr := tc.d.Div(tc.v.Decimal())
				require.Equal(t, want, got)
				require.Equal(t, wantErr, err)
			}
		})
	}
}

// TestDivisorConstructionAndErrors covers the constructor contract and every
// error the Divisor API reports: a zero divisor, an invalid (zero or nil)
// Divisor, and the Must twins' panics, whose values must match the sentinel.
func TestDivisorConstructionAndErrors(t *testing.T) {
	t.Parallel()
	t.Run("new_divisor_zero", func(t *testing.T) {
		v, err := NewDivisor(Decimal{})
		require.ErrorIs(t, err, ErrDivideByZero)
		require.Equal(t, Divisor{}, v)
	})
	t.Run("must_new_divisor_zero_panics", func(t *testing.T) {
		requireDivisorPanic(t, ErrDivideByZero, func() { MustNewDivisor(Decimal{}) })
	})
	t.Run("must_new_divisor", func(t *testing.T) {
		e := MustNew(-150, -2) // -1.50: the trailing zero must survive
		v := MustNewDivisor(e)
		require.Equal(t, e, v.Decimal())
		require.Equal(t, uint8(2), v.Decimal().Prec())
	})
	t.Run("decimal_of_invalid_divisors", func(t *testing.T) {
		var zero Divisor
		var nilDivisor *Divisor
		require.Equal(t, Decimal{}, zero.Decimal())
		require.Equal(t, Decimal{}, nilDivisor.Decimal())
	})
	t.Run("div_by_invalid", func(t *testing.T) {
		var zero Divisor
		d := NewFromInt(7)
		for _, v := range []*Divisor{nil, &zero} {
			got, err := d.DivBy(v)
			require.ErrorIs(t, err, ErrDivideByZero)
			require.Equal(t, Decimal{}, got)
			got, err = Decimal{}.DivBy(v)
			require.ErrorIs(t, err, ErrDivideByZero, "a zero dividend does not excuse an invalid divisor")
			require.Equal(t, Decimal{}, got)
			requireDivisorPanic(t, ErrDivideByZero, func() { d.MustDivBy(v) })
		}
	})
	t.Run("div_by_overflow", func(t *testing.T) {
		// The maximum coefficient over 10^-19: even the integer quotient
		// exceeds 128 bits.
		maxDec := newDecimal(u128{hi: maxUint64, lo: maxUint64}, false, 0)
		v := MustNewDivisor(newDecimal(u128{lo: 1}, false, MaxPrec))
		got, err := maxDec.DivBy(&v)
		require.ErrorIs(t, err, ErrOverflow)
		require.Equal(t, Decimal{}, got)
		requireDivisorPanic(t, ErrOverflow, func() { maxDec.MustDivBy(&v) })
		got, err = maxDec.DivRoundBy(&v, 0, ToNearestEven)
		require.ErrorIs(t, err, ErrOverflow)
		require.Equal(t, Decimal{}, got)
	})
	t.Run("must_div_by", func(t *testing.T) {
		v := MustNewDivisor(NewFromInt(3))
		require.Equal(t, NewFromInt(2).MustDiv(NewFromInt(3)), NewFromInt(2).MustDivBy(&v))
	})
	t.Run("signs_and_precision", func(t *testing.T) {
		// Negative and fractional divisors keep their sign and precision.
		v := MustNewDivisor(MustNew(-25, -1)) // -2.5
		got, err := MustNew(1, 0).DivRoundBy(&v, 2, ToNearestEven)
		require.NoError(t, err)
		require.Equal(t, MustNew(-40, -2), got)
		got, err = MustNew(-5, 0).DivBy(&v)
		require.NoError(t, err)
		require.Equal(t, MustNew(5, 0).MustDiv(MustNew(25, -1)), got)
		require.True(t, got.Equal(NewFromInt(2)))
	})
}

// requireDivisorPanic requires fn to panic with an error matching want.
func requireDivisorPanic(t *testing.T, want error, fn func()) {
	t.Helper()
	pv := func() (pv any) {
		defer func() { pv = recover() }()
		fn()
		return nil
	}()
	require.NotNil(t, pv, "must panic with %v", want)
	err, ok := pv.(error)
	require.Truef(t, ok, "panic value must be an error, got %T", pv)
	require.ErrorIs(t, err, want)
}

// reciprocal3by2Big is the oracle for reciprocal3by2: ⌊(2^192-1)/d⌋ - 2^64.
func reciprocal3by2Big(t *testing.T, d u128) uint64 {
	t.Helper()
	num := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 192), big.NewInt(1))
	num.Quo(num, u128ToBig(d))
	num.Sub(num, new(big.Int).Lsh(big.NewInt(1), 64))
	require.True(t, num.IsUint64(), "reciprocal of a normalized divisor fits 64 bits: d=%+v", d)
	return num.Uint64()
}

// TestReciprocal3by2 checks the two-limb reciprocal against math/big on the
// boundary divisors (top bit just set, d.hi = 2^63 with d.lo at 0, 1 and
// 2^64-1, the maximum) and on random normalized divisors.
func TestReciprocal3by2(t *testing.T) {
	t.Parallel()
	divisors := []u128{
		{hi: 1 << 63},
		{hi: 1 << 63, lo: 1},
		{hi: 1 << 63, lo: maxUint64},
		{hi: 1<<63 + 1},
		{hi: 1<<63 + 1, lo: maxUint64},
		{hi: maxUint64, lo: maxUint64},
		{hi: maxUint64, lo: maxUint64 - 1},
		{hi: maxUint64},
		pow10u128[38].lsh(1),
		{hi: 0x8ac7230489e80000}, // 10^19 << 64
	}
	rng := rand.New(rand.NewPCG(0x7ec1, 0x3b2))
	for range divisorRaceScale(200_000) {
		divisors = append(divisors, u128{hi: rng.Uint64() | 1<<63, lo: randShaped64(rng)})
	}
	for _, d := range divisors {
		if got, want := reciprocal3by2(d), reciprocal3by2Big(t, d); got != want {
			require.Failf(t, "reciprocal3by2 mismatch", "d=%+v: got %#x, want %#x", d, got, want)
		}
	}
}

// requireDiv3by2Recip checks one div3by2Recip call against math/big and
// against div3by2, which shares its contract but divides in hardware.
func requireDiv3by2Recip(t *testing.T, u2, u1, u0 uint64, d u128) {
	t.Helper()
	q, r := div3by2Recip(u2, u1, u0, d, reciprocal3by2(d))
	wantQ, wantR := div3by2(u2, u1, u0, d)
	if q != wantQ || r != wantR {
		require.Failf(t, "div3by2Recip differs from div3by2", "{%#x,%#x,%#x} / %+v: got q=%#x r=%+v, want q=%#x r=%+v",
			u2, u1, u0, d, q, r, wantQ, wantR)
	}
	u := u256ToBig(u256{d0: u0, d1: u1, d2: u2})
	bq, br := new(big.Int).QuoRem(u, u128ToBig(d), new(big.Int))
	if !bq.IsUint64() || bq.Uint64() != q || br.Cmp(u128ToBig(r)) != 0 {
		require.Failf(t, "div3by2Recip differs from math/big", "{%#x,%#x,%#x} / %+v: got q=%#x r=%+v, want q=%s r=%s",
			u2, u1, u0, d, q, r, bq, br)
	}
}

// TestDiv3by2Recip checks the reciprocal 3-by-2 step on the shapes that steer
// its corrections (the largest dividend below d·2^64, u2 = d.hi with u1 just
// below d.lo, where div3by2 clamps its trial digit, exact multiples of d and
// their neighbors) and on random dividends satisfying {u2,u1} < d.
func TestDiv3by2Recip(t *testing.T) {
	t.Parallel()
	divisors := []u128{
		{hi: 1 << 63},
		{hi: 1 << 63, lo: 1},
		{hi: 1 << 63, lo: maxUint64},
		{hi: maxUint64, lo: maxUint64},
		{hi: maxUint64},
		{hi: 0xc000000000000001, lo: 0x8000000000000000},
	}
	for _, d := range divisors {
		top := u128{hi: d.hi, lo: d.lo}
		top, _ = sub128(top, u128{lo: 1}) // d - 1: the largest {u2,u1}
		for _, hi := range []u128{{}, {lo: 1}, top, {hi: d.hi, lo: d.lo / 2}} {
			if !less128(hi, d) {
				continue
			}
			for _, u0 := range []uint64{0, 1, d.lo, maxUint64} {
				requireDiv3by2Recip(t, hi.hi, hi.lo, u0, d)
			}
		}
		// q·d + r for chosen digits q and remainders r ∈ {0, 1, d-1}.
		for _, q := range []uint64{0, 1, 2, 1 << 63, maxUint64 - 1, maxUint64} {
			for _, r := range []u128{{}, {lo: 1}, top} {
				d2, d1, d0 := mul128by64to192(d, q)
				lo, c := bits.Add64(d0, r.lo, 0)
				mid, c := bits.Add64(d1, r.hi, c)
				requireDiv3by2Recip(t, d2+c, mid, lo, d)
			}
		}
	}
	rng := rand.New(rand.NewPCG(0x3b2, 0x7ec1))
	for range divisorRaceScale(500_000) {
		d := u128{hi: rng.Uint64() | 1<<63, lo: randShaped64(rng)}
		// u2 is clamped to d.hi about half the time: div3by2's clamp shape.
		hi := u128{hi: min(rng.Uint64(), d.hi), lo: randShaped64(rng)}
		if !less128(hi, d) {
			hi, _ = sub128(d, u128{lo: 1 + rng.Uint64N(1<<20)})
		}
		requireDiv3by2Recip(t, hi.hi, hi.lo, randShaped64(rng), d)
	}
}

// requireDivisorQuoRem checks v.quoRem(num) against math/big and against the
// division it replaces, div256by64 for a one-limb coefficient and
// div256by128 for a two-limb one: the quotient and remainder of num over v's
// coefficient, and fits exactly when the quotient is below 2^128. It also
// holds v.divRound(num) to divRoundWide(num, c), all four results, which is
// the drop-in contract divRound documents.
func requireDivisorQuoRem(t *testing.T, v *Divisor, num u256) {
	t.Helper()
	c := v.e.coef
	gq, grem, ghalf, gfits := v.divRound(num)
	if wq, wrem, whalf, wfits := divRoundWide(num, c); gq != wq || grem != wrem || ghalf != whalf || gfits != wfits {
		require.Failf(t, "divRound differs from divRoundWide", "num=%+v c=%+v: got %+v %t %d %t, want %+v %t %d %t",
			num, c, gq, grem, ghalf, gfits, wq, wrem, whalf, wfits)
	}
	q, r, fits := v.quoRem(num)
	bq, br := new(big.Int).QuoRem(u256ToBig(num), u128ToBig(c), new(big.Int))
	if bq.Cmp(mod128big) >= 0 {
		if fits {
			require.Failf(t, "quoRem must report a quotient past 128 bits", "num=%+v c=%+v", num, c)
		}
		return
	}
	if !fits || u128ToBig(q).Cmp(bq) != 0 || u128ToBig(r).Cmp(br) != 0 {
		require.Failf(t, "quoRem differs from math/big", "num=%+v c=%+v: got q=%+v r=%+v fits=%t, want q=%s r=%s",
			num, c, q, r, fits, bq, br)
	}
	var wq, wr u128
	if c.hi == 0 {
		var r64 uint64
		var err error
		wq, r64, err = div256by64(num, c.lo)
		require.NoError(t, err)
		wr = u128{lo: r64}
	} else {
		wq, wr = div256by128(num, c)
	}
	if q != wq || r != wr {
		require.Failf(t, "quoRem differs from the division it replaces", "num=%+v c=%+v: got %+v %+v, want %+v %+v", num, c, q, r, wq, wr)
	}
}

// TestDivisorQuoRemOneLimb checks quoRem's one-limb reciprocal path:
// one-limb numerators, numerators whose quotient has a zero high limb (one
// div2by1 step), full two-limb quotients, the fits boundary num.d2 = c - 1 /
// c, and numerators past it.
func TestDivisorQuoRemOneLimb(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x1157, 0xd1))
	for i := range divisorRaceScale(300_000) {
		c := shapedCoef(rng, 64).lo
		v := newTestDivisor(t, newDecimal(u128{lo: c}, false, 0))
		var num u256
		switch i % 5 {
		case 0:
			num = u256{d0: randShaped64(rng)}
		case 1:
			num = u256{d0: rng.Uint64(), d1: rng.Uint64N(c)}
		case 2:
			num = u256{d0: rng.Uint64(), d1: rng.Uint64(), d2: rng.Uint64N(c)}
		case 3:
			num = u256{d0: randShaped64(rng), d1: randShaped64(rng), d2: c - rng.Uint64N(2)}
		default:
			num = u256{d0: rng.Uint64(), d1: rng.Uint64(), d2: rng.Uint64(), d3: rng.Uint64N(2)}
		}
		requireDivisorQuoRem(t, v, num)
	}
}

// TestDivisorQuoRemTwoLimb checks quoRem's two-limb reciprocal path:
// numerators below 2^128 (a single digit), wider ones with and without a
// high quotient digit, the fits boundary hi128(num) = c - 1 / c, and
// numerators past it.
func TestDivisorQuoRemTwoLimb(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x2157, 0xd1))
	for i := range divisorRaceScale(300_000) {
		c := shapedCoef(rng, 128)
		if c.hi == 0 {
			c.hi = 1 | randShaped64(rng)>>uint(rng.IntN(64))
		}
		v := newTestDivisor(t, newDecimal(c, true, 7))
		var num u256
		switch i % 4 {
		case 0:
			num = u256{d0: rng.Uint64(), d1: randShaped64(rng)}
		case 1:
			hi := u128{hi: rng.Uint64N(c.hi), lo: rng.Uint64()}
			num = u256{d0: rng.Uint64(), d1: rng.Uint64(), d2: hi.lo, d3: hi.hi}
		case 2:
			hi, _ := sub128(c, u128{lo: rng.Uint64N(2)})
			num = u256{d0: randShaped64(rng), d1: randShaped64(rng), d2: hi.lo, d3: hi.hi}
		default:
			num = u256{d0: rng.Uint64(), d1: rng.Uint64(), d2: rng.Uint64(), d3: rng.Uint64()}
		}
		requireDivisorQuoRem(t, v, num)
	}
}

// TestDivisorConcurrentUse shares one Divisor across goroutines. A Divisor is
// immutable after construction, so concurrent divisions are plain reads; run
// under -race (make test) this proves it.
func TestDivisorConcurrentUse(t *testing.T) {
	t.Parallel()
	shared := MustNewDivisor(MustNew(2_544_930_000, -9))
	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for g := range 8 {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(g), 0xc0c0))
			for range 2_000 {
				d := shapedDecimal(rng, 96, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
				want, wantErr := d.Div(shared.Decimal())
				got, err := d.DivBy(&shared)
				wantR, wantRErr := d.DivRound(shared.Decimal(), 2, ToNearestEven)
				gotR, rErr := d.DivRoundBy(&shared, 2, ToNearestEven)
				if got != want || !errors.Is(err, wantErr) || gotR != wantR || !errors.Is(rErr, wantRErr) {
					errs <- fmt.Sprintf("goroutine %d: d=%s", g, divisorRepr(d))
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}
