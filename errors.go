package zerodecimal

import "errors"

// Sentinel errors for every fallible operation in the package. They are
// returned bare (never wrapped) on hot paths so that error returns stay
// allocation-free; match with errors.Is.
var (
	// ErrOverflow is returned when the exact result of an operation does not
	// fit a 128-bit coefficient at the result precision.
	ErrOverflow = errors.New("zerodecimal: value overflows 128-bit coefficient")

	// ErrDivideByZero is returned by Div, QuoRem, and Mod when the divisor is zero.
	ErrDivideByZero = errors.New("zerodecimal: division by zero")

	// ErrPrecOutOfRange is returned when a requested or parsed precision
	// exceeds MaxPrec fractional digits.
	ErrPrecOutOfRange = errors.New("zerodecimal: precision out of range")

	// ErrInvalidFormat is returned when parsing input that is not a valid decimal.
	ErrInvalidFormat = errors.New("zerodecimal: invalid format")

	// ErrEmptyString is returned when parsing empty input.
	ErrEmptyString = errors.New("zerodecimal: empty input")

	// ErrMaxStrLen is returned when parsing input longer than 200 bytes.
	ErrMaxStrLen = errors.New("zerodecimal: input exceeds 200 bytes")

	// ErrInvalidFloat is returned by float constructors for NaN or infinities.
	ErrInvalidFloat = errors.New("zerodecimal: NaN or Infinity")

	// ErrIntPartOverflow is returned when the integer part does not fit int64.
	ErrIntPartOverflow = errors.New("zerodecimal: integer part overflows int64")

	// ErrInvalidBinaryData is returned by UnmarshalBinary for malformed input.
	ErrInvalidBinaryData = errors.New("zerodecimal: invalid binary data")

	// ErrScanNil is returned when scanning SQL NULL into a Decimal; use
	// NullDecimal for nullable columns.
	ErrScanNil = errors.New("zerodecimal: cannot scan nil into Decimal")

	// ErrScanType is returned when scanning an unsupported source type.
	ErrScanType = errors.New("zerodecimal: unsupported Scan source type")
)
