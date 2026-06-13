package zerodecimal

import (
	"fmt"
	"math/big"
	"math/bits"
	"math/rand/v2"
	"os/exec"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pow10Big returns 10^k as a big.Int oracle value.
func pow10Big(k int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(k)), nil)
}

// recip2by1 recomputes the Möller-Granlund reciprocal ⌊(2^128-1)/dn⌋ - 2^64
// from its bits.Div64 identity, so tests never trust the generated tables
// for it.
func recip2by1(dn uint64) uint64 {
	q, _ := bits.Div64(^dn, ^uint64(0), dn)
	return q
}

// requireDiv2by1MatchesHW checks div2by1 against bits.Div64, which has the
// identical contract under the same preconditions.
func requireDiv2by1MatchesHW(t *testing.T, u1, u0, dn uint64) {
	t.Helper()
	q, r := div2by1(u1, u0, dn, recip2by1(dn))
	wantQ, wantR := bits.Div64(u1, u0, dn)
	if q != wantQ || r != wantR {
		require.Fail(t, "div2by1 mismatch",
			"{%#x,%#x} / %#x: got q=%#x r=%#x, want q=%#x r=%#x",
			u1, u0, dn, q, r, wantQ, wantR)
	}
}

func TestDiv2by1Boundaries(t *testing.T) {
	divisors := []struct {
		name string
		dn   uint64
	}{
		{"pow2_63", 1 << 63},
		{"pow2_63_plus_1", 1<<63 + 1},
		{"normalized_pow10_1", 0xa000000000000000},
		{"pow10_19", 1e19}, // already normalized: 10^19 > 2^63
		{"max64", ^uint64(0)},
	}
	highs := []struct {
		name string
		u1   func(dn uint64) uint64
	}{
		{"u1_zero", func(uint64) uint64 { return 0 }},
		{"u1_one", func(uint64) uint64 { return 1 }},
		{"u1_dn_minus_1", func(dn uint64) uint64 { return dn - 1 }},
	}
	lows := []struct {
		name string
		u0   uint64
	}{
		{"u0_zero", 0},
		{"u0_one", 1},
		{"u0_max64", ^uint64(0)},
	}
	for _, td := range divisors {
		for _, th := range highs {
			for _, tl := range lows {
				t.Run(td.name+"_"+th.name+"_"+tl.name, func(t *testing.T) {
					requireDiv2by1MatchesHW(t, th.u1(td.dn), tl.u0, td.dn)
				})
			}
		}
	}
}

func TestDiv2by1Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x2B1, 0x0010))
	for range 1_000_000 {
		dn := rng.Uint64() | 1<<63 // normalized: top bit set
		u1 := rng.Uint64N(dn)      // u1 < dn keeps the quotient in 64 bits
		u0 := rng.Uint64()
		requireDiv2by1MatchesHW(t, u1, u0, dn)
	}
}

func TestDivmod64Pow10Boundaries(t *testing.T) {
	for k := uint8(1); k <= MaxPrec; k++ {
		d := pow10u64[k]
		// 2*d-1 deliberately wraps mod 2^64 for k = 19; the wrapped value is
		// just as valid a dividend.
		cases := []struct {
			name string
			n    uint64
		}{
			{"zero", 0},
			{"one", 1},
			{"d_minus_1", d - 1},
			{"d", d},
			{"d_plus_1", d + 1},
			{"two_d_minus_1", 2*d - 1},
			{"pow2_32", 1 << 32},
			{"max64", ^uint64(0)},
			{"pow2_64_minus_d", ^uint64(0) - d + 1},
		}
		for _, tc := range cases {
			t.Run(fmt.Sprintf("k_%02d_%s", k, tc.name), func(t *testing.T) {
				q, r := divmod64Pow10(tc.n, k)
				assert.Equal(t, tc.n/d, q, "quotient")
				assert.Equal(t, tc.n%d, r, "remainder")
			})
		}
	}
}

func TestDivmod64Pow10Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x64D, 0x0011))
	for k := uint8(1); k <= MaxPrec; k++ {
		d := pow10u64[k]
		for range 100_000 {
			n := randShaped64(rng)
			q, r := divmod64Pow10(n, k)
			if q != n/d || r != n%d {
				require.Fail(t, "divmod64Pow10 mismatch",
					"%d / 10^%d: got q=%d r=%d, want q=%d r=%d",
					n, k, q, r, n/d, n%d)
			}
		}
	}
}

