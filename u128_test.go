package zerodecimal

import (
	"fmt"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shared big.Int constants for the differential tests.
var (
	mask64big  = new(big.Int).SetUint64(^uint64(0))
	mod128big  = new(big.Int).Lsh(big.NewInt(1), 128)
	mask128big = new(big.Int).Sub(mod128big, big.NewInt(1))
)

// u128ToBig converts u to a big.Int for differential testing.
func u128ToBig(u u128) *big.Int {
	b := new(big.Int).SetUint64(u.hi)
	b.Lsh(b, 64)
	return b.Or(b, new(big.Int).SetUint64(u.lo))
}

// u256ToBig converts u to a big.Int for differential testing.
func u256ToBig(u u256) *big.Int {
	b := new(big.Int).SetUint64(u.d3)
	for _, limb := range []uint64{u.d2, u.d1, u.d0} {
		b.Lsh(b, 64)
		b.Or(b, new(big.Int).SetUint64(limb))
	}
	return b
}

// bigToU128 converts b to a u128, failing the test if b is negative or does
// not fit in 128 bits.
func bigToU128(t *testing.T, b *big.Int) u128 {
	t.Helper()
	require.GreaterOrEqual(t, b.Sign(), 0, "bigToU128: negative value %s", b)
	require.LessOrEqual(t, b.BitLen(), 128, "bigToU128: %s does not fit 128 bits", b)
	return u128{
		hi: new(big.Int).Rsh(b, 64).Uint64(),
		lo: new(big.Int).And(b, mask64big).Uint64(),
	}
}

// bigToU256 converts b to a u256, failing the test if b is negative or does
// not fit in 256 bits.
func bigToU256(t *testing.T, b *big.Int) u256 {
	t.Helper()
	require.GreaterOrEqual(t, b.Sign(), 0, "bigToU256: negative value %s", b)
	require.LessOrEqual(t, b.BitLen(), 256, "bigToU256: %s does not fit 256 bits", b)
	var limbs [4]uint64
	rest := new(big.Int).Set(b)
	for i := range limbs {
		limbs[i] = new(big.Int).And(rest, mask64big).Uint64()
		rest.Rsh(rest, 64)
	}
	return u256{d0: limbs[0], d1: limbs[1], d2: limbs[2], d3: limbs[3]}
}

// requireU128EqualsBig asserts that got equals want, where want must already
// be reduced into [0, 2^128).
func requireU128EqualsBig(t *testing.T, want *big.Int, got u128, msgAndArgs ...any) {
	t.Helper()
	if u128ToBig(got).Cmp(want) != 0 {
		require.Fail(t,
			fmt.Sprintf("u128 mismatch: want %s, got %s", want.Text(16), u128ToBig(got).Text(16)),
			msgAndArgs...)
	}
}

// boundary128 enumerates the limb patterns most likely to expose carry,
// borrow, and normalization bugs.
var boundary128 = []struct {
	name string
	v    u128
}{
	{"zero", u128{}},
	{"one", u128{lo: 1}},
	{"two", u128{lo: 2}},
	{"pow2_32", u128{lo: 1 << 32}},
	{"pow2_63", u128{lo: 1 << 63}},
	{"pow2_64_minus_1", u128{lo: ^uint64(0)}},
	{"pow2_64", u128{hi: 1}},
	{"pow2_64_plus_1", u128{hi: 1, lo: 1}},
	{"pow10_19", u128{lo: 1e19}},
	{"pow2_127", u128{hi: 1 << 63}},
	{"pow2_127_plus_lo_max", u128{hi: 1 << 63, lo: ^uint64(0)}},
	{"hi_max_lo_zero", u128{hi: ^uint64(0)}},
	{"max128", u128{hi: ^uint64(0), lo: ^uint64(0)}},
}

// boundary64 enumerates 64-bit operands for the *by64 primitives.
var boundary64 = []struct {
	name string
	v    uint64
}{
	{"zero", 0},
	{"one", 1},
	{"two", 2},
	{"three", 3},
	{"ten", 10},
	{"pow2_32", 1 << 32},
	{"pow2_63", 1 << 63},
	{"pow10_19", 1e19},
	{"max64", ^uint64(0)},
}

// randShapedU128 returns mostly uniform values, biased toward limbs that are
// 0, 1, or all-ones so carry chains and short paths stay well covered.
func randShapedU128(rng *rand.Rand) u128 {
	return u128{hi: randShaped64(rng), lo: randShaped64(rng)}
}

// randShaped64 is the single-limb flavor of randShapedU128.
func randShaped64(rng *rand.Rand) uint64 {
	switch rng.IntN(8) {
	case 0:
		return 0
	case 1:
		return 1
	case 2:
		return ^uint64(0)
	default:
		return rng.Uint64()
	}
}

func TestBigConversionRoundTrip(t *testing.T) {
	// The big.Int helpers are the oracle for every differential test below,
	// so they must at least be exact inverses of each other.
	rng := rand.New(rand.NewPCG(0xB16, 0x000E))
	for range 10_000 {
		u := randShapedU128(rng)
		require.Equal(t, u, bigToU128(t, u128ToBig(u)), "u128 round trip of %+v", u)

		w := u256{d0: rng.Uint64(), d1: rng.Uint64(), d2: rng.Uint64(), d3: rng.Uint64()}
		require.Equal(t, w, bigToU256(t, u256ToBig(w)), "u256 round trip of %+v", w)
	}
}

func TestU128IsZero(t *testing.T) {
	tests := []struct {
		name string
		u    u128
		want bool
	}{
		{"zero_value", u128{}, true},
		{"lo_set", u128{lo: 1}, false},
		{"hi_set", u128{hi: 1}, false},
		{"both_set", u128{hi: 1, lo: 1}, false},
		{"max128", u128{hi: ^uint64(0), lo: ^uint64(0)}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.u.isZero())
		})
	}
}

