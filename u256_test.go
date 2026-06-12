package zerodecimal

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestU256IsZeroUpper(t *testing.T) {
	tests := []struct {
		name string
		u    u256
		want bool
	}{
		{"zero_value", u256{}, true},
		{"only_low_128_set", u256{d0: ^uint64(0), d1: ^uint64(0)}, true},
		{"d2_set", u256{d2: 1}, false},
		{"d3_set", u256{d3: 1}, false},
		{"all_set", u256{d0: 1, d1: 1, d2: 1, d3: 1}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.u.isZeroUpper())
		})
	}
}

func TestU256Lo128Hi128(t *testing.T) {
	tests := []struct {
		name   string
		u      u256
		wantLo u128
		wantHi u128
	}{
		{"zero_value", u256{}, u128{}, u128{}},
		{
			"distinct_limbs",
			u256{d0: 10, d1: 20, d2: 30, d3: 40},
			u128{hi: 20, lo: 10},
			u128{hi: 40, lo: 30},
		},
		{
			"max_low_zero_high",
			u256{d0: ^uint64(0), d1: ^uint64(0)},
			u128{hi: ^uint64(0), lo: ^uint64(0)},
			u128{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantLo, tc.u.lo128(), "lo128")
			assert.Equal(t, tc.wantHi, tc.u.hi128(), "hi128")
		})
	}
}

func TestMulToU256ExactBoundaries(t *testing.T) {
	tests := []struct {
		name string
		u, v u128
		want u256
	}{
		{
			"zero_times_max128",
			u128{},
			u128{hi: ^uint64(0), lo: ^uint64(0)},
			u256{},
		},
		{
			"one_times_max128",
			u128{lo: 1},
			u128{hi: ^uint64(0), lo: ^uint64(0)},
			u256{d0: ^uint64(0), d1: ^uint64(0)},
		},
		{
			// (2^64-1)·(2^64+1) = 2^128-1: fills the low half exactly.
			"max64_times_pow2_64_plus_1_fits_128",
			u128{lo: ^uint64(0)},
			u128{hi: 1, lo: 1},
			u256{d0: ^uint64(0), d1: ^uint64(0)},
		},
		{
			// 2^64·2^64 = 2^128: one past the low half.
			"pow2_64_times_pow2_64_overflows_by_one",
			u128{hi: 1},
			u128{hi: 1},
			u256{d2: 1},
		},
		{
			// (2^64-1)² = 2^128 - 2^65 + 1.
			"max64_times_max64",
			u128{lo: ^uint64(0)},
			u128{lo: ^uint64(0)},
			u256{d0: 1, d1: ^uint64(1)},
		},
		{
			// (2^128-1)² = 2^256 - 2^129 + 1.
			"max128_times_max128",
			u128{hi: ^uint64(0), lo: ^uint64(0)},
			u128{hi: ^uint64(0), lo: ^uint64(0)},
			u256{d0: 1, d1: 0, d2: ^uint64(1), d3: ^uint64(0)},
		},
		{
			// 2^127·2 = 2^128: smallest carry out of the schoolbook path.
			"pow2_127_times_two",
			u128{hi: 1 << 63},
			u128{lo: 2},
			u256{d2: 1},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := mulToU256(tc.u, tc.v)
			assert.Equal(t, tc.want, got)
			// Multiplication is commutative; both argument orders must agree.
			assert.Equal(t, tc.want, mulToU256(tc.v, tc.u), "swapped operands")
		})
	}
}

func TestMulToU256Boundaries(t *testing.T) {
	for _, tu := range boundary128 {
		for _, tv := range boundary128 {
			t.Run(tu.name+"_times_"+tv.name, func(t *testing.T) {
				got := mulToU256(tu.v, tv.v)
				want := new(big.Int).Mul(u128ToBig(tu.v), u128ToBig(tv.v))
				require.Zero(t, want.Cmp(u256ToBig(got)),
					"want %s, got %s", want.Text(16), u256ToBig(got).Text(16))
			})
		}
	}
}

