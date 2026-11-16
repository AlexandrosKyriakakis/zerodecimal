package zerodecimal

import (
	"encoding/binary"
	"math/bits"
)

// PostgreSQL's binary NUMERIC wire format, as numeric_send writes it and
// numeric_recv reads it (src/backend/utils/adt/numeric.c). Every field is a
// big-endian 16-bit word:
//
//	ndigits  uint16  number of base-10000 digits that follow
//	weight   int16   power of 10000 of the first digit
//	sign     uint16  0x0000 positive, 0x4000 negative, 0xC000 NaN,
//	                 0xD000 +Infinity, 0xF000 -Infinity (PostgreSQL 14+)
//	dscale   uint16  display scale: decimal digits after the point
//	digits   ndigits words, each 0..9999
//
// The value is Σ digits[i]·10000^(weight-i), negated for the negative sign,
// and truncated toward zero to dscale fractional digits. numeric_send strips
// zero digits at both ends, sends zero as ndigits 0 and never sends a nonzero
// digit past dscale. numeric_recv also accepts zero digits at either end,
// which other encoders (pgx's pgtype.Numeric among them) do write, and
// truncates the digits a smaller dscale hides (trunc_var) before it stores
// the value.
const (
	pgHeaderLen = 8
	pgSignNeg   = 0x4000
	pgSignNaN   = 0xC000
	pgSignPInf  = 0xD000
	pgSignNInf  = 0xF000
	pgDscaleMax = 0x3FFF // NUMERIC_DSCALE_MAX

	// pgMaxDigits is the most base-10000 digits a Decimal's value can span
	// once zero digits are stripped. With nonzero first and last digits, n
	// digits make a coefficient of at least 10000^(n-1)/10^3 — the last
	// digit ends in at most three decimal zeros — which is past 2^128 from
	// n = 12 on (10^41). AppendPgNumeric never needs more (PgNumericMaxLen).
	pgMaxDigits = 11

	// pgBufLen is the encoder's scratch size, PgNumericMaxLen rounded up to
	// a power of two so that masked indices need no bounds checks.
	pgBufLen  = 32
	pgBufMask = pgBufLen - 1
)

// PgNumericMaxLen bounds len(d.AppendPgNumeric(nil)) for every Decimal: the
// 8-byte header and at most 11 base-10000 digits. A value with I integer
// digits and P = d.Prec() fractional digits spans ⌈I/4⌉ + ⌈P/4⌉ digits
// before zero digits are stripped; a nonzero integer part bounds I + P by the
// 39-digit coefficient, so the count is at most ⌊(39+6)/4⌋ = 11, and a
// pure fraction spans at most ⌈19/4⌉ = 5. A buffer with this much spare
// capacity makes AppendPgNumeric allocation-free.
const PgNumericMaxLen = 30