func TestU128Eq(t *testing.T) {
	tests := []struct {
		name string
		u, v u128
		want bool
	}{
		{"zero_eq_zero", u128{}, u128{}, true},
		{"max_eq_max", u128{hi: ^uint64(0), lo: ^uint64(0)}, u128{hi: ^uint64(0), lo: ^uint64(0)}, true},
		{"differ_in_lo", u128{hi: 5, lo: 1}, u128{hi: 5, lo: 2}, false},
		{"differ_in_hi", u128{hi: 1, lo: 7}, u128{hi: 2, lo: 7}, false},
		{"swapped_limbs", u128{hi: 1, lo: 2}, u128{hi: 2, lo: 1}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.u.eq(tc.v))
		})
	}
}

func TestLess128Cmp128Boundaries(t *testing.T) {
	for _, tu := range boundary128 {
		for _, tv := range boundary128 {
			t.Run(tu.name+"_vs_"+tv.name, func(t *testing.T) {
				wantCmp := u128ToBig(tu.v).Cmp(u128ToBig(tv.v))
				assert.Equal(t, wantCmp, cmp128(tu.v, tv.v), "cmp128")
				assert.Equal(t, wantCmp < 0, less128(tu.v, tv.v), "less128")
			})
		}
	}
}

func TestLess128Cmp128Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xC0DE, 0x0001))
	for range 100_000 {
		u, v := randShapedU128(rng), randShapedU128(rng)
		wantCmp := u128ToBig(u).Cmp(u128ToBig(v))
		require.Equal(t, wantCmp, cmp128(u, v), "cmp128(%+v, %+v)", u, v)
		require.Equal(t, wantCmp < 0, less128(u, v), "less128(%+v, %+v)", u, v)
		require.Equal(t, wantCmp == 0, u.eq(v), "eq(%+v, %+v)", u, v)
	}
}

func TestAdd128CarrySemantics(t *testing.T) {
	maxVal := u128{hi: ^uint64(0), lo: ^uint64(0)}
	tests := []struct {
		name      string
		u, v      u128
		want      u128
		wantCarry uint64
	}{
		{"zero_plus_zero_carry_0", u128{}, u128{}, u128{}, 0},
		{"max128_plus_zero_carry_0", maxVal, u128{}, maxVal, 0},
		{"max128_plus_one_wraps_carry_1", maxVal, u128{lo: 1}, u128{}, 1},
		{"lo_carry_ripples_into_hi", u128{lo: ^uint64(0)}, u128{lo: 1}, u128{hi: 1}, 0},
		{"pow2_127_plus_pow2_127_carry_1", u128{hi: 1 << 63}, u128{hi: 1 << 63}, u128{}, 1},
		{"hi_max_plus_hi_max_carry_1", u128{hi: ^uint64(0)}, u128{hi: ^uint64(0)}, u128{hi: ^uint64(1)}, 1},
		{"max128_plus_max128_carry_1", maxVal, maxVal, u128{hi: ^uint64(0), lo: ^uint64(1)}, 1},
		{"just_below_wrap_carry_0", u128{hi: ^uint64(0), lo: ^uint64(1)}, u128{lo: 1}, maxVal, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, carry := add128(tc.u, tc.v)
			assert.Equal(t, tc.want, got, "sum")
			assert.Equal(t, tc.wantCarry, carry, "carry")
		})
	}
}