func TestMulToU256Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x256, 0x000A))
	for range 100_000 {
		u, v := randShapedU128(rng), randShapedU128(rng)
		got := mulToU256(u, v)
		want := new(big.Int).Mul(u128ToBig(u), u128ToBig(v))
		require.Zero(t, want.Cmp(u256ToBig(got)),
			"%+v * %+v: want %s, got %s", u, v, want.Text(16), u256ToBig(got).Text(16))
	}
}

// requireDivMatchesBig divides u by v with div256by128 and checks both
// outputs against big.Int. Callers must respect the div256by128
// preconditions: v.hi != 0 and hi128(u) < v.
func requireDivMatchesBig(t *testing.T, u u256, v u128) {
	t.Helper()
	q, r := div256by128(u, v)
	wantQ, wantR := new(big.Int).QuoRem(u256ToBig(u), u128ToBig(v), new(big.Int))
	requireU128EqualsBig(t, wantQ, q, "quotient of %+v / %+v", u, v)
	requireU128EqualsBig(t, wantR, r, "remainder of %+v / %+v", u, v)
}

func TestDiv256by128KnownCases(t *testing.T) {
	tests := []struct {
		name  string
		u     u256
		v     u128
		wantQ u128
		wantR u128
	}{
		{
			"zero_dividend",
			u256{},
			u128{hi: 1},
			u128{},
			u128{},
		},
		{
			"dividend_equals_divisor",
			u256{d1: 1, d0: 5},
			u128{hi: 1, lo: 5},
			u128{lo: 1},
			u128{},
		},
		{
			"dividend_below_divisor_all_remainder",
			u256{d1: 1},
			u128{hi: 1, lo: 5},
			u128{},
			u128{hi: 1},
		},
		{
			// (2^128-1)·(2^128-1) + (2^128-2) = 2^256 - 2^129 + 1 + 2^128 - 2
			// = 2^256 - 2^128 - 1: largest dividend the preconditions allow.
			"max_quotient_max_remainder",
			u256{d0: ^uint64(0), d1: ^uint64(0), d2: ^uint64(1), d3: ^uint64(0)},
			u128{hi: ^uint64(0), lo: ^uint64(0)},
			u128{hi: ^uint64(0), lo: ^uint64(0)},
			u128{hi: ^uint64(0), lo: ^uint64(1)},
		},
		{
			// Crafted so the second digit's trial quotient overshoots the
			// true digit by exactly 2 (the k=2 correction): v = 2^127+2^64-1
			// (minimal normalized hi, maximal lo), dividend (2^127-2^63)·2^64.
			"trial_quotient_overshoots_by_two",
			u256{d0: 0, d1: 1 << 63, d2: (1 << 63) - 1, d3: 0},
			u128{hi: 1 << 63, lo: ^uint64(0)},
			u128{lo: ^uint64(2)},
			u128{hi: 3, lo: ^uint64(2)},
		},
		{
			// First digit step: a3 == v.hi forces the Knuth D3 clamp
			// (tq = 2^64-1). udecimal's port panics on this input shape.
			"trial_quotient_clamp_first_digit",
			u256{d0: 7, d1: 9, d2: 4, d3: 1 << 63},
			u128{hi: 1 << 63, lo: 5},
			u128{hi: ^uint64(0), lo: ^uint64(1)},
			u128{hi: 9, lo: 17},
		},
		{
			// Second digit step: a3 == 0 and a2 == v.hi with a1 < v.lo also
			// reaches the clamp, through the skipped-first-digit path.
			"trial_quotient_clamp_second_digit",
			u256{d0: 11, d1: 4, d2: 1 << 63, d3: 0},
			u128{hi: 1 << 63, lo: 5},
			u128{lo: ^uint64(0)},
			u128{hi: (1 << 63) - 1, lo: 16},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, r := div256by128(tc.u, tc.v)
			assert.Equal(t, tc.wantQ, q, "quotient")
			assert.Equal(t, tc.wantR, r, "remainder")
			requireDivMatchesBig(t, tc.u, tc.v)
		})
	}
}

