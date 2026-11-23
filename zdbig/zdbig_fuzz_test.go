//go:build fuzz

package zdbig

import (
	"math"
	"math/big"
	"testing"

	"github.com/AlexandrosKyriakakis/zerodecimal"
)

// FuzzFromBigInt feeds FromBigInt arbitrary coefficients (up to 40 bytes,
// optionally multiplied by up to 10^63 so that trailing zeros, the only thing
// stripping can use, are as common as in real data) and exponents, and holds
// the result to the same three oracles as the deterministic tests: the strict
// parser on "<coef>e<exp>", plain big.Int arithmetic for small exponents, and
// the representation rule. Success, the exact value, the sentinel on failure
// and the chosen precision must all agree, and the argument must come back
// untouched.
func FuzzFromBigInt(f *testing.F) {
	max128Bytes := max128.Bytes()
	for _, s := range []struct {
		mag   []byte
		neg   bool
		exp   int32
		zeros uint8
	}{
		{nil, false, 0, 0},
		{nil, true, math.MinInt32, 0},
		{[]byte{1}, false, 0, 0},
		{[]byte{1}, false, -2, 0},
		{[]byte{1, 0x2c}, true, -2, 0}, // -300 at 2 places
		{max128Bytes, false, 0, 0},
		{max128Bytes, true, -1, 0},
		{max128Bytes, false, -19, 0},
		{max128Bytes, false, -20, 0},
		{max128Bytes, false, 1, 0},
		{two128.Bytes(), false, 0, 0},
		{two128.Bytes(), false, -1, 0},
		{[]byte{1}, false, -40, 45}, // 10^45 at exp -40: wide, strippable
		{[]byte{1}, false, -10, 45},
		{[]byte{1}, false, -5, 45},
		{[]byte{1}, false, -64, 45},
		{[]byte{1}, false, -65, 45},
		{[]byte{1}, true, -57, 38},
		{[]byte{1}, false, -58, 38},
		{[]byte{1}, false, 38, 0},
		{[]byte{4}, false, 38, 0},
		{[]byte{1}, false, 39, 0},
		{[]byte{1}, false, math.MaxInt32, 0},
		{[]byte{1}, false, math.MinInt32, 0},
		{max128Bytes, false, -5, 5}, // (2^128-1)·10^5 at exp -5: wide, fits at precision 0
		{max128Bytes, false, -4, 5},
	} {
		f.Add(s.mag, s.neg, s.exp, s.zeros)
	}
	f.Fuzz(func(t *testing.T, mag []byte, neg bool, exp int32, zeros uint8) {
		if len(mag) > 40 {
			mag = mag[:40] // keep "<coef>e<exp>" under the parser's 200-byte bound
		}
		coef := new(big.Int).SetBytes(mag)
		coef.Mul(coef, pow10Big(int(zeros%64)))
		if neg {
			coef.Neg(coef)
		}
		checkFromBigInt(t, coef, exp)

		// whatever was accepted converts back to the same value
		d, err := FromBigInt(coef, exp)
		if err != nil {
			return
		}
		c2, e2 := ToBigInt(d)
		if e2 > 0 || e2 < -int32(zerodecimal.MaxPrec) {
			t.Fatalf("ToBigInt exponent %d outside -MaxPrec..0 for %+v", e2, d)
		}
		back, err := FromBigInt(c2, e2)
		if err != nil || back != d {
			t.Fatalf("round trip of %+v through (%s, %d) gave %+v, %v", d, c2, e2, back, err)
		}
	})
}

// FuzzToBigInt builds a Decimal from arbitrary limbs, sign and precision and
// checks ToBigInt against the plain construction (SetUint64, Lsh, Or, Neg),
// then the round trip through FromBigInt, representation included.
func FuzzToBigInt(f *testing.F) {
	for _, s := range []struct {
		hi, lo uint64
		neg    bool
		prec   uint8
	}{
		{0, 0, false, 0},
		{0, 0, true, 5},
		{0, 1, false, 0},
		{0, 1, true, 19},
		{0, math.MaxUint64, false, 0},
		{1, 0, false, 0},
		{1, 0, true, 19},
		{math.MaxUint64, math.MaxUint64, false, 0},
		{math.MaxUint64, math.MaxUint64, true, 19},
		{1 << 63, 0, false, 7},
		{0x4B3B4CA85A86C47A, 0x098A224000000000, false, 0}, // 10^38
	} {
		f.Add(s.hi, s.lo, s.neg, s.prec)
	}
	f.Fuzz(func(t *testing.T, hi, lo uint64, neg bool, prec uint8) {
		d, err := zerodecimal.NewFromHiLo(neg, hi, lo, prec%(zerodecimal.MaxPrec+1))
		if err != nil {
			t.Fatalf("NewFromHiLo: %v", err)
		}
		coef, exp := ToBigInt(d)
		if exp != -int32(d.Prec()) {
			t.Fatalf("exp = %d for precision %d", exp, d.Prec())
		}
		want := bigFromHiLo(hi, lo)
		if neg {
			want.Neg(want)
		}
		if coef.Cmp(want) != 0 {
			t.Fatalf("ToBigInt(%+v) coefficient %s, want %s", d, coef, want)
		}
		back, err := FromBigInt(coef, exp)
		if err != nil || back != d {
			t.Fatalf("round trip of %+v gave %+v, %v", d, back, err)
		}
	})
}