func TestAdd128Boundaries(t *testing.T) {
	for _, tu := range boundary128 {
		for _, tv := range boundary128 {
			t.Run(tu.name+"_plus_"+tv.name, func(t *testing.T) {
				got, carry := add128(tu.v, tv.v)
				want := new(big.Int).Add(u128ToBig(tu.v), u128ToBig(tv.v))
				assert.Equal(t, uint64(want.Bit(128)), carry, "carry")
				requireU128EqualsBig(t, want.And(want, mask128big), got)
			})
		}
	}
}

func TestAdd128Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0xADD, 0x0002))
	for range 100_000 {
		u, v := randShapedU128(rng), randShapedU128(rng)
		got, carry := add128(u, v)
		want := new(big.Int).Add(u128ToBig(u), u128ToBig(v))
		require.Equal(t, uint64(want.Bit(128)), carry, "carry of %+v + %+v", u, v)
		requireU128EqualsBig(t, want.And(want, mask128big), got, "%+v + %+v", u, v)
	}
}

func TestSub128BorrowSemantics(t *testing.T) {
	maxVal := u128{hi: ^uint64(0), lo: ^uint64(0)}
	tests := []struct {
		name       string
		u, v       u128
		want       u128
		wantBorrow uint64
	}{
		{"zero_minus_zero_borrow_0", u128{}, u128{}, u128{}, 0},
		{"zero_minus_one_wraps_borrow_1", u128{}, u128{lo: 1}, maxVal, 1},
		{"one_minus_one_borrow_0", u128{lo: 1}, u128{lo: 1}, u128{}, 0},
		{"max_minus_max_borrow_0", maxVal, maxVal, u128{}, 0},
		{"borrow_ripples_through_hi", u128{hi: 1}, u128{lo: 1}, u128{lo: ^uint64(0)}, 0},
		{"smaller_minus_larger_borrow_1", u128{lo: 5}, u128{hi: 1}, u128{hi: ^uint64(0), lo: 5}, 1},
		{"zero_minus_max_wraps_to_one", u128{}, maxVal, u128{lo: 1}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, borrow := sub128(tc.u, tc.v)
			assert.Equal(t, tc.want, got, "difference")
			assert.Equal(t, tc.wantBorrow, borrow, "borrow")
		})
	}
}

func TestSub128Boundaries(t *testing.T) {
	for _, tu := range boundary128 {
		for _, tv := range boundary128 {
			t.Run(tu.name+"_minus_"+tv.name, func(t *testing.T) {
				got, borrow := sub128(tu.v, tv.v)
				want := new(big.Int).Sub(u128ToBig(tu.v), u128ToBig(tv.v))
				var wantBorrow uint64
				if want.Sign() < 0 {
					wantBorrow = 1
					want.Add(want, mod128big)
				}
				assert.Equal(t, wantBorrow, borrow, "borrow")
				requireU128EqualsBig(t, want, got)
			})
		}
	}
}

func TestSub128Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x5AB, 0x0003))
	for range 100_000 {
		u, v := randShapedU128(rng), randShapedU128(rng)
		got, borrow := sub128(u, v)
		want := new(big.Int).Sub(u128ToBig(u), u128ToBig(v))
		var wantBorrow uint64
		if want.Sign() < 0 {
			wantBorrow = 1
			want.Add(want, mod128big)
		}
		require.Equal(t, wantBorrow, borrow, "borrow of %+v - %+v", u, v)
		requireU128EqualsBig(t, want, got, "%+v - %+v", u, v)
	}
}

func TestNeg128(t *testing.T) {
	tests := []struct {
		name string
		u    u128
		want u128
	}{
		{"neg_zero_is_zero", u128{}, u128{}},
		{"neg_one_is_max128", u128{lo: 1}, u128{hi: ^uint64(0), lo: ^uint64(0)}},
		{"neg_max128_is_one", u128{hi: ^uint64(0), lo: ^uint64(0)}, u128{lo: 1}},
		{"neg_pow2_64", u128{hi: 1}, u128{hi: ^uint64(0)}},
		{"neg_pow2_64_minus_1", u128{lo: ^uint64(0)}, u128{hi: ^uint64(0), lo: 1}},
		{"neg_pow2_127_is_itself", u128{hi: 1 << 63}, u128{hi: 1 << 63}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, neg128(tc.u))
		})
	}
}

