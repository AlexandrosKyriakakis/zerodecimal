package zerodecimal

import (
	"math"
	"math/bits"
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
// truncation of a sub-2^128 coefficient is exactly zero. Trunc-mode digit
// dropping preserves both sides: a saturated exponent needs at least four
// bytes of exponent text, so fewer than maxParseLen-42 mantissa digits can
// be dropped, leaving the folded effective precision beyond 3·MaxPrec (where
// truncation is still exactly zero) and the dropped-tail overflow guard even
// further into certain ErrOverflow. Beyond the cap only the side matters,
// never the magnitude, and saturation keeps the int accumulator
// overflow-free (it never exceeds 10·expCap + 9).
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
// skipped) and must fit 128 bits. A digit that does not fit returns
// ErrOverflow when trunc is false; when trunc is true the remaining mantissa
// tail is dropped instead — only its positions and first nonzero digit are
// tracked — and folded into the precision arithmetic after the exponent is
// known, so over-long input parses whenever its MaxPrec-truncated value is
// representable. The exponent then shifts the fractional length: a value
// needing negative precision is scaled up into the integer domain
// (ErrOverflow past 2^128-1), and one needing more than MaxPrec fractional
// digit positions — even positions holding zeros — returns ErrPrecOutOfRange
// when trunc is false, or is truncated toward zero (possibly to exactly
// zero) at prec MaxPrec when trunc is true.
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

	// Fast path: a plain literal — digits with at most one dot, no exponent —
	// whose digits after the leading-zero skip fit one uint64 limb. The digit
	// arm of the tight loop mutates only lo and i, keeping every other local
	// out of the backedge's register set (the dot validates itself in place,
	// so no dot index or second accumulator stays live across iterations);
	// anything the loop cannot prove exact or valid (a second dot, an
	// exponent marker, any other byte, too many digits, too much precision)
	// falls through to the general parser, which re-scans from the sign with
	// full validation. The digit count is conservative: zeros between the dot
	// and the first significant digit ("0.0005") count toward the 19-digit
	// budget even though they carry no value, which only ever sends
	// exact-but-long input down the slow path, never the reverse.
	start := i
	if s[i] == '0' { // typical input opens with a significant digit: one compare, no loop
		for i < n && s[i] == '0' {
			i++ // leading integer zeros: positions without value
		}
	}
	zeros := i - start
	if n-i > 20 {
		// More than 19 digits plus a dot can remain: the mantissa cannot be
		// proven one-limb (nor a 20-digit integer, the one two-limb shape the
		// fast path folds itself), so skip the fast scan instead of paying
		// for it twice. Plain long literals get their own two-limb path;
		// anything else falls through to the general parser from there.
		return parseLongPlain(s, start, i, neg, trunc)
	}
	{
		var lo uint64
		frac := -1 // seen-dot sentinel: stays negative until a dot fixes it
		for ; i < n; i++ {
			c := s[i]
			if d := c - '0'; d <= 9 {
				lo = lo*10 + uint64(d)
				continue
			}
			if c == '.' && frac < 0 {
				// One dot with a digit on each side: rejects ".1", "1.", and
				// a bare "." (i == start covers it: no digit, zero or
				// otherwise, precedes the dot). Validating here keeps the
				// dot's position out of the loop-carried state — only the
				// fraction length survives, and the digit arm never touches
				// it.
				if i == start || i == n-1 {
					return Decimal{}, ErrInvalidFormat
				}
				frac = n - 1 - i
				continue
			}
			goto general // exponent, second dot, or invalid byte
		}
		digits := n - start - zeros
		if frac < 0 {
			frac = 0 // no dot: an integer with zero fractional digits
		} else {
			digits-- // the dot occupied one of the counted positions
		}
		if digits > 19 {
			// Exactly 20 dot-less digits — the only way past 19 under the
			// length bail, so the run ended at n and s[start+zeros] is the
			// first significant digit d0 (nonzero: the zero skip stopped on
			// it). lo may have wrapped, but the trailing 19 digits' value R
			// is below 10^19 < 2^64, so R = lo - d0·10^19 is exact in
			// wrapping uint64 arithmetic and d0·10^19 + R rebuilds the value
			// in 128 bits — at most 10^20-1, always in range (float64
			// shortest forms of large magnitudes land here).
			d0 := uint64(s[start+zeros] - '0')
			coef, _ := mul128by64(u128{lo: d0}, pow10u64[19])
			coef, _ = add128(coef, u128{lo: lo - d0*pow10u64[19]})
			return newDecimal(coef, neg, 0), nil
		}
		if frac > int(MaxPrec) {
			if !trunc {
				return Decimal{}, ErrPrecOutOfRange
			}
			goto general // truncation semantics live in the general parser
		}
		// Canonical form: strip trailing fractional zeros (also collapses
		// "0.000" to the canonical zero). The final text byte is the last
		// fractional digit and lo holds it exactly (digits ≤ 19, no wrap),
		// so lo%10 == 0 iff s[n-1] == '0' — a nonzero final byte skips the
		// magic-mod chain outright.
		if frac > 0 && s[n-1] == '0' {
			for frac > 0 && lo%10 == 0 {
				lo /= 10
				frac--
			}
		}
		//nolint:gosec // the sentinel reset above pinned frac ≥ 0 and the MaxPrec check capped it at 19, so the uint8 conversion is exact
		return newDecimal(u128{lo: lo}, neg, uint8(frac)), nil
	}

general:
	return parseGeneral(s, start, neg, trunc)
}

