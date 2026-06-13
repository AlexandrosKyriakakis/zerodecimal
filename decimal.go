package zerodecimal

// Decimal is a fixed-point decimal number:
//
//	value = (neg ? -1 : +1) · coef / 10^prec
//
// with coef an unsigned 128-bit coefficient and prec 0..MaxPrec fractional
// digits. A Decimal is a 24-byte pointer-free value — copy it freely; the
// zero value is the canonical decimal zero and ready to use. The field order
// is load-bearing: coef first keeps the hot-path limb loads at offset zero.
//
// INVARIANT (canonical zero): a Decimal with a zero coefficient is always
// exactly Decimal{} — neg false, prec 0. Every code path that can produce
// zero routes through newDecimal, so a negative zero cannot exist and sign
// predicates need no zero checks.
//
// Arithmetic never trims trailing fractional zeros, so == compares
// representations, not numbers (1.5 != 1.50 under ==); use Equal or Cmp for
// numeric comparison.
type Decimal struct {
	coef u128
	neg  bool
	prec uint8
}

// Zero is the canonical decimal zero, identical to the zero value Decimal{}.
var Zero = Decimal{}

// One is the decimal one with precision 0.
var One = NewFromInt(1)

// newDecimal assembles a Decimal from raw parts while enforcing the
// canonical-zero invariant: a zero coefficient collapses to exactly
// Decimal{}, dropping any sign or precision tag. Every constructor and
// operation that can produce a zero result must build it here.
//
// PRECONDITION (not checked): prec ≤ MaxPrec.
func newDecimal(coef u128, neg bool, prec uint8) Decimal {
	if coef.isZero() {
		return Decimal{}
	}
	return Decimal{coef: coef, neg: neg, prec: prec}
}

// NewFromInt returns the Decimal with the exact value of v and precision 0.
func NewFromInt(v int64) Decimal {
	neg := v < 0
	//nolint:gosec // deliberate two's-complement reinterpretation, negated just below
	mag := uint64(v)
	if neg {
		mag = -mag // exact for MinInt64: -(uint64(-2^63)) wraps back to 2^63
	}
	return newDecimal(u128{lo: mag}, neg, 0)
}

// NewFromInt32 returns the Decimal with the exact value of v and precision 0.
func NewFromInt32(v int32) Decimal {
	return NewFromInt(int64(v))
}

// NewFromUint64 returns the Decimal with the exact value of v and precision 0.
func NewFromUint64(v uint64) Decimal {
	return newDecimal(u128{lo: v}, false, 0)
}

// New returns value · 10^exp. A positive exp scales the coefficient and
// returns ErrOverflow when the result exceeds 128 bits (always, once
// exp > 38 with a nonzero value). A negative exp becomes fractional
// precision; when it exceeds MaxPrec, factors of ten are stripped from value
// while it stays exact, and ErrPrecOutOfRange is returned if the precision
// still cannot fit. New(0, exp) is Zero for every exp.
func New(value int64, exp int32) (Decimal, error) {
	if value == 0 {
		return Decimal{}, nil
	}
	neg := value < 0
	//nolint:gosec // deliberate two's-complement reinterpretation, negated just below
	mag := uint64(value)
	if neg {
		mag = -mag
	}

	if exp >= 0 {
		// value · 10^39 ≥ 10^39 > 2^128 for every nonzero value.
		if exp > 38 {
			return Decimal{}, ErrOverflow
		}
		coef, overflow := mul128by64(pow10u128[exp&63], mag)
		if overflow != 0 {
			return Decimal{}, ErrOverflow
		}
		return newDecimal(coef, neg, 0), nil
	}

	// exp < 0: the scale becomes fractional precision. Negated in int64 so
	// exp == MinInt32 cannot wrap.
	scale := -int64(exp)
	for scale > int64(MaxPrec) && mag%10 == 0 {
		mag /= 10
		scale--
	}
	if scale > int64(MaxPrec) {
		return Decimal{}, ErrPrecOutOfRange
	}
	//nolint:gosec // scale ≤ MaxPrec ≤ 19 here, so the uint8 conversion is exact
	return newDecimal(u128{lo: mag}, neg, uint8(scale)), nil
}

// MustNew is New for arguments with proven bounds: it panics instead of
// returning an error.
func MustNew(value int64, exp int32) Decimal {
	d, err := New(value, exp)
	if err != nil {
		panic(err)
	}
	return d
}

// NewFromHiLo assembles a Decimal from its raw representation — sign,
// 128-bit coefficient limbs, and fractional precision — the inverse of
// ToHiLo, for zero-cost interop with other 128-bit decimal layouts. The
// coefficient is taken verbatim: trailing fractional zeros are NOT trimmed,
// though a zero coefficient still collapses to the canonical Decimal{}.
// Returns ErrPrecOutOfRange when prec > MaxPrec.
func NewFromHiLo(neg bool, hi, lo uint64, prec uint8) (Decimal, error) {
	if prec > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	return newDecimal(u128{hi: hi, lo: lo}, neg, prec), nil
}

// ToHiLo exposes the raw representation of d, such that
// d = (neg ? -1 : +1) · (hi·2^64 + lo) / 10^prec. It is total: every Decimal
// round-trips exactly through NewFromHiLo.
func (d Decimal) ToHiLo() (neg bool, hi, lo uint64, prec uint8) {
	return d.neg, d.coef.hi, d.coef.lo, d.prec
}

// Prec returns the number of fractional digits of d (0..MaxPrec).
func (d Decimal) Prec() uint8 {
	return d.prec
}

