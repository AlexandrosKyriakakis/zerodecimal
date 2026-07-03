package zerodecimal

import (
	"database/sql"
	"database/sql/driver"
	"encoding"
	"encoding/json"
	"fmt"
	"time"
)

// Compile-time interface assertions for NullDecimal (see codec.go for the
// Decimal block): marshalers and Valuer on the value, unmarshalers and
// Scanner on the pointer.
var (
	_ sql.Scanner              = (*NullDecimal)(nil)
	_ driver.Valuer            = NullDecimal{}
	_ json.Marshaler           = NullDecimal{}
	_ json.Unmarshaler         = (*NullDecimal)(nil)
	_ encoding.TextMarshaler   = NullDecimal{}
	_ encoding.TextUnmarshaler = (*NullDecimal)(nil)
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
// use NullDecimal for nullable columns. Any other type returns an error
// wrapping ErrScanType. d is left unchanged on every error path.
//
// Every path is allocation-free, error paths included: the legal-but-
// unsupported driver.Value types bool and time.Time return precomputed
// wrapped errors (errScanBool, errScanTime), and any other type returns the
// bare ErrScanType sentinel, so scanning a mis-typed column allocates nothing
// per row.
func (d *Decimal) Scan(src any) error {
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

// Value implements driver.Valuer, rendering d as its canonical string — the
// one decimal wire form every SQL driver and database agree on, with no
// float rounding and no driver-side numeric truncation. Values inside the
// small-value cache window return a pre-boxed driver.Value with zero
// allocations; everything else costs exactly two — the canonical string plus
// boxing its header into the interface (the bytes themselves are shared, not
// copied).
func (d Decimal) Value() (driver.Value, error) {
	if v, ok := cachedValue(d); ok {
		return v, nil
	}
	// Render and convert directly rather than delegating to String: String
	// would re-run the cache probe (cacheIndex) a second time for every miss.
	// The result is still exactly the canonical string, so the documented
	// exactly-two-allocations contract (string + interface header) holds.
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

// NullDecimal is a Decimal that can represent SQL NULL: Valid false means
// NULL, and then Decimal holds the zero value. It follows the
// database/sql Null* convention so nullable columns scan without errors.
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
// after a failed scan.
func (n *NullDecimal) Scan(src any) error {
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
// invalid zero NullDecimal (unlike Decimal, where null is a no-op — here the
// type exists to record it); anything else must parse per
// Decimal.UnmarshalJSON and marks n valid. A parse error leaves n unchanged.
func (n *NullDecimal) UnmarshalJSON(data []byte) error {
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
// literal and marks n valid. A parse error leaves n unchanged.
func (n *NullDecimal) UnmarshalText(b []byte) error {
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
