package zerodecimal

import (
	"fmt"
	"math/big"
	"math/bits"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPow10TabPinnedEntries pins independently hand-derived entries so that
// even a bug shared by the generator and the recomputation below cannot slip
// through unnoticed.
func TestPow10TabPinnedEntries(t *testing.T) {
	tests := []struct {
		name string
		k    int
		want pow10Entry
	}{
		{
			// (2^128-1)/2^63 = 2^65-1, minus 2^64 leaves 2^64-1.
			"k_0_magicless",
			0,
			pow10Entry{d: 1, m: 0, dn: 1 << 63, v: ^uint64(0), p: 0, s: 63},
		},
		{
			// p = 2 is minimal (5 > 2^(1+1)) and m = ⌈2^66/5⌉.
			"k_1_worked_example",
			1,
			pow10Entry{
				d:  10,
				m:  0xCCCCCCCCCCCCCCCD,
				dn: 10 << 60,
				v:  0x9999999999999999,
				p:  2,
				s:  60,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pow10Tab[tc.k])
		})
	}
}

// TestPow10TabMatchesDefinition recomputes every entry from its definition
// with math/big, guarding against table corruption or stale generation.
func TestPow10TabMatchesDefinition(t *testing.T) {
	one := big.NewInt(1)
	pow2 := func(n int) *big.Int { return new(big.Int).Lsh(one, uint(n)) }

	for k := 0; k <= int(MaxPrec); k++ {
		t.Run(fmt.Sprintf("k_%02d", k), func(t *testing.T) {
			e := pow10Tab[k]

			dBig := pow10Big(k)
			require.LessOrEqual(t, dBig.BitLen(), 64, "10^k must fit uint64")
			d := dBig.Uint64()
			assert.Equal(t, d, e.d, "d")

			wantS := uint8(bits.LeadingZeros64(d))
			assert.Equal(t, wantS, e.s, "s")
			assert.Equal(t, d<<wantS, e.dn, "dn")
			assert.Equal(t, uint64(1), e.dn>>63, "dn must be normalized")

			// v = ⌊(2^128-1)/dn⌋ - 2^64.
			wantV := new(big.Int).Sub(pow2(128), one)
			wantV.Div(wantV, new(big.Int).SetUint64(e.dn))
			wantV.Sub(wantV, pow2(64))
			require.LessOrEqual(t, wantV.BitLen(), 64, "v must fit uint64")
			assert.Equal(t, wantV.Uint64(), e.v, "v")

			if k == 0 {
				assert.Zero(t, e.m, "m is unused for k = 0")
				assert.Zero(t, e.p, "p is unused for k = 0")
				return
			}

			// p: the smallest integer with 5^k ≤ 2^(p+k).
			five := new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(k)), nil)
			p := 0
			for pow2(p+k).Cmp(five) < 0 {
				p++
			}
			require.LessOrEqual(t, p, 255, "p must fit uint8")
			assert.Equal(t, uint8(p), e.p, "p")

			// m = ⌈2^(64+p)/5^k⌉.
			wantM := new(big.Int).Sub(five, one)
			wantM.Add(wantM, pow2(64+p))
			wantM.Div(wantM, five)
			require.LessOrEqual(t, wantM.BitLen(), 64, "m must fit uint64")
			assert.Equal(t, wantM.Uint64(), e.m, "m")
		})
	}
}

func TestPow10TabPaddingIsZero(t *testing.T) {
	for k := int(MaxPrec) + 1; k < len(pow10Tab); k++ {
		assert.Equal(t, pow10Entry{}, pow10Tab[k], "entry %d must stay zero", k)
	}
}

func TestPow10U64MatchesBig(t *testing.T) {
	for k := range pow10u64 {
		if k <= int(MaxPrec) {
			want := pow10Big(k)
			require.LessOrEqual(t, want.BitLen(), 64, "10^%d must fit uint64", k)
			assert.Equal(t, want.Uint64(), pow10u64[k], "10^%d", k)
			continue
		}
		assert.Zero(t, pow10u64[k], "entry %d must stay zero", k)
	}
}

func TestPow10U128MatchesBig(t *testing.T) {
	const maxFactor = 2 * int(MaxPrec) // 10^38: largest Mul rescale divisor
	for k := range pow10u128 {
		if k <= maxFactor {
			requireU128EqualsBig(t, pow10Big(k), pow10u128[k], "10^%d", k)
			continue
		}
		assert.Equal(t, u128{}, pow10u128[k], "entry %d must stay zero", k)
	}

	// Spot-pin the two extremes the arithmetic layers lean on.
	assert.Equal(t, uint64(1e19), pow10u64[MaxPrec], "10^19")
	assert.Equal(t,
		u128{hi: 0x4B3B4CA85A86C47A, lo: 0x098A224000000000},
		pow10u128[maxFactor], "10^38")
}
