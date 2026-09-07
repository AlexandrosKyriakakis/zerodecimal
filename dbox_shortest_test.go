package zerodecimal

// Differential tests of the float constructors against strconv's shortest
// decimal conversion. The oracle corrects the known float32 tie defect in
// older strconv versions using independently checked literals. The pinned bit
// patterns steer the Dragonbox core through its short-interval endpoints,
// tie parities, exact halves and round-up corrections; the sweeps cross-check
// the contract over every power of two and a seeded random slice of the domain.

import (
	"math"
	"math/big"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// shortestFloatText keeps the differential oracle independent of the copied
// Go 1.26 Dragonbox defect: at float32 +/-2^-12, two equally close shortest
// decimals round-trip, and the even final digit must win. Pin these two values
// on every toolchain; TestNewFromFloat32ShortestTieToEven checks the midpoint
// and round trips without relying on strconv's formatter.
func shortestFloatText(f float64, bitSize int) string {
	if bitSize == 32 {
		switch f {
		case 0x1p-12:
			return "0.00024414062"
		case -0x1p-12:
			return "-0.00024414062"
		}
	}
	return strconv.FormatFloat(f, 'f', -1, bitSize)
}

// checkShortestFloat asserts the shortest-decimal contract for one input. When the
// constructor reports ErrPrecOutOfRange the oracle independently confirms the
// shortest form needs more than MaxPrec fractional digits, so the error path
// is verified rather than skipped — 2^-63 and 2^-62 pass the 10^-19 magnitude
// guard yet legitimately error this way after fully exercising the core.
func checkShortestFloat(t *testing.T, f float64, bitSize int) {
	t.Helper()
	want := shortestFloatText(f, bitSize)
	var (
		d   Decimal
		err error
	)
	if bitSize == 32 {
		d, err = NewFromFloat32(float32(f))
	} else {
		d, err = NewFromFloat(f)
	}
	if err != nil {
		require.ErrorIsf(t, err, ErrPrecOutOfRange, "f=%v (%x)", f, f)
		frac := 0
		if i := strings.IndexByte(want, '.'); i >= 0 {
			frac = len(want) - i - 1
		}
		require.Greaterf(t, frac, int(MaxPrec), "ErrPrecOutOfRange but shortest form %q fits MaxPrec (f=%x)", want, f)
		return
	}
	require.NoErrorf(t, err, "f=%v (%x)", f, f)
	if f == 0 {
		want = "0" // ±0.0 collapses to the canonical zero, sign dropped
	}
	require.Equalf(t, want, d.String(), "f=%v (%x)", f, f)
}

func TestNewFromFloat32ShortestTieToEven(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input float32
		even  string
		odd   string
		exact string
	}{
		{"positive", 0x1p-12, "0.00024414062", "0.00024414063", "0.000244140625"},
		{"negative", -0x1p-12, "-0.00024414062", "-0.00024414063", "-0.000244140625"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Both shortest candidates round-trip and their exact midpoint is
			// the input, so nearest-even must choose the literal ending in 2.
			even, ok := new(big.Rat).SetString(tc.even)
			require.True(t, ok)
			odd, ok := new(big.Rat).SetString(tc.odd)
			require.True(t, ok)
			midpoint := new(big.Rat).Add(even, odd)
			midpoint.Quo(midpoint, big.NewRat(2, 1))
			exact := new(big.Rat).SetFloat64(float64(tc.input))
			require.Zero(t, midpoint.Cmp(exact))
			for _, candidate := range []string{tc.even, tc.odd} {
				parsed, err := strconv.ParseFloat(candidate, 32)
				require.NoError(t, err)
				require.Equal(t, tc.input, float32(parsed))
			}

			d, err := NewFromFloat32(tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.even, d.String())

			// The same binary value at float64 precision has a different
			// shortest form; the correction must remain float32-specific.
			d64, err := NewFromFloat(float64(tc.input))
			require.NoError(t, err)
			require.Equal(t, tc.exact, d64.String())
		})
	}
}

