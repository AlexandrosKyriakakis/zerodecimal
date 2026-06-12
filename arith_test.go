package zerodecimal

import (
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mustFromBig builds a Decimal whose coefficient is the (nonnegative)
// big.Int coef, failing the test if it does not fit 128 bits.
func mustFromBig(t *testing.T, neg bool, coef *big.Int, prec uint8) Decimal {
	t.Helper()
	u := bigToU128(t, coef)
	return mustHiLo(t, neg, u.hi, u.lo, prec)
}

// randDecimal returns a canonical Decimal with shaped coefficient limbs and
// precision biased toward the 0 and MaxPrec extremes.
func randDecimal(rng *rand.Rand) Decimal {
	var prec uint8
	switch rng.IntN(4) {
	case 0:
		prec = 0
	case 1:
		prec = MaxPrec
	default:
		prec = uint8(rng.Uint64N(uint64(MaxPrec) + 1))
	}
	return newDecimal(randShapedU128(rng), rng.Uint64()&1 == 1, prec)
}

func TestBitlen10Table(t *testing.T) {
	for f := range len(bitlen10) {
		assert.Equal(t, pow10Big(f).BitLen(), int(bitlen10[f]), "bitlen10[%d]", f)
	}
}

func TestAddSamePrecision(t *testing.T) {
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	tests := []struct {
		name     string
		a, b     Decimal
		want     string
		wantPrec uint8
		wantErr  error
	}{
		{name: "int_plus_int", a: NewFromInt(2), b: NewFromInt(3), want: "5", wantPrec: 0},
		{name: "price_plus_price", a: RequireFromString("1234.5678"), b: RequireFromString("0.0322"), want: "1234.6", wantPrec: 4},
		{name: "negatives_accumulate", a: RequireFromString("-1.5"), b: RequireFromString("-2.5"), want: "-4", wantPrec: 1},
		{name: "zero_plus_zero", a: Zero, b: Zero, want: "0", wantPrec: 0},
		{name: "zero_plus_int", a: Zero, b: NewFromInt(7), want: "7", wantPrec: 0},
		{name: "sum_just_fits_2_128_minus_1", a: Decimal{coef: u128{hi: maxUint64, lo: maxUint64 - 1}}, b: One, want: "340282366920938463463374607431768211455", wantPrec: 0},
		{name: "sum_exactly_2_128_overflows", a: Decimal{coef: maxCoef}, b: One, wantErr: ErrOverflow},
		{name: "two_pow127_plus_two_pow127_overflows", a: Decimal{coef: u128{hi: 1 << 63}}, b: Decimal{coef: u128{hi: 1 << 63}}, wantErr: ErrOverflow},
		{name: "negative_sum_overflows", a: Decimal{coef: maxCoef, neg: true}, b: NewFromInt(-1), wantErr: ErrOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.a.Add(tc.b)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Equal(t, Decimal{}, got, "errors must return the zero Decimal")
				return
			}
			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, tc.wantPrec, got.Prec())
		})
	}
}

func TestAddMixedSignSamePrecision(t *testing.T) {
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	tests := []struct {
		name string
		a, b Decimal
		want string
	}{
		{name: "larger_positive_wins", a: NewFromInt(5), b: NewFromInt(-3), want: "2"},
		{name: "larger_negative_wins_borrow_path", a: NewFromInt(3), b: NewFromInt(-5), want: "-2"},
		{name: "fractional_sign_crossing", a: RequireFromString("-1.25"), b: RequireFromString("0.25"), want: "-1"},
		{name: "exact_cancel_is_canonical_zero", a: RequireFromString("-3.5"), b: RequireFromString("3.5"), want: "0"},
		{name: "max_coef_cancel", a: Decimal{coef: maxCoef, neg: true}, b: Decimal{coef: maxCoef}, want: "0"},
		{name: "mixed_sign_never_overflows", a: Decimal{coef: maxCoef}, b: NewFromInt(-1), want: "340282366920938463463374607431768211454"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.a.Add(tc.b)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.String())
			if got.IsZero() {
				assert.Equal(t, Decimal{}, got, "zero results must be canonical")
			}
		})
	}
}