// ParsePgNumeric decodes PostgreSQL's binary NUMERIC wire format
// (numeric_send) into the value numeric_recv would store for the same bytes.
// The result is canonical — trailing fractional zeros removed, exactly what
// ParseBytes returns for the value's text form when that rendering fits
// ParseBytes' 200-byte limit — so the binary and text protocols then scan to
// == values. A longer rendering (a numeric(1000,500) column, say) fails
// ParseBytes with ErrMaxStrLen, while ParsePgNumeric still decodes the value
// or returns its range sentinel. SQL NULL is the caller's concern (pgx passes
// src == nil, which fails here as too short).
//
// Like numeric_recv, it reads ndigits unsigned, accepts zero digits at either
// end of the digit array, and truncates toward zero any digit that lies past
// dscale fractional places, which numeric_send never sends but another
// encoder may: 1.2345 sent with dscale 2 decodes as 1.23, and a value whose
// digits all lie past dscale decodes as zero. Truncation comes before the
// range checks, so only the kept digits must fit. dscale never pads the
// result, which stays canonical.
//
// Errors: ErrInvalidBinaryData for malformed input (fewer than 8 bytes, a
// length that disagrees with ndigits, a digit ≥ 10000 anywhere, an unknown
// sign word, dscale > 0x3FFF); ErrInvalidFloat for NaN and ±Infinity;
// ErrPrecOutOfRange when a kept nonzero digit lies beyond MaxPrec fractional
// places; ErrOverflow when the coefficient exceeds 128 bits. Malformed input
// takes precedence, so a malformed NaN is ErrInvalidBinaryData, and NaN and
// ±Infinity come next. The range errors follow the text parser's order, so a
// text form within ParseBytes' limit fails with the same sentinel. The text
// protocol renders the special values as NaN, Infinity and -Infinity, which
// ParseBytes rejects with ErrInvalidFormat, not ErrInvalidFloat. Errors are
// bare sentinels with a zero Decimal. It never allocates.
func ParsePgNumeric(src []byte) (Decimal, error) {
	if len(src) < pgHeaderLen {
		return Decimal{}, ErrInvalidBinaryData
	}
	// ndigits is unsigned, as numeric_recv reads it: numeric_send writes
	// 0x8000..0x9000 for values of 131,072 digits or more.
	ndigits := int(binary.BigEndian.Uint16(src))
	// Sign-extend the int16 weight arithmetically: 0x8000..0xFFFF map to
	// -32768..-1.
	weight := int(binary.BigEndian.Uint16(src[2:])^0x8000) - 0x8000
	sign := binary.BigEndian.Uint16(src[4:])
	dscale := int(binary.BigEndian.Uint16(src[6:]))
	if len(src) != pgHeaderLen+2*ndigits || dscale > pgDscaleMax {
		return Decimal{}, ErrInvalidBinaryData
	}
	digits := src[pgHeaderLen:]
	if sign&^pgSignNeg != 0 {
		return Decimal{}, pgSpecialError(digits, sign)
	}

	// Zero digits at either end carry no value (and are valid), so strip
	// them, leaving the significant digits with both ends nonzero. Each
	// leading zero moves the first significant digit one power of 10000
	// down. The explicit length tests let the compiler drop bounds checks.
	for len(digits) >= 2 && digits[0]|digits[1] == 0 {
		digits = digits[2:]
		weight--
	}
	for len(digits) >= 2 && digits[len(digits)-2]|digits[len(digits)-1] == 0 {
		digits = digits[:len(digits)-2]
	}
	if len(digits) < 2 {
		return Decimal{}, nil // zero, whatever the sign and dscale
	}

	// Drop the last digit's trailing decimal zeros so the coefficient comes
	// out minimal, as the text parser's does: the digit is nonzero and below
	// 10^4, so there are at most three, and it then folds in at base
	// 10^(4-z) instead of 10^4.
	last := uint64(digits[len(digits)-2])<<8 | uint64(digits[len(digits)-1])
	if last > 9999 {
		return Decimal{}, ErrInvalidBinaryData
	}
	z := 0
	for last%10 == 0 {
		last /= 10
		z++
	}

	// rest holds the digits before the last, which stands at
	// 10000^(weight-len(rest)/2); dropping its z zeros moves the
	// coefficient's final digit z places up, to 10^exp. A nonzero digit past
	// dscale places (exp < -dscale) is what numeric_recv truncates before it
	// stores the value; numeric_send never sends one, so the cut stays off
	// the common path.
	rest := digits[:len(digits)-2]
	exp := 4*(weight-len(rest)>>1) + z
	if exp < -dscale {
		var ok bool
		if rest, last, z, ok = pgTruncate(digits, -4*(weight-len(rest)>>1)-dscale); !ok {
			return Decimal{}, ErrInvalidBinaryData
		}
		if last == 0 {
			return Decimal{}, nil // every nonzero digit lay past dscale
		}
		exp = 4*(weight-len(rest)>>1) + z
	}
	if len(rest) > 2*(pgMaxDigits-1) {
		return Decimal{}, pgDigitsError(rest, ErrOverflow) // last is valid
	}

	// Fold the leading digits, rest, in chunks of four: a chunk fits one limb
	// (< 10^16), two chunks fit 10^32 < 2^128 unchecked, and only the third
	// chunk (at most two digits) or the last digit can pass 128 bits. Every
	// digit of a chunk is validated before its fold can report overflow,
	// so a malformed digit always wins.
	var coef u128
	if len(rest) > 0 {
		k := min(len(rest), 8)
		a, ok := pgChunk(rest[:k])
		if !ok {
			return Decimal{}, ErrInvalidBinaryData
		}
		coef.lo = a
		if rest = rest[k:]; len(rest) > 0 {
			k = min(len(rest), 8)
			b, ok := pgChunk(rest[:k])
			if !ok {
				return Decimal{}, ErrInvalidBinaryData
			}
			var carry uint64
			// k bytes hold k/2 digits, worth 10000^(k/2) = 10^(2k).
			coef.hi, coef.lo = bits.Mul64(a, pow10u64[2*k&31])
			coef.lo, carry = bits.Add64(coef.lo, b, 0)
			coef.hi += carry
			if rest = rest[k:]; len(rest) > 0 {
				c, ok := pgChunk(rest)
				if !ok {
					return Decimal{}, ErrInvalidBinaryData
				}
				var over uint64
				coef, over = mul128by64(coef, pow10u64[2*len(rest)&31])
				coef, carry = add128(coef, u128{lo: c})
				if over|carry != 0 {
					return Decimal{}, ErrOverflow
				}
			}
		}
	}
	coef, over := mul128by64(coef, pow10u64[(4-z)&31])
	coef, carry := add128(coef, u128{lo: last})
	if over|carry != 0 {
		return Decimal{}, ErrOverflow
	}

	// Like the text parser, check the precision only once the significant
	// digits are known to fit.
	neg := sign == pgSignNeg
	if exp < 0 {
		prec := -exp
		if prec > int(MaxPrec) {
			return Decimal{}, ErrPrecOutOfRange
		}
		return newDecimal(coef, neg, uint8(prec)), nil
	}
	if exp > 0 {
		var err error
		if coef, err = scaleUpExact(coef, exp); err != nil {
			return Decimal{}, err
		}
	}
	return newDecimal(coef, neg, 0), nil
}

