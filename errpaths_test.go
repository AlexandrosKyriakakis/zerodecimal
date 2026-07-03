package zerodecimal

import (
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseGeneralFirstScaleOverflow pins the first mul128by64 inside
// parseGeneral's effPrec<0 arm: an exponent whose integer scale-up needs
// more than MaxPrec zeros splits the multiply into a 10^MaxPrec step and a
// remainder step, and the first step overflows when the value crosses
// 2^128-1 = 340282366920938463463374607431768211455.
func TestParseGeneralFirstScaleOverflow(t *testing.T) {
	// 3·10^38 fits: it is the largest control below 2^128-1, hand-verified
	// hi/lo (3e38 = hi·2^64 + lo) so the expectation never calls the parser.
	control := mustHiLo(t, false, 16263032587282566510, 2062198654202019840, 0)

	tests := []struct {
		name    string
		in      string
		want    Decimal
		wantErr error
	}{
		// 4·10^19 (a 20-digit coefficient that fits in 128 bits) scaled by
		// 10^20: up=20 > MaxPrec so the first multiply lifts it by 10^19 to
		// 4·10^38, which exceeds 2^128-1 (≈3.4028·10^38) and overflows.
		{name: "first_multiply_overflows", in: "40000000000000000000e20", wantErr: ErrOverflow},
		// 3.5·10^19 scaled the same way overflows the first multiply by
		// exactly one 2^128 (3.5·10^38 = 2^128 + 9.717…·10^36), and the
		// wrapped remainder times the second step's 10^1 fits in 128 bits —
		// only the first overflow word can reject it.
		{name: "first_multiply_overflow_exact_boundary", in: "35000000000000000000e20", wantErr: ErrOverflow},
		// Control on the passing side of the same code path.
		{name: "largest_scale_up_below_max", in: "3e38", want: control},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewFromString(tc.in)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Equal(t, Decimal{}, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			// Independent big.Rat oracle for the control value.
			want := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil))
			want.Mul(want, big.NewRat(3, 1))
			require.Zero(t, decimalToRat(got).Cmp(want), "3e38 must equal 3·10^38")
		})
	}
}

// TestMustRescale covers the panic arm of MustRescale and its passing path.
// The panic arm forwards Rescale's ErrPrecOutOfRange (prec > MaxPrec); the
// happy path scales 1 to two fractional digits, giving 1.00 = coef 100.
func TestMustRescale(t *testing.T) {
	t.Run("panics_out_of_range", func(t *testing.T) {
		var recovered error
		func() {
			defer func() {
				if r := recover(); r != nil {
					recovered, _ = r.(error)
				}
			}()
			NewFromInt(1).MustRescale(MaxPrec + 1)
			t.Fatal("MustRescale did not panic")
		}()
		require.True(t, errors.Is(recovered, ErrPrecOutOfRange),
			"panic value must wrap ErrPrecOutOfRange, got %v", recovered)
	})

	t.Run("scales_up", func(t *testing.T) {
		want := mustHiLo(t, false, 0, 100, 2) // 1.00
		r, err := NewFromInt(1).Rescale(2)
		require.NoError(t, err)
		require.Equal(t, want, r)
		require.Equal(t, want, NewFromInt(1).MustRescale(2))
	})
}
