package zerodecimal

import (
	"errors"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	exactResultSink Decimal
	errExactSink    error
)

func TestMulExact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a, b     string
		want     string
		wantPrec uint8
		wantErr  error
	}{
		{name: "zero", a: "0", b: "1.234", want: "0"},
		{name: "ordinary", a: "1.20", b: "3.0", want: "3.6", wantPrec: 1},
		{name: "negative", a: "-1.25", b: "0.4", want: "-0.500", wantPrec: 3},
		{name: "max_times_one_with_scale_reduces_to_fit", a: exactMaxString, b: "1.0", want: exactMaxString},
		{name: "precision_reduced_exactly", a: "0.0000000000000000001", b: "1.0", want: "0.0000000000000000001", wantPrec: 19},
		{name: "underflow", a: "0.0000000000000000001", b: "0.1", wantErr: ErrUnderflow},
		{name: "negative_underflow", a: "-0.0000000000000000001", b: "0.1", wantErr: ErrUnderflow},
		{name: "fractional_precision_loss", a: "0.0000000000000000001", b: "1.1", wantErr: ErrInexact},
		{name: "magnitude_overflow", a: exactMaxString, b: "2", wantErr: ErrOverflow},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := RequireFromString(tc.a)
			b := RequireFromString(tc.b)
			got, err := a.MulExact(b)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, Decimal{}, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.StringFixed(tc.wantPrec))
			require.Equal(t, tc.wantPrec, got.Prec())
		})
	}
}

func TestMulExactRepresentationReduction(t *testing.T) {
	t.Parallel()

	maxDecimal, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 0)
	require.NoError(t, err)
	onePointZero, err := NewFromHiLo(false, 0, 10, 1)
	require.NoError(t, err)
	oneAtMaxPrec, err := NewFromHiLo(false, 0, 1, MaxPrec)
	require.NoError(t, err)

	// The raw product coefficient is max*10 (>128 bits), but removing its
	// exact trailing zero recovers max at precision zero.
	got, err := maxDecimal.MulExact(onePointZero)
	require.NoError(t, err)
	require.Equal(t, maxDecimal, got)

	// Precision 20 is reduced exactly to the minimum representable quantum.
	got, err = oneAtMaxPrec.MulExact(onePointZero)
	require.NoError(t, err)
	require.Equal(t, oneAtMaxPrec, got)

	// Preserve natural precision when no reduction is required, including
	// trailing zeros carried intentionally by raw Decimal representations.
	a, err := NewFromHiLo(false, 0, 120, 2)
	require.NoError(t, err)
	b, err := NewFromHiLo(false, 0, 30, 1)
	require.NoError(t, err)
	got, err = a.MulExact(b)
	require.NoError(t, err)
	require.Equal(t, "3.600", got.StringFixed(3))
	require.Equal(t, uint8(3), got.Prec())

	// max*0.5 is within the magnitude range but needs a 129-bit coefficient
	// to retain its half unit, so the failure is inexact rather than overflow.
	half, err := NewFromHiLo(false, 0, 5, 1)
	require.NoError(t, err)
	_, err = maxDecimal.MulExact(half)
	require.ErrorIs(t, err, ErrInexact)
}

func TestDivExact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		a, b     string
		want     string
		wantPrec uint8
		wantErr  error
	}{
		{name: "zero", a: "0", b: "7", want: "0"},
		{name: "half_retains_greatest_precision", a: "1", b: "2", want: "0.5000000000000000000", wantPrec: 19},
		{name: "negative", a: "-1", b: "8", want: "-0.1250000000000000000", wantPrec: 19},
		{name: "minimum_quantum", a: "0.0000000000000000001", b: "1", want: "0.0000000000000000001", wantPrec: 19},
		{name: "large_integer_degrades_precision_exactly", a: exactMaxString, b: "1", want: exactMaxString},
		{name: "non_terminating", a: "1", b: "3", wantErr: ErrInexact},
		{name: "coefficient_precision_conflict", a: "340282366920938463463374607431768211453", b: "2", wantErr: ErrInexact},
		{name: "underflow", a: "0.0000000000000000001", b: "2", wantErr: ErrUnderflow},
		{name: "overflow", a: exactMaxString, b: "0.5", wantErr: ErrOverflow},
		{name: "divide_by_zero", a: "1", b: "0", wantErr: ErrDivideByZero},
		{name: "zero_divide_by_zero", a: "0", b: "0", wantErr: ErrDivideByZero},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RequireFromString(tc.a).DivExact(RequireFromString(tc.b))
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, Decimal{}, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.StringFixed(tc.wantPrec))
			require.Equal(t, tc.wantPrec, got.Prec())
		})
	}
}