// pgChunk folds the big-endian base-10000 digits in b — at most four, so the
// value stays below 10^16 — into one limb, reporting false when a digit
// exceeds 9999.
func pgChunk(b []byte) (uint64, bool) {
	var v uint64
	for ; len(b) >= 2; b = b[2:] {
		d := uint64(b[0])<<8 | uint64(b[1])
		if d > 9999 {
			return 0, false
		}
		v = v*10000 + d
	}
	return v, true
}

// pgTruncate is numeric_recv's trunc_var on digits, whose first and last
// digits are nonzero: it drops the cut > 0 lowest decimal places, the ones
// past dscale, then strips the zero digits this leaves at the end. It
// returns the kept digits before the new last one, that last digit with its
// z trailing decimal zeros removed (0 when no nonzero digit is left), and
// false when any digit exceeds 9999, dropped digits included, since
// numeric_recv validates every digit before it truncates.
func pgTruncate(digits []byte, cut int) (rest []byte, last uint64, z int, ok bool) {
	// The bytes left once the whole dropped digits go. cut > 0 already
	// bounds them by len(digits); the min restates that for the compiler.
	keep := min(len(digits)-cut>>2<<1, len(digits))
	if keep < 2 {
		return nil, 0, 0, pgDigitsError(digits, nil) == nil
	}
	if pgDigitsError(digits[keep:], nil) != nil {
		return nil, 0, 0, false
	}
	// The last kept digit loses its cut&3 lowest places.
	z = cut & 3
	last = uint64(digits[keep-2])<<8 | uint64(digits[keep-1])
	if last > 9999 {
		return nil, 0, 0, false
	}
	rest = digits[:keep-2]
	if last /= pow10u64[z]; last == 0 {
		// The cut cleared the digit, so the zero digits before it now end
		// the array, and trunc_var's strip_var removes them.
		for len(rest) >= 2 && rest[len(rest)-2]|rest[len(rest)-1] == 0 {
			rest = rest[:len(rest)-2]
		}
		if len(rest) < 2 {
			return nil, 0, 0, true
		}
		z = 0
		last = uint64(rest[len(rest)-2])<<8 | uint64(rest[len(rest)-1])
		if last > 9999 {
			return nil, 0, 0, false
		}
		rest = rest[:len(rest)-2]
	}
	for last%10 == 0 {
		last /= 10
		z++
	}
	return rest, last, z, true
}

// pgSpecialError classifies a sign word that is neither positive nor
// negative. NaN and ±Infinity are ErrInvalidFloat when the encoding is
// otherwise well formed (numeric_recv validates their digits too, then
// ignores them); any other word is ErrInvalidBinaryData.
func pgSpecialError(digits []byte, sign uint16) error {
	if sign != pgSignNaN && sign != pgSignPInf && sign != pgSignNInf {
		return ErrInvalidBinaryData
	}
	return pgDigitsError(digits, ErrInvalidFloat)
}

// pgDigitsError returns ErrInvalidBinaryData when a digit in b exceeds 9999,
// and err otherwise: a malformed digit outranks the verdict the caller
// reached without reading every digit.
func pgDigitsError(b []byte, err error) error {
	for ; len(b) >= 2; b = b[2:] {
		if uint16(b[0])<<8|uint16(b[1]) > 9999 {
			return ErrInvalidBinaryData
		}
	}
	return err
}

