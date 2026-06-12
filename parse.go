package zerodecimal

import (
	"math"
	"strconv"
)

// maxParseLen is the maximum input length the parser accepts, in bytes.
// The widest canonical value — sign, 39 coefficient digits, decimal point —
// is 41 bytes; 200 leaves generous room for redundant zeros and exponents
// while bounding the work an adversarial input can demand. Longer input
// returns ErrMaxStrLen before any scanning.
const maxParseLen = 200

// expCap is the exponent-accumulator saturation bound. Exponent digits past
// the bound stop accumulating (they are still validated), which cannot change
// the outcome: the mantissa carries at most maxParseLen fractional digits, so
// any exponent above expCap demands a scale-up beyond 10^38 — certain
// ErrOverflow for a nonzero coefficient — and any exponent below -expCap
// pushes the effective precision at least 39 digits past MaxPrec, where
// truncation of a sub-2^128 coefficient is exactly zero. Beyond the cap only
// the side matters, never the magnitude, and saturation keeps the int
// accumulator overflow-free (it never exceeds 10·expCap + 9).
const expCap = maxParseLen + 2*int(MaxPrec) + 1

// parseCore parses the decimal literal s without copying or allocating; it is
// the shared core of the string and []byte constructors, and never retains or
// modifies s.
//
// Grammar: ['+'|'-'] digits ['.' digits] [('e'|'E') ['+'|'-'] digits].
// Scientific notation is accepted ("1.23e4" is 12300, "1E-7" is 0.0000001).
// Integer and fractional digit runs must be non-empty where present: a bare
// sign, "1.", ".1", doubled dots or signs, spaces, underscores, non-ASCII
// digits, and NaN/Inf words are all ErrInvalidFormat. Rejecting leading and
// trailing dots is deliberately stricter than shopspring/decimal.
//
// The coefficient accumulates over significant digits only (leading zeros are
// skipped) and must fit 128 bits, else ErrOverflow. The exponent then shifts
// the fractional length: a value needing negative precision is scaled up into
// the integer domain (ErrOverflow past 2^128-1), and one needing more than
// MaxPrec fractional digit positions — even positions holding zeros — returns
// ErrPrecOutOfRange when trunc is false, or is truncated toward zero (possibly
// to exactly zero) at prec MaxPrec when trunc is true.
//
// The result is canonical: trailing fractional zeros are trimmed ("1.500"
// parses identically to "1.5") and zero is always Decimal{}. All errors are
// bare sentinels — ErrEmptyString, ErrMaxStrLen, ErrInvalidFormat,
// ErrOverflow, ErrPrecOutOfRange — so failure paths allocate nothing.
func parseCore[T string | []byte](s T, trunc bool) (Decimal, error) {
	n := len(s)
	if n == 0 {
		return Decimal{}, ErrEmptyString
	}
	if n > maxParseLen {
		return Decimal{}, ErrMaxStrLen
	}

	i := 0
	neg := false
	if c := s[0]; c == '+' || c == '-' {
		neg = c == '-'
		i = 1
		if i == n {
			return Decimal{}, ErrInvalidFormat // bare sign
		}
	}

	var (
		lo       uint64 // coefficient accumulator while sig ≤ 19
		coef     u128   // coefficient accumulator once sig > 19
		sig      int    // significant digits accumulated (leading zeros skipped)
		frac     int    // digit positions seen after the decimal point
		sawDot   bool
		sawDigit bool
	)

scan:
	for ; i < n; i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			sawDigit = true
			if sawDot {
				frac++
			}
			if c == '0' && sig == 0 {
				continue // leading zero: the position counts, the value is nothing
			}
			sig++
			if sig <= 19 {
				// 19 digits max out at 10^19-1 < 2^64: no overflow possible.
				lo = lo*10 + uint64(c-'0')
				continue
			}
			if sig == 20 {
				coef = u128{lo: lo} // promote to two limbs for digits 20..39
			}
			var over, carry uint64
			coef, over = mul128by64(coef, 10)
			coef, carry = add128(coef, u128{lo: uint64(c - '0')})
			if over|carry != 0 {
				return Decimal{}, ErrOverflow // 40th digit always lands here
			}
		case c == '.':
			// One dot, with a digit on each side: rejects ".1", "1.", "1..2".
			if sawDot || !sawDigit || i+1 == n || s[i+1] < '0' || s[i+1] > '9' {
				return Decimal{}, ErrInvalidFormat
			}
			sawDot = true
		case c == 'e' || c == 'E':
			if !sawDigit {
				return Decimal{}, ErrInvalidFormat // "e5" has no mantissa
			}
			break scan
		default:
			return Decimal{}, ErrInvalidFormat
		}
	}

	exp := 0
	if i < n { // the scan stopped at 'e' or 'E': parse the exponent
		i++
		expNeg := false
		if i < n && (s[i] == '+' || s[i] == '-') {
			expNeg = s[i] == '-'
			i++
		}
		if i == n {
			return Decimal{}, ErrInvalidFormat // "1e", "1e+", "1e-"
		}
		for ; i < n; i++ {
			c := s[i]
			if c < '0' || c > '9' {
				return Decimal{}, ErrInvalidFormat
			}
			if exp <= expCap { // saturate, keep validating (see expCap)
				exp = exp*10 + int(c-'0')
			}
		}
		if expNeg {
			exp = -exp
		}
	}

	if sig <= 19 {
		coef = u128{lo: lo}
	}

	// Combine the exponent with the fractional length into the precision.
	var prec uint8
	switch effPrec := frac - exp; {
	case effPrec < 0:
		// The value is an integer with up trailing zeros: scale the
		// coefficient up and settle at precision 0.
		if coef.isZero() {
			return Decimal{}, nil // 0·10^anything is zero
		}
		up := -effPrec
		if up > 2*int(MaxPrec) {
			return Decimal{}, ErrOverflow // nonzero·10^39 ≥ 10^39 > 2^128-1
		}
		if up > int(MaxPrec) {
			var over uint64
			coef, over = mul128by64(coef, pow10u64[MaxPrec])
			if over != 0 {
				return Decimal{}, ErrOverflow
			}
			up -= int(MaxPrec)
		}
		var over uint64
		coef, over = mul128by64(coef, pow10u64[up&31])
		if over != 0 {
			return Decimal{}, ErrOverflow
		}
	case effPrec <= int(MaxPrec):
		prec = uint8(effPrec) // 0 ≤ effPrec ≤ MaxPrec ≤ 19: the conversion is exact
	case !trunc:
		return Decimal{}, ErrPrecOutOfRange
	default:
		// Truncate the excess fractional digits toward zero at prec MaxPrec.
		// Two chained passes cover every nonzero outcome: ⌊⌊u/a⌋/b⌋ == ⌊u/ab⌋.
		excess := effPrec - int(MaxPrec)
		if excess > 2*int(MaxPrec) {
			return Decimal{}, nil // coef < 2^128 < 10^39: the quotient is zero
		}
		if excess > int(MaxPrec) {
			coef, _ = divmod128Pow10(coef, MaxPrec)
			excess -= int(MaxPrec)
		}
		coef, _ = divmod128Pow10(coef, uint8(excess)) // 1 ≤ excess ≤ 19: exact

		prec = MaxPrec
	}

	// Canonical form: strip trailing fractional zeros so equal values parse
	// to identical representations ("1.500" and "1.5" both become {15, 1}).
	for prec > 0 {
		q, r := divmod128Pow10(coef, 1)
		if r != 0 {
			break
		}
		coef = q
		prec--
	}
	return newDecimal(coef, neg, prec), nil
}