func TestDiv256by128Boundaries(t *testing.T) {
	for _, tq := range boundary128 {
		for _, tv := range boundary128 {
			if tv.v.hi == 0 {
				continue // v.hi != 0 is a documented precondition
			}
			vMinus1, _ := sub128(tv.v, u128{lo: 1})
			rems := []struct {
				name string
				r    u128
			}{
				{"zero", u128{}},
				{"one", u128{lo: 1}},
				{"v_minus_1", vMinus1},
			}
			for _, tr := range rems {
				t.Run(fmt.Sprintf("q_%s_v_%s_r_%s", tq.name, tv.name, tr.name), func(t *testing.T) {
					// u = q·v + r < v·2^128, so hi128(u) < v always holds.
					ub := new(big.Int).Mul(u128ToBig(tq.v), u128ToBig(tv.v))
					ub.Add(ub, u128ToBig(tr.r))

					gotQ, gotR := div256by128(bigToU256(t, ub), tv.v)
					assert.Equal(t, tq.v, gotQ, "quotient")
					assert.Equal(t, tr.r, gotR, "remainder")
				})
			}
		}
	}
}

func TestDiv256by128TrialQuotientClampRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xC1A4, 0x000B))
	for i := range 100_000 {
		// v is pre-normalized (top bit set) with v.lo > 0 so that dividends
		// with a limb equal to v.hi are reachable under the preconditions.
		v := u128{hi: rng.Uint64() | 1<<63, lo: rng.Uint64() | 1}
		var u u256
		if i%2 == 0 {
			// First digit step sees u2 == v.hi.
			u = u256{d3: v.hi, d2: rng.Uint64N(v.lo), d1: rng.Uint64(), d0: rng.Uint64()}
		} else {
			// First digit step is skipped; second sees u2 == v.hi.
			u = u256{d2: v.hi, d1: rng.Uint64N(v.lo), d0: rng.Uint64()}
		}
		requireDivMatchesBig(t, u, v)
	}
}

func TestDiv256by128Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xD1F, 0x000C))
	for i := range 100_000 {
		// Build u = q·v + r from random parts with v.hi != 0 and r < v, then
		// require the division to recover exactly (q, r).
		q := randShapedU128(rng)
		v := randShapedU128(rng)
		if v.hi == 0 {
			v.hi = rng.Uint64() | 1
		}
		r := randShapedU128(rng)
		if i%8 == 0 {
			// Periodically pin the remainder to its maximum v-1.
			r, _ = sub128(v, u128{lo: 1})
		} else if !less128(r, v) {
			// Cheap reduction below v: any r.hi < v.hi suffices.
			r.hi %= v.hi
		}

		ub := new(big.Int).Mul(u128ToBig(q), u128ToBig(v))
		ub.Add(ub, u128ToBig(r))

		gotQ, gotR := div256by128(bigToU256(t, ub), v)
		require.Equal(t, q, gotQ, "quotient of %s / %+v", ub.Text(16), v)
		require.Equal(t, r, gotR, "remainder of %s / %+v", ub.Text(16), v)
	}
}

func TestDiv256by128MulRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x707, 0x000D))
	for range 100_000 {
		// a·b < b·2^128 guarantees hi128(a·b) < b, so dividing the full
		// product by b must recover a exactly with zero remainder.
		a := randShapedU128(rng)
		b := randShapedU128(rng)
		if b.hi == 0 {
			b.hi = rng.Uint64() | 1
		}

		q, r := div256by128(mulToU256(a, b), b)
		require.Equal(t, a, q, "quotient of (%+v * %+v) / %+v", a, b, b)
		require.Equal(t, u128{}, r, "remainder of (%+v * %+v) / %+v", a, b, b)
	}
}
