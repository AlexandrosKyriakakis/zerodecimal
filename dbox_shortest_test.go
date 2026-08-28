package zerodecimal

// Differential tests of the float constructors against strconv, the documented
// shortest-round-trip oracle. Go 1.27 changed one float32 tie choice from the
// equally short ...063 to ...062, while zerodecimal deliberately preserves its
// Go 1.26 output. Exact matches remain the normal case; an alternative is valid
// only when it has the same significant-digit count and parses to identical
// float bits. The pinned bit patterns steer the Dragonbox core through each of
// its branches (short-interval endpoints, tie parities, exact halves and the
// round-up corrections); the sweeps then cross-check the same contract over
// every power of two and a seeded random slice of the domain.

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// checkShortestFloat asserts the strconv contract for one input. When the
// constructor reports ErrPrecOutOfRange the oracle independently confirms the
// shortest form needs more than MaxPrec fractional digits, so the error path
// is verified rather than skipped — 2^-63 and 2^-62 pass the 10^-19 magnitude
// guard yet legitimately error this way after fully exercising the core.
func checkShortestFloat(t *testing.T, f float64, bitSize int) {
	t.Helper()
	want := strconv.FormatFloat(f, 'f', -1, bitSize)
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
	got := d.String()
	if got == want {
		return
	}
	parsed, parseErr := strconv.ParseFloat(got, bitSize)
	require.NoErrorf(t, parseErr, "alternative shortest form %q must parse: f=%v (%x)", got, f, f)
	if bitSize == 32 {
		require.Equalf(t, math.Float32bits(float32(f)), math.Float32bits(float32(parsed)), "alternative %q does not round-trip: f=%v (%x)", got, f, f)
	} else {
		require.Equalf(t, math.Float64bits(f), math.Float64bits(parsed), "alternative %q does not round-trip: f=%v (%x)", got, f, f)
	}
	significantDigits := func(s string) int {
		return len(strings.TrimLeft(strings.ReplaceAll(s, ".", ""), "-0"))
	}
	require.Equalf(t, significantDigits(want), significantDigits(got), "alternative %q is not equally short as strconv %q: f=%v (%x)", got, want, f, f)
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