// NewFromString parses a decimal literal — optional sign, digits, optional
// fraction, optional e/E exponent — into the exact Decimal it denotes. Values
// needing more than MaxPrec fractional digits return ErrPrecOutOfRange (use
// NewFromStringTrunc to truncate instead), coefficients past 2^128-1 return
// ErrOverflow, and grammar violations return ErrInvalidFormat. Stricter than
// shopspring/decimal: "1." and ".1" are rejected — both sides of the dot
// need a digit. The result is canonical (trailing fractional zeros trimmed)
// and parsing never allocates.
func NewFromString(s string) (Decimal, error) {
	return parseCore(s, false)
}

// NewFromStringTrunc is NewFromString except that a value needing more than
// MaxPrec fractional digits is truncated toward zero at prec MaxPrec —
// possibly to exactly zero — instead of returning ErrPrecOutOfRange. All
// other errors are unchanged.
func NewFromStringTrunc(s string) (Decimal, error) {
	return parseCore(s, true)
}

// ParseBytes is NewFromString over a byte slice, avoiding any string
// conversion: b is read in place, never retained, and never modified.
func ParseBytes(b []byte) (Decimal, error) {
	return parseCore(b, false)
}

// ParseBytesTrunc is NewFromStringTrunc over a byte slice (see ParseBytes).
func ParseBytesTrunc(b []byte) (Decimal, error) {
	return parseCore(b, true)
}

// RequireFromString is NewFromString for literals with proven validity, such
// as constants and test fixtures: it panics instead of returning an error.
func RequireFromString(s string) Decimal {
	d, err := parseCore(s, false)
	if err != nil {
		panic(err)
	}
	return d
}

// NewFromFloat converts f through its shortest decimal representation — the
// digits strconv prints for the exact bits of f — with no silent rounding:
// NaN and infinities return ErrInvalidFloat, |f| ≥ 2^128 returns ErrOverflow,
// and a nonzero |f| below 10^-19 or a shortest form needing more than MaxPrec
// fractional digits returns ErrPrecOutOfRange. For lossy ingestion of
// arbitrary floats, make the rounding explicit instead:
// NewFromStringTrunc(strconv.FormatFloat(f, 'f', -1, 64)).
func NewFromFloat(f float64) (Decimal, error) {
	return newFromFloat(f, 64)
}

// NewFromFloat32 is NewFromFloat with the shortest form computed at 32-bit
// precision: NewFromFloat32(0.1) is exactly 0.1, not the longer expansion of
// float64(float32(0.1)).
func NewFromFloat32(f float32) (Decimal, error) {
	return newFromFloat(float64(f), 32)
}

// RequireFromFloat is NewFromFloat for values with proven bounds: it panics
// instead of returning an error.
func RequireFromFloat(f float64) Decimal {
	d, err := newFromFloat(f, 64)
	if err != nil {
		panic(err)
	}
	return d
}

// newFromFloat implements the float constructors: guard the domain, print the
// shortest 'f'-form decimal for the given bit size into a stack buffer, and
// reuse the byte parser. The guards also bound the text — |f| < 2^128 caps
// the integer part at 39 digits and |f| ≥ 10^-19 caps the leading fractional
// zeros at 18, so with at most 17 significant digits the 64-byte buffer never
// grows — keeping the whole conversion allocation-free. This is the one place
// the library calls into strconv formatting.
func newFromFloat(f float64, bitSize int) (Decimal, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Decimal{}, ErrInvalidFloat
	}
	if math.Abs(f) >= 0x1p128 {
		return Decimal{}, ErrOverflow
	}
	if f != 0 && math.Abs(f) < 1e-19 {
		return Decimal{}, ErrPrecOutOfRange
	}
	var buf [64]byte
	return ParseBytes(strconv.AppendFloat(buf[:0], f, 'f', -1, bitSize))
}