func TestDirectRoundingModes(t *testing.T) {
	t.Parallel()

	modes := []struct {
		name string
		mode RoundingMode
	}{
		{"nearest_away", ToNearestAway},
		{"nearest_even", ToNearestEven},
		{"away", AwayFromZero},
		{"zero", TowardZero},
		{"positive", TowardPositive},
		{"negative", TowardNegative},
	}
	cases := []struct {
		name         string
		num          string
		wantPositive [6]string
		wantNegative [6]string
	}{
		{
			name:         "below_half",
			num:          "24",
			wantPositive: [6]string{"2", "2", "3", "2", "3", "2"},
			wantNegative: [6]string{"-2", "-2", "-3", "-2", "-2", "-3"},
		},
		{
			name:         "even_half",
			num:          "25",
			wantPositive: [6]string{"3", "2", "3", "2", "3", "2"},
			wantNegative: [6]string{"-3", "-2", "-3", "-2", "-2", "-3"},
		},
		{
			name:         "odd_half",
			num:          "35",
			wantPositive: [6]string{"4", "4", "4", "3", "4", "3"},
			wantNegative: [6]string{"-4", "-4", "-4", "-3", "-3", "-4"},
		},
		{
			name:         "above_half",
			num:          "26",
			wantPositive: [6]string{"3", "3", "3", "2", "3", "2"},
			wantNegative: [6]string{"-3", "-3", "-3", "-2", "-2", "-3"},
		},
	}

	for _, tc := range cases {
		for i, m := range modes {
			t.Run(tc.name+"/"+m.name, func(t *testing.T) {
				den := NewFromInt(10)
				pos, err := RequireFromString(tc.num).DivRound(den, 0, m.mode)
				require.NoError(t, err)
				require.Equal(t, tc.wantPositive[i], pos.String())

				neg, err := RequireFromString("-"+tc.num).DivRound(den, 0, m.mode)
				require.NoError(t, err)
				require.Equal(t, tc.wantNegative[i], neg.String())
			})
		}
	}
}

func TestMulRoundUsesFullProduct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		a, b   string
		places uint8
		mode   RoundingMode
		want   string
	}{
		{name: "half_away", a: "1.25", b: "1", places: 1, mode: ToNearestAway, want: "1.3"},
		{name: "half_even", a: "1.25", b: "1", places: 1, mode: ToNearestEven, want: "1.2"},
		{name: "sticky_above_half", a: "1.2500000000000000001", b: "1", places: 1, mode: ToNearestEven, want: "1.3"},
		{name: "full_38_digit_product", a: "0.0000000000000000001", b: "0.0500000000000000001", places: 19, mode: ToNearestAway, want: "0.0000000000000000000"},
		{name: "padding", a: "1.2", b: "3", places: 4, mode: ToNearestEven, want: "3.6000"},
		{name: "negative_floor", a: "-1.01", b: "1", places: 1, mode: TowardNegative, want: "-1.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := RequireFromString(tc.a).MulRound(RequireFromString(tc.b), tc.places, tc.mode)
			require.NoError(t, err)
			require.Equal(t, tc.want, got.StringFixed(tc.places))
			if !got.IsZero() {
				require.Equal(t, tc.places, got.Prec())
			}
		})
	}
}

func TestDivRoundAvoidsDoubleRounding(t *testing.T) {
	t.Parallel()

	numerator := RequireFromString("5000000000000000001")
	denominator := RequireFromString("1000000000000000000000")

	direct, err := numerator.DivRound(denominator, 2, ToNearestEven)
	require.NoError(t, err)
	require.Equal(t, "0.01", direct.String())

	legacy, err := numerator.Div(denominator)
	require.NoError(t, err)
	require.Equal(t, "0.0050000000000000000", legacy.StringFixed(19))
	require.Equal(t, "0", legacy.RoundBank(2).String())
}