// AppendPgNumeric appends d in PostgreSQL's binary NUMERIC wire format, as
// numeric_send produces it for the same value with dscale = d.Prec(): leading
// and trailing zero base-10000 digits stripped, zero as ndigits = 0. It
// allocates only when b lacks capacity; the encoding is at most
// PgNumericMaxLen bytes.
//
// The digits are rendered right-aligned into a stack buffer, least
// significant first, so the header, which needs their count and weight, is
// written last; one append then copies the encoding out.
func (d Decimal) AppendPgNumeric(b []byte) []byte {
	if d.coef.isZero() {
		// ndigits 0, weight 0, a positive sign, and dscale = prec, which
		// the canonical-zero invariant pins to 0: numeric_send's zero.
		return append(b, 0, 0, 0, 0, 0, 0, 0, d.prec)
	}
	var buf [pgBufLen]byte
	q, fr := d.coef, uint64(0)
	if d.prec != 0 {
		// Open-coded divmod128Pow10 dispatch (see appendCanonical).
		if q.hi == 0 {
			q.lo, fr = divmod64Pow10(q.lo, d.prec)
		} else {
			q, fr = divmod128Pow10Slow(q, d.prec)
		}
	}
	pos := pgBufLen
	if fr != 0 {
		// The fraction spans ⌈prec/4⌉ digits. The last holds its final
		// r = 1..4 decimal digits, padded on the right to four; the rest
		// are fr / 10^r, below 10^16, four decimal digits each.
		nf := (d.prec + 3) >> 2
		r := d.prec - (nf-1)<<2
		rest, low := divmod64Pow10(fr, r)
		pos = pgPutDigit(&buf, pos, low*pow10u64[(4-r)&31])
		for ; nf > 1; nf-- {
			pos = pgPutDigit(&buf, pos, rest%10000)
			rest /= 10000
		}
	}
	weight := uint16(0xFFFF) // -1: a pure fraction starts after the point
	if q.isZero() {
		// Strip the fraction's leading zero digits, each lowering the
		// weight. fr != 0 here, so a nonzero digit ends the loop.
		for buf[pos&pgBufMask]|buf[(pos+1)&pgBufMask] == 0 {
			pos += 2
			weight--
		}
	} else {
		var ni uint16
		pos, ni = pgPutInt(&buf, pos, q)
		weight = ni - 1
	}
	sign := uint64(0)
	if d.neg {
		sign = pgSignNeg
	}
	ndigits := uint64((pgBufLen-pos)&pgBufMask) >> 1 // the mask is the identity: at most 22 bytes
	pos -= pgHeaderLen
	binary.BigEndian.PutUint64(buf[pos&pgBufMask:], ndigits<<48|uint64(weight)<<32|sign<<16|uint64(d.prec))
	return append(b, buf[pos&pgBufMask:]...)
}

// pgPutInt writes the base-10000 digits of the integer part q ≠ 0 right to
// left before pos, and returns the new position and the number of integer
// digits, trailing zeros dropped by pgPutDigit included: the first digit's
// weight is that count minus one.
func pgPutInt(buf *[pgBufLen]byte, pos int, q u128) (int, uint16) {
	var n uint16
	// Peel four digits at a time with the reciprocal divider until the rest
	// fits one limb — at most twice, since 2^128/10^32 < 2^64. A two-limb q
	// is at least 2^64 > 10^16, so the quotient stays nonzero and the
	// zero-padded four-digit blocks never lead.
	for q.hi != 0 {
		var r uint64
		q, r = divmod128Pow10Slow(q, 16)
		for range 4 {
			pos = pgPutDigit(buf, pos, r%10000)
			r /= 10000
		}
		n += 4
	}
	for v := q.lo; v != 0; v /= 10000 {
		pos = pgPutDigit(buf, pos, v%10000)
		n++
	}
	return pos, n
}

// pgPutDigit writes the base-10000 digit g as a big-endian word just before
// pos and returns the new position. A zero digit with nothing written after
// it is a trailing zero, which numeric_send strips, so it is dropped.
func pgPutDigit(buf *[pgBufLen]byte, pos int, g uint64) int {
	if g == 0 && pos == pgBufLen {
		return pos
	}
	pos -= 2
	buf[pos&pgBufMask] = byte(g >> 8 & 0xFF)
	buf[(pos+1)&pgBufMask] = byte(g & 0xFF)
	return pos
}
