package zerodecimal

import (
	"database/sql"
	"database/sql/driver"
	"encoding"
	"encoding/json"
	"fmt"
	"time"
)

// Compile-time interface assertions for the SQL wrappers (see codec.go for
// the Decimal block): marshalers and Valuer on values, unmarshalers and
// Scanner on pointers.
var (
	_ sql.Scanner                = (*NullDecimal)(nil)
	_ driver.Valuer              = NullDecimal{}
	_ sql.Scanner                = (*StrictSQLDecimal)(nil)
	_ driver.Valuer              = StrictSQLDecimal{}
	_ encoding.TextUnmarshaler   = (*StrictSQLDecimal)(nil)
	_ encoding.BinaryUnmarshaler = (*StrictSQLDecimal)(nil)
	_ json.Unmarshaler           = (*StrictSQLDecimal)(nil)
	_ sql.Scanner                = (*StrictNullDecimal)(nil)
	_ driver.Valuer              = StrictNullDecimal{}
	_ json.Marshaler             = NullDecimal{}
	_ json.Unmarshaler           = (*NullDecimal)(nil)
	_ encoding.TextMarshaler     = NullDecimal{}
	_ encoding.TextUnmarshaler   = (*NullDecimal)(nil)
	_ json.Marshaler             = StrictNullDecimal{}
	_ json.Unmarshaler           = (*StrictNullDecimal)(nil)
	_ encoding.TextMarshaler     = StrictNullDecimal{}
	_ encoding.TextUnmarshaler   = (*StrictNullDecimal)(nil)
)

// Precomputed wrapped errors for the legal-but-unsupported driver.Value types
// Scan can meet per row. Built once at init so the branch that returns them
// stays allocation-free; each wraps ErrScanType, so errors.Is still matches.
var (
	errScanBool = fmt.Errorf("%w: bool", ErrScanType)
	errScanTime = fmt.Errorf("%w: time.Time", ErrScanType)
)

// Scan implements sql.Scanner, populating d from a value produced by a
// database driver. string and []byte parse as strict decimal literals
// (NewFromString grammar, scientific notation included); int64, int32, int,
// and uint64 convert exactly at precision 0; float64 converts through its
// shortest decimal representation (NewFromFloat semantics — NaN and
// infinities return ErrInvalidFloat). A nil src (SQL NULL) returns ErrScanNil;
// use NullDecimal for nullable legacy scanning or StrictNullDecimal for a
// nullable exact-source policy. Any other type returns an error wrapping
// ErrScanType. d is left unchanged on every error path.
//
// Every path is allocation-free, error paths included: the legal-but-
// unsupported driver.Value types bool and time.Time return precomputed
// wrapped errors (errScanBool, errScanTime), and any other type returns the
// bare ErrScanType sentinel, so scanning a mis-typed column allocates nothing
// per row. A nil receiver returns ErrNilReceiver before src is inspected.
func (d *Decimal) Scan(src any) error {
	if d == nil {
		return ErrNilReceiver
	}
	switch v := src.(type) {
	case string:
		dec, err := parseCore(v, false)
		if err != nil {
			return err
		}
		*d = dec
	case []byte:
		dec, err := parseCore(v, false)
		if err != nil {
			return err
		}
		*d = dec
	case int64:
		*d = NewFromInt(v)
	case int32:
		*d = NewFromInt32(v)
	case int:
		*d = NewFromInt(int64(v))
	case uint64:
		*d = NewFromUint64(v)
	case float64:
		dec, err := NewFromFloat(v)
		if err != nil {
			return err
		}
		*d = dec
	case bool:
		return errScanBool
	case time.Time:
		return errScanTime
	case nil:
		return ErrScanNil
	default:
		return ErrScanType
	}
	return nil
}

