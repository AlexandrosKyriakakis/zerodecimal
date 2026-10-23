package zerodecimal

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/require"
)

// shapedCoef returns a nonzero coefficient whose bit length is uniform over
// 1..maxBits, so one-limb, two-limb and boundary widths all occur often —
// unlike exactRandomDecimal's uniform 128-bit draws, which almost never take
// the narrow division paths.
func shapedCoef(rng *rand.Rand, maxBits int) u128 {
	n := 1 + rng.IntN(maxBits)
	var c u128
	switch {
	case n <= 64:
		c.lo = rng.Uint64() >> (64 - n)
		c.lo |= 1 << (n - 1)
	default:
		c.hi = rng.Uint64() >> (128 - n)
		c.hi |= 1 << (n - 65)
		c.lo = rng.Uint64()
	}
	return c
}

func shapedDecimal(rng *rand.Rand, maxBits int, prec uint8) Decimal {
	return newDecimal(shapedCoef(rng, maxBits), rng.Uint64()&1 == 1, prec)
}

// TestDirectRoundNarrowShapesAgainstBigRat holds MulRound and DivRound to the
// exact big.Rat oracle on the operand widths their narrow paths dispatch on:
// one-limb products and quotients (single multiply, reciprocal or hardware
// divide), two-limb ones, products past 128 bits, and power-of-ten
// denominators past 10^MaxPrec (two reciprocal passes).
func TestDirectRoundNarrowShapesAgainstBigRat(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6e6172726f77, 0x726f756e64))
	for i := range 60_000 {
		widths := [...]int{20, 40, 64, 96, 128}
		a := shapedDecimal(rng, widths[i%5], uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		b := shapedDecimal(rng, widths[(i/5)%5], uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		places := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		mode := RoundingMode(rng.Uint64N(uint64(TowardNegative) + 1))
		checkDirectRoundOracle(t, "mul", a, b, places, mode, a.MulRound)
		checkDirectRoundOracle(t, "div", a, b, places, mode, a.DivRound)
	}
}

// TestMulRoundPow10Ties pins every outcome of divRoundPow10's half comparison
// in all rounding modes: just below, exactly on and just above a tie, for
// one-limb (k ≤ MaxPrec, reciprocal) and two-limb (k > MaxPrec) powers of
// ten, whose ties hinge on remainder digits below the 19th.
func TestMulRoundPow10Ties(t *testing.T) {
	t.Parallel()
	one := NewFromInt(1)
	for k := 1; k <= 2*int(MaxPrec); k++ {
		// d·1 at precision k + places puts the tie of d's coefficient at 10^k/2.
		for _, places := range []uint8{0, 2} {
			natural := k + int(places)
			if natural > 2*int(MaxPrec) {
				continue
			}
			dPrec := uint8(min(natural, int(MaxPrec)))
			ePrec := uint8(natural) - dPrec
			e := newDecimal(one.coef, false, ePrec)
			half := new(big.Int).Div(exactPow10(k), big.NewInt(2))
			for _, q := range []int64{0, 1, 2, 7} {
				base := new(big.Int).Mul(big.NewInt(q), exactPow10(k))
				base.Add(base, half)
				for _, delta := range []int64{-1, 0, 1} {
					c := new(big.Int).Add(base, big.NewInt(delta))
					if c.Sign() <= 0 || c.BitLen() > 128 {
						continue
					}
					for _, neg := range []bool{false, true} {
						d := newDecimal(exactU128FromBig(c), neg, dPrec)
						for mode := ToNearestAway; mode <= TowardNegative; mode++ {
							checkDirectRoundOracle(t, "mul", d, e, places, mode, d.MulRound)
						}
					}
				}
			}
		}
	}
}

// TestDivRoundNarrowTies pins DivRound ties on each width of the divRoundWide
// dispatch: (2m+1)x / 2x is exactly half at places 0 whatever x is, so x
// picks the path while the tie stays exact; ±1 on the dividend straddles it.
func TestDivRoundNarrowTies(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x7469, 0x6573))
	for range 4_000 {
		x := shapedCoef(rng, 63)
		m := rng.Uint64N(1 << uint(rng.IntN(60)))
		xb := exactBigFromU128(x)
		num := new(big.Int).Mul(xb, new(big.Int).SetUint64(2*m+1))
		den := new(big.Int).Lsh(xb, 1)
		for _, delta := range []int64{-1, 0, 1} {
			n := new(big.Int).Add(num, big.NewInt(delta))
			if n.Sign() <= 0 || n.BitLen() > 128 {
				continue
			}
			d := newDecimal(exactU128FromBig(n), rng.Uint64()&1 == 1, 0)
			e := newDecimal(exactU128FromBig(den), rng.Uint64()&1 == 1, 0)
			for mode := ToNearestAway; mode <= TowardNegative; mode++ {
				checkDirectRoundOracle(t, "div", d, e, 0, mode, d.DivRound)
			}
		}
	}
}