func TestAddUnalignedPrecision(t *testing.T) {
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	// 3.5e37 at precision 0: widened by one digit it exceeds 2^128, so only
	// the exact 192-bit difference decides overflow.
	wide := RequireFromString("35000000000000000000000000000000000000")
	tests := []struct {
		name     string
		a, b     Decimal
		want     string
		wantPrec uint8
		wantErr  error
	}{
		{name: "smallest_unit_lands_in_widened_gap", a: RequireFromString("1.5"), b: RequireFromString("0.0000000000000000001"), want: "1.5000000000000000001", wantPrec: 19},
		{name: "same_sign_simple", a: RequireFromString("1.5"), b: RequireFromString("0.25"), want: "1.75", wantPrec: 2},
		{name: "mixed_sign_lower_prec_wins", a: RequireFromString("1.5"), b: RequireFromString("-0.25"), want: "1.25", wantPrec: 2},
		{name: "mixed_sign_higher_prec_wins", a: RequireFromString("0.25"), b: RequireFromString("-1.5"), want: "-1.25", wantPrec: 2},
		{name: "zero_keeps_other_precision", a: Zero, b: RequireFromString("0.25"), want: "0.25", wantPrec: 2},
		{name: "cancel_across_precisions", a: RequireFromString("1.5"), b: mustHiLo(t, true, 0, 150, 2), want: "0", wantPrec: 0},
		{name: "same_sign_widened_overflow", a: Decimal{coef: maxCoef}, b: RequireFromString("0.1"), wantErr: ErrOverflow},
		{
			name: "scaled_operand_overflows_but_difference_fits",
			a:    wide, b: mustFromBig(t, true, pow10Big(37), 1),
			want: "34000000000000000000000000000000000000", wantPrec: 1,
		},
		{
			name: "difference_exceeding_2_128_overflows",
			a:    wide, b: mustFromBig(t, true, new(big.Int).Mul(big.NewInt(9), pow10Big(36)), 1),
			wantErr: ErrOverflow,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.a.Add(tc.b)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				return
			}
			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, tc.wantPrec, got.Prec())
		})
	}
}

func TestSub(t *testing.T) {
	maxCoef := u128{hi: maxUint64, lo: maxUint64}
	negA := RequireFromString("-12.75")
	tests := []struct {
		name     string
		a, b     Decimal
		want     string
		wantPrec uint8
		wantErr  error
	}{
		{name: "int_minus_int", a: NewFromInt(5), b: NewFromInt(3), want: "2", wantPrec: 0},
		{name: "result_crosses_zero", a: NewFromInt(3), b: NewFromInt(5), want: "-2", wantPrec: 0},
		{name: "minus_negative_adds_magnitudes", a: NewFromInt(3), b: NewFromInt(-5), want: "8", wantPrec: 0},
		{name: "negative_self_cancel_is_canonical_zero", a: negA, b: negA, want: "0", wantPrec: 0},
		{name: "subtract_zero_is_identity", a: negA, b: Zero, want: "-12.75", wantPrec: 2},
		{name: "zero_minus_x_negates", a: Zero, b: negA, want: "12.75", wantPrec: 2},
		{name: "unaligned_precisions", a: RequireFromString("1.5"), b: RequireFromString("0.0000000000000000001"), want: "1.4999999999999999999", wantPrec: 19},
		{name: "opposite_sign_magnitude_add_overflows", a: Decimal{coef: maxCoef}, b: NewFromInt(-1), wantErr: ErrOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.a.Sub(tc.b)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				return
			}
			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, tc.wantPrec, got.Prec())
			if got.IsZero() {
				assert.Equal(t, Decimal{}, got, "zero results must be canonical")
			}
		})
	}
}

