package zdbig

import (
	"math"
	"math/big"
	"strconv"
	"testing"

	"github.com/AlexandrosKyriakakis/zerodecimal"
)

// Package-level inputs and sinks (see zdbig_alloc_test.go for why neither may
// be local).
var (
	benchDecimal zerodecimal.Decimal
	benchBig     *big.Int
	benchExp     int32
	errBench     error
)

// benchLit parses a decimal integer literal for a package-level input.
func benchLit(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		panic("bad integer literal " + s)
	}
	return n
}

// benchShapes are the (coefficient, exponent) pairs FromBigInt is timed on:
// what a pgtype.Numeric column of money, quantities and rates looks like,
// then each of the other paths and the error returns.
var benchShapes = []struct {
	name string
	coef *big.Int
	exp  int32
}{
	{"money", big.NewInt(1_234_567), -2}, // 12345.67: one limb, two places
	{"negative_money", big.NewInt(-1_234_567), -2},
	{"integer", big.NewInt(42), 0},
	{"rate_19dp", benchLit("1234567890123456789"), -19}, // a 19-digit coefficient at 19 places
	{"two_limbs", benchLit("123456789012345678901234567890"), -10},
	{"max128", max128, -19},
	{"scale_up", big.NewInt(12_345), 6},
	{"strip", pow10Big(30), -30},      // scale past MaxPrec: zeros stripped in 128 bits
	{"wide_strip", pow10Big(45), -40}, // past 128 bits: the one allocating path
	{"overflow", two128, 0},           // error: wide, rejected on its limbs
	{"precision_error", big.NewInt(1), -20},
}

// BenchmarkFromBigInt times FromBigInt against the two ways a caller gets
// there without it: handrolled is the hand-rolled conversion a caller writes
// without zdbig for a pgtype.Numeric (copy the magnitude, scale and strip
// with big.Int arithmetic, split into halves), and string is the naive route
// through the strict parser. Both of those trim trailing zeros, which
// FromBigInt leaves to Trim; the results are the same value either way.
func BenchmarkFromBigInt(b *testing.B) {
	for _, s := range benchShapes {
		coef, exp := s.coef, s.exp
		b.Run(s.name+"/zdbig", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchDecimal, errBench = FromBigInt(coef, exp)
			}
		})
		b.Run(s.name+"/handrolled", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchDecimal, errBench = handRolledFromBigInt(coef, exp)
			}
		})
		b.Run(s.name+"/string", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchDecimal, errBench = zerodecimal.NewFromString(coef.String() + "e" + strconv.Itoa(int(exp)))
			}
		})
	}
}

// benchDecimals are the Decimals ToBigInt is timed on.
var benchDecimals = []struct {
	name string
	d    zerodecimal.Decimal
}{
	{"zero", zerodecimal.Decimal{}},
	{"money", zerodecimal.MustNew(1_234_567, -2)},
	{"negative_money", zerodecimal.MustNew(-1_234_567, -2)},
	{"rate_19dp", zerodecimal.RequireFromString("0.1234567890123456789")},
	{"two_limbs", zerodecimal.RequireFromString("1234567890123456789012345.6789")},
	{"max128_negative", func() zerodecimal.Decimal {
		d, err := zerodecimal.NewFromHiLo(true, math.MaxUint64, math.MaxUint64, 19)
		if err != nil {
			panic(err)
		}
		return d
	}()},
}

// BenchmarkToBigInt times ToBigInt against the hand-rolled conversion a
// caller writes without zdbig: the coefficient built from hi and lo with
// SetUint64, Lsh and Or, then negated.
func BenchmarkToBigInt(b *testing.B) {
	for _, s := range benchDecimals {
		d := s.d
		b.Run(s.name+"/zdbig", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchBig, benchExp = ToBigInt(d)
			}
		})
		b.Run(s.name+"/handrolled", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchBig, benchExp = handRolledToBigInt(d)
			}
		})
	}
}