func TestNeg128Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x4E6, 0x0004))
	for range 100_000 {
		u := randShapedU128(rng)
		got := neg128(u)
		want := new(big.Int).Sub(mod128big, u128ToBig(u))
		want.And(want, mask128big)
		requireU128EqualsBig(t, want, got, "neg128(%+v)", u)

		// u + (-u) must always wrap back to exactly zero.
		sum, _ := add128(u, got)
		require.Equal(t, u128{}, sum, "u + neg128(u) for %+v", u)
	}
}

func TestInc128(t *testing.T) {
	tests := []struct {
		name string
		u    u128
		want u128
	}{
		{"zero_to_one", u128{}, u128{lo: 1}},
		{"lo_carry_into_hi", u128{lo: ^uint64(0)}, u128{hi: 1}},
		{"no_carry", u128{hi: 7, lo: 41}, u128{hi: 7, lo: 42}},
		{"max128_wraps_to_zero", u128{hi: ^uint64(0), lo: ^uint64(0)}, u128{}},
		{"pow2_127_boundary", u128{hi: (1 << 63) - 1, lo: ^uint64(0)}, u128{hi: 1 << 63}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, inc128(tc.u))
		})
	}
}

func TestInc128Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x14C, 0x0005))
	one := u128{lo: 1}
	for range 100_000 {
		u := randShapedU128(rng)
		got := inc128(u)
		want, _ := add128(u, one)
		require.Equal(t, want, got, "inc128(%+v)", u)
	}
}

func TestMul128by64OverflowSemantics(t *testing.T) {
	maxVal := u128{hi: ^uint64(0), lo: ^uint64(0)}
	tests := []struct {
		name         string
		u            u128
		v            uint64
		want         u128
		wantOverflow uint64
	}{
		{"max128_times_zero", maxVal, 0, u128{}, 0},
		{"max128_times_one_no_overflow", maxVal, 1, maxVal, 0},
		{"max128_times_two_overflows_by_one", maxVal, 2, u128{hi: ^uint64(0), lo: ^uint64(1)}, 1},
		{"pow2_127_times_two_wraps_to_zero", u128{hi: 1 << 63}, 2, u128{}, 1},
		{"pow2_127_times_three", u128{hi: 1 << 63}, 3, u128{hi: 1 << 63}, 1},
		{"max128_times_max64", maxVal, ^uint64(0), u128{hi: ^uint64(0), lo: 1}, ^uint64(1)},
		{"pow2_64_minus_1_times_max64_fits", u128{lo: ^uint64(0)}, ^uint64(0), u128{hi: ^uint64(1), lo: 1}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, overflow := mul128by64(tc.u, tc.v)
			assert.Equal(t, tc.want, got, "low 128 bits")
			assert.Equal(t, tc.wantOverflow, overflow, "overflow word")
		})
	}
}

func TestMul128by64Boundaries(t *testing.T) {
	for _, tu := range boundary128 {
		for _, tv := range boundary64 {
			t.Run(tu.name+"_times_"+tv.name, func(t *testing.T) {
				full := new(big.Int).Mul(u128ToBig(tu.v), new(big.Int).SetUint64(tv.v))

				got, overflow := mul128by64(tu.v, tv.v)
				assert.Equal(t, new(big.Int).Rsh(full, 128).Uint64(), overflow, "overflow word")
				requireU128EqualsBig(t, new(big.Int).And(full, mask128big), got)

				d2, d1, d0 := mul128by64to192(tu.v, tv.v)
				assert.Equal(t, got, u128{hi: d1, lo: d0}, "to192 low words")
				assert.Equal(t, overflow, d2, "to192 top word")
			})
		}
	}
}

func TestMul128by64Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x4163, 0x0006))
	for range 100_000 {
		u, v := randShapedU128(rng), randShaped64(rng)
		full := new(big.Int).Mul(u128ToBig(u), new(big.Int).SetUint64(v))

		got, overflow := mul128by64(u, v)
		require.Equal(t, new(big.Int).Rsh(full, 128).Uint64(), overflow, "overflow of %+v * %d", u, v)
		requireU128EqualsBig(t, new(big.Int).And(full, mask128big), got, "%+v * %d", u, v)
	}
}