// Sign returns -1 when d < 0, 0 when d == 0, and +1 when d > 0.
func (d Decimal) Sign() int {
	if d.neg {
		return -1
	}
	if d.coef.isZero() {
		return 0
	}
	return 1
}

// IsZero reports whether d == 0.
func (d Decimal) IsZero() bool {
	return d.coef.isZero()
}

// IsPositive reports whether d > 0.
func (d Decimal) IsPositive() bool {
	return !d.neg && !d.coef.isZero()
}

// IsNegative reports whether d < 0. The bare flag is exact because the
// canonical-zero invariant keeps zero unsigned.
func (d Decimal) IsNegative() bool {
	return d.neg
}

// Neg returns -d, preserving precision; the negation of zero is zero.
func (d Decimal) Neg() Decimal {
	return newDecimal(d.coef, !d.neg, d.prec)
}

// Abs returns |d|, preserving precision.
func (d Decimal) Abs() Decimal {
	return newDecimal(d.coef, false, d.prec)
}

// IntPart returns the integer part of d truncated toward zero, or
// ErrIntPartOverflow when it does not fit int64. MinInt64 round-trips:
// NewFromInt(math.MinInt64).IntPart() returns math.MinInt64.
func (d Decimal) IntPart() (int64, error) {
	q, _ := divmod128Pow10(d.coef, d.prec)
	if q.hi != 0 {
		return 0, ErrIntPartOverflow
	}
	if d.neg {
		if q.lo > 1<<63 {
			return 0, ErrIntPartOverflow
		}
		// q.lo == 2^63 reinterprets to MinInt64, whose two's-complement
		// negation is itself — exactly the wanted value.
		//nolint:gosec // bounds-checked above; the 2^63 wrap is the MinInt64 case
		return -int64(q.lo), nil
	}
	if q.lo >= 1<<63 {
		return 0, ErrIntPartOverflow
	}
	//nolint:gosec // q.lo < 2^63 here, so the conversion is value-preserving
	return int64(q.lo), nil
}

// Cmp returns -1 when d < e, 0 when d == e numerically, and +1 when d > e.
// Decimals of different precision compare by value: 1.5 equals 1.50.
// Comparison is infallible — no precision alignment can overflow.
func (d Decimal) Cmp(e Decimal) int {
	// Sign-class discrimination by the neg flag alone is exact: canonical zero
	// never carries a sign (decimal.go INVARIANT), so neg partitions operands
	// into negatives and non-negatives with no Sign()/isZero check needed.
	if d.prec == e.prec && d.neg == e.neg {
		// Aligned, same sign class: one dual-borrow magnitude compare, then
		// orient. The negate compiles to a branch-over-SUB (CSNEG-class).
		c := cmp128i(d.coef, e.coef)
		if d.neg {
			return -c
		}
		return c
	}
	if d.neg != e.neg {
		// Opposite sign classes: the negative one is smaller. Equal coefficients
		// cannot both be zero here (canonical zero is non-negative), so this is
		// never a spurious 0.
		if d.neg {
			return -1
		}
		return 1
	}
	return cmpSlow(d, e)
}

// cmpSlow handles the differing-precision arm of Cmp for operands already
// known to share a sign class. It compares magnitudes via cmpUnaligned and
// orients by that shared sign. Outlined so Cmp's aligned fast path stays lean;
// it cannot inline cmpUnaligned (cost), so this is one extra frame on the
// non-benchmarked prec-mismatch path only.
func cmpSlow(d, e Decimal) int {
	c := cmpUnaligned(d, e)
	if d.neg {
		return -c
	}
	return c
}

// cmpUnaligned compares the magnitudes of d and e across differing
// precisions: the lower-precision coefficient is widened by 10^diff into 192
// bits via mul128by64to192 and compared against the other coefficient
// zero-extended. The widened value may exceed 128 bits but never 192, so no
// rescaled coefficient is ever materialized in 128 bits and the comparison
// cannot overflow. Deliberately outlined so Cmp's aligned fast path stays
// within the inlining budget.
//
// PRECONDITIONS (not checked): d.prec != e.prec, both ≤ MaxPrec (diff ≤ 19).
func cmpUnaligned(d, e Decimal) int {
	sign := 1
	if d.prec > e.prec {
		d, e = e, d
		sign = -1
	}
	diff := e.prec - d.prec
	w2, w1, w0 := mul128by64to192(d.coef, pow10u64[diff&31])
	if w2 != 0 {
		return sign
	}
	return sign * cmp128(u128{hi: w1, lo: w0}, e.coef)
}

// Equal reports whether d and e are numerically equal, ignoring trailing
// fractional zeros (1.5 equals 1.50). Use it instead of ==, which is
// representation equality. Identical representations short-circuit before
// any comparison work.
func (d Decimal) Equal(e Decimal) bool {
	return d == e || d.Cmp(e) == 0
}

// GreaterThan reports whether d > e.
func (d Decimal) GreaterThan(e Decimal) bool {
	return d.Cmp(e) > 0
}

// GreaterThanOrEqual reports whether d ≥ e.
func (d Decimal) GreaterThanOrEqual(e Decimal) bool {
	return d.Cmp(e) >= 0
}

// LessThan reports whether d < e.
func (d Decimal) LessThan(e Decimal) bool {
	return d.Cmp(e) < 0
}

// LessThanOrEqual reports whether d ≤ e.
func (d Decimal) LessThanOrEqual(e Decimal) bool {
	return d.Cmp(e) <= 0
}
