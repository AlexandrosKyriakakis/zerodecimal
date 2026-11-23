package zdbig

import (
	"math/big"
	"math/bits"
	"strings"

	"github.com/AlexandrosKyriakakis/zerodecimal"
)

// maxPrec is the largest fractional precision of a Decimal.
const maxPrec = int64(zerodecimal.MaxPrec)

// pow10 holds 10^k for k = 0..19, the powers of ten that fit one 64-bit limb.
var pow10 = [20]uint64{
	1,
	10,
	100,
	1_000,
	10_000,
	100_000,
	1_000_000,
	10_000_000,
	100_000_000,
	1_000_000_000,
	10_000_000_000,
	100_000_000_000,
	1_000_000_000_000,
	10_000_000_000_000,
	100_000_000_000_000,
	1_000_000_000_000_000,
	10_000_000_000_000_000,
	100_000_000_000_000_000,
	1_000_000_000_000_000_000,
	10_000_000_000_000_000_000,
}

// FromBigInt returns coef·10^exp, exactly. It generalizes zerodecimal.New to
// an arbitrary-precision coefficient, with the same contract:
//
//   - A nil or zero coef gives the canonical zero Decimal{} for every exp.
//   - A positive exp scales the coefficient: ErrOverflow when the result
//     exceeds 128 bits, which is always so once exp > 38 for a nonzero coef.
//   - A negative exp becomes the fractional precision -exp. While that exceeds
//     MaxPrec, or while the coefficient is wider than 128 bits, factors of ten
//     are stripped from the coefficient, but only as many as needed and only
//     while the value stays exact. If the coefficient cannot be brought into
//     128 bits that way, the result is ErrOverflow. If it can, but the
//     precision still exceeds MaxPrec, the result is ErrPrecOutOfRange.
//
// Like New, and unlike parsing, FromBigInt keeps trailing zeros: (150, -2) is
// 1.50 at precision 2, and a coefficient that needs no stripping keeps the
// precision -exp. Trim canonicalizes the result. The sign of coef is the
// sign of the result, and coef itself is neither modified nor retained.
//
// FromBigInt does not allocate when |coef| fits 128 bits, on success and on
// error. A wider coefficient costs about one decimal conversion of itself,
// unless it is too large for exp or not a multiple of ten, which are turned
// away after at most a pass over its limbs; cap the size of untrusted input
// before converting it. The two errors are the bare zerodecimal sentinels, as
// for New.
//
// A nil coef is zero, and so is the Int of a pgtype.Numeric that is SQL NULL,
// NaN or an infinity: FromBigInt cannot tell those from a zero, and returns
// Decimal{} with no error. Test the pgtype.Numeric first, and convert only a
// finite one:
//
//	if !n.Valid || n.NaN || n.InfinityModifier != pgtype.Finite {
//		// NULL, NaN or ±Infinity: there is no Decimal to convert to
//	}
//	d, err := zdbig.FromBigInt(n.Int, n.Exp)
func FromBigInt(coef *big.Int, exp int32) (zerodecimal.Decimal, error) {
	if coef == nil {
		return zerodecimal.Decimal{}, nil
	}
	limbs := coef.Bits() // the magnitude, in place: no copy
	if len(limbs) == 0 {
		return zerodecimal.Decimal{}, nil
	}
	hi, lo, ok := u128FromLimbs(limbs, bits.UintSize)
	if !ok {
		return fromWide(coef, exp)
	}
	return from128(coef.Sign() < 0, hi, lo, exp)
}

// from128 converts the nonzero magnitude hi:lo, which fits 128 bits.
func from128(neg bool, hi, lo uint64, exp int32) (zerodecimal.Decimal, error) {
	switch {
	case exp == 0:
		return zerodecimal.NewFromHiLo(neg, hi, lo, 0)
	case exp > 0:
		// 10^exp itself has no 128-bit form past exp 38, and the coefficient
		// is at least 1.
		pow, err := zerodecimal.New(1, exp)
		if err != nil {
			return zerodecimal.Decimal{}, zerodecimal.ErrOverflow
		}
		scaled, _ := zerodecimal.NewFromHiLo(neg, hi, lo, 0) // precision 0 cannot fail
		return scaled.MulExact(pow)                          // ErrOverflow, nothing else at precision 0
	}

	// The negated exponent is the precision. Negate in int64: exp can be
	// MinInt32.
	scale := -int64(exp)
	if scale <= maxPrec {
		return zerodecimal.NewFromHiLo(neg, hi, lo, uint8(scale))
	}
	hi, lo, ok := divPow10Exact(hi, lo, scale-maxPrec)
	if !ok {
		return zerodecimal.Decimal{}, zerodecimal.ErrPrecOutOfRange
	}
	return zerodecimal.NewFromHiLo(neg, hi, lo, zerodecimal.MaxPrec)
}

