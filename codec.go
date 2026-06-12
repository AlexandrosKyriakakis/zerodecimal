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

// marshalCap is the capacity the single-allocation marshalers reserve up
// front: the widest canonical rendering is 41 bytes (sign, 39 digits, point)
// and MarshalJSON adds two quotes for 43, so a 48-byte buffer never regrows
// and the one make is the only allocation.
const marshalCap = 48

// jsonNull is the JSON null literal; the unmarshalers match it byte-for-byte
// before any quote handling, so a quoted "null" string is a parse error, not
// a null.
const jsonNull = "null"

// MarshalText implements encoding.TextMarshaler, returning the canonical
// decimal representation of d (the exact bytes String produces) in a freshly
// allocated slice. It costs exactly one allocation — the result.
func (d Decimal) MarshalText() ([]byte, error) {
	return appendCanonical(make([]byte, 0, marshalCap), d), nil
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
// past 2^53. The result is built directly in one freshly allocated slice; it
// deliberately never aliases the small-value string cache, since callers own
// the returned bytes and may mutate them.
func (d Decimal) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, marshalCap)
	b = append(b, '"')
	b = appendCanonical(b, d)
	return append(b, '"'), nil
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
// allocation — the result.
func (d Decimal) MarshalBinary() ([]byte, error) {
	return d.AppendBinary(make([]byte, 0, binSizeHi))
}

// AppendBinary implements encoding.BinaryAppender, appending the
// MarshalBinary encoding of d to b and returning the extended slice. The
// error is always nil. It allocates only if b lacks capacity.
func (d Decimal) AppendBinary(b []byte) ([]byte, error) {
	var buf [binSizeHi]byte
	flags := byte(0)
	if d.neg {
		flags = binFlagNeg
	}
	buf[1] = d.prec
	binary.BigEndian.PutUint64(buf[2:10], d.coef.lo)
	if d.coef.hi != 0 {
		buf[0] = flags | binFlagHiPresent
		binary.BigEndian.PutUint64(buf[10:18], d.coef.hi)
		return append(b, buf[:binSizeHi]...), nil
	}
	buf[0] = flags
	return append(b, buf[:binSizeLo]...), nil
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
	if len(data) != binSizeLo && len(data) != binSizeHi {
		return ErrInvalidBinaryData
	}
	flags := data[0]
	if flags&^(binFlagNeg|binFlagHiPresent) != 0 {
		return ErrInvalidBinaryData
	}
	hiPresent := flags&binFlagHiPresent != 0
	if hiPresent != (len(data) == binSizeHi) {
		return ErrInvalidBinaryData
	}
	prec := data[1]
	if prec > MaxPrec {
		return ErrInvalidBinaryData
	}
	coef := u128{lo: binary.BigEndian.Uint64(data[2:10])}
	if hiPresent {
		coef.hi = binary.BigEndian.Uint64(data[10:18])
		if coef.hi == 0 {
			return ErrInvalidBinaryData
		}
	}
	*d = newDecimal(coef, flags&binFlagNeg != 0, prec)
	return nil
}
