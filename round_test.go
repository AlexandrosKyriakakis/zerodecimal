package zerodecimal

import (
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roundingModes pairs every per-places rounding method with the name its
// big.Rat oracle dispatches on.
var roundingModes = []struct {
	name  string
	apply func(Decimal, uint8) Decimal
}{
	{"round", Decimal.Round},
	{"bank", Decimal.RoundBank},
	{"up", Decimal.RoundUp},
	{"down", Decimal.RoundDown},
	{"ceil", Decimal.RoundCeil},
	{"floor", Decimal.RoundFloor},
}

// roundModeOracle returns the exact value of d rounded to places under the
// named mode, derived from big.Rat floor/fraction arithmetic — fully
// independent of the library's reciprocal-division core.
func roundModeOracle(d Decimal, places uint8, mode string) *big.Rat {
	scale := pow10Big(int(places))
	xs := new(big.Rat).Mul(decimalToRat(d), new(big.Rat).SetInt(scale))
	floor := new(big.Int).Div(xs.Num(), xs.Denom()) // floored: fraction ∈ [0, 1)
	frac := new(big.Rat).Sub(xs, new(big.Rat).SetInt(floor))
	half := big.NewRat(1, 2)
	pos := d.Sign() >= 0

	var up bool // step from floor to floor + 1
	switch mode {
	case "round": // ties away from zero
		c := frac.Cmp(half)
		up = c > 0 || (c == 0 && pos)
	case "bank": // ties to even
		c := frac.Cmp(half)
		up = c > 0 || (c == 0 && floor.Bit(0) == 1)
	case "up": // away from zero
		up = frac.Sign() != 0 && pos
	case "down": // toward zero
		up = frac.Sign() != 0 && !pos
	case "ceil":
		up = frac.Sign() != 0
	case "floor":
	}
	if up {
		floor.Add(floor, big.NewInt(1))
	}
	return new(big.Rat).SetFrac(floor, scale)
}

func TestRoundTieTables(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		places uint8
		// One pinned expectation per mode, in roundingModes order.
		round, bank, up, down, ceil, floor string
	}{
		{name: "pos_0_5_to_int", in: "0.5", places: 0, round: "1", bank: "0", up: "1", down: "0", ceil: "1", floor: "0"},
		{name: "pos_1_5_to_int", in: "1.5", places: 0, round: "2", bank: "2", up: "2", down: "1", ceil: "2", floor: "1"},
		{name: "pos_2_5_to_int", in: "2.5", places: 0, round: "3", bank: "2", up: "3", down: "2", ceil: "3", floor: "2"},
		{name: "neg_0_5_to_int", in: "-0.5", places: 0, round: "-1", bank: "0", up: "-1", down: "0", ceil: "0", floor: "-1"},
		{name: "neg_1_5_to_int", in: "-1.5", places: 0, round: "-2", bank: "-2", up: "-2", down: "-1", ceil: "-1", floor: "-2"},
		{name: "neg_2_5_to_int", in: "-2.5", places: 0, round: "-3", bank: "-2", up: "-3", down: "-2", ceil: "-2", floor: "-3"},
		{name: "pos_0_05_to_1", in: "0.05", places: 1, round: "0.1", bank: "0", up: "0.1", down: "0", ceil: "0.1", floor: "0"},
		{name: "pos_0_15_to_1", in: "0.15", places: 1, round: "0.2", bank: "0.2", up: "0.2", down: "0.1", ceil: "0.2", floor: "0.1"},
		{name: "pos_0_25_to_1", in: "0.25", places: 1, round: "0.3", bank: "0.2", up: "0.3", down: "0.2", ceil: "0.3", floor: "0.2"},
		{name: "neg_0_15_to_1", in: "-0.15", places: 1, round: "-0.2", bank: "-0.2", up: "-0.2", down: "-0.1", ceil: "-0.1", floor: "-0.2"},
		{name: "neg_0_25_to_1", in: "-0.25", places: 1, round: "-0.3", bank: "-0.2", up: "-0.3", down: "-0.2", ceil: "-0.2", floor: "-0.3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString(tc.in)
			want := []string{tc.round, tc.bank, tc.up, tc.down, tc.ceil, tc.floor}
			for i, mode := range roundingModes {
				got := mode.apply(d, tc.places)
				assert.Equal(t, want[i], got.String(), "mode %s", mode.name)
				if got.IsZero() {
					assert.Equal(t, Decimal{}, got, "mode %s: zero must be canonical", mode.name)
				} else {
					assert.Equal(t, tc.places, got.Prec(), "mode %s", mode.name)
				}
			}
			assert.Equal(t, tc.down, d.Truncate(tc.places).String(), "Truncate must match RoundDown")
		})
	}
}

func TestRoundCarryChain(t *testing.T) {
	d := RequireFromString("9.9999999999999999995")
	require.Equal(t, MaxPrec, d.Prec())

	got := d.Round(18)
	assert.Equal(t, "10", got.String(), "the half-tie must carry through every 9")
	assert.Equal(t, uint8(18), got.Prec())

	assert.Equal(t, "10", d.RoundBank(18).String(), "odd quotient ties up")
	assert.Equal(t, "9.999999999999999999", d.RoundDown(18).String())
	assert.Equal(t, "10", d.Ceil().String())
	assert.Equal(t, "9", d.Floor().String())
}