// Value implements driver.Valuer, rendering d as its canonical string: the
// package's portable exact driver.Value choice, with no float conversion in
// this package. String binding, casts, and column conversion remain specific
// to the driver and database and must be validated in integration tests. In a
// zerodecimal_strcache build, cache-window hits return a pre-boxed driver.Value
// with zero allocations. The gated ordinary multi-byte miss costs exactly two
// allocations—the canonical string plus boxing its header into the interface;
// runtime one-byte-string special cases can cost less.
func (d Decimal) Value() (driver.Value, error) {
	if v, ok := cachedValue(d); ok {
		return v, nil
	}
	// Render and convert directly rather than delegating to String: String
	// would re-run the cache probe (cacheIndex) a second time for every miss.
	// The result is still exactly the canonical string. Ordinary multi-byte
	// values follow the gated two-allocation contract (string + interface header).
	var scratch [scratchLen]byte
	pos, end := canonicalScratch(&scratch, d)
	// 0 ≤ pos ≤ end ≤ len(scratch) holds on every path (see
	// canonicalScratch); the guard drops the slice bounds check.
	//nolint:gosec // deliberate: the uint view sends negative cursors above len, failing the guard
	if uint(end) > uint(len(scratch)) || uint(pos) > uint(end) {
		return "", nil
	}
	return string(scratch[pos:end]), nil
}

// StrictSQLDecimal is an opt-in, required-value database boundary for money
// and other values that must never pass through floating point. It embeds a
// Decimal, so Decimal's read and arithmetic methods remain directly
// available while Scan applies the narrower source policy documented below.
// Its zero value contains Decimal's canonical zero and is ready to use.
//
// Use StrictSQLDecimal for required exact NUMERIC/DECIMAL columns and
// StrictNullDecimal for nullable ones. Use Decimal or NullDecimal only when
// their broader legacy Scan behavior is intentional; in particular,
// Decimal.Scan continues to accept float64 for v1 compatibility. This is a
// Go-side source-type policy, not proof of database provenance: a driver that
// returns a floating column as string or []byte is indistinguishable from
// exact decimal text. End-to-end use therefore requires NUMERIC/DECIMAL schema
// types and integration tests against the actual driver protocol and casts.
type StrictSQLDecimal struct {
	Decimal
}

// NewStrictSQLDecimal wraps d for strict SQL scanning and valuing. The
// wrapper has the same size and pointer-free representation as Decimal.
func NewStrictSQLDecimal(d Decimal) StrictSQLDecimal {
	return StrictSQLDecimal{Decimal: d}
}

// Scan implements sql.Scanner with an exact-source, required-value policy.
// It accepts strict decimal literals in string and []byte form and every
// ordinary signed or unsigned Go integer width. All conversions are exact.
// SQL NULL returns ErrScanNil, float32 and float64 return ErrScanFloat even
// when the particular value is exactly representable, and all other types
// return an error matching ErrScanType. The receiver is unchanged on every
// error path, and every success and error path allocates zero bytes.
//
// uintptr is intentionally not accepted: it represents an address-sized
// bit pattern rather than a database numeric source. A nil receiver returns
// ErrNilReceiver before src is inspected.
func (d *StrictSQLDecimal) Scan(src any) error {
	if d == nil {
		return ErrNilReceiver
	}
	var dec Decimal
	switch v := src.(type) {
	case string:
		var err error
		dec, err = parseCore(v, false)
		if err != nil {
			return err
		}
	case []byte:
		var err error
		dec, err = parseCore(v, false)
		if err != nil {
			return err
		}
	case int:
		dec = NewFromInt(int64(v))
	case int8:
		dec = NewFromInt(int64(v))
	case int16:
		dec = NewFromInt(int64(v))
	case int32:
		dec = NewFromInt32(v)
	case int64:
		dec = NewFromInt(v)
	case uint:
		dec = NewFromUint64(uint64(v))
	case uint8:
		dec = NewFromUint64(uint64(v))
	case uint16:
		dec = NewFromUint64(uint64(v))
	case uint32:
		dec = NewFromUint64(uint64(v))
	case uint64:
		dec = NewFromUint64(v)
	case float32, float64:
		return ErrScanFloat
	case bool:
		return errScanBool
	case time.Time:
		return errScanTime
	case nil:
		return ErrScanNil
	default:
		return ErrScanType
	}
	*d = StrictSQLDecimal{Decimal: dec}
	return nil
}

// UnmarshalText implements encoding.TextUnmarshaler with Decimal's strict
// decimal-literal semantics. The explicit forwarding method preserves the
// package-wide nil-receiver contract: a nil d returns ErrNilReceiver before b
// is inspected. For a non-nil receiver, success updates the embedded Decimal
// and every error leaves it unchanged.
func (d *StrictSQLDecimal) UnmarshalText(b []byte) error {
	if d == nil {
		return ErrNilReceiver
	}
	return d.Decimal.UnmarshalText(b)
}

