package zerodecimal

import "math/bits"

// IsRounded and FracDigits answer "how many fractional digits does this value
// need?" without building a rounded or trimmed Decimal. Both follow the
// rounding family's wrapper shape (round.go): an inlinable early-out and one
// call into an outlined core, so a caller whose value already fits pays only
// the early-out.
//
// Both reduce to one question about the coefficient: how many of its lowest
// decimal digits are zero. Precision never exceeds MaxPrec, so only the
// MaxPrec lowest digits can matter, and they are the coefficient modulo
// 10^MaxPrec < 2^64: one limb. A one-limb coefficient is its own answer;
// modTenToMaxPrec reduces a two-limb one with a single reciprocal step. (Its
// low limb alone would not do: 2^64 is not a multiple of any 10^k.)

// tenToMaxPrec is 10^MaxPrec, the largest power of ten below 2^64. It is also
// above 2^63, so as a divisor it is already normalized for div2by1
// (pow10Tab[MaxPrec].dn == tenToMaxPrec, shift zero).
const tenToMaxPrec = 10_000_000_000_000_000_000

// modTenToMaxPrec returns u mod 10^MaxPrec, the MaxPrec lowest decimal digits
// of u.
//
// It is divmod128Pow10Slow at k = MaxPrec with the first of its two div2by1
// steps folded away. The normalization shift is zero, so that step divides u.hi
// by 10^MaxPrec, a quotient of 0 or 1 (u.hi < 2^64 < 2·10^MaxPrec) that a
// compare and subtract replace; the remainder r1 < 10^MaxPrec then meets
// div2by1's precondition, as do the divisor and its reciprocal.
func modTenToMaxPrec(u u128) uint64 {
	hi := u.hi
	if hi >= tenToMaxPrec {
		hi -= tenToMaxPrec
	}
	_, r := div2by1(hi, u.lo, tenToMaxPrec, pow10Tab[MaxPrec].v)
	return r
}

// IsRounded reports whether d has no nonzero digit past places fractional
// digits, i.e. whether d equals d.Truncate(places) numerically. Then rounding
// d to places digits changes nothing in any mode, so IsRounded is the check
// d.Equal(d.RoundBank(places)) without building the rounded value or
// comparing across precisions. It is always true when places ≥ d.Prec(), and
// for zero. Infallible; it never allocates.
func (d Decimal) IsRounded(places uint8) bool {
	if places >= d.prec {
		return true
	}
	return d.isRoundedCore(places)
}

// isRoundedCore reports whether 10^k divides the coefficient, k = d.prec -
// places: exactly then its k lowest digits are zero, and only the remainder
// of the division matters. Caller guarantees places < d.prec, so
// 1 ≤ k ≤ MaxPrec, the divmod helpers' precondition. Since k ≤ MaxPrec, 10^k
// divides the coefficient iff it divides its MaxPrec lowest digits, which is
// where a two-limb coefficient starts.
func (d Decimal) isRoundedCore(places uint8) bool {
	k := d.prec - places
	x := d.coef.lo
	if d.coef.hi != 0 {
		x = modTenToMaxPrec(d.coef)
	}
	_, r := divmod64Pow10(x, k)
	return r == 0
}

// FracDigits returns the number of significant fractional digits of d: the
// precision of d.Trim(), computed without building the trimmed value. Prec is
// only the stored precision, and arithmetic keeps trailing zeros, so 0.75+0.75
// has Prec 2 but FracDigits 1; use FracDigits for "how many places does this
// value need". Zero and integers have 0. Infallible; it never allocates.
func (d Decimal) FracDigits() uint8 {
	if d.prec == 0 {
		return 0
	}
	return d.fracDigitsCore()
}