func TestRoundNearMaxCoefficientNeverOverflows(t *testing.T) {
	pos := mustHiLo(t, false, maxUint64, maxUint64, 19)
	neg := mustHiLo(t, true, maxUint64, maxUint64, 19)
	tests := []struct {
		name   string
		d      Decimal
		apply  func(Decimal, uint8) Decimal
		places uint8
		want   string
	}{
		{name: "round_up_to_int", d: pos, apply: Decimal.RoundUp, places: 0, want: "34028236692093846347"},
		{name: "round_up_to_18", d: pos, apply: Decimal.RoundUp, places: 18, want: "34028236692093846346.337460743176821146"},
		{name: "half_away_stays_down", d: pos, apply: Decimal.Round, places: 0, want: "34028236692093846346"},
		{name: "negative_round_up_to_int", d: neg, apply: Decimal.RoundUp, places: 0, want: "-34028236692093846347"},
		{name: "negative_ceil_truncates", d: neg, apply: Decimal.RoundCeil, places: 0, want: "-34028236692093846346"},
		{name: "negative_floor_steps_down", d: neg, apply: Decimal.RoundFloor, places: 0, want: "-34028236692093846347"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.apply(tc.d, tc.places).String())
		})
	}
}

func TestRoundPlacesAtOrAbovePrecIsNoOp(t *testing.T) {
	tests := []struct {
		name string
		d    Decimal
	}{
		{"price", RequireFromString("-1234.5678")},
		{"max_prec", mustHiLo(t, false, maxUint64, maxUint64, 19)},
		{"integer", NewFromInt(42)},
		{"zero", Zero},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range roundingModes {
				for _, places := range []uint8{tc.d.Prec(), tc.d.Prec() + 1, MaxPrec, 200} {
					got := mode.apply(tc.d, places)
					require.Equal(t, tc.d, got,
						"mode %s at places %d must return d unchanged", mode.name, places)
				}
			}
		})
	}
}

func TestFloorCeilTruncate(t *testing.T) {
	tests := []struct {
		name                       string
		in                         string
		wantFloor, wantCeil, wantT string
	}{
		{name: "neg_2_5", in: "-2.5", wantFloor: "-3", wantCeil: "-2", wantT: "-2"},
		{name: "neg_2_9", in: "-2.9", wantFloor: "-3", wantCeil: "-2", wantT: "-2"},
		{name: "pos_2_5", in: "2.5", wantFloor: "2", wantCeil: "3", wantT: "2"},
		{name: "neg_0_4", in: "-0.4", wantFloor: "-1", wantCeil: "0", wantT: "0"},
		{name: "pos_0_4", in: "0.4", wantFloor: "0", wantCeil: "1", wantT: "0"},
		{name: "integer_is_noop", in: "-7", wantFloor: "-7", wantCeil: "-7", wantT: "-7"},
		{name: "zero", in: "0", wantFloor: "0", wantCeil: "0", wantT: "0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := RequireFromString(tc.in)
			assert.Equal(t, tc.wantFloor, d.Floor().String(), "Floor")
			assert.Equal(t, tc.wantCeil, d.Ceil().String(), "Ceil")
			assert.Equal(t, tc.wantT, d.Truncate(0).String(), "Truncate")
			assert.Equal(t, d.RoundFloor(0), d.Floor(), "Floor must match RoundFloor(0)")
			assert.Equal(t, d.RoundCeil(0), d.Ceil(), "Ceil must match RoundCeil(0)")
		})
	}
}

func TestRoundNegativeToZeroIsCanonical(t *testing.T) {
	tests := []struct {
		name  string
		apply func() Decimal
	}{
		{"round_neg_0_4", func() Decimal { return RequireFromString("-0.4").Round(0) }},
		{"bank_neg_0_5", func() Decimal { return RequireFromString("-0.5").RoundBank(0) }},
		{"down_neg_0_9", func() Decimal { return RequireFromString("-0.9").RoundDown(0) }},
		{"ceil_neg_0_9", func() Decimal { return RequireFromString("-0.9").Ceil() }},
		{"truncate_neg_0_05", func() Decimal { return RequireFromString("-0.05").Truncate(1) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.apply()
			require.Equal(t, Decimal{}, got, "negative values rounding to zero must lose the sign")
			assert.Equal(t, "0", got.String())
			assert.False(t, got.IsNegative())
		})
	}
}

func TestRoundRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x10D, 0x0B01))
	for range 10_000 {
		d := randDecimal(rng)
		places := uint8(rng.Uint64N(22)) // exceeds MaxPrec to cover no-ops
		for _, mode := range roundingModes {
			got := mode.apply(d, places)
			want := roundModeOracle(d, places, mode.name)
			if want.Cmp(decimalToRat(got)) != 0 {
				require.Fail(t, "rounding mismatch",
					"%s of %s at %d places: want %s, got %s", mode.name, d, places, want, got)
			}
			switch {
			case places >= d.Prec():
				require.Equal(t, d, got, "%s of %s at %d places must be a no-op", mode.name, d, places)
			case got.IsZero():
				require.Equal(t, Decimal{}, got, "%s of %s at %d places: canonical zero", mode.name, d, places)
			default:
				require.Equal(t, places, got.Prec(), "%s of %s at %d places", mode.name, d, places)
			}
		}
		require.Equal(t, d.RoundDown(places), d.Truncate(places), "Truncate must alias RoundDown")
	}
}