// requireAddSubMatchOracle checks Add and Sub of (a, b) against exact big.Rat
// arithmetic, including the exact overflow contract: ErrOverflow iff the
// exact coefficient at max(a.prec, b.prec) is ≥ 2^128.
func requireAddSubMatchOracle(t *testing.T, a, b Decimal) {
	t.Helper()
	ops := []struct {
		name string
		got  func() (Decimal, error)
		want *big.Rat
	}{
		{"add", func() (Decimal, error) { return a.Add(b) }, new(big.Rat).Add(decimalToRat(a), decimalToRat(b))},
		{"sub", func() (Decimal, error) { return a.Sub(b) }, new(big.Rat).Sub(decimalToRat(a), decimalToRat(b))},
	}
	prec := max(a.Prec(), b.Prec())
	scale := pow10Big(int(prec))
	for _, op := range ops {
		coef := new(big.Int).Mul(op.want.Num(), scale)
		coef.Quo(coef, op.want.Denom()) // exact: the denominator divides 10^prec
		coef.Abs(coef)

		got, err := op.got()
		if coef.BitLen() > 128 {
			require.ErrorIs(t, err, ErrOverflow, "%s of %s and %s must overflow", op.name, a, b)
			continue
		}
		require.NoError(t, err, "%s of %s and %s", op.name, a, b)
		require.Zero(t, op.want.Cmp(decimalToRat(got)), "%s of %s and %s: want %s, got %s", op.name, a, b, op.want, got)
		if coef.Sign() == 0 {
			require.Equal(t, Decimal{}, got, "%s of %s and %s: zero must be canonical", op.name, a, b)
		} else {
			require.Equal(t, prec, got.Prec(), "%s of %s and %s", op.name, a, b)
		}
	}
}

func TestAddSubRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xADD, 0x0A01))
	for range 30_000 {
		requireAddSubMatchOracle(t, randDecimal(rng), randDecimal(rng))
	}
}

func TestMul(t *testing.T) {
	tests := []struct {
		name     string
		a, b     Decimal
		want     string
		wantPrec uint8
		wantErr  error
	}{
		{name: "zero_times_negative_is_canonical_zero", a: Zero, b: RequireFromString("-123.45"), want: "0", wantPrec: 0},
		{name: "negative_times_zero_is_canonical_zero", a: RequireFromString("-123.45"), b: Zero, want: "0", wantPrec: 0},
		{name: "one_is_identity", a: One, b: RequireFromString("1.5"), want: "1.5", wantPrec: 1},
		{name: "minus_one_negates", a: NewFromInt(-1), b: RequireFromString("1.5"), want: "-1.5", wantPrec: 1},
		{name: "precisions_sum", a: RequireFromString("1.5"), b: RequireFromString("0.25"), want: "0.375", wantPrec: 3},
		{name: "sign_rule_pos_neg", a: RequireFromString("1.5"), b: NewFromInt(-2), want: "-3", wantPrec: 1},
		{name: "sign_rule_neg_neg", a: RequireFromString("-1.5"), b: NewFromInt(-2), want: "3", wantPrec: 1},
		{
			name: "excess_precision_truncates_toward_zero",
			a:    RequireFromString("0.1234567890123456789"), b: RequireFromString("0.9"),
			want: "0.111111110111111111", wantPrec: 19,
		},
		{
			name: "negative_excess_precision_truncates_toward_zero",
			a:    RequireFromString("-0.1234567890123456789"), b: RequireFromString("0.9"),
			want: "-0.111111110111111111", wantPrec: 19,
		},
		{
			name: "tiny_product_truncates_to_canonical_zero",
			a:    mustHiLo(t, false, 0, 1, 10), b: mustHiLo(t, true, 0, 1, 11),
			want: "0", wantPrec: 0,
		},
		{
			name: "one_limb_inputs_two_limb_product_rescales",
			a:    mustHiLo(t, false, 0, 4_000_000_000, 10), b: mustHiLo(t, false, 0, 5_000_000_000, 10),
			want: "0.2", wantPrec: 19,
		},
		{
			name: "max128_fits_exactly",
			a:    mustHiLo(t, false, 0, maxUint64, 0), b: mustHiLo(t, false, 1, 1, 0),
			want: "340282366920938463463374607431768211455", wantPrec: 0,
		},
		{name: "pow2_128_overflows", a: mustHiLo(t, false, 1, 0, 0), b: mustHiLo(t, false, 1, 0, 0), wantErr: ErrOverflow},
		{
			// 10(2^64-1) · (2^64+1) = 10(2^128-1) at 20 fractional digits:
			// the one-digit rescale lands exactly on the max coefficient.
			name: "rescaled_product_just_fits",
			a:    mustHiLo(t, false, 9, maxUint64-9, 1), b: mustHiLo(t, false, 1, 1, 19),
			want: "34028236692093846346.3374607431768211455", wantPrec: 19,
		},
		{
			// 10·2^64 · 2^64 = 10·2^128: the rescaled quotient is exactly
			// 2^128 and must overflow.
			name: "rescaled_product_just_overflows",
			a:    mustHiLo(t, false, 10, 0, 1), b: mustHiLo(t, false, 1, 0, 19),
			wantErr: ErrOverflow,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.a.Mul(tc.b)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Equal(t, Decimal{}, got, "errors must return the zero Decimal")
				return
			}
			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, tc.wantPrec, got.Prec())
			if got.IsZero() {
				assert.Equal(t, Decimal{}, got, "zero results must be canonical")
			}
		})
	}
}

func TestMulRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x111A, 0x0A02))
	for range 30_000 {
		a, b := randDecimal(rng), randDecimal(rng)

		prec := min(a.Prec()+b.Prec(), DefaultPrec)
		prod := new(big.Rat).Mul(decimalToRat(a), decimalToRat(b))
		coef := new(big.Int).Mul(new(big.Int).Abs(prod.Num()), pow10Big(int(prec)))
		coef.Quo(coef, prod.Denom()) // truncation toward zero on |product|

		got, err := a.Mul(b)
		if coef.BitLen() > 128 {
			require.ErrorIs(t, err, ErrOverflow, "%s * %s must overflow", a, b)
			continue
		}
		require.NoError(t, err, "%s * %s", a, b)
		want := mustFromBig(t, a.IsNegative() != b.IsNegative() && coef.Sign() != 0, coef, prec)
		require.Equal(t, want, got, "%s * %s", a, b)
	}
}

func TestDiv(t *testing.T) {
	maxInt := mustHiLo(t, false, maxUint64, maxUint64, 0)
	tests := []struct {
		name     string
		a, b     Decimal
		want     string
		wantPrec uint8
		wantErr  error
	}{
		{name: "divide_by_zero", a: NewFromInt(1), b: Zero, wantErr: ErrDivideByZero},
		{name: "zero_dividend_is_canonical_zero", a: Zero, b: NewFromInt(-3), want: "0", wantPrec: 0},
		{name: "one_third_truncates_at_default_prec", a: One, b: NewFromInt(3), want: "0.3333333333333333333", wantPrec: 19},
		{name: "exact_eighth_keeps_default_prec", a: One, b: NewFromInt(8), want: "0.125", wantPrec: 19},
		{name: "seven_halves", a: NewFromInt(7), b: NewFromInt(2), want: "3.5", wantPrec: 19},
		{name: "sign_rule_neg_pos", a: NewFromInt(-7), b: NewFromInt(2), want: "-3.5", wantPrec: 19},
		{name: "sign_rule_pos_neg", a: NewFromInt(7), b: NewFromInt(-2), want: "-3.5", wantPrec: 19},
		{name: "sign_rule_neg_neg", a: NewFromInt(-7), b: NewFromInt(-2), want: "3.5", wantPrec: 19},
		{name: "max_prec_operands", a: RequireFromString("0.0000000000000000007"), b: RequireFromString("0.0000000000000000002"), want: "3.5", wantPrec: 19},
		{
			// 10^30 with 8 more digits saturates the 38-significant-digit
			// budget: the adaptive precision lands exactly at p = 8.
			name: "huge_dividend_degrades_precision",
			a:    RequireFromString("1000000000000000000000000000000"), b: One,
			want: "1000000000000000000000000000000", wantPrec: 8,
		},
		{
			name: "max_coefficient_integer_quotient_pins_p_0",
			a:    maxInt, b: One,
			want: "340282366920938463463374607431768211455", wantPrec: 0,
		},
		{
			name: "max_coefficient_at_max_prec_divides_exactly",
			a:    mustHiLo(t, false, maxUint64, maxUint64, 19), b: NewFromInt(3),
			want: "11342745564031282115.4458202477256070485", wantPrec: 19,
		},
		{
			// 2^127/7 fits at p = 1 but the bit-length estimate stops at
			// p = 0: the result must still come back at the promoted p = 1.
			name: "probe_promotes_past_conservative_estimate",
			a:    mustHiLo(t, false, 1<<63, 0, 0), b: NewFromInt(7),
			want: "24305883351495604533098186245126300818.2", wantPrec: 1,
		},
		{name: "tiny_over_huge_is_canonical_zero", a: One, b: mustHiLo(t, false, 1<<63, 0, 0), want: "0", wantPrec: 0},
		{name: "negative_zero_quotient_is_canonical", a: NewFromInt(-1), b: mustHiLo(t, false, 1<<63, 0, 0), want: "0", wantPrec: 0},
		{name: "integer_quotient_overflow", a: maxInt, b: RequireFromString("0.1"), wantErr: ErrOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.a.Div(tc.b)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Equal(t, Decimal{}, got, "errors must return the zero Decimal")
				return
			}
			assert.Equal(t, tc.want, got.String())
			assert.Equal(t, tc.wantPrec, got.Prec())
			if got.IsZero() {
				assert.Equal(t, Decimal{}, got, "zero results must be canonical")
			}
		})
	}
}

