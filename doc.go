// Package zerodecimal provides a fixed-point decimal type for
// latency-critical financial code: strictly zero heap allocations on every
// operation, panic-free sentinel errors, and arithmetic that is bit-exact
// against arbitrary-precision references.
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
// # Equality
//
// Arithmetic does not trim trailing fractional zeros, so == on Decimal
// values is representation equality, not numeric equality: a result equal to
// 1.50 differs from a parsed 1.5 under ==. Use Equal or Cmp for numeric
// comparison. Parsing and constructors do produce trimmed canonical form,
// and zero is always the zero value Decimal{}.
package zerodecimal
