// Package zerodecimal provides a fixed-point decimal type for
// latency-critical financial code: zero heap allocations on every hot path,
// panic-free sentinel errors, and arithmetic that is bit-exact against
// arbitrary-precision references.
//
// # Representation
//
// A Decimal is a 24-byte value: value = (neg ? -1 : +1) * coef / 10^prec,
// where coef is an unsigned 128-bit integer and prec is 0..19. There is no
// big.Int fallback anywhere: every operation runs on fixed-width integer
// math, so nothing escapes to the heap. The representable domain is
// |value| < 2^128 / 10^prec (up to 39 significant digits); operations whose
// exact result leaves the domain return ErrOverflow instead of degrading.
//
// # Error model
//
// Operations that can fail return (Decimal, error) with package-level
// sentinel errors and never panic. Each fallible operation has a Must twin
// that panics, for constants, tests, and call sites with proven bounds.
// Operations that cannot fail (Neg, Abs, rounding, comparisons, formatting)
// return Decimal directly and chain freely.
//
// # Allocations
//
// Parsing, arithmetic, comparison, rounding, conversions, the Append*
// methods, and every Unmarshal and Scan path allocate nothing — on success
// and error paths alike. String and StringFixed cost exactly one allocation
// (the returned string; values within ±1000.00 at up to two decimal places
// are served from a precomputed cache for zero), the Marshal* methods
// exactly one (the returned slice), and SQL Value at most two. The default
// test suite enforces these counts exactly with testing.AllocsPerRun.
//
// # Equality
//
// Arithmetic does not trim trailing fractional zeros, so == on Decimal
// values is representation equality, not numeric equality: a result equal to
// 1.50 differs from a parsed 1.5 under ==. Use Equal or Cmp for numeric
// comparison. Parsing and string constructors trim trailing fractional
// zeros; NewFromHiLo keeps the supplied precision verbatim for raw interop.
// Zero is always the zero value Decimal{} — no operation produces a negative
// zero.
//
// # Build tags
//
// DefaultPrec — the precision Div targets and strict parsing accepts — is a
// compile-time constant: 19 by default, lowered to 9 or 12 with the
// zerodecimal_prec9 or zerodecimal_prec12 build tags. The
// zerodecimal_nostrcache tag compiles out the small-value string cache that
// String and Value consult.
package zerodecimal