// UnmarshalJSON implements json.Unmarshaler with Decimal's required-value
// JSON semantics, including ErrJSONNull for a null literal. A nil d returns
// ErrNilReceiver before data is inspected. For a non-nil receiver, success
// updates the embedded Decimal and every error leaves it unchanged.
func (d *StrictSQLDecimal) UnmarshalJSON(data []byte) error {
	if d == nil {
		return ErrNilReceiver
	}
	return d.Decimal.UnmarshalJSON(data)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler with Decimal's strict
// wire-format validation. A nil d returns ErrNilReceiver before data is
// inspected. For a non-nil receiver, success updates the embedded Decimal and
// every error leaves it unchanged.
func (d *StrictSQLDecimal) UnmarshalBinary(data []byte) error {
	if d == nil {
		return ErrNilReceiver
	}
	return d.Decimal.UnmarshalBinary(data)
}

// Value implements driver.Valuer. A StrictSQLDecimal value always returns
// Decimal's canonical string form, never a float and never SQL NULL. A nil
// *StrictSQLDecimal passed as a bind parameter is different: database/sql
// converts nil Valuer pointers to SQL NULL without invoking this value-receiver
// method, so applications must reject nil pointers for required parameters.
// Allocation behavior is identical to Decimal.Value: zero for an enabled
// cache hit; an ordinary uncached multi-byte value is gated at exactly two
// allocations.
func (d StrictSQLDecimal) Value() (driver.Value, error) {
	return d.Decimal.Value()
}

// NullDecimal is a Decimal that can represent SQL NULL: Valid false means
// NULL, and then Decimal holds the zero value. It follows the database/sql
// Null* convention and retains Decimal.Scan's legacy float64-accepting source
// policy. Use StrictNullDecimal when floating-point provenance must be
// rejected.
type NullDecimal struct {
	Decimal Decimal
	Valid   bool
}

// NewNullDecimal returns a valid NullDecimal holding d.
func NewNullDecimal(d Decimal) NullDecimal {
	return NullDecimal{Decimal: d, Valid: true}
}

// Scan implements sql.Scanner: a nil src (SQL NULL) clears n to the invalid
// zero NullDecimal without error; any other src follows Decimal.Scan, with a
// conversion error also clearing n — a NullDecimal never holds a stale value
// after a failed scan. A nil receiver returns ErrNilReceiver before src is
// inspected.
func (n *NullDecimal) Scan(src any) error {
	if n == nil {
		return ErrNilReceiver
	}
	if src == nil {
		*n = NullDecimal{}
		return nil
	}
	if err := n.Decimal.Scan(src); err != nil {
		*n = NullDecimal{}
		return err
	}
	n.Valid = true
	return nil
}

// Value implements driver.Valuer: SQL NULL when n is invalid, otherwise the
// canonical string of the held Decimal (see Decimal.Value).
func (n NullDecimal) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.Decimal.Value()
}

// MarshalJSON implements json.Marshaler: the JSON null literal when n is
// invalid, otherwise the double-quoted canonical string of the held Decimal.
// The null bytes are freshly allocated too — callers own MarshalJSON results
// and may mutate them.
func (n NullDecimal) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte(jsonNull), nil
	}
	return n.Decimal.MarshalJSON()
}

// UnmarshalJSON implements json.Unmarshaler: a literal null clears n to the
// invalid zero NullDecimal (unlike Decimal, where null returns ErrJSONNull);
// anything else must parse per Decimal.UnmarshalJSON and marks n valid. A
// parse error leaves n unchanged. A nil receiver returns ErrNilReceiver before
// data is inspected.
func (n *NullDecimal) UnmarshalJSON(data []byte) error {
	if n == nil {
		return ErrNilReceiver
	}
	if string(data) == jsonNull {
		*n = NullDecimal{}
		return nil
	}
	if err := n.Decimal.UnmarshalJSON(data); err != nil {
		return err
	}
	n.Valid = true
	return nil
}

// MarshalText implements encoding.TextMarshaler: the empty string when n is
// invalid (the conventional text rendering of NULL), otherwise the canonical
// bytes of the held Decimal.
func (n NullDecimal) MarshalText() ([]byte, error) {
	if !n.Valid {
		return []byte{}, nil
	}
	return n.Decimal.MarshalText()
}

