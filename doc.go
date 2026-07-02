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
// |value| < 2^128 / 10^prec (up to 39 significant digits). ErrOverflow covers
// magnitude (coefficient) overflow; a result requiring more than DefaultPrec
// fractional digits is instead truncated toward zero by Mul and Div — possibly
// to exact zero, as with a tiny nonzero product — as documented on those
// methods.
//
// # Error model
//
// Operations that can fail return (Decimal, error) with package-level
// sentinel errors and never panic. The constructors and the arithmetic
// operations have panicking twins (MustNew, RequireFromString, MustAdd, ...)
// for constants, tests, and call sites with proven bounds; the Trunc
// parsers, NewFromHiLo, IntPart, the Unmarshal methods, and Scan have none.
// Operations that cannot fail (Neg, Abs, rounding, comparisons, formatting)
// return Decimal directly and chain freely.
//
// # Allocations
//
// Parsing, arithmetic, comparison, rounding, canonicalization, conversions,
// the Append* methods, and every Unmarshal and Scan path allocate nothing —
// on success and error paths alike. String and StringFixed cost exactly one allocation
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
// comparison. Trim canonicalizes a representation — numerically equal values
// Trim to identical Decimals, safe for == and map keys — and Rescale sets an
// exact representation precision for fixed-scale interop. Parsing and string
// constructors trim trailing fractional zeros; NewFromHiLo keeps the
// supplied precision verbatim for raw interop. Zero is always the zero value
// Decimal{} — no operation produces a negative zero.
//
// # Build tags
//
// DefaultPrec — the precision Div targets and the cap Mul rescales down to —
// is a compile-time constant: 19 by default, lowered to 9 or 12 with the
// zerodecimal_prec9 or zerodecimal_prec12 build tags. Strict parsing is not
// bound by it: NewFromString accepts up to MaxPrec (19) fractional digits
// under every build tag, so under zerodecimal_prec9 or zerodecimal_prec12 a
// parsed value may carry more fractional digits than DefaultPrec, which later
// arithmetic then truncates down to DefaultPrec. The full test suites assume
// DefaultPrec = 19; the zerodecimal_prec9 and zerodecimal_prec12
// configurations are verified at compile + vet level only in v1. The
// zerodecimal_nostrcache tag compiles out the small-value string cache that
// String and Value consult.
package zerodecimal
