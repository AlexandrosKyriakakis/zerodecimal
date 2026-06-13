package zerodecimal

import (
	"database/sql"
	"database/sql/driver"
	"encoding"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// Compile-time interface assertions. Marshalers and Valuer take value
// receivers (a Decimal is a 24-byte pointer-free value); unmarshalers and
// Scanner need pointer receivers to write the result back. fmt is imported
// for the Stringer assertion only — no library code path calls into it
// except the documented cold-path wrap in Scan.
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
// Errors are the bare parse sentinels — match with errors.Is — and d is left
// unchanged on every error path. It never allocates.
func (d *Decimal) UnmarshalText(b []byte) error {
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
// own quoted form ("1.23") and bare JSON numbers — the strict parse grammar
// includes scientific notation, so float-encoded numbers like 1.5e-7 decode
// exactly. A literal null is a no-op (the stdlib convention, mirroring
// json.Unmarshal on pointers); use NullDecimal to distinguish null from a
// value. Exactly one balanced quote pair is stripped, so "null" in quotes
// and unbalanced quotes are parse errors. Errors are the bare parse
// sentinels and leave d unchanged. It never allocates.
func (d *Decimal) UnmarshalJSON(data []byte) error {
	if string(data) == jsonNull {
		return nil
	}
	if len(data) >= 2 && data[0] == '"' && data[len(data)-1] == '"' {
		data = data[1 : len(data)-1]
	}
	dec, err := parseCore(data, false)
	if err != nil {
		return err
	}
	*d = dec
	return nil
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
// to the canonical Decimal{}. It never allocates.
func (d *Decimal) UnmarshalBinary(data []byte) error {
	// Dispatch on the length first: each arm then needs a single combined
	// validity test, because the length fixes what the flag byte must be —
	// 10 bytes demand every non-sign bit clear (no high limb, no reserved
	// bits) and 18 bytes demand exactly the high-limb bit among them, which
	// folds the reserved-bit and flag-length-consistency checks into one
	// comparison each.
	switch len(data) {
	case binSizeLo:
		flags, prec := data[0], data[1]
		if flags&^binFlagNeg != 0 || prec > MaxPrec {
			return ErrInvalidBinaryData
		}
		*d = newDecimal(u128{lo: binary.BigEndian.Uint64(data[2:10])}, flags != 0, prec)
		return nil
	case binSizeHi:
		flags, prec := data[0], data[1]
		//nolint:gosec // len(data) == binSizeHi == 18 in this arm, so data[10:18] is in range
		hi := binary.BigEndian.Uint64(data[10:18])
		if flags&^binFlagNeg != binFlagHiPresent || prec > MaxPrec || hi == 0 {
			return ErrInvalidBinaryData
		}
		*d = newDecimal(u128{hi: hi, lo: binary.BigEndian.Uint64(data[2:10])}, flags&binFlagNeg != 0, prec)
		return nil
	}
	return ErrInvalidBinaryData
}