func TestMul128by64to192Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x4192, 0x0007))
	for range 100_000 {
		u, v := randShapedU128(rng), randShaped64(rng)
		d2, d1, d0 := mul128by64to192(u, v)

		got := new(big.Int).SetUint64(d2)
		for _, limb := range []uint64{d1, d0} {
			got.Lsh(got, 64)
			got.Or(got, new(big.Int).SetUint64(limb))
		}
		want := new(big.Int).Mul(u128ToBig(u), new(big.Int).SetUint64(v))
		require.Zero(t, want.Cmp(got), "%+v * %d: want %s, got %s", u, v, want.Text(16), got.Text(16))
	}
}

func TestU128Shifts(t *testing.T) {
	shifts := []uint{0, 1, 7, 31, 32, 33, 63}
	for _, tu := range boundary128 {
		for _, n := range shifts {
			t.Run(fmt.Sprintf("%s_by_%d", tu.name, n), func(t *testing.T) {
				wantL := new(big.Int).Lsh(u128ToBig(tu.v), n)
				wantL.And(wantL, mask128big)
				requireU128EqualsBig(t, wantL, tu.v.lsh(n), "lsh")

				wantR := new(big.Int).Rsh(u128ToBig(tu.v), n)
				requireU128EqualsBig(t, wantR, tu.v.rsh(n), "rsh")
			})
		}
	}
}

func TestU128ShiftsRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x5417, 0x0008))
	for range 100_000 {
		u := randShapedU128(rng)
		n := rng.UintN(64)

		wantL := new(big.Int).Lsh(u128ToBig(u), n)
		wantL.And(wantL, mask128big)
		requireU128EqualsBig(t, wantL, u.lsh(n), "%+v << %d", u, n)

		wantR := new(big.Int).Rsh(u128ToBig(u), n)
		requireU128EqualsBig(t, wantR, u.rsh(n), "%+v >> %d", u, n)
	}
}

func TestQuoRem64TwoStepSemantics(t *testing.T) {
	maxVal := u128{hi: ^uint64(0), lo: ^uint64(0)}
	tests := []struct {
		name  string
		u     u128
		v     uint64
		wantQ u128
		wantR uint64
	}{
		{"zero_div_one", u128{}, 1, u128{}, 0},
		{"max128_div_one", maxVal, 1, maxVal, 0},
		{"pow2_64_div_two", u128{hi: 1}, 2, u128{lo: 1 << 63}, 0},
		{"pow2_64_plus_1_div_two", u128{hi: 1, lo: 1}, 2, u128{lo: 1 << 63}, 1},
		{"hi_ge_divisor_two_steps", u128{hi: 7, lo: 9}, 2, u128{hi: 3, lo: (1 << 63) + 4}, 1},
		{"max128_div_two", maxVal, 2, u128{hi: (1 << 63) - 1, lo: ^uint64(0)}, 1},
		{"max128_div_max64", maxVal, ^uint64(0), u128{hi: 1, lo: 1}, 0},
		{"hi_equal_divisor", u128{hi: 5, lo: 3}, 5, u128{hi: 1, lo: 0}, 3},
		{"small_div_large_all_remainder", u128{lo: 41}, 42, u128{}, 41},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q, r := quoRem64(tc.u, tc.v)
			assert.Equal(t, tc.wantQ, q, "quotient")
			assert.Equal(t, tc.wantR, r, "remainder")
		})
	}
}

func TestQuoRem64Boundaries(t *testing.T) {
	for _, tu := range boundary128 {
		for _, tv := range boundary64 {
			if tv.v == 0 {
				continue // v != 0 is a documented precondition
			}
			t.Run(tu.name+"_div_"+tv.name, func(t *testing.T) {
				q, r := quoRem64(tu.v, tv.v)
				wantQ, wantR := new(big.Int).QuoRem(
					u128ToBig(tu.v), new(big.Int).SetUint64(tv.v), new(big.Int))
				requireU128EqualsBig(t, wantQ, q, "quotient")
				assert.Equal(t, wantR.Uint64(), r, "remainder")
			})
		}
	}
}

func TestQuoRem64Random(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x99e0, 0x0009))
	for range 100_000 {
		u := randShapedU128(rng)
		v := randShaped64(rng)
		if v == 0 {
			v = 1
		}
		q, r := quoRem64(u, v)
		wantQ, wantR := new(big.Int).QuoRem(u128ToBig(u), new(big.Int).SetUint64(v), new(big.Int))
		requireU128EqualsBig(t, wantQ, q, "quotient of %+v / %d", u, v)
		require.Equal(t, wantR.Uint64(), r, "remainder of %+v / %d", u, v)
	}
}