// inlineReportEntry is one function's verdict from the compiler's -m=2
// inlining diagnostics: whether it can inline and at what cost.
type inlineReportEntry struct {
	canInline bool
	cost      int
}

// buildInlineReport compiles the package with -gcflags=-m=2 and parses the
// per-function inlining verdicts. The build cache replays compiler
// diagnostics, so repeated runs stay fast and deterministic.
func buildInlineReport(t *testing.T) map[string]inlineReportEntry {
	t.Helper()
	out, err := exec.Command("go", "build", "-gcflags=-m=2", ".").CombinedOutput()
	require.NoError(t, err, "go build -gcflags=-m=2: %s", out)

	report := make(map[string]inlineReportEntry)
	// Names may be plain functions ("div2by1") or methods ("Decimal.Add").
	canRE := regexp.MustCompile(`: can inline ([\w.]+) with cost (\d+) `)
	for _, m := range canRE.FindAllStringSubmatch(string(out), -1) {
		cost, perr := strconv.Atoi(m[2])
		require.NoError(t, perr, "parse cost in %q", m[0])
		report[m[1]] = inlineReportEntry{canInline: true, cost: cost}
	}
	cannotRE := regexp.MustCompile(`: cannot inline ([\w.]+): function too complex: cost (\d+) `)
	for _, m := range cannotRE.FindAllStringSubmatch(string(out), -1) {
		cost, perr := strconv.Atoi(m[2])
		require.NoError(t, perr, "parse cost in %q", m[0])
		report[m[1]] = inlineReportEntry{canInline: false, cost: cost}
	}
	return report
}

func TestDiv10InlineBudgets(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; cannot query inlining diagnostics")
	}
	report := buildInlineReport(t)

	// The dispatcher itself can never inline under the default budget of 80:
	// the inlined divmod64Pow10 fast path (34) plus one outlined call (57)
	// already cost 91. What the cost bound below protects is the split — a
	// re-merged two-limb body measures 275, while the thin dispatcher
	// measures 134; 200 separates the two with headroom for compiler
	// cost-model drift across releases.
	tests := []struct {
		name string
		fn   string
		// requireInline asserts the function fits the default inline budget.
		requireInline bool
		// maxCost bounds the reported cost; 0 means presence-only.
		maxCost int
	}{
		{"div2by1_fits_inline_budget", "div2by1", true, 0},
		{"divmod64Pow10_fits_inline_budget", "divmod64Pow10", true, 0},
		{"divmod128Pow10_dispatcher_stays_thin", "divmod128Pow10", false, 200},
		{"divmod128Pow10Slow_stays_outlined", "divmod128Pow10Slow", false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, ok := report[tc.fn]
			require.True(t, ok, "%s missing from -m=2 inline report", tc.fn)
			if tc.requireInline {
				assert.True(t, entry.canInline,
					"%s must fit the default inline budget, got cost %d",
					tc.fn, entry.cost)
			}
			if tc.maxCost > 0 {
				assert.LessOrEqual(t, entry.cost, tc.maxCost,
					"%s inline cost regressed", tc.fn)
			}
		})
	}
}