// divPow10Exact returns hi:lo / 10^k when 10^k divides it. The value is
// nonzero and below 2^128 < 10^39, so no k past 38 can divide it, and it
// divides by one limb of digits at a time, each remainder checked.
func divPow10Exact(hi, lo uint64, k int64) (quoHi, quoLo uint64, ok bool) {
	if k > 38 {
		return 0, 0, false
	}
	for k > 0 {
		step := min(k, 19)
		d := pow10[step]
		qHi := hi / d
		qLo, rem := bits.Div64(hi%d, lo, d) // hi%d < d, so the quotient fits a limb
		if rem != 0 {
			return 0, 0, false
		}
		hi, lo = qHi, qLo
		k -= step
	}
	return hi, lo, true
}

// two128Digits is 2^128 in decimal, 39 digits.
const two128Digits = "340282366920938463463374607431768211456"

// fromWide converts a coefficient wider than 128 bits. Only a negative exp
// can help: it makes room to strip trailing decimal zeros until the
// coefficient fits. This is the one path that allocates.
//
// The decision is closed-form on the decimal digits. Let the coefficient have
// n digits, z of them trailing zeros, and let fit be the fewest digits to drop
// for the rest to be below 2^128. Stripping s digits leaves precision
// scale-s, so a result needs
//
//	s ≥ fit        (the coefficient fits 128 bits)
//	s ≥ scale-19   (the precision fits MaxPrec)
//	s ≤ z          (only zeros are dropped, the value stays exact)
//	s ≤ scale      (the precision is not negative)
//
// and the smallest such s is the answer, as New would pick: strip only as many
// digits as needed. When none exists, ErrOverflow means the coefficient with
// all its zeros removed (fit > z), or the integer part of the value (fit >
// scale), is still past 128 bits; otherwise the coefficient fits and only the
// precision (scale-z > MaxPrec) does not: ErrPrecOutOfRange. That is the
// parser's classification, and ErrOverflow comes first there as well.
func fromWide(coef *big.Int, exp int32) (zerodecimal.Decimal, error) {
	if exp >= 0 {
		return zerodecimal.Decimal{}, zerodecimal.ErrOverflow // |coef|·10^exp ≥ |coef| ≥ 2^128
	}
	scale := -int64(exp)

	// Two cheap exits before any conversion. If |coef| ≥ 2^128·10^scale even
	// stripping scale digits leaves a magnitude past 2^128. And a coefficient
	// that is not a multiple of ten has no zero to strip (z = 0 < fit), which
	// is the usual reason a wide coefficient fails: nine random ones in ten.
	if int64(coef.BitLen())-1 >= 128+pow10Bits(scale) || !multipleOf10(coef.Bits()) {
		return zerodecimal.Decimal{}, zerodecimal.ErrOverflow
	}

	digits := coef.Text(10)
	neg := digits[0] == '-'
	if neg {
		digits = digits[1:]
	}
	n := int64(len(digits)) // at least 39: the coefficient is at least 2^128
	zeros := n - int64(len(strings.TrimRight(digits, "0")))
	fit := n - 38 // the first 39 digits are 2^128 or more: drop one more
	if digits[:39] < two128Digits {
		fit = n - 39
	}
	switch {
	case fit > zeros || fit > scale:
		return zerodecimal.Decimal{}, zerodecimal.ErrOverflow
	case scale-maxPrec > zeros:
		return zerodecimal.Decimal{}, zerodecimal.ErrPrecOutOfRange
	}
	strip := max(fit, scale-maxPrec)
	//nolint:gosec // 0 ≤ scale-strip ≤ MaxPrec here (strip ≥ scale-MaxPrec, strip ≤ scale), so the uint8 conversion is exact
	return decimalFromDigits(neg, digits[:n-strip], uint8(scale-strip))
}