func TestDivRoundWideScaledDenominatorTie(t *testing.T) {
	t.Parallel()

	// d/e = (max/10)/(max/5) = 0.5. Aligning e to d's precision makes the
	// denominator 2*max (>128 bits), exercising the wide-denominator path and
	// its exact half comparison.
	d, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 1)
	require.NoError(t, err)
	e := RequireFromString("68056473384187692692674921486353642291")

	even, err := d.DivRound(e, 0, ToNearestEven)
	require.NoError(t, err)
	require.True(t, even.IsZero())
	away, err := d.DivRound(e, 0, ToNearestAway)
	require.NoError(t, err)
	require.Equal(t, "1", away.String())
	negative, err := d.Neg().DivRound(e, 0, TowardNegative)
	require.NoError(t, err)
	require.Equal(t, "-1", negative.String())
}

func TestRoundQuotientIncrementOverflow(t *testing.T) {
	t.Parallel()

	maxCoef := u128{hi: ^uint64(0), lo: ^uint64(0)}
	_, err := roundQuotient(maxCoef, true, 1, false, AwayFromZero)
	require.ErrorIs(t, err, ErrOverflow)
	got, err := roundQuotient(maxCoef, true, 1, false, TowardZero)
	require.NoError(t, err)
	require.Equal(t, maxCoef, got)
}

func TestDirectRoundRangeAndValidation(t *testing.T) {
	t.Parallel()

	invalid := RoundingMode(255)
	maxDecimal := RequireFromString(exactMaxString)
	one := One
	zero := Zero

	_, err := one.MulRound(one, MaxPrec+1, invalid)
	require.ErrorIs(t, err, ErrPrecOutOfRange)
	_, err = one.MulRound(one, 0, invalid)
	require.ErrorIs(t, err, ErrInvalidRoundingMode)
	_, err = one.DivRound(zero, MaxPrec+1, invalid)
	require.ErrorIs(t, err, ErrPrecOutOfRange)
	_, err = one.DivRound(zero, 0, invalid)
	require.ErrorIs(t, err, ErrInvalidRoundingMode)
	_, err = one.DivRound(zero, 0, ToNearestEven)
	require.ErrorIs(t, err, ErrDivideByZero)

	_, err = maxDecimal.MulRound(one, 1, TowardZero)
	require.ErrorIs(t, err, ErrOverflow)
	_, err = maxDecimal.DivRound(one, 1, TowardZero)
	require.ErrorIs(t, err, ErrOverflow)

	for mode := ToNearestAway; mode <= TowardNegative; mode++ {
		got, roundErr := zero.DivRound(one, MaxPrec, mode)
		require.NoError(t, roundErr)
		require.Equal(t, Decimal{}, got)
	}
}

func TestExactErrorRelationships(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, ErrUnderflow, ErrUnderflow)
	require.ErrorIs(t, ErrUnderflow, ErrInexact)
	require.NotErrorIs(t, ErrInexact, ErrUnderflow)
	require.NotErrorIs(t, ErrOverflow, ErrInexact)
	require.NotErrorIs(t, ErrPrecOutOfRange, ErrInvalidRoundingMode)
}

func TestDirectRoundRandomAgainstBigRat(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(0x65786163742d726f, 0x756e642d6f726163))
	for i := 0; i < 12_000; i++ {
		a := exactRandomDecimal(rng, false)
		b := exactRandomDecimal(rng, i%23 == 0)
		places := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		mode := RoundingMode(rng.Uint64N(uint64(TowardNegative) + 1))

		checkDirectRoundOracle(t, "mul", a, b, places, mode, a.MulRound)
		checkDirectRoundOracle(t, "div", a, b, places, mode, a.DivRound)
	}
}

func TestExactRandomAgainstBigRat(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(0x65786163742d6172, 0x6974682d6f726163))
	for i := 0; i < 8_000; i++ {
		a := exactRandomDecimal(rng, false)
		b := exactRandomDecimal(rng, i%19 == 0)

		mulRat := new(big.Rat).Mul(exactDecimalRat(a), exactDecimalRat(b))
		mul, mulErr := a.MulExact(b)
		checkExactOracle(t, "mul", a, b, mulRat, mul, mulErr)

		if b.IsZero() {
			_, divErr := a.DivExact(b)
			require.ErrorIsf(t, divErr, ErrDivideByZero, "a=%+v b=%+v", a, b)
			continue
		}
		divRat := new(big.Rat).Quo(exactDecimalRat(a), exactDecimalRat(b))
		div, divErr := a.DivExact(b)
		checkExactOracle(t, "div", a, b, divRat, div, divErr)
	}
}