// TestArithRoundingInlineBudgets pins the inlining shape of the arithmetic
// and rounding layer the same way TestDiv10InlineBudgets pins the division
// core's.
//
// Decision record, measured against the go1.26 cost model:
//
//   - Decimal.Add and Decimal.Mul (152) CANNOT fit the default inline budget
//     of 80. Their mandatory call into the outlined slow arm costs 57 on its
//     own and an inlined 128-bit fast path another 33, so no restructuring
//     brings them under budget — the same structural ceiling Decimal.Cmp (192)
//     and divmod128Pow10 (134) already live with. Their ceilings below protect
//     the thin-wrapper/outlined-slow-arm split: re-merging mulSlow (422) into
//     Mul would roughly triple its cost.
//   - Decimal.Add and Decimal.Sub now both inline BOTH same-precision arms —
//     the same-sign magnitude add and the opposite-sign magnitude subtract
//     (one sub128 + conditional neg128 + newDecimal) — and outline straight
//     into addUnaligned, the single shared differing-precision arm, only when
//     precisions differ. addSlow is gone: with both same-precision arms of
//     both operations inlined, its body was dead. This pulls a
//     stack-guard+frame and addSlow's duplicate prec/sign re-tests off every
//     same-precision Add and Sub; measurement showed all five Sub shapes got
//     faster, and the differing-precision rows (e.g. Add/near_max) drop one
//     outlined frame too. Neither wrapper inlines, so the larger bodies are no
//     regression; the ceilings guard against unbounded growth.
//   - The rounding family is split into per-mode outlined cores (truncCore,
//     roundHalfAwayCore, roundBankCore, dirCore) behind thin exported
//     wrappers. The wrappers inline an early-out plus exactly ONE call into
//     their mode's core, so the mode is a compile-time fact: each core carries
//     only its own work (truncCore dead-code-eliminates the remainder
//     reconstruction, the half load lives only in the cores that use it). The
//     cores stay outlined (well over the budget once both limb paths are
//     counted); they MUST NOT inline, or the per-mode DCE would collapse back
//     into the wrappers.
//   - Every exported rounding method is a one-call wrapper and MUST keep
//     fitting the budget. The wrappers sit at cost ~68 against the budget of
//     80 — only ~12 points of headroom — so a maxCost guard surfaces any edit
//     (or inliner cost-model change) that would silently de-inline them.
func TestArithRoundingInlineBudgets(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; cannot query inlining diagnostics")
	}
	report := buildInlineReport(t)

	tests := []struct {
		name string
		fn   string
		// requireInline asserts the function fits the default inline budget.
		requireInline bool
		// maxCost bounds the reported cost; 0 means presence-only.
		maxCost int
	}{
		{"add_wrapper_stays_thin", "Decimal.Add", false, 320},
		{"sub_wrapper_stays_thin", "Decimal.Sub", false, 320},
		{"mul_wrapper_stays_thin", "Decimal.Mul", false, 200},
		{"addUnaligned_stays_outlined", "addUnaligned", false, 0},
		{"mulSlow_stays_outlined", "mulSlow", false, 0},
		{"trunc_core_stays_outlined", "Decimal.truncCore", false, 0},
		{"round_half_away_core_stays_outlined", "Decimal.roundHalfAwayCore", false, 0},
		{"round_bank_core_stays_outlined", "Decimal.roundBankCore", false, 0},
		{"dir_core_stays_outlined", "Decimal.dirCore", false, 0},
		{"round_fits_inline_budget", "Decimal.Round", true, 75},
		{"round_bank_fits_inline_budget", "Decimal.RoundBank", true, 75},
		{"round_up_fits_inline_budget", "Decimal.RoundUp", true, 75},
		{"round_down_fits_inline_budget", "Decimal.RoundDown", true, 75},
		{"round_ceil_fits_inline_budget", "Decimal.RoundCeil", true, 75},
		{"round_floor_fits_inline_budget", "Decimal.RoundFloor", true, 75},
		{"truncate_fits_inline_budget", "Decimal.Truncate", true, 75},
		{"floor_fits_inline_budget", "Decimal.Floor", true, 75},
		{"ceil_fits_inline_budget", "Decimal.Ceil", true, 75},
		{"string_fixed_fits_inline_budget", "Decimal.StringFixed", true, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry, ok := report[tc.fn]
			require.True(t, ok, "%s missing from -m=2 inline report", tc.fn)
			if tc.requireInline {
				assert.True(t, entry.canInline,
					"%s must fit the default inline budget, got cost %d",
					tc.fn, entry.cost)
			}
			if tc.maxCost > 0 {
				assert.LessOrEqual(t, entry.cost, tc.maxCost,
					"%s inline cost regressed", tc.fn)
			}
		})
	}
}

func TestDivmod128Pow10Boundaries(t *testing.T) {
	for k := uint8(0); k <= MaxPrec; k++ {
		d := pow10u64[k]
		dBig := new(big.Int).SetUint64(d)
		for _, tu := range boundary128 {
			t.Run(fmt.Sprintf("k_%02d_%s", k, tu.name), func(t *testing.T) {
				q, r := divmod128Pow10(tu.v, k)

				wantQBig, wantRBig := new(big.Int).QuoRem(
					u128ToBig(tu.v), dBig, new(big.Int))
				requireU128EqualsBig(t, wantQBig, q, "quotient")
				assert.Equal(t, wantRBig.Uint64(), r, "remainder")

				// The two-DIV fallback must agree exactly.
				wantQ, wantR := quoRem64(tu.v, d)
				assert.Equal(t, wantQ, q, "quotient vs quoRem64")
				assert.Equal(t, wantR, r, "remainder vs quoRem64")
			})
		}
	}
}