func TestDivCoefAtNegativeFactor(t *testing.T) {
	// Direct unit coverage of divCoefAt's f < 0 arm, which only Div on
	// reduced-DefaultPrec builds reaches through the public API: the divisor
	// scales by 10^-f instead of the numerator.
	tests := []struct {
		name string
		dCo  u128
		eCo  u128
		p    int
	}{
		{name: "scaled_divisor_one_limb", dCo: u128{hi: maxUint64, lo: maxUint64}, eCo: u128{lo: 5}, p: 0},
		{name: "scaled_divisor_two_limbs", dCo: u128{hi: maxUint64, lo: maxUint64}, eCo: u128{lo: 1 << 60}, p: 0},
		{name: "scaled_divisor_overflows_quotient_zero", dCo: u128{hi: maxUint64, lo: maxUint64}, eCo: u128{hi: 1 << 56}, p: 0},
		{name: "partial_scale", dCo: u128{lo: 123456789123456789}, eCo: u128{lo: 7}, p: 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := mustHiLo(t, false, tc.dCo.hi, tc.dCo.lo, 19)
			e := mustHiLo(t, false, tc.eCo.hi, tc.eCo.lo, 0)
			require.Negative(t, tc.p+0-19, "case must exercise f < 0")

			got, ok := divCoefAt(d, e, tc.p)
			require.True(t, ok, "f < 0 quotients always fit")

			// Oracle at a common scale: q = trunc(d.coef·10^p / e.coef·10^19).
			num := new(big.Int).Mul(u128ToBig(tc.dCo), pow10Big(tc.p))
			den := new(big.Int).Mul(u128ToBig(tc.eCo), pow10Big(19))
			requireU128EqualsBig(t, num.Quo(num, den), got)
		})
	}
}