// The rungs of fracDigitsCore's ladder test and divide by 10^s for
// s = 16, 8, 4, 2, 1 with multiplicative inverses (Granlund-Montgomery;
// Hacker's Delight 10-17). For each s, fracInv5pS is the inverse of 5^s modulo
// 2^64 and fracMaxQuoS is ⌊(2^64-1)/10^s⌋; fracDigitsStep explains the use.
// TestFracDigitsLadderConstants recomputes all ten from their definitions.
const (
	fracInv5p16, fracMaxQuo16 = 0xe4a4d1417cd9a041, 0x734
	fracInv5p8, fracMaxQuo8   = 0xc767074b22e90e21, 0x2af31dc461
	fracInv5p4, fracMaxQuo4   = 0xd288ce703afb7e91, 0x68db8bac710cb
	fracInv5p2, fracMaxQuo2   = 0x8f5c28f5c28f5c29, 0x28f5c28f5c28f5c
	fracInv5p1, fracMaxQuo1   = 0xcccccccccccccccd, 0x1999999999999999
)

// fracDigitsStep returns x/10^s and true when 10^s divides x, otherwise some
// value above maxQuo and false, where inv is the inverse of 5^s modulo 2^64
// and maxQuo = ⌊(2^64-1)/10^s⌋. If x = 10^s·q then x·inv = 2^s·q (mod 2^64),
// and q < 2^(64-s) keeps that below 2^64, so rotating it right by s returns
// q ≤ maxQuo; if 10^s does not divide x the rotated product exceeds maxQuo.
// One multiply, one rotate and one compare decide divisibility and produce the
// quotient together, where x%10^s == 0 followed by x/10^s costs a division.
func fracDigitsStep(x, inv, maxQuo uint64, s int) (uint64, bool) {
	q := bits.RotateLeft64(x*inv, -s)
	return q, q <= maxQuo
}

// fracDigitsCore returns prec minus the trailing decimal zeros of the
// coefficient, at most prec of them (Trim stops at precision 0). Caller
// guarantees d.prec > 0.
//
// The zeros are counted on one limb x: the coefficient itself, or for two
// limbs its MaxPrec lowest digits. A value with no trailing zero, the common
// case, leaves after one test of x%10. Otherwise a binary search sheds the
// zeros in rungs of 16, 8, 4, 2 and 1, where rem counts the digits still
// allowed to go: it starts at prec, so a coefficient with more zeros than prec
// stops at prec, and a small prec (money values) runs only its small rungs.
//
// Why the descent is exact. Let R = min(zeros of x, rem), the digits still to
// shed. Before rung s, R < 2s (before the first rung, R <= prec <= 19 < 32). A
// rung fires exactly when rem >= s and 10^s divides x, that is when R >= s, and
// then R - s < s digits remain; a rung that does not fire has R < s. Either way
// R < s before the next rung, so after the last rung (s = 1) none remain and
// exactly min(zeros, prec) digits were shed.
//
// x is zero only for a two-limb coefficient that is a multiple of 10^MaxPrec;
// every rung that rem allows then fires and rem ends at 0.
func (d Decimal) fracDigitsCore() uint8 {
	x := d.coef.lo
	if d.coef.hi != 0 {
		x = modTenToMaxPrec(d.coef)
	}
	if x%10 != 0 {
		return d.prec
	}
	rem := d.prec
	if rem >= 16 {
		if q, ok := fracDigitsStep(x, fracInv5p16, fracMaxQuo16, 16); ok {
			x, rem = q, rem-16
		}
	}
	if rem >= 8 {
		if q, ok := fracDigitsStep(x, fracInv5p8, fracMaxQuo8, 8); ok {
			x, rem = q, rem-8
		}
	}
	if rem >= 4 {
		if q, ok := fracDigitsStep(x, fracInv5p4, fracMaxQuo4, 4); ok {
			x, rem = q, rem-4
		}
	}
	if rem >= 2 {
		if q, ok := fracDigitsStep(x, fracInv5p2, fracMaxQuo2, 2); ok {
			x, rem = q, rem-2
		}
	}
	if rem >= 1 {
		if _, ok := fracDigitsStep(x, fracInv5p1, fracMaxQuo1, 1); ok {
			rem--
		}
	}
	return rem
}