func TestDivmod128Pow10Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x128, 0x0012))
	for k := uint8(0); k <= MaxPrec; k++ {
		d := pow10u64[k]
		for range 100_000 {
			u := randShapedU128(rng)
			q, r := divmod128Pow10(u, k)
			wantQ, wantR := quoRem64(u, d)
			if q != wantQ || r != wantR {
				require.Fail(t, "divmod128Pow10 mismatch",
					"%+v / 10^%d: got q=%+v r=%d, want q=%+v r=%d",
					u, k, q, r, wantQ, wantR)
			}
		}
	}
}

// divU256Pow10Ks are the spec'd scale factors: one-pass (≤ 19) and two-pass
// (> 19) cases, including both extremes.
var divU256Pow10Ks = []uint8{1, 5, 19, 20, 29, 38}

// overflowBound256 returns 10^k·2^128, the smallest dividend whose quotient
// by 10^k no longer fits 128 bits.
func overflowBound256(k uint8) *big.Int {
	return new(big.Int).Lsh(pow10Big(int(k)), 128)
}

func TestDivU256Pow10KZero(t *testing.T) {
	maxLo128 := u256{d0: ^uint64(0), d1: ^uint64(0)}
	tests := []struct {
		name    string
		u       u256
		want    u128
		wantErr error
	}{
		{"zero", u256{}, u128{}, nil},
		{"fits_low_128", u256{d0: 5, d1: 7}, u128{hi: 7, lo: 5}, nil},
		{"max_low_128", maxLo128, u128{hi: ^uint64(0), lo: ^uint64(0)}, nil},
		{"d2_set_overflows", u256{d2: 1}, u128{}, ErrOverflow},
		{"d3_set_overflows", u256{d3: 1}, u128{}, ErrOverflow},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, err := divU256Pow10(tc.u, 0)
			require.ErrorIs(t, err, tc.wantErr)
			assert.Equal(t, tc.want, q)
		})
	}
}

func TestDivU256Pow10ExactOverflowBoundary(t *testing.T) {
	for _, k := range divU256Pow10Ks {
		t.Run(fmt.Sprintf("k_%02d", k), func(t *testing.T) {
			bound := overflowBound256(k)

			// u = 10^k·2^128 - 1 is the largest fitting dividend; its
			// quotient is exactly 2^128 - 1.
			below := new(big.Int).Sub(bound, big.NewInt(1))
			q, err := divU256Pow10(bigToU256(t, below), k)
			require.NoError(t, err, "u = 10^k*2^128 - 1 must fit")
			assert.Equal(t, u128{hi: ^uint64(0), lo: ^uint64(0)}, q)

			// u = 10^k·2^128 is the smallest overflowing dividend.
			_, err = divU256Pow10(bigToU256(t, bound), k)
			require.ErrorIs(t, err, ErrOverflow, "u = 10^k*2^128 must overflow")
		})
	}
}

func TestDivU256Pow10RandomBelowBoundary(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x256, 0x0013))
	for _, k := range divU256Pow10Ks {
		dBig := pow10Big(int(k))
		for range 20_000 {
			// u = q·10^k + r with q < 2^128 and r < 10^k never overflows and
			// must reproduce q exactly (r is truncated away).
			q := randShapedU128(rng)
			r := new(big.Int).Rem(u128ToBig(randShapedU128(rng)), dBig)
			u := new(big.Int).Mul(u128ToBig(q), dBig)
			u.Add(u, r)

			gotQ, err := divU256Pow10(bigToU256(t, u), k)
			require.NoError(t, err, "%s / 10^%d", u, k)
			require.Equal(t, q, gotQ, "quotient of %s / 10^%d", u, k)
		}
	}
}