func TestExactArithmeticAllocs(t *testing.T) {
	// Keep these serial: AllocsPerRun temporarily changes GOMAXPROCS.
	a := RequireFromString("123456789.123456789")
	b := RequireFromString("7.000000001")
	tiny := RequireFromString("0.0000000000000000001")
	maxDecimal := RequireFromString(exactMaxString)
	onePointOne := RequireFromString("1.1")
	oneTenth := RequireFromString("0.1")
	half := RequireFromString("0.5")
	two := NewFromInt(2)
	three := NewFromInt(3)
	invalid := RoundingMode(255)

	tests := []struct {
		name string
		fn   func() (Decimal, error)
	}{
		{name: "mul_exact_success", fn: func() (Decimal, error) { return a.MulExact(One) }},
		{name: "mul_exact_inexact", fn: func() (Decimal, error) { return tiny.MulExact(onePointOne) }},
		{name: "mul_exact_underflow", fn: func() (Decimal, error) { return tiny.MulExact(oneTenth) }},
		{name: "mul_exact_overflow", fn: func() (Decimal, error) { return maxDecimal.MulExact(two) }},
		{name: "div_exact_success", fn: func() (Decimal, error) { return a.DivExact(One) }},
		{name: "div_exact_inexact", fn: func() (Decimal, error) { return One.DivExact(three) }},
		{name: "div_exact_underflow", fn: func() (Decimal, error) { return tiny.DivExact(two) }},
		{name: "div_exact_overflow", fn: func() (Decimal, error) { return maxDecimal.DivExact(half) }},
		{name: "div_exact_zero", fn: func() (Decimal, error) { return a.DivExact(Zero) }},
		{name: "mul_round", fn: func() (Decimal, error) { return a.MulRound(b, 8, ToNearestEven) }},
		{name: "mul_round_overflow", fn: func() (Decimal, error) { return maxDecimal.MulRound(One, 1, TowardZero) }},
		{name: "div_round", fn: func() (Decimal, error) { return a.DivRound(b, 8, ToNearestEven) }},
		{name: "div_round_overflow", fn: func() (Decimal, error) { return maxDecimal.DivRound(One, 1, TowardZero) }},
		{name: "invalid_mode", fn: func() (Decimal, error) { return a.DivRound(b, 8, invalid) }},
		{name: "invalid_precision", fn: func() (Decimal, error) { return a.DivRound(b, MaxPrec+1, ToNearestEven) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			allocs := testing.AllocsPerRun(1_000, func() {
				exactResultSink, errExactSink = tc.fn()
			})
			require.Zero(t, allocs)
		})
	}
}

const exactMaxString = "340282366920938463463374607431768211455"

func exactRandomDecimal(rng *rand.Rand, allowZero bool) Decimal {
	hi, lo := rng.Uint64(), rng.Uint64()
	if allowZero && rng.Uint64N(8) == 0 {
		hi, lo = 0, 0
	} else if hi|lo == 0 {
		lo = 1
	}
	prec := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
	d, err := NewFromHiLo(rng.Uint64()&1 != 0, hi, lo, prec)
	if err != nil {
		panic(err)
	}
	return d
}

func checkDirectRoundOracle(
	t *testing.T,
	op string,
	a, b Decimal,
	places uint8,
	mode RoundingMode,
	call func(Decimal, uint8, RoundingMode) (Decimal, error),
) {
	t.Helper()
	got, err := call(b, places, mode)
	if op == "div" && b.IsZero() {
		require.ErrorIsf(t, err, ErrDivideByZero, "a=%+v b=%+v", a, b)
		return
	}

	wantRat := new(big.Rat)
	if op == "mul" {
		wantRat.Mul(exactDecimalRat(a), exactDecimalRat(b))
	} else {
		wantRat.Quo(exactDecimalRat(a), exactDecimalRat(b))
	}
	wantCoef, wantNeg := exactRoundRat(wantRat, places, mode)
	if wantCoef.BitLen() > 128 {
		require.ErrorIsf(t, err, ErrOverflow,
			"%s overflow a=%+v b=%+v places=%d mode=%d want=%s", op, a, b, places, mode, wantCoef)
		return
	}
	require.NoErrorf(t, err, "%s a=%+v b=%+v places=%d mode=%d", op, a, b, places, mode)
	want := newDecimal(exactU128FromBig(wantCoef), wantNeg, places)
	require.Equalf(t, want, got,
		"%s a=%+v b=%+v places=%d mode=%d rat=%s", op, a, b, places, mode, wantRat.RatString())
}