// TestDirectRoundNarrowBoundaries pins the width boundaries the dispatch
// tests: products and scaled dividends at 2^64-1 and 2^64, a quotient whose
// high limb equals the divisor (the bits.Div64 trap boundary), and the
// 10^k·2^128 fit boundary of a wide power-of-ten division.
func TestDirectRoundNarrowBoundaries(t *testing.T) {
	t.Parallel()
	maxLo := Decimal{coef: u128{lo: maxUint64}}
	twoTo64 := Decimal{coef: u128{hi: 1}}
	maxCoef := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	cases := []struct{ a, b Decimal }{
		{maxLo, NewFromInt(1)},
		{twoTo64, NewFromInt(1)},
		{maxLo, NewFromInt(math.MaxInt64)},
		{maxLo, maxLo},
		{maxCoef, maxCoef},
		{maxCoef, NewFromInt(10)},
		{Decimal{coef: u128{hi: 7, lo: 5}}, NewFromInt(7)},
		{Decimal{coef: u128{hi: 6, lo: maxUint64}}, NewFromInt(7)},
		{Decimal{coef: u128{hi: 7}}, NewFromInt(8)},
		{RequireFromString("1.5"), RequireFromString("3.7")},
		{RequireFromString("0.1234567890123456789"), RequireFromString("0.9876543210987654321")},
		{RequireFromString("3402823669209384634.6337460743176821145"), RequireFromString("0.0000000000000000001")},
	}
	for _, c := range cases {
		for _, a := range []Decimal{c.a, c.a.Neg()} {
			for places := uint8(0); places <= MaxPrec; places++ {
				for mode := ToNearestAway; mode <= TowardNegative; mode++ {
					checkDirectRoundOracle(t, "mul", a, c.b, places, mode, a.MulRound)
					checkDirectRoundOracle(t, "mul", c.b, a, places, mode, c.b.MulRound)
					checkDirectRoundOracle(t, "div", a, c.b, places, mode, a.DivRound)
					checkDirectRoundOracle(t, "div", c.b, a, places, mode, c.b.DivRound)
				}
			}
		}
	}
}

func TestDirectRoundNarrowAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	ops := []struct {
		name string
		fn   func()
	}{
		{"mul_round_one_limb", func() { directSink, errDirect = directQty.MulRound(directPrice, 2, ToNearestEven) }},
		{"mul_round_wide", func() { directSink, errDirect = directWideA.MulRound(directWideB, 2, ToNearestEven) }},
		{"div_round_one_limb", func() { directSink, errDirect = directCentsQty.DivRound(directTotalQty, 2, ToNearestEven) }},
		{"div_round_wide", func() { directSink, errDirect = directWideA.DivRound(directWideB, 19, ToNearestEven) }},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			requireAllocs(t, 0, op.fn)
		})
	}
}

func TestCmpHalf64(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x68616c66, 0x3634))
	check := func(r, den uint64) {
		want := cmpDouble128(u128{lo: r}, u128{lo: den})
		require.Equal(t, want, cmpHalf64(r, den), "r=%d den=%d", r, den)
	}
	for _, den := range []uint64{1, 2, 3, 10, 1 << 63, 1<<63 + 1, maxUint64} {
		for _, r := range []uint64{0, den / 2, (den - 1) / 2, den/2 + 1, den - 1} {
			if r < den {
				check(r, den)
			}
		}
	}
	for range 100_000 {
		den := rng.Uint64() | 1
		check(rng.Uint64N(den), den)
	}
}
