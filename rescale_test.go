package zerodecimal

import (
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTrimTables(t *testing.T) {
	tests := []struct {
		name string
		neg  bool
		hi   uint64
		lo   uint64
		prec uint8
		want Decimal
	}{
		{name: "zero", want: Decimal{}},
		{name: "one_trailing_zero", lo: 150, prec: 2, want: mustHiLo(t, false, 0, 15, 1)},
		{name: "all_fractional_zeros", lo: 1000, prec: 3, want: mustHiLo(t, false, 0, 1, 0)},
		{name: "already_canonical", lo: 15, prec: 1, want: mustHiLo(t, false, 0, 15, 1)},
		{name: "integer_untouched", lo: 1500, prec: 0, want: mustHiLo(t, false, 0, 1500, 0)},
		{name: "negative", neg: true, lo: 1500, prec: 3, want: mustHiLo(t, true, 0, 15, 1)},
		// 10^20 = {hi: 5, lo: 0x6BC75E2D63100000}: the trim must shed digits
		// through the two-limb path before the one-limb tail finishes.
		{name: "two_limb_trailing_zero", hi: 5, lo: 0x6BC75E2D63100000, prec: 19, want: mustHiLo(t, false, 0, 10, 0)},
		{name: "max_coef_no_zeros", hi: maxUint64, lo: maxUint64, prec: 19, want: mustHiLo(t, false, maxUint64, maxUint64, 19)},
		{name: "prec_19_strips_to_0", lo: pow10u64[19], prec: 19, want: mustHiLo(t, false, 0, 1, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := mustHiLo(t, tc.neg, tc.hi, tc.lo, tc.prec)
			got := d.Trim()
			require.Equal(t, tc.want, got)
			require.True(t, got.Equal(d), "trim must preserve the value")
			require.Equal(t, got, got.Trim(), "trim must be idempotent")
		})
	}
}

func TestTrimRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x7217, 0x0CA0))
	for range 20_000 {
		d := newDecimal(randShapedU128(rng), rng.Uint64()&1 == 1, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		got := d.Trim()
		require.Zerof(t, decimalToRat(d).Cmp(decimalToRat(got)), "trim preserves the value: d=%+v got=%+v", d, got)
		require.Equalf(t, got, got.Trim(), "trim idempotence: d=%+v", d)
		if got.IsZero() {
			require.Equalf(t, Decimal{}, got, "trim zero must be canonical: d=%+v", d)
		} else if got.Prec() > 0 {
			// Minimality: a trimmed coefficient behind a nonzero precision
			// must not divide by ten, or a shorter representation existed.
			_, r := divmod128Pow10(got.coef, 1)
			require.NotZerof(t, r, "trim must reach the minimal precision: d=%+v got=%+v", d, got)
		}
		if d.Prec() < MaxPrec {
			if scaled, over := mul128by64(d.coef, 10); over == 0 {
				// The widened twin denotes the same number, so it must trim
				// to the identical — ==-comparable — Decimal.
				e := newDecimal(scaled, d.neg, d.Prec()+1)
				require.Equalf(t, got, e.Trim(), "equal values must trim to one representation: d=%+v e=%+v", d, e)
			}
		}
	}
}

func TestRescaleTables(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		prec    uint8
		want    Decimal
		wantErr error
	}{
		{name: "raise_1_5_to_2", in: "1.5", prec: 2, want: mustHiLo(t, false, 0, 150, 2)},
		{name: "noop_same_prec", in: "1.5", prec: 1, want: mustHiLo(t, false, 0, 15, 1)},
		{name: "lower_ties_even_down", in: "2.5", prec: 0, want: RequireFromString("2")},
		{name: "lower_ties_even_up", in: "3.5", prec: 0, want: RequireFromString("4")},
		{name: "lower_neg_tie", in: "-2.5", prec: 0, want: RequireFromString("-2")},
		{name: "prec_out_of_range", in: "1.5", prec: 20, wantErr: ErrPrecOutOfRange},
		{name: "prec_255", in: "1.5", prec: 255, wantErr: ErrPrecOutOfRange},
		{name: "raise_overflow", in: "340282366920938463463374607431768211455", prec: 1, wantErr: ErrOverflow},
		{name: "noop_max_at_own_prec", in: "34028236692093846346.3374607431768211455", prec: 19, want: RequireFromString("34028236692093846346.3374607431768211455")},
		// Pins the canonical-zero decision: a zero never carries the
		// requested precision.
		{name: "zero_stays_canonical", in: "0", prec: 5, want: Decimal{}},
		{name: "raise_to_max_prec", in: "1", prec: 19, want: mustHiLo(t, false, 0, pow10u64[19], 19)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString(tc.in)
			got, err := d.Rescale(tc.prec)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, Decimal{}, got, "failed Rescale must return the zero value")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			if !got.IsZero() {
				require.Equal(t, tc.prec, got.Prec())
			}
		})
	}
}

func TestRescaleRaiseOverflowBoundary(t *testing.T) {
	// ⌊(2^128-1)/10⌋ at prec 1 is the largest coefficient a one-digit raise
	// still fits; one more unit must overflow.
	fits := mustHiLo(t, false, 0x1999999999999999, 0x9999999999999999, 1)
	got, err := fits.Rescale(2)
	require.NoError(t, err)
	require.Equal(t, uint8(2), got.Prec())
	require.True(t, got.Equal(fits), "raising must preserve the value")

	over := mustHiLo(t, false, 0x1999999999999999, 0x999999999999999A, 1)
	_, err = over.Rescale(2)
	require.ErrorIs(t, err, ErrOverflow)
}

func TestRescaleLoweringEqualsRoundBank(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xBEEF, 0xCAFE))
	for range 20_000 {
		d := newDecimal(randShapedU128(rng), rng.Uint64()&1 == 1, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		prec := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		got, err := d.Rescale(prec)
		if prec < d.Prec() {
			require.NoErrorf(t, err, "rescale lower: d=%+v prec=%d", d, prec)
			require.Equalf(t, d.RoundBank(prec), got, "rescale lowering must equal RoundBank: d=%+v prec=%d", d, prec)
		} else {
			exact := new(big.Int).Mul(u128ToBig(d.coef), bp10(int(prec-d.Prec())))
			if exact.Cmp(mod128big) >= 0 {
				require.ErrorIsf(t, err, ErrOverflow, "rescale raise overflow oracle: d=%+v prec=%d", d, prec)
				continue
			}
			require.NoErrorf(t, err, "rescale raise: d=%+v prec=%d", d, prec)
			require.Zerof(t, decimalToRat(d).Cmp(decimalToRat(got)), "raising preserves the value: d=%+v got=%+v", d, got)
			if got.IsZero() {
				require.Equalf(t, Decimal{}, got, "zero must be canonical: d=%+v prec=%d", d, prec)
			} else {
				require.Equalf(t, prec, got.Prec(), "raised precision: d=%+v prec=%d", d, prec)
			}
		}
		require.Truef(t, got.Equal(d.RoundBank(min(prec, d.Prec()))),
			"rescale value vs RoundBank: d=%+v prec=%d", d, prec)
	}
}