// parseLongPlain parses the plain literals the fast path bails on for length
// alone: digits with at most one dot and no exponent, more than 20 bytes
// remaining after the leading-zero skip. start is the post-sign index and i
// the post-skip index, exactly as parseCore left them. Anything beyond that
// shape — an exponent marker, a second dot or other invalid byte, a trailing
// dot ("NNN." is ErrInvalidFormat), a fraction past MaxPrec, more than 39
// integer digits, or a coefficient fold crossing 2^128 — falls through to
// parseGeneral with the pre-skip start index, so every error and truncation
// decision stays in exactly one place: a fold overflow here may still parse
// there under trunc mode, and routing ErrOverflow through parseGeneral keeps
// the proof that every such error is genuine.
//
// Digit runs are located by the eight-byte mask scan (digitRunLen) and
// converted eight digits per step (digitRunVal); the fraction folds into the
// integer limbs with a single mul128by64 + add128, and only a '0' final byte
// pays the trailing-zero trim probe.
func parseLongPlain[T string | []byte](s T, start, i int, neg, trunc bool) (Decimal, error) {
	n := len(s)

	// Integer run first; the only byte allowed to stop it short of the end
	// is a single dot with a non-empty in-range all-digit fraction after it.
	intLen := digitRunLen(s, i)
	frac := 0
	dot := i + intLen
	switch {
	case dot == n:
		// Pure integer: no fraction, nothing left to validate.
	case s[dot] != '.' || dot == n-1:
		goto general // exponent or invalid byte, or a trailing dot
	default:
		frac = n - 1 - dot
		if frac > int(MaxPrec) || digitRunLen(s, dot+1) != frac {
			// Excess precision keeps its ErrPrecOutOfRange-or-truncate
			// semantics in parseGeneral; a short fraction run hides a second
			// dot, an exponent, or an invalid byte. Either way only the two
			// scans were paid. An empty integer run always lands here too:
			// dot == i forces frac = n-1-i ≥ 20 under the length bail, so
			// "000.00…" (and the invalid ".00…") cannot reach the success
			// path below — do not "optimize" the intLen == 0 case separately.
			goto general
		}
	}
	if intLen > 39 {
		goto general // 40+ significant digits exceed 2^128-1 exactly; trunc mode may still parse
	}
	{
		// First (up to) 19 integer digits fill one limb exactly (10^19-1 <
		// 2^64); the remainder folds in ≤19-digit chunks as coef·10^c + chunk.
		// The leading digit is significant (the zero skip stopped on it), so
		// a successful fold is never zero — newDecimal guards regardless.
		cnt := min(intLen, 19)
		coef := u128{lo: digitRunVal(s, i, cnt)}
		for j := i + cnt; j < dot; {
			c := min(dot-j, 19)
			grown, over := mul128by64(coef, pow10u64[c&31])
			grown, carry := add128(grown, u128{lo: digitRunVal(s, j, c)})
			if over|carry != 0 {
				goto general
			}
			coef = grown
			j += c
		}
		if frac > 0 {
			grown, over := mul128by64(coef, pow10u64[frac&31])
			grown, carry := add128(grown, u128{lo: digitRunVal(s, dot+1, frac)})
			if over|carry != 0 {
				goto general
			}
			coef = grown
			// Canonical form: strip trailing fractional zeros, exactly the
			// general parser's loop. The final text byte is the last
			// fractional digit, so a nonzero one skips the divmod probe.
			if s[n-1] == '0' {
				for frac > 0 {
					q, r := divmod128Pow10(coef, 1)
					if r != 0 {
						break
					}
					coef = q
					frac--
				}
			}
		}
		//nolint:gosec // 0 ≤ frac ≤ MaxPrec ≤ 19 on this path, so the uint8 conversion is exact
		return newDecimal(coef, neg, uint8(frac)), nil
	}

general:
	return parseGeneral(s, start, neg, trunc)
}