// BenchmarkFromBigIntHostile times the worst input a wide coefficient can be:
// 10^n with an exponent that makes every one of its trailing zeros
// strippable, so that no cheap exit turns it away. The cost is one decimal
// conversion of the coefficient, which grows a little faster than linearly.
// The not_a_multiple_of_ten rows are the inputs the cheap exits do turn away.
func BenchmarkFromBigIntHostile(b *testing.B) {
	for _, digits := range []int{1_000, 10_000, 100_000} {
		coef, exp := pow10Big(digits), int32(-(digits + 19))
		b.Run(strconv.Itoa(digits)+"_digits", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchDecimal, errBench = FromBigInt(coef, exp)
			}
		})
	}
	// The same sizes as 2^n: even, never a multiple of five, and at an
	// exponent that leaves the value small, so the size exit does not see
	// them either. The multiple-of-ten exit turns them away after a pass over
	// the limbs, with no decimal conversion.
	for _, digits := range []int{1_000, 10_000, 100_000} {
		coef, exp := new(big.Int).Lsh(bigOne, uint(digits*332/100)), int32(-(digits - 20))
		b.Run("not_a_multiple_of_ten_"+strconv.Itoa(digits)+"_digits", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				benchDecimal, errBench = FromBigInt(coef, exp)
			}
		})
	}
}

// handRolledFromBigInt is the hand-rolled conversion a caller writes without
// zdbig for a finite Numeric: copy the magnitude, scale up with Exp and Mul,
// strip every trailing zero with one QuoRem per digit, split the coefficient
// into halves with And and Rsh, and fall back to a decimal string when it is
// wider than 128 bits.
func handRolledFromBigInt(n *big.Int, exp int32) (zerodecimal.Decimal, error) {
	if n == nil || n.Sign() == 0 {
		return zerodecimal.Decimal{}, nil
	}
	neg := n.Sign() < 0
	coef := new(big.Int).Abs(n)
	if exp > 0 {
		mul := new(big.Int).Exp(bigTen, big.NewInt(int64(exp)), nil)
		coef.Mul(coef, mul)
		exp = 0
	}
	for exp < 0 && coef.Sign() != 0 {
		var rem big.Int
		q := new(big.Int)
		q.QuoRem(coef, bigTen, &rem)
		if rem.Sign() != 0 {
			break
		}
		coef = q
		exp++
	}
	var prec uint8
	if exp < 0 {
		scale := -exp
		if scale > int32(zerodecimal.MaxPrec) {
			return zerodecimal.Decimal{}, zerodecimal.ErrPrecOutOfRange
		}
		prec = uint8(scale)
	}
	if coef.BitLen() <= 128 {
		lo := new(big.Int).And(coef, new(big.Int).SetUint64(math.MaxUint64)).Uint64()
		hi := new(big.Int).Rsh(new(big.Int).Set(coef), 64).Uint64()
		return zerodecimal.NewFromHiLo(neg, hi, lo, prec)
	}
	s := coef.String()
	if prec > 0 {
		// A hand-rolled conversion zero-pads here when the coefficient is
		// shorter than the precision; a coefficient past 128 bits has 39
		// digits or more, so that never applies on this path.
		i := len(s) - int(prec)
		s = s[:i] + "." + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return zerodecimal.NewFromString(s)
}

// handRolledToBigInt is the hand-rolled conversion a caller writes without
// zdbig for a finite Decimal: a 128-bit coefficient (SetUint64 of the high
// half, Lsh, Or with the low half), Neg for the sign, and the exponent -prec.
func handRolledToBigInt(d zerodecimal.Decimal) (*big.Int, int32) {
	neg, hi, lo, prec := d.ToHiLo()
	n := new(big.Int).SetUint64(hi)
	n.Lsh(n, 64)
	n.Or(n, new(big.Int).SetUint64(lo))
	if neg {
		n.Neg(n)
	}
	return n, -int32(prec)
}
