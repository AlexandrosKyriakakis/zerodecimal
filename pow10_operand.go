package zerodecimal

import "math/bits"

// Power-of-ten divisors. Money code divides by contract multipliers whose
// coefficient is a power of ten: 1 for stocks, 100 for options, 0.01 for
// fixed income. Dividing by 10^j only rescales the dividend, so Div routes
// those divisors here instead of through a general 128/64 divide (bits.Div64
// is a software routine on arm64) or a 3-by-2 divide. Every result is
// bit-identical to the general path's.

// isPow10Divisor reports whether Div takes the rescale path for the divisor
// coefficient c: c is 1 (1, 0.1, 0.01, …) or 100. The test is restricted to
// those two coefficients because it runs before every general division: two
// compares cost a non-power-of-ten divisor four instructions, while detecting
// every 10^j ≤ 10^19 (a trailing-zero count and a table load) costs eight,
// about 4% of the general path. divPow10Coef itself handles any 10^j. It must
// inline (TestPow10OperandInlineBudgets).
func isPow10Divisor(c u128) bool {
	return (c.lo == 1 || c.lo == 100) && c.hi == 0
}

// divPow10Coef is Div's quotient coefficient at p = DefaultPrec for a divisor
// coefficient p10 = 10^j: trunc(|d|/|e|·10^DefaultPrec) = ⌊c·10^g⌋ with
// g = DefaultPrec + ePrec − j − dPrec. That is exactly divCoefAt's
// ⌊c·10^f / 10^j⌋ with f = DefaultPrec + ePrec − dPrec, its scaled-divisor
// branch for f < 0 included, with the division folded into the scale. ok is
// false iff the quotient does not fit 128 bits, which only a positive g can
// cause; Div then falls through to the general path, which owns the
// degradation to a lower precision.
//
// PRECONDITIONS (not checked): p10 = 10^j with j ≤ MaxPrec; dPrec, ePrec ≤
// MaxPrec.
func divPow10Coef(c u128, dPrec, ePrec uint8, p10 uint64) (u128, bool) {
	// 10^j = 2^j·5^j has exactly j trailing zero bits.
	j := uint8(bits.TrailingZeros64(p10) & 31)
	up, down := DefaultPrec+ePrec, j+dPrec // both ≤ 2·MaxPrec
	if up >= down {
		g := up - down // ≤ DefaultPrec + MaxPrec
		if g <= MaxPrec {
			q, overflow := mul128by64(c, pow10u64[g&31])
			return q, overflow == 0
		}
		// 10^g ≥ 10^20 > 2^66, so a two-limb c cannot fit; a one-limb c
		// fits iff the 192-bit product's top word is zero.
		if c.hi != 0 {
			return u128{}, false
		}
		q, overflow := mul128by64(pow10u128[g&63], c.lo)
		return q, overflow == 0
	}
	// A truncating rescale: the quotient is at most c, so it always fits.
	return quoPow10(c, down-up), true
}

// quoPow10 returns ⌊c/10^k⌋ from the reciprocal tables. A factor above
// 10^MaxPrec takes a full 10^MaxPrec pass first: ⌊⌊c/a⌋/b⌋ = ⌊c/(a·b)⌋.
//
// Div does not reach that arm today. From divPow10Coef, k = j + dPrec −
// DefaultPrec − ePrec exceeds MaxPrec only when j > DefaultPrec (j ≥ 10 under
// zerodecimal_prec9, j ≥ 13 under zerodecimal_prec12, never in the default
// build), and isPow10Divisor admits only j = 0 and 2. The arm stays so that
// divPow10Coef keeps its contract for every 10^j, which
// TestDivPow10CoefAllExponents checks under those builds: admitting more
// powers of ten in isPow10Divisor then stays a one-line change.
//
// PRECONDITION (not checked): 1 ≤ k ≤ 2·MaxPrec.
func quoPow10(c u128, k uint8) u128 {
	if k > MaxPrec {
		c, _ = divmod128Pow10(c, MaxPrec)
		k -= MaxPrec
	}
	if c.hi == 0 {
		q, _ := divmod64Pow10(c.lo, k)
		return u128{lo: q}
	}
	q, _ := divmod128Pow10Slow(c, k)
	return q
}