// digitRunLen returns the length of the ASCII-digit run starting at s[i],
// scanning eight bytes per step through the nonDigitMask probe and finishing
// the sub-word tail byte by byte.
func digitRunLen[T string | []byte](s T, i int) int {
	j := i
	for len(s)-j >= 8 {
		if m := nonDigitMask(le64(s[j:])); m != 0 {
			return j + bits.TrailingZeros64(m)>>3 - i
		}
		j += 8
	}
	for j < len(s) && s[j]-'0' <= 9 {
		j++
	}
	return j - i
}

// digitRunVal converts the digit run s[i:i+cnt] to its numeric value, eight
// digits per multiply-add step, then four, then one.
//
// PRECONDITION: every byte of the run is an ASCII digit (a digitRunLen scan
// already proved it) and 1 ≤ cnt ≤ 19, so the accumulator cannot wrap
// (10^19-1 < 2^64).
func digitRunVal[T string | []byte](s T, i, cnt int) uint64 {
	var v uint64
	for cnt >= 8 {
		v = v*100_000_000 + swarVal8(le64(s[i:]))
		i += 8
		cnt -= 8
	}
	if cnt >= 4 {
		v = v*10_000 + swarVal4(le32(s[i:]))
		i += 4
		cnt -= 4
	}
	for ; cnt > 0; cnt-- {
		v = v*10 + uint64(s[i]-'0')
		i++
	}
	return v
}