func checkExactOracle(t *testing.T, op string, a, b Decimal, want *big.Rat, got Decimal, err error) {
	t.Helper()
	wantErr := exactRepresentabilityError(want)
	if wantErr != nil {
		require.ErrorIsf(t, err, wantErr, "%s a=%+v b=%+v value=%s", op, a, b, want.RatString())
		require.Equal(t, Decimal{}, got)
		return
	}
	require.NoErrorf(t, err, "%s a=%+v b=%+v value=%s", op, a, b, want.RatString())
	require.Equalf(t, 0, exactDecimalRat(got).Cmp(want), "%s a=%+v b=%+v got=%+v", op, a, b, got)
	require.LessOrEqual(t, got.Prec(), MaxPrec)
}

func exactRepresentabilityError(v *big.Rat) error {
	if v.Sign() == 0 {
		return nil
	}
	abs := new(big.Rat).Abs(new(big.Rat).Set(v))
	maxValue := new(big.Rat).SetInt(exactMaxBig())
	if abs.Cmp(maxValue) > 0 {
		return ErrOverflow
	}
	minimum := new(big.Rat).SetFrac(big.NewInt(1), exactPow10(int(MaxPrec)))
	if abs.Cmp(minimum) < 0 {
		return ErrUnderflow
	}
	for p := 0; p <= int(MaxPrec); p++ {
		scaled := new(big.Int).Mul(abs.Num(), exactPow10(p))
		q, r := new(big.Int).QuoRem(scaled, abs.Denom(), new(big.Int))
		if r.Sign() == 0 && q.BitLen() <= 128 {
			return nil
		}
	}
	return ErrInexact
}

func exactRoundRat(v *big.Rat, places uint8, mode RoundingMode) (*big.Int, bool) {
	neg := v.Sign() < 0
	absNum := new(big.Int).Abs(new(big.Int).Set(v.Num()))
	absNum.Mul(absNum, exactPow10(int(places)))
	q, r := new(big.Int).QuoRem(absNum, v.Denom(), new(big.Int))
	twiceR := new(big.Int).Lsh(new(big.Int).Set(r), 1)
	halfCmp := twiceR.Cmp(v.Denom())

	up := false
	switch mode {
	case ToNearestAway:
		up = r.Sign() != 0 && halfCmp >= 0
	case ToNearestEven:
		up = r.Sign() != 0 && (halfCmp > 0 || (halfCmp == 0 && q.Bit(0) != 0))
	case AwayFromZero:
		up = r.Sign() != 0
	case TowardZero:
	case TowardPositive:
		up = r.Sign() != 0 && !neg
	case TowardNegative:
		up = r.Sign() != 0 && neg
	default:
		panic("invalid rounding mode in test oracle")
	}
	if up {
		q.Add(q, big.NewInt(1))
	}
	return q, neg && q.Sign() != 0
}

func exactDecimalRat(d Decimal) *big.Rat {
	n := exactBigFromU128(d.coef)
	if d.neg {
		n.Neg(n)
	}
	return new(big.Rat).SetFrac(n, exactPow10(int(d.prec)))
}

func exactBigFromU128(u u128) *big.Int {
	hi := new(big.Int).SetUint64(u.hi)
	hi.Lsh(hi, 64)
	return hi.Add(hi, new(big.Int).SetUint64(u.lo))
}

func exactU128FromBig(v *big.Int) u128 {
	lo := v.Uint64()
	hi := new(big.Int).Rsh(new(big.Int).Set(v), 64).Uint64()
	return u128{hi: hi, lo: lo}
}

func exactMaxBig() *big.Int {
	return new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))
}

func exactPow10(p int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(p)), nil)
}

func TestExactErrorsAreBareSentinels(t *testing.T) {
	// Pin direct equality as well as errors.Is: hot arithmetic paths return the
	// immutable package sentinels without per-call wrapping.
	tiny := RequireFromString("0.0000000000000000001")
	_, underflow := tiny.MulExact(RequireFromString("0.1"))
	_, inexact := One.DivExact(NewFromInt(3))
	_, invalid := One.DivRound(One, 0, RoundingMode(99))
	require.Equal(t, ErrUnderflow, underflow)
	require.Equal(t, ErrInexact, inexact)
	require.Equal(t, ErrInvalidRoundingMode, invalid)
	require.True(t, errors.Is(underflow, ErrInexact))
}