func TestDivRandomAdaptivePrecision(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xD1F, 0x0A03))
	for range 20_000 {
		a, b := randDecimal(rng), randDecimal(rng)
		if b.IsZero() {
			b = One
		}

		// Oracle: coefficient at p is trunc(a.coef·10^(p+b.prec) / b.coef·10^a.prec);
		// nested truncation makes every lower p a 10^k truncation of p = 19.
		_, aHi, aLo, aPrec := a.ToHiLo()
		_, bHi, bLo, bPrec := b.ToHiLo()
		num := new(big.Int).Mul(u128ToBig(u128{hi: aHi, lo: aLo}), pow10Big(int(DefaultPrec)+int(bPrec)))
		den := new(big.Int).Mul(u128ToBig(u128{hi: bHi, lo: bLo}), pow10Big(int(aPrec)))
		coef := num.Quo(num, den)

		p := int(DefaultPrec)
		ten := big.NewInt(10)
		for p > 0 && coef.BitLen() > 128 {
			coef.Quo(coef, ten)
			p--
		}

		got, err := a.Div(b)
		if coef.BitLen() > 128 {
			require.ErrorIs(t, err, ErrOverflow, "%s / %s must overflow", a, b)
			continue
		}
		require.NoError(t, err, "%s / %s", a, b)
		want := mustFromBig(t, a.IsNegative() != b.IsNegative() && coef.Sign() != 0, coef, uint8(p))
		require.Equal(t, want, got, "%s / %s: largest fitting p is %d", a, b, p)
	}
}

func TestQuoRem(t *testing.T) {
	maxInt := mustHiLo(t, false, maxUint64, maxUint64, 0)
	tests := []struct {
		name      string
		a, b      Decimal
		wantQ     string
		wantR     string
		wantRPrec uint8
		wantErr   error
	}{
		{name: "divide_by_zero", a: NewFromInt(7), b: Zero, wantErr: ErrDivideByZero},
		{name: "pos_pos", a: NewFromInt(7), b: NewFromInt(2), wantQ: "3", wantR: "1"},
		{name: "neg_pos_truncated_quotient", a: NewFromInt(-7), b: NewFromInt(2), wantQ: "-3", wantR: "-1"},
		{name: "pos_neg_truncated_quotient", a: NewFromInt(7), b: NewFromInt(-2), wantQ: "-3", wantR: "1"},
		{name: "neg_neg", a: NewFromInt(-7), b: NewFromInt(-2), wantQ: "3", wantR: "-1"},
		{name: "exact_division_zero_remainder", a: NewFromInt(4), b: NewFromInt(2), wantQ: "2", wantR: "0"},
		{name: "fractional_remainder", a: RequireFromString("7.5"), b: NewFromInt(2), wantQ: "3", wantR: "1.5", wantRPrec: 1},
		{name: "dividend_smaller_than_divisor", a: RequireFromString("0.5"), b: NewFromInt(2), wantQ: "0", wantR: "0.5", wantRPrec: 1},
		{name: "negative_fractional", a: RequireFromString("-7.5"), b: NewFromInt(2), wantQ: "-3", wantR: "-1.5", wantRPrec: 1},
		// (2^128-1) ÷ (2^64/10): num = 10·2^128 - 10, den = 2^64, so
		// q = 10·2^64 - 1 and r = 2^64 - 10 at the common scale f = 1.
		{name: "two_limb_divisor", a: maxInt, b: mustHiLo(t, false, 1, 0, 1), wantQ: "184467440737095516159", wantR: "1844674407370955160.6", wantRPrec: 1},
		{name: "quotient_overflows", a: maxInt, b: RequireFromString("0.1"), wantErr: ErrOverflow},
		{name: "aligned_divisor_overflows", a: RequireFromString("0.0000000000000000001"), b: mustHiLo(t, false, 1<<63, 0, 0), wantErr: ErrOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, r, err := tc.a.QuoRem(tc.b)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				assert.Equal(t, Decimal{}, q)
				assert.Equal(t, Decimal{}, r)
				return
			}
			assert.Equal(t, tc.wantQ, q.String(), "quotient")
			assert.Equal(t, uint8(0), q.Prec(), "quotient precision")
			assert.Equal(t, tc.wantR, r.String(), "remainder")
			if !r.IsZero() {
				assert.Equal(t, tc.wantRPrec, r.Prec(), "remainder precision")
			} else {
				assert.Equal(t, Decimal{}, r, "zero remainder must be canonical")
			}
		})
	}
}

func TestQuoRemRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x9047, 0x0A04))
	for range 20_000 {
		a, b := randDecimal(rng), randDecimal(rng)
		if b.IsZero() {
			b = One
		}
		_, aHi, aLo, aPrec := a.ToHiLo()
		_, bHi, bLo, bPrec := b.ToHiLo()
		f := max(aPrec, bPrec)

		num := new(big.Int).Mul(u128ToBig(u128{hi: aHi, lo: aLo}), pow10Big(int(f-aPrec)))
		den := new(big.Int).Mul(u128ToBig(u128{hi: bHi, lo: bLo}), pow10Big(int(f-bPrec)))

		q, r, err := a.QuoRem(b)
		qBig, rBig := new(big.Int).QuoRem(num, den, new(big.Int))
		if den.BitLen() > 128 || qBig.BitLen() > 128 {
			require.ErrorIs(t, err, ErrOverflow, "%s quorem %s must overflow", a, b)
			continue
		}
		require.NoError(t, err, "%s quorem %s", a, b)

		wantQ := mustFromBig(t, a.IsNegative() != b.IsNegative() && qBig.Sign() != 0, qBig, 0)
		wantR := mustFromBig(t, a.IsNegative() && rBig.Sign() != 0, rBig, f)
		require.Equal(t, wantQ, q, "quotient of %s quorem %s", a, b)
		require.Equal(t, wantR, r, "remainder of %s quorem %s", a, b)

		// Identity d = q·e + r, exactly.
		recon := new(big.Rat).Add(new(big.Rat).Mul(decimalToRat(q), decimalToRat(b)), decimalToRat(r))
		require.Zero(t, recon.Cmp(decimalToRat(a)), "identity for %s quorem %s", a, b)
		// |r| < |e|.
		require.Negative(t, r.Abs().Cmp(b.Abs()), "remainder bound for %s quorem %s", a, b)

		m, err := a.Mod(b)
		require.NoError(t, err)
		require.Equal(t, r, m, "Mod must match QuoRem remainder")
	}
}

func TestMod(t *testing.T) {
	tests := []struct {
		name    string
		a, b    Decimal
		want    string
		wantErr error
	}{
		{name: "divide_by_zero", a: NewFromInt(7), b: Zero, wantErr: ErrDivideByZero},
		{name: "sign_follows_dividend", a: NewFromInt(-7), b: NewFromInt(2), want: "-1"},
		{name: "positive_dividend_negative_divisor", a: NewFromInt(7), b: NewFromInt(-2), want: "1"},
		{name: "fractional", a: RequireFromString("7.5"), b: NewFromInt(2), want: "1.5"},
		{name: "exact_is_canonical_zero", a: NewFromInt(6), b: NewFromInt(3), want: "0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.a.Mod(tc.b)
			require.ErrorIs(t, err, tc.wantErr)
			if tc.wantErr != nil {
				return
			}
			assert.Equal(t, tc.want, got.String())
		})
	}
}

