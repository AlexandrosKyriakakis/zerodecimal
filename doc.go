// Package zerodecimal provides checked fixed-point decimal arithmetic with a
// pointer-free 128-bit coefficient and at most 19 fractional digits.
// Arithmetic, parsing, comparisons, rounding, Unmarshal methods, and Scanner
// methods allocate no heap memory on either success or error paths.
//
// # Representation
//
// A Decimal represents
//
//	(negative ? -1 : +1) * coefficient / 10^precision
//
// where coefficient is an unsigned 128-bit integer and precision is in
// 0..MaxPrec. Decimal is 24 bytes on targets with 8-byte maximum alignment
// and 20 bytes on gc's 32-bit max-align-4 ports. The zero value Decimal{} is
// canonical zero. Decimal has no arbitrary-precision fallback; a magnitude
// outside the domain returns ErrOverflow.
//
// Decimal methods do not mutate value operands. Representation equality and
// numeric equality differ when trailing fractional zeros differ: use Equal or
// Cmp for numeric comparison, Trim for canonical representation equality, and
// Rescale for a requested fixed representation scale.
//
// # Arithmetic loss policy
//
// Mul, Div, and Avg are compatibility operations. They truncate toward zero
// when their result exceeds DefaultPrec, and discarded digits are not an
// error. Div and Avg use the greatest fitting adaptive precision at or below
// DefaultPrec. Rounding one of these results later cannot recover discarded
// tie or sticky information.
//
// MulExact, DivExact, and AvgExact require an exact Decimal representation and
// report ErrInexact, ErrUnderflow, or ErrOverflow as appropriate. MulRound,
// DivRound, and AvgRound round directly from the full product or remainder to
// a caller-selected scale and RoundingMode. Use these APIs for calculations
// whose loss policy must be explicit.
//
// Sum and all average variants use a wide signed accumulator, making
// cancellation independent of operand order. Sum checks its final coefficient
// at the greatest input precision and preserves that precision for a nonzero
// result; it returns ErrOverflow when the coefficient does not fit there even
// if a lower precision could express the same integer. Zero remains canonical.
// QuoRem and Mod implement truncated/T-division and handle an aligned divisor
// wider than 128 bits by returning the exact zero quotient when it exceeds the
// numerator.
//
// # Parsing and codecs
//
// Strict parsing accepts ASCII decimal and scientific notation under a
// 200-byte work bound. It validates the canonical value after folding the
// exponent and removing trailing fractional zeros, so a wide lexical mantissa
// or scale is accepted whenever the exact canonical result fits. Leading or
// trailing decimal points, whitespace, underscores, non-ASCII digits, NaN,
// and infinities are rejected.
//
// JSON marshaling emits quoted canonical strings. JSON unmarshaling accepts
// bare JSON numbers and quoted decimal strings, validates and decodes JSON
// escapes, and then parses the value exactly. Bare input rejects a leading plus
// and integer leading zeros; those spellings remain valid inside quoted decimal
// strings. A JSON null into Decimal returns ErrJSONNull without mutation;
// NullDecimal accepts null and clears itself.
// Every fallible pointer-receiver decoder or Scanner returns ErrNilReceiver
// before inspecting input when its receiver is nil.
//
// # Error handling
//
// Match package sentinels with errors.Is; database/sql may wrap Scanner
// errors. ErrUnderflow is a specialized ErrInexact condition, so
// errors.Is(ErrUnderflow, ErrInexact) is true. Must and Require helpers panic
// by contract and are intended only for constants or caller-proven bounds.
//
// # SQL boundaries
//
// StrictSQLDecimal and StrictNullDecimal are the required and nullable SQL
// boundaries for exact decimal provenance. Both reject float32/float64 with
// ErrScanFloat; StrictSQLDecimal rejects SQL NULL with ErrScanNil, while
// StrictNullDecimal clears itself. This rejects Go float source types; a driver
// that returns a floating column as text is indistinguishable from exact
// decimal text, so end-to-end provenance also requires NUMERIC/DECIMAL schema
// types and driver integration tests. Decimal.Scan and NullDecimal.Scan retain
// v1 compatibility and accept float64.
//
// # Allocations and cache
//
// Parsing, arithmetic (including exact, rounded, and aggregate variants),
// Append methods with sufficient destination capacity, Unmarshal methods, and
// Scanner methods allocate exactly zero bytes. StringFixed and Decimal's
// MarshalText, MarshalJSON, and MarshalBinary methods allocate exactly one
// owned result. Uncached multi-byte String results normally allocate once; an
// uncached representative multi-byte SQL Value is gated at exactly two
// allocations.
//
// The eager small-value string and driver.Value cache is absent by default.
// The zerodecimal_strcache build tag enables it; cache hits allocate nothing
// but the process pays eager startup, memory, heap-object, and GC-root costs.
// The zerodecimal_nostrcache tag forces it off and takes precedence.
//
// # Build tags
//
// DefaultPrec is 19 by default and can be lowered to 9 or 12 with the mutually
// exclusive zerodecimal_prec9 and zerodecimal_prec12 tags. These tags affect
// legacy Mul, Div, and Avg truncation. MaxPrec, strict parsing, exact
// arithmetic, and directly rounded arithmetic remain at 19 places. The full
// test suite runs under every supported precision/cache combination.
//
// # Deprecated compatibility variables
//
// Zero and One are mutable exported variables retained for source
// compatibility. Do not assign to or depend on them; use Decimal{} and
// NewFromInt(1). Package internals do not read them.
package zerodecimal
