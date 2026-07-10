package zerodecimal

import (
	"database/sql"
	"database/sql/driver"
	"encoding"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Compile-time interface assertions. Marshalers and Valuer take value
// receivers (Decimal is a small pointer-free value whose layout is documented
// on the type); unmarshalers and Scanner need pointer receivers to write the
// result back. fmt is imported for the Stringer assertion only — no library
// code path calls into it except the documented cold-path wrap in Scan.
var (
	_ fmt.Stringer               = Decimal{}
	_ json.Marshaler             = Decimal{}
	_ json.Unmarshaler           = (*Decimal)(nil)
	_ encoding.TextMarshaler     = Decimal{}
	_ encoding.TextAppender      = Decimal{}
	_ encoding.TextUnmarshaler   = (*Decimal)(nil)
	_ encoding.BinaryMarshaler   = Decimal{}
	_ encoding.BinaryAppender    = Decimal{}
	_ encoding.BinaryUnmarshaler = (*Decimal)(nil)
	_ sql.Scanner                = (*Decimal)(nil)
	_ driver.Valuer              = Decimal{}
)

// marshalCap is the size of the stack buffer MarshalJSON renders into before
// copying out: the widest canonical rendering is 41 bytes (sign, 39 digits,
// point) and the two quotes bring it to 43, so a 48-byte buffer always
// suffices. Rendering on the stack first lets the one real allocation be
// exactly the rendered length.
const marshalCap = 48

// jsonNull is the JSON null literal; the unmarshalers match it byte-for-byte
// before any quote handling, so a quoted "null" string is a parse error, not
// a null.
const jsonNull = "null"

// maxQuotedJSONLen is the largest encoded JSON string that can decode to the
// parser's maxParseLen-byte input: every decoded byte can occupy at most six
// source bytes (a \u00XX escape), plus the balanced quote pair. Rejecting
// anything larger before scanning keeps adversarial JSON work bounded while
// still admitting every valid spelling of every parseable decimal string.
const maxQuotedJSONLen = 2 + 6*maxParseLen

// MarshalText implements encoding.TextMarshaler, returning the canonical
// decimal representation of d (the exact bytes String produces) in a freshly
// allocated slice. It costs exactly one allocation — the result, sized
// exactly (the rendering happens in a stack buffer first).
func (d Decimal) MarshalText() ([]byte, error) {
	var scratch [scratchLen]byte
	pos, end := canonicalScratch(&scratch, d)
	// 0 ≤ pos ≤ end ≤ len(scratch) holds on every path (see
	// canonicalScratch); the guard drops the slice bounds check.
	//nolint:gosec // deliberate: the uint view sends negative cursors above len, failing the guard
	if uint(end) > uint(len(scratch)) || uint(pos) > uint(end) {
		return []byte{}, nil
	}
	out := make([]byte, end-pos)
	copy(out, scratch[pos:end])
	return out, nil
}

// UnmarshalText implements encoding.TextUnmarshaler, parsing the strict
// decimal literal grammar of NewFromString (scientific notation included).
// A nil receiver returns ErrNilReceiver before input validation. Errors are
// bare sentinels — match with errors.Is — and d is left unchanged on every
// error path. It never allocates.
func (d *Decimal) UnmarshalText(b []byte) error {
	if d == nil {
		return ErrNilReceiver
	}
	dec, err := parseCore(b, false)
	if err != nil {
		return err
	}
	*d = dec
	return nil
}

// MarshalJSON implements json.Marshaler, rendering d as a double-quoted
// canonical decimal string ("1.23") — ALWAYS quoted, because a bare JSON
// number is read as float64 by most consumers and would silently lose digits
// past 2^53. The rendering happens in a stack buffer, so the one allocation
// is the exactly-sized result; it deliberately never aliases the small-value
// string cache, since callers own the returned bytes and may mutate them.
func (d Decimal) MarshalJSON() ([]byte, error) {
	var buf [marshalCap]byte
	b := buf[:0]
	b = append(b, '"')
	b = appendCanonical(b, d)
	b = append(b, '"')
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

// UnmarshalJSON implements json.Unmarshaler, accepting both the package's
// own quoted form ("1.23") and bare JSON numbers — scientific notation is
// included, so float-encoded numbers like 1.5e-7 decode exactly. Bare input
// follows JSON's number grammar: a leading plus and integer leading zeros are
// rejected. Quoted strings retain the decimal-literal grammar, so "+1" and
// "01" remain valid decimal strings. A literal null returns ErrJSONNull and
// leaves d unchanged; use
// NullDecimal when null is a valid domain value. Quoted strings are decoded
// according to the JSON string grammar,
// including Unicode escapes and surrogate pairs, before the strict decimal
// parser sees them; therefore spellings such as "1\u002e5" are equivalent to
// "1.5". Exactly one balanced quote pair is required, so "null" in quotes
// and unbalanced quotes are parse errors. Decoded input remains subject to the
// parser's 200-byte limit. Errors are bare package sentinels and leave d
// unchanged. A nil receiver returns ErrNilReceiver before inspecting data.
// It never allocates.
func (d *Decimal) UnmarshalJSON(data []byte) error {
	if d == nil {
		return ErrNilReceiver
	}
	if string(data) == jsonNull {
		return ErrJSONNull
	}
	if len(data) >= 2 && data[0] == '"' && data[len(data)-1] == '"' {
		body := data[1 : len(data)-1]
		// Preserve MarshalJSON's escape-free hot path: try the semantic bytes
		// directly, then pay for escape discovery and decoding only when the
		// raw body is not itself a decimal. A successful decimal contains only
		// JSON-safe ASCII, so success here proves both grammars at once.
		dec, err := parseCore(body, false)
		if err == nil {
			*d = dec
			return nil
		}
		if len(data) > maxQuotedJSONLen {
			return ErrMaxStrLen
		}
		for _, c := range body {
			if c == '\\' {
				dec, err = parseEscapedQuotedJSON(body)
				if err != nil {
					return err
				}
				*d = dec
				return nil
			}
		}
		// Any unescaped quote, control byte, or malformed UTF-8 byte is not a
		// decimal-grammar byte, so parseCore's original error rejects every
		// malformed JSON string on this zero-copy arm as well.
		return err
	}
	if !bareJSONNumberStartOK(data) {
		return ErrInvalidFormat
	}
	dec, err := parseCore(data, false)
	if err != nil {
		return err
	}
	*d = dec
	return nil
}

// bareJSONNumberStartOK checks the only JSON-number constraints not already
// enforced by parseCore's decimal grammar: JSON has no leading plus, and a
// zero integer part must be the single zero before a point, exponent, or end.
// Empty and over-limit inputs deliberately pass through so parseCore preserves
// ErrEmptyString and ErrMaxStrLen precedence. Every other grammar byte is also
// left to parseCore, keeping the ordinary bare-number path to a two-byte prefix
// check instead of scanning the input twice.
func bareJSONNumberStartOK(data []byte) bool {
	n := len(data)
	if n == 0 || n > maxParseLen {
		return true
	}
	first := data[0]
	if first == '+' {
		return false
	}
	if first == '-' {
		return n < 3 || data[1] != '0' || data[2] < '0' || data[2] > '9'
	}
	return first != '0' || n < 2 || data[1] < '0' || data[1] > '9'
}

// parseEscapedQuotedJSON decodes one known-escaped JSON string body into a
// fixed stack buffer and parses its semantic contents. Keeping this cold path
// separate leaves MarshalJSON's ordinary quoted wire form on the same direct
// parseCore path it used before escape interoperability was added.
func parseEscapedQuotedJSON(body []byte) (Decimal, error) {
	var decoded [maxParseLen]byte
	n, err := unescapeJSONString(body, &decoded)
	if err != nil {
		return Decimal{}, err
	}
	return parseCore(decoded[:n], false)
}

// unescapeJSONString validates and decodes the contents between a JSON
// string's quotes. It accepts precisely JSON's eight short escapes and
// \uXXXX escapes, rejects unescaped controls and malformed UTF-8, and requires
// UTF-16 surrogate pairs to be well formed. ErrMaxStrLen is returned as soon
// as the decoded UTF-8 representation would exceed the parser's fixed bound.
func unescapeJSONString(src []byte, dst *[maxParseLen]byte) (int, error) {
	n := 0
	for i := 0; i < len(src); {
		c := src[i]
		i++

		switch {
		case c == '"' || c < 0x20:
			return 0, ErrInvalidFormat
		case c == '\\':
			if i == len(src) {
				return 0, ErrInvalidFormat
			}
			esc := src[i]
			i++
			switch esc {
			case '"', '\\', '/':
				c = esc
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 'u':
				r, ok := jsonHexRune(src, i)
				if !ok {
					return 0, ErrInvalidFormat
				}
				i += 4
				if r >= 0xD800 && r <= 0xDBFF {
					if len(src)-i < 6 || src[i] != '\\' || src[i+1] != 'u' {
						return 0, ErrInvalidFormat
					}
					lo, lowOK := jsonHexRune(src, i+2)
					if !lowOK || lo < 0xDC00 || lo > 0xDFFF {
						return 0, ErrInvalidFormat
					}
					i += 6
					r = 0x10000 + (r-0xD800)<<10 + lo - 0xDC00
				} else if r >= 0xDC00 && r <= 0xDFFF {
					return 0, ErrInvalidFormat
				}
				var appendOK bool
				n, appendOK = appendJSONRune(dst, n, r)
				if !appendOK {
					return 0, ErrMaxStrLen
				}
				continue
			default:
				return 0, ErrInvalidFormat
			}
			if n == len(dst) {
				return 0, ErrMaxStrLen
			}
			dst[n] = c
			n++
		case c < utf8.RuneSelf:
			if n == len(dst) {
				return 0, ErrMaxStrLen
			}
			dst[n] = c
			n++
		default:
			r, size := utf8.DecodeRune(src[i-1:])
			if r == utf8.RuneError && size == 1 {
				return 0, ErrInvalidFormat
			}
			if size > len(dst)-n {
				return 0, ErrMaxStrLen
			}
			copy(dst[n:n+size], src[i-1:i-1+size])
			n += size
			i += size - 1
		}
	}
	return n, nil
}

// jsonHexRune decodes exactly four hexadecimal bytes at off. The length
// check precedes all indexing, keeping arbitrary/truncated input panic-free.
func jsonHexRune(src []byte, off int) (rune, bool) {
	if len(src)-off < 4 {
		return 0, false
	}
	var r rune
	for _, c := range src[off : off+4] {
		var digit byte
		switch {
		case c >= '0' && c <= '9':
			digit = c - '0'
		case c >= 'a' && c <= 'f':
			digit = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			digit = c - 'A' + 10
		default:
			return 0, false
		}
		r = r<<4 | rune(digit)
	}
	return r, true
}

// appendJSONRune writes r's UTF-8 encoding to dst. jsonHexRune and the
// surrogate checks guarantee r is a Unicode scalar value; RuneLen retains a
// defensive invalid-rune check so this helper remains total for future callers.
func appendJSONRune(dst *[maxParseLen]byte, n int, r rune) (int, bool) {
	size := utf8.RuneLen(r)
	if size < 0 {
		return n, false
	}
	if size > len(dst)-n {
		return n, false
	}
	utf8.EncodeRune(dst[n:n+size], r)
	return n + size, true
}

// Binary wire format constants. The layout is fixed and compact:
//
//	byte 0      flags: bit0 = negative, bit1 = high limb present,
//	            bits 2..7 RESERVED, must be zero (future format versions
//	            will claim them, so today's decoder rejects them)
//	byte 1      prec (0..MaxPrec)
//	bytes 2..9  coef.lo, big-endian
//	bytes 10..17 coef.hi, big-endian — present only when bit1 is set,
//	            and bit1 is set only when coef.hi != 0
//
// Total size is therefore exactly 10 or 18 bytes. This format is NOT
// wire-compatible with github.com/quagmt/udecimal (different flag layout, no
// length byte, no big.Int arm).
const (
	binFlagNeg       byte = 1 << 0
	binFlagHiPresent byte = 1 << 1
	binSizeLo             = 10
	binSizeHi             = 18
)

// MarshalBinary implements encoding.BinaryMarshaler using the compact wire
// format documented at the binFlag constants: 10 bytes when the coefficient
// fits one limb, 18 otherwise, NOT udecimal-compatible. It costs exactly one
// allocation — the result, sized exactly.
func (d Decimal) MarshalBinary() ([]byte, error) {
	var buf [binSizeHi]byte
	size := d.putBinary(&buf)
	out := make([]byte, size)
	copy(out, buf[:size])
	return out, nil
}

// AppendBinary implements encoding.BinaryAppender, appending the
// MarshalBinary encoding of d to b and returning the extended slice. The
// error is always nil. It allocates only if b lacks capacity.
func (d Decimal) AppendBinary(b []byte) ([]byte, error) {
	var buf [binSizeHi]byte
	return append(b, buf[:d.putBinary(&buf)]...), nil
}

// putBinary renders the wire encoding of d into buf and returns its size,
// binSizeLo or binSizeHi — the shared core of MarshalBinary and AppendBinary.
func (d Decimal) putBinary(buf *[binSizeHi]byte) int {
	flags := byte(0)
	if d.neg {
		flags = binFlagNeg
	}
	buf[1] = d.prec
	binary.BigEndian.PutUint64(buf[2:10], d.coef.lo)
	if d.coef.hi != 0 {
		buf[0] = flags | binFlagHiPresent
		binary.BigEndian.PutUint64(buf[10:18], d.coef.hi)
		return binSizeHi
	}
	buf[0] = flags
	return binSizeLo
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler for the MarshalBinary
// wire format. Validation is strict — any violation returns
// ErrInvalidBinaryData and leaves d unchanged:
//
//   - the length must be exactly 10 or 18 bytes, consistent with the
//     high-limb flag bit (every truncation is caught, never read past)
//   - reserved flag bits 2..7 must be zero (the format-version guard)
//   - prec must not exceed MaxPrec
//   - a present high limb must be nonzero (the canonical encoding emits the
//     short form whenever it can, so a zero high limb marks a foreign or
//     corrupted encoder)
//
// A zero coefficient is normalized through newDecimal regardless of the
// encoded sign and precision, so a foreign encoder's "-0.000" still decodes
// to the canonical Decimal{}. A nil receiver returns ErrNilReceiver before
// inspecting data. It never allocates.
func (d *Decimal) UnmarshalBinary(data []byte) error {
	if d == nil {
		return ErrNilReceiver
	}
	// Dispatch on the length first: each arm then needs a single combined
	// validity test, because the length fixes what the flag byte must be —
	// 10 bytes demand every non-sign bit clear (no high limb, no reserved
	// bits) and 18 bytes demand exactly the high-limb bit among them, which
	// folds the reserved-bit and flag-length-consistency checks into one
	// comparison each.
	switch len(data) {
	case binSizeLo:
		//nolint:gosec // this switch arm proves len(data) == binSizeLo == 10
		flags, prec := data[0], data[1]
		if flags&^binFlagNeg != 0 || prec > MaxPrec {
			return ErrInvalidBinaryData
		}
		//nolint:gosec // this switch arm proves len(data) == binSizeLo == 10
		*d = newDecimal(u128{lo: binary.BigEndian.Uint64(data[2:10])}, flags != 0, prec)
		return nil
	case binSizeHi:
		//nolint:gosec // this switch arm proves len(data) == binSizeHi == 18
		flags, prec := data[0], data[1]
		//nolint:gosec // len(data) == binSizeHi == 18 in this arm, so data[10:18] is in range
		hi := binary.BigEndian.Uint64(data[10:18])
		if flags&^binFlagNeg != binFlagHiPresent || prec > MaxPrec || hi == 0 {
			return ErrInvalidBinaryData
		}
		//nolint:gosec // this switch arm proves len(data) == binSizeHi == 18
		*d = newDecimal(u128{hi: hi, lo: binary.BigEndian.Uint64(data[2:10])}, flags&binFlagNeg != 0, prec)
		return nil
	}
	return ErrInvalidBinaryData
}