func TestNewFromFloatShortestPinned(t *testing.T) {
	tests := []struct {
		name string
		bits uint64
	}{
		{"r_equals_delta_falls_through", 0x435d1c47aedaaacb},
		{"r_equals_delta_trims", 0x437b6574dd2718b4},
		{"rho_zero_rounds_down", 0x431eb7fcd82760ed},
		{"exact_tie_reopens_interval", 0xc35587d2a7851bef},
		{"rho_zero_keeps_round_up", 0xc1acd9f551180278},
		{"r_below_delta_trims", 0x41f27cc6f3875d04},
		{"plain_short_interval", 0x3c04951aa42655d9},
		{"uadd128_carry_propagates", 0xc3ea3b9393f93f33},
		{"two_pow_minus_63_round_up_kept", 0x3c00000000000000},
		{"two_pow_minus_62_narrow_trims", 0x3c10000000000000},
		{"two_pow_minus_25_exp_minus_77", 0x3e60000000000000},
		{"two_pow_54_exp_two_keeps_left_endpoint", 0x4350000000000000},
		{"two_pow_55_exp_three_keeps_left_endpoint", 0x4360000000000000},
		{"two_pow_89_round_up_bumped", 0x4580000000000000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkShortestFloat(t, math.Float64frombits(tc.bits), 64)
		})
	}
}

func TestNewFromFloat32ShortestPinned(t *testing.T) {
	tests := []struct {
		name string
		bits uint32
	}{
		{"before_two_pow_minus_12", 0x397fffff},
		{"two_pow_minus_12_tie_to_even", 0x39800000},
		{"after_two_pow_minus_12", 0x39800001},
		{"two_pow_minus_63_round_up_kept", 0x20000000},
		{"two_pow_minus_62_narrow_trims", 0x20800000},
		{"two_pow_25_exp_two_keeps_left_endpoint", 0x4c000000},
		{"two_pow_26_exp_three_keeps_left_endpoint", 0x4c800000},
		{"two_pow_87_round_up_bumped", 0x6b000000},
		{"r_below_delta_trims", 0x5f3f164f},
		{"rho_zero_rounds_down", 0x392907a0},
		{"r_equals_delta_trims", 0x3f71f8cb},
		{"r_equals_delta_falls_through", 0x4c330f1d},
		{"exact_tie_reopens_interval", 0x4d49461f},
		{"plain_short_interval", 0x49c6e2d1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkShortestFloat(t, float64(math.Float32frombits(tc.bits)), 32)
		})
	}
}

func TestNewFromFloatShortestPowersOfTwo(t *testing.T) {
	// Every power of two in the guarded domain, both widths and both signs.
	// The mant == 2^p short-interval branch (Dragonbox Algorithm 5.6) only
	// fires on these inputs, so the sweep pins all of its exponent arms.
	for n := -63; n <= 127; n++ {
		f := math.Ldexp(1, n)
		checkShortestFloat(t, f, 64)
		checkShortestFloat(t, -f, 64)
		checkShortestFloat(t, f, 32)
		checkShortestFloat(t, -f, 32)
	}
}

func TestNewFromFloatShortestRandom(t *testing.T) {
	inDomain := func(f float64) bool {
		return !math.IsNaN(f) && !math.IsInf(f, 0) && math.Abs(f) < 0x1p128 &&
			(f == 0 || math.Abs(f) >= 1e-19)
	}
	t.Run("float64_raw_bits", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(1, 0xDB0864))
		for range 20_000 {
			f := math.Float64frombits(rng.Uint64())
			if !inDomain(f) {
				continue
			}
			checkShortestFloat(t, f, 64)
		}
	})
	t.Run("float64_in_domain_exponents", func(t *testing.T) {
		// Raw bits mostly miss the guarded domain, so a second sweep draws
		// the exponent field from [960, 1151) — unbiased 2^-63 .. 2^127 —
		// keeping every sample in range with a random sign and significand.
		rng := rand.New(rand.NewPCG(1, 0xDB0865))
		for range 20_000 {
			bits := rng.Uint64()&(1<<63|1<<52-1) | (960+rng.Uint64N(191))<<52
			f := math.Float64frombits(bits)
			require.Truef(t, inDomain(f), "constructed exponent out of domain: %x", f)
			checkShortestFloat(t, f, 64)
		}
	})
	t.Run("float32_raw_bits", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(1, 0xDB0832))
		for range 20_000 {
			f := float64(math.Float32frombits(rng.Uint32()))
			if !inDomain(f) {
				continue
			}
			checkShortestFloat(t, f, 32)
		}
	})
}