// le64 assembles the first eight bytes of b into a little-endian word. The
// shift-or chain compiles to a single 8-byte load on little-endian targets,
// and the explicit shifts keep the packing — byte 0 in the low lane, the one
// nonDigitMask, swarVal8, and TrailingZeros64>>3 all assume — portable to
// big-endian hosts.
func le64[T string | []byte](b T) uint64 {
	_ = b[7] // one bounds check for all eight loads
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

// le32 assembles the first four bytes of b into a little-endian word
// (see le64).
func le32[T string | []byte](b T) uint64 {
	_ = b[3] // one bounds check for all four loads
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
}

// nonDigitMask returns a word whose bit 7 is set in the first byte of w that
// is not an ASCII digit — zero iff all eight bytes are digits. After the
// '0'-xor, digits map to 0x00..0x09: the add flags 0x0A..0x89 through their
// high bit and the or-in of x itself flags 0x80 and above. A byte past 0x89
// carries into its higher neighbor and can falsely flag a digit there, but
// digit bytes never carry, so bytes below the first offender stay clean and
// the lowest set bit — all the scan consumes — is always the true first
// non-digit.
func nonDigitMask(w uint64) uint64 {
	x := w ^ 0x3030303030303030
	return ((x + 0x7676767676767676) | x) & 0x8080808080808080
}

// swarVal8 converts eight ASCII digits packed little-endian (text order from
// the low byte up) to their numeric value, 0..99999999. The multiply-shift
// folds adjacent digits into byte pairs (no byte exceeds 99, so nothing
// carries), then two mask-multiplies sum the four pairs with their decimal
// weights in bits 32..63 of a single product each.
//
// PRECONDITION: every byte of w is an ASCII digit.
func swarVal8(w uint64) uint64 {
	const (
		pairs = 0x000000FF000000FF // byte lanes 0 and 4 after the pair fold
		even  = 100 + 1_000_000<<32
		odd   = 1 + 10_000<<32
	)
	w -= 0x3030303030303030
	w = w*10 + w>>8
	return ((w&pairs)*even + (w>>16&pairs)*odd) >> 32
}

// swarVal4 is swarVal8 for exactly four digits in the low bytes of w,
// yielding 0..9999.
//
// PRECONDITION: the low four bytes of w are ASCII digits and the rest of w
// is zero (le32 guarantees both).
func swarVal4(w uint64) uint64 {
	w -= 0x30303030
	w = w*10 + w>>8
	return (w&0xFF)*100 + w>>16&0xFF
}

// accumRun folds the digit run starting at s[i] into coef and returns the
// index of the first non-digit byte along with the updated accumulator state.
// Digits accumulate through a one-limb chunk of up to 19 (10^19-1 < 2^64, so
// the chunk cannot wrap) and each chunk folds into coef with one 128-bit
// multiply-add — coef·10^len + chunk — instead of one per digit.
//
// A fold that would cross 2^128 replays its chunk digit by digit to find the
// exact boundary: ErrOverflow when trunc is false, otherwise the remaining
// digits are dropped — dropped counts them and dropNZ records the 1-based
// position of the first nonzero one — exactly matching parseGeneral's
// documented truncation semantics. dropped and dropNZ thread through calls so
// a fraction run continues dropping where the integer run stopped.
func accumRun[T string | []byte](s T, i int, coef u128, dropped, dropNZ int, trunc bool) (int, u128, int, int, error) {
	n := len(s)
	for i < n {
		if dropped > 0 {
			// Saturated: only positions and the first nonzero digit matter.
			for ; i < n; i++ {
				c := s[i] - '0'
				if c > 9 {
					break
				}
				dropped++
				if dropNZ == 0 && c != 0 {
					dropNZ = dropped
				}
			}
			return i, coef, dropped, dropNZ, nil
		}

		var lo uint64
		cnt := 0
		for i < n && cnt < 19 {
			c := s[i] - '0'
			if c > 9 {
				break
			}
			lo = lo*10 + uint64(c)
			cnt++
			i++
		}
		if cnt == 0 {
			break // the run ended exactly on a chunk boundary
		}
		grown, over := mul128by64(coef, pow10u64[cnt&31])
		grown, carry := add128(grown, u128{lo: lo})
		if over|carry != 0 {
			// The chunk crosses 2^128: replay it per digit. Some prefix may
			// still fit; the per-digit accumulate finds the exact last digit
			// that does, then starts (or rejects) the dropped tail.
			for j := i - cnt; j < i; j++ {
				c := s[j] - '0'
				if dropped > 0 {
					dropped++
					if dropNZ == 0 && c != 0 {
						dropNZ = dropped
					}
					continue
				}
				g, ov := mul128by64(coef, 10)
				g, cy := add128(g, u128{lo: uint64(c)})
				if ov|cy != 0 {
					if !trunc {
						return i, coef, 0, 0, ErrOverflow
					}
					dropped = 1
					if c != 0 {
						dropNZ = 1
					}
					continue
				}
				coef = g
			}
			continue
		}
		coef = grown
		if cnt < 19 {
			break // a non-digit (or the end) stopped the chunk early
		}
	}
	return i, coef, dropped, dropNZ, nil
}

// parseGeneral is the full-grammar parser behind parseCore's fast path:
// scientific notation, over-long mantissas, and every malformed input land
// here. i indexes the first byte after the optional sign; the scan restarts
// there so the fast path never has to hand over partial state.
//
// The mantissa is two digit runs — integer and fraction — accumulated by
// accumRun in 19-digit chunks, so the per-digit work is one range check and
// one uint64 multiply-add regardless of length; 128-bit arithmetic happens
// once per chunk, not once per digit. Leading zeros need no special casing:
// they accumulate as value zero and cannot trigger a fold overflow.
func parseGeneral[T string | []byte](s T, i int, neg, trunc bool) (Decimal, error) {
	n := len(s)
	var (
		coef    u128
		dropped int // mantissa digits past the accumulator (trunc mode only)
		dropNZ  int // 1-based index of the first nonzero dropped digit; 0 = all zero
		err     error
	)

	intStart := i
	i, coef, dropped, dropNZ, err = accumRun(s, i, coef, 0, 0, trunc)
	if err != nil {
		return Decimal{}, err
	}
	if i == intStart {
		return Decimal{}, ErrInvalidFormat // no digit starts the mantissa: ".5", "e5", "x"
	}

	frac := 0
	if i < n && s[i] == '.' {
		fracStart := i + 1
		i, coef, dropped, dropNZ, err = accumRun(s, fracStart, coef, dropped, dropNZ, trunc)
		if err != nil {
			return Decimal{}, err
		}
		// frac counts every digit position after the dot — dropped digits
		// included, mirroring the dropped-tail fold below — and must be
		// non-empty: "1." and "1.e5" are ErrInvalidFormat.
		frac = i - fracStart
		if frac == 0 {
			return Decimal{}, ErrInvalidFormat
		}
	}

	exp := 0
	if i < n { // mantissa stopped before the end: only an exponent may follow
		if c := s[i]; c != 'e' && c != 'E' {
			return Decimal{}, ErrInvalidFormat // second dot, stray byte
		}
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

	// Fold the dropped mantissa tail (trunc mode only) into the precision
	// arithmetic. The full coefficient is coef·10^dropped + tail with
	// tail < 10^dropped, and shift counts the dropped positions that land at
	// or above the 10^-MaxPrec place of the final value:
	//
	//   - shift ≤ 0: every dropped digit lies below the truncation point, so
	//     ⌊full/10^excess⌋ == ⌊coef/10^(excess-dropped)⌋ exactly and reducing
	//     frac by dropped loses nothing.
	//   - 1 ≤ shift ≤ MaxPrec with dropped digits 1..shift all zero: the
	//     truncated value is exactly coef·10^-(MaxPrec-shift), which the
	//     reduced frac hands to the exact branch of the switch below.
	//   - anything else: the truncated value retains a digit beyond the 39
	//     accumulated ones, so its canonical coefficient is at least the
	//     overflowing coef·10 + first dropped digit ≥ 2^128 — ErrOverflow.
	if dropped > 0 {
		shift := dropped - (frac - exp - int(MaxPrec))
		if shift > 0 && (shift > int(MaxPrec) || (dropNZ != 0 && dropNZ <= shift)) {
			return Decimal{}, ErrOverflow
		}
		frac -= dropped
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
		// 1 ≤ excess ≤ MaxPrec ≤ 19 here, so the uint8 conversion is exact.
		coef, _ = divmod128Pow10(coef, uint8(excess))

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
// possibly to exactly zero — instead of returning ErrPrecOutOfRange. The
// truncation also covers mantissas longer than the 128-bit coefficient can
// hold: input with forty or more significant digits parses whenever the
// truncated value itself is representable, and ErrOverflow remains only for
// values whose truncated coefficient exceeds 2^128-1. All other errors are
// unchanged.
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