func TestMustTwins(t *testing.T) {
	maxInt := mustHiLo(t, false, maxUint64, maxUint64, 0)

	t.Run("success_passthrough", func(t *testing.T) {
		assert.Equal(t, "5", NewFromInt(2).MustAdd(NewFromInt(3)).String())
		assert.Equal(t, "-1", NewFromInt(2).MustSub(NewFromInt(3)).String())
		assert.Equal(t, "6", NewFromInt(2).MustMul(NewFromInt(3)).String())
		assert.Equal(t, "3.5", NewFromInt(7).MustDiv(NewFromInt(2)).String())
		q, r := NewFromInt(7).MustQuoRem(NewFromInt(2))
		assert.Equal(t, "3", q.String())
		assert.Equal(t, "1", r.String())
		assert.Equal(t, "1", NewFromInt(7).MustMod(NewFromInt(2)).String())
		assert.Equal(t, "6", MustSum(NewFromInt(1), NewFromInt(2), NewFromInt(3)).String())
		assert.Equal(t, "1.5", MustAvg(NewFromInt(1), NewFromInt(2)).String())
	})

	t.Run("must_add_panics_on_overflow", func(t *testing.T) {
		require.Panics(t, func() { maxInt.MustAdd(One) })
	})
	t.Run("must_sub_panics_on_overflow", func(t *testing.T) {
		require.Panics(t, func() { maxInt.MustSub(NewFromInt(-1)) })
	})
	t.Run("must_mul_panics_on_overflow", func(t *testing.T) {
		two64 := mustHiLo(t, false, 1, 0, 0)
		require.Panics(t, func() { two64.MustMul(two64) })
	})
	t.Run("must_div_panics_on_zero_divisor", func(t *testing.T) {
		require.Panics(t, func() { One.MustDiv(Zero) })
	})
	t.Run("must_quorem_panics_on_zero_divisor", func(t *testing.T) {
		require.Panics(t, func() { One.MustQuoRem(Zero) })
	})
	t.Run("must_mod_panics_on_zero_divisor", func(t *testing.T) {
		require.Panics(t, func() { One.MustMod(Zero) })
	})
	t.Run("must_sum_panics_on_overflow", func(t *testing.T) {
		require.Panics(t, func() { MustSum(maxInt, One) })
	})
	t.Run("must_avg_panics_on_overflow", func(t *testing.T) {
		require.Panics(t, func() { MustAvg(maxInt, maxInt) })
	})
}

func TestMinMax(t *testing.T) {
	tests := []struct {
		name    string
		first   Decimal
		rest    []Decimal
		wantMin string
		wantMax string
	}{
		{name: "single_argument", first: RequireFromString("-1.5"), wantMin: "-1.5", wantMax: "-1.5"},
		{
			name:  "mixed_signs",
			first: RequireFromString("1.2"), rest: []Decimal{RequireFromString("-0.5"), NewFromInt(3)},
			wantMin: "-0.5", wantMax: "3",
		},
		{
			name:  "cross_precision_compare_by_value",
			first: RequireFromString("0.0000000000000000001"), rest: []Decimal{NewFromInt(-1), RequireFromString("0.1")},
			wantMin: "-1", wantMax: "0.1",
		},
		{
			name:  "all_negative",
			first: NewFromInt(-7), rest: []Decimal{RequireFromString("-7.0001"), RequireFromString("-6.9999")},
			wantMin: "-7.0001", wantMax: "-6.9999",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantMin, Min(tc.first, tc.rest...).String(), "Min")
			assert.Equal(t, tc.wantMax, Max(tc.first, tc.rest...).String(), "Max")
		})
	}
}

func TestSumAvg(t *testing.T) {
	maxInt := mustHiLo(t, false, maxUint64, maxUint64, 0)

	t.Run("sum_of_single_value_is_identity", func(t *testing.T) {
		got, err := Sum(RequireFromString("-1.5"))
		require.NoError(t, err)
		assert.Equal(t, RequireFromString("-1.5"), got)
	})
	t.Run("sum_folds_mixed_precisions", func(t *testing.T) {
		got, err := Sum(NewFromInt(1), RequireFromString("0.5"), RequireFromString("-0.25"))
		require.NoError(t, err)
		assert.Equal(t, "1.25", got.String())
	})
	t.Run("sum_overflow", func(t *testing.T) {
		_, err := Sum(maxInt, Zero, One)
		require.ErrorIs(t, err, ErrOverflow)
	})
	t.Run("avg_of_1_and_2_is_1_5", func(t *testing.T) {
		got, err := Avg(NewFromInt(1), NewFromInt(2))
		require.NoError(t, err)
		assert.Equal(t, "1.5", got.String())
		assert.Equal(t, DefaultPrec, got.Prec())
	})
	t.Run("avg_truncates_at_default_prec", func(t *testing.T) {
		got, err := Avg(NewFromInt(1), NewFromInt(2), NewFromInt(4))
		require.NoError(t, err)
		assert.Equal(t, "2.3333333333333333333", got.String())
	})
	t.Run("avg_sum_overflow", func(t *testing.T) {
		_, err := Avg(maxInt, maxInt)
		require.ErrorIs(t, err, ErrOverflow)
	})
}