func TestDivU256Pow10RandomAboveBoundary(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x256, 0x0014))
	max256 := new(big.Int).Lsh(big.NewInt(1), 256)
	for _, k := range divU256Pow10Ks {
		bound := overflowBound256(k)
		span := new(big.Int).Sub(max256, bound)
		for range 20_000 {
			// u uniform-ish in [10^k·2^128, 2^256) must always overflow.
			offset := new(big.Int).Rem(u256ToBig(u256{
				d0: rng.Uint64(), d1: rng.Uint64(),
				d2: rng.Uint64(), d3: rng.Uint64(),
			}), span)
			u := new(big.Int).Add(bound, offset)

			_, err := divU256Pow10(bigToU256(t, u), k)
			require.ErrorIs(t, err, ErrOverflow, "%s / 10^%d", u, k)
		}
	}
}

func TestDivU256Pow10RandomShaped(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x256, 0x0015))
	for _, k := range divU256Pow10Ks {
		dBig := pow10Big(int(k))
		for range 20_000 {
			// Shaped limbs land on both sides of the boundary; big.Int
			// decides which outcome is correct.
			u := u256{
				d0: randShaped64(rng), d1: randShaped64(rng),
				d2: randShaped64(rng), d3: randShaped64(rng),
			}
			gotQ, err := divU256Pow10(u, k)

			wantQ := new(big.Int).Quo(u256ToBig(u), dBig)
			if wantQ.BitLen() > 128 {
				require.ErrorIs(t, err, ErrOverflow, "%+v / 10^%d", u, k)
				continue
			}
			require.NoError(t, err, "%+v / 10^%d", u, k)
			requireU128EqualsBig(t, wantQ, gotQ, "%+v / 10^%d", u, k)
		}
	}
}

func TestDiv256by64DivideByZero(t *testing.T) {
	tests := []struct {
		name string
		u    u256
	}{
		{"zero_dividend", u256{}},
		{"nonzero_dividend", u256{d0: 1}},
		{"huge_dividend", u256{d3: ^uint64(0)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, r, err := div256by64(tc.u, 0)
			require.ErrorIs(t, err, ErrDivideByZero)
			assert.Equal(t, u128{}, q)
			assert.Zero(t, r)
		})
	}
}

func TestDiv256by64ExactOverflowBoundary(t *testing.T) {
	for _, tv := range boundary64 {
		if tv.v == 0 {
			continue // covered by TestDiv256by64DivideByZero
		}
		t.Run(tv.name, func(t *testing.T) {
			bound := new(big.Int).Lsh(new(big.Int).SetUint64(tv.v), 128)

			// u = v·2^128 - 1: largest fitting dividend, quotient 2^128 - 1,
			// remainder v - 1.
			below := new(big.Int).Sub(bound, big.NewInt(1))
			q, r, err := div256by64(bigToU256(t, below), tv.v)
			require.NoError(t, err, "u = v*2^128 - 1 must fit")
			assert.Equal(t, u128{hi: ^uint64(0), lo: ^uint64(0)}, q, "quotient")
			assert.Equal(t, tv.v-1, r, "remainder")

			// u = v·2^128: smallest overflowing dividend.
			_, _, err = div256by64(bigToU256(t, bound), tv.v)
			require.ErrorIs(t, err, ErrOverflow, "u = v*2^128 must overflow")
		})
	}
}

func TestDiv256by64Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x256, 0x0016))
	for range 100_000 {
		// Reconstruction: u = q·v + r with r < v must come back as (q, r).
		q := randShapedU128(rng)
		v := randShaped64(rng)
		if v == 0 {
			v = 1
		}
		r := rng.Uint64N(v)

		u := new(big.Int).Mul(u128ToBig(q), new(big.Int).SetUint64(v))
		u.Add(u, new(big.Int).SetUint64(r))

		gotQ, gotR, err := div256by64(bigToU256(t, u), v)
		require.NoError(t, err, "%s / %d", u, v)
		require.Equal(t, q, gotQ, "quotient of %s / %d", u, v)
		require.Equal(t, r, gotR, "remainder of %s / %d", u, v)
	}
}

func TestDiv256by64RandomOverflow(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x256, 0x0017))
	for range 100_000 {
		// Any u with hi128(u) ≥ v has a quotient ≥ 2^128 and must overflow.
		v := randShaped64(rng)
		if v == 0 {
			v = 1
		}
		u := u256{
			d0: rng.Uint64(),
			d1: rng.Uint64(),
			d2: v + rng.Uint64N(^uint64(0)-v+1), // d2 ≥ v
			d3: randShaped64(rng),
		}
		_, _, err := div256by64(u, v)
		require.ErrorIs(t, err, ErrOverflow, "%+v / %d", u, v)
	}
}