// UnmarshalText implements encoding.TextUnmarshaler: empty input clears n to
// the invalid zero NullDecimal; anything else must parse as a strict decimal
// literal and marks n valid. A parse error leaves n unchanged. A nil receiver
// returns ErrNilReceiver before b is inspected.
func (n *NullDecimal) UnmarshalText(b []byte) error {
	if n == nil {
		return ErrNilReceiver
	}
	if len(b) == 0 {
		*n = NullDecimal{}
		return nil
	}
	if err := n.Decimal.UnmarshalText(b); err != nil {
		return err
	}
	n.Valid = true
	return nil
}

// StrictNullDecimal is the nullable counterpart to StrictSQLDecimal. Valid
// false represents SQL NULL and the zero value is ready to scan. Valid true
// holds Decimal exactly. Non-null Scan inputs follow StrictSQLDecimal's
// exact-source policy, so floating-point provenance is always rejected.
//
// The field layout intentionally matches NullDecimal for straightforward
// migration and zero-cost conversion inside the codec delegation below.
// NullDecimal retains its legacy float-accepting Scan behavior unchanged.
type StrictNullDecimal struct {
	Decimal Decimal
	Valid   bool
}

// NewStrictNullDecimal returns a valid StrictNullDecimal holding d.
func NewStrictNullDecimal(d Decimal) StrictNullDecimal {
	return StrictNullDecimal{Decimal: d, Valid: true}
}

// Scan implements sql.Scanner with nullable, exact-source semantics. SQL NULL
// clears n to its invalid zero value without error. Every non-null source is
// handled by StrictSQLDecimal.Scan: strings, byte slices, and integers convert
// exactly, while float32 and float64 return ErrScanFloat. Every failed scan
// clears n so a reused row destination can never retain a stale amount. A nil
// receiver returns ErrNilReceiver before src is inspected. All paths allocate
// zero bytes.
func (n *StrictNullDecimal) Scan(src any) error {
	if n == nil {
		return ErrNilReceiver
	}
	if src == nil {
		*n = StrictNullDecimal{}
		return nil
	}
	var strict StrictSQLDecimal
	if err := strict.Scan(src); err != nil {
		*n = StrictNullDecimal{}
		return err
	}
	*n = NewStrictNullDecimal(strict.Decimal)
	return nil
}

// Value implements driver.Valuer: SQL NULL when n is invalid, otherwise the
// canonical string of the held Decimal. It delegates to NullDecimal's proven
// wire contract without changing NullDecimal's Scan policy.
func (n StrictNullDecimal) Value() (driver.Value, error) {
	return n.asNullDecimal().Value()
}

// MarshalJSON emits null for an invalid value and Decimal's quoted canonical
// string for a valid value. The explicit method prevents encoding/json from
// treating StrictNullDecimal as an ordinary exported-field struct.
func (n StrictNullDecimal) MarshalJSON() ([]byte, error) {
	return n.asNullDecimal().MarshalJSON()
}

// UnmarshalJSON mirrors NullDecimal's nullable JSON contract: null clears,
// valid decimal input sets Valid, and an error leaves n unchanged. A nil
// receiver returns ErrNilReceiver before data is inspected.
func (n *StrictNullDecimal) UnmarshalJSON(data []byte) error {
	if n == nil {
		return ErrNilReceiver
	}
	legacy := n.asNullDecimal()
	if err := legacy.UnmarshalJSON(data); err != nil {
		return err
	}
	*n = strictNullDecimalFromNull(legacy)
	return nil
}

// MarshalText emits empty text for an invalid value and Decimal's canonical
// bytes for a valid value.
func (n StrictNullDecimal) MarshalText() ([]byte, error) {
	return n.asNullDecimal().MarshalText()
}

// UnmarshalText mirrors NullDecimal's nullable text contract: empty input
// clears, valid decimal text sets Valid, and an error leaves n unchanged. A
// nil receiver returns ErrNilReceiver before b is inspected.
func (n *StrictNullDecimal) UnmarshalText(b []byte) error {
	if n == nil {
		return ErrNilReceiver
	}
	legacy := n.asNullDecimal()
	if err := legacy.UnmarshalText(b); err != nil {
		return err
	}
	*n = strictNullDecimalFromNull(legacy)
	return nil
}

func (n StrictNullDecimal) asNullDecimal() NullDecimal {
	return NullDecimal(n)
}

func strictNullDecimalFromNull(n NullDecimal) StrictNullDecimal {
	return StrictNullDecimal(n)
}
