package zerodecimal

import (
	"math/big"
	"math/rand/v2"
	"testing"
)

func TestDivmodU256Pow10AgainstBigInt(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x526f756e64, 0x506f773130))
	for k := uint8(1); k <= MaxPrec; k++ {
		den := pow10Big(int(k))
		check := func(u u256) {
			t.Helper()
			wantQ, wantR := new(big.Int).QuoRem(u256ToBig(u), den, new(big.Int))
			q, r, fits := divmodU256Pow10(u, k)
			if fits != (wantQ.BitLen() <= 128) {
				t.Fatalf("k=%d u=%+v: fits=%t, quotient=%s", k, u, fits, wantQ)
			}
			if fits && (u128ToBig(q).Cmp(wantQ) != 0 || r != wantR.Uint64()) {
				t.Fatalf("k=%d u=%+v: got (%+v,%d), want (%s,%s)", k, u, q, r, wantQ, wantR)
			}
		}
		// The exact overflow threshold and both adjacent dividends, plus
		// ties on both quotient parities at each representational width.
		bound := overflowBound256(k)
		for _, delta := range []int64{-1, 0, 1} {
			check(bigToU256(t, new(big.Int).Add(bound, big.NewInt(delta))))
		}
		for _, q := range []u128{{}, {lo: 1}, {lo: ^uint64(0)}, {hi: 1}, {hi: ^uint64(0), lo: ^uint64(0)}} {
			for _, r := range []uint64{0, pow10u64[k]/2 - 1, pow10u64[k] / 2, pow10u64[k]/2 + 1, pow10u64[k] - 1} {
				u := new(big.Int).Mul(u128ToBig(q), den)
				u.Add(u, new(big.Int).SetUint64(r))
				check(bigToU256(t, u))
			}
		}
		for i := range 2_000 {
			u := u256{d0: rng.Uint64()}
			if i%4 >= 1 {
				u.d1 = rng.Uint64()
			}
			if i%4 >= 2 {
				u.d2 = rng.Uint64N(pow10u64[k])
			}
			if i%4 == 3 {
				u.d3 = rng.Uint64()
			}
			check(u)
		}
	}
}

func TestMulRoundPowerTenWidthsAndModes(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x4d756c526f756e64, 0x576964746873))
	for i := range 6_000 {
		a, b := exactRandomDecimal(rng, false), exactRandomDecimal(rng, false)
		// The existing full-width oracle mostly sees overflow. Deliberately
		// cover 64-bit products, 128-bit products, and wide fitting products.
		switch i % 4 {
		case 0:
			a.coef, b.coef = u128{lo: uint64(uint32(a.coef.lo))}, u128{lo: uint64(uint32(b.coef.lo))}
		case 1:
			a.coef.hi, b.coef.hi = 0, 0
		case 2:
			b.coef.hi = 0
		}
		a = newDecimal(a.coef, a.neg, a.prec)
		b = newDecimal(b.coef, b.neg, b.prec)
		places := uint8(rng.Uint64N(uint64(MaxPrec) + 1))
		for mode := ToNearestAway; mode <= TowardNegative; mode++ {
			checkDirectRoundOracle(t, "mul", a, b, places, mode, a.MulRound)
		}
	}
}