// decimalFromDigits returns the integer written by digits, with sign neg, as a
// Decimal of precision prec. fromWide hands it at most 39 digits that name a
// value below 2^128, which none of the parser's limits refuses, so it cannot
// fail today. The parser's error is propagated, not dropped: if the parser's
// contract ever changed, the caller gets an error here instead of a silently
// lost coefficient, which would be a wrong zero.
func decimalFromDigits(neg bool, digits string, prec uint8) (zerodecimal.Decimal, error) {
	kept, err := zerodecimal.NewFromString(digits)
	if err != nil {
		return zerodecimal.Decimal{}, err
	}
	_, hi, lo, _ := kept.ToHiLo()
	return zerodecimal.NewFromHiLo(neg, hi, lo, prec)
}

// pow10Bits returns b with 10^scale < 2^b, for 0 ≤ scale ≤ 2^31. It rounds
// log2(10) = 3.32192809… up, so the bound is safe, and 2^31·33219281 stays
// far from overflowing int64.
func pow10Bits(scale int64) int64 {
	return scale*33_219_281/10_000_000 + 1
}

// multipleOf10 reports whether the number held by the little-endian limbs of a
// big.Int, of either word width, is a multiple of ten, in one pass and
// without dividing or allocating. It is even when its low bit is clear. It is
// a multiple of five when the sum of its limbs is a multiple of five, because
// 2^32 and 2^64 are both 1 mod 5: each limb counts once, whatever the word
// width.
func multipleOf10(limbs []big.Word) bool {
	if len(limbs) == 0 {
		return true // zero
	}
	if limbs[0]&1 != 0 {
		return false
	}
	var sum uint
	for _, w := range limbs {
		sum += uint(w % 5) // at most 4 per limb: no overflow before 2^61 limbs
	}
	return sum%5 == 0
}

// u128FromLimbs reads the magnitude held by little-endian limbs of limbBits
// bits (the width of big.Word: 32 or 64) as hi·2^64 + lo. ok is false when
// the value does not fit 128 bits. A normalized slice, like the one
// big.Int.Bits returns, is rejected from its top limb alone; zero limbs past
// the 128th bit are tolerated.
func u128FromLimbs(limbs []big.Word, limbBits int) (hi, lo uint64, ok bool) {
	room := 128 / limbBits // limbs that fit below bit 128
	for i := len(limbs) - 1; i >= room; i-- {
		if limbs[i] != 0 {
			return 0, 0, false
		}
	}
	for i := min(len(limbs), room) - 1; i >= 0; i-- {
		if pos := i * limbBits; pos < 64 {
			lo |= uint64(limbs[i]) << pos
		} else {
			hi |= uint64(limbs[i]) << (pos - 64)
		}
	}
	return hi, lo, true
}

// putLimbs writes hi·2^64 + lo into dst as little-endian limbs of limbBits
// bits, all 128/limbBits of them, leading zero limbs included (big.Int's
// SetBits trims those).
func putLimbs(dst []big.Word, limbBits int, hi, lo uint64) {
	mask := ^uint64(0) >> (64 - limbBits)
	for i := range 128 / limbBits {
		var limb uint64
		if pos := i * limbBits; pos < 64 {
			limb = lo >> pos
		} else {
			limb = hi >> (pos - 64)
		}
		dst[i] = big.Word(limb & mask)
	}
}

// bigBuf holds a big.Int together with the storage of its limbs, so that
// ToBigInt makes one allocation instead of two. The limbs are as many as
// 128 bits take at the platform's word size.
type bigBuf struct {
	i big.Int
	w [128 / bits.UintSize]big.Word
}

// ToBigInt returns coef and exp with d = coef·10^exp and exp = -int32(d.Prec()):
// the signed coefficient of d and the negated precision. Zero is (0, 0). The
// pair keeps d's trailing zeros, so FromBigInt(ToBigInt(d)) returns d exactly,
// representation included. It cannot fail.
//
// ToBigInt allocates the returned big.Int, once, limbs included; the caller
// owns it and may modify it freely. A pgtype.Numeric takes the pair as is:
//
//	coef, exp := zdbig.ToBigInt(d)
//	n := pgtype.Numeric{Int: coef, Exp: exp, Valid: true}
func ToBigInt(d zerodecimal.Decimal) (coef *big.Int, exp int32) {
	neg, hi, lo, prec := d.ToHiLo()
	buf := new(bigBuf)
	putLimbs(buf.w[:], bits.UintSize, hi, lo)
	buf.i.SetBits(buf.w[:])
	if neg {
		buf.i.Neg(&buf.i)
	}
	return &buf.i, -int32(prec)
}
