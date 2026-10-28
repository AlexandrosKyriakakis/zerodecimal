// Package zdbig converts between zerodecimal.Decimal and math/big values.
//
// The zerodecimal package never touches math/big: every operation runs on
// fixed-width integers, and none of its source files imports it. Code that
// exchanges numbers with arbitrary-precision systems still needs the
// conversion, and it lives here so that only programs that ask for it depend
// on a math/big API. pgx's pgtype.Numeric, shopspring/decimal and apd all
// describe a decimal as a coefficient *big.Int and an int32 exponent, the pair
// FromBigInt consumes and ToBigInt produces.
//
// # Exactness
//
// Neither function rounds. FromBigInt returns zerodecimal.ErrOverflow or
// zerodecimal.ErrPrecOutOfRange rather than a nearby value when coef·10^exp has
// no Decimal form, and ToBigInt is total. A Decimal survives a round trip
// through ToBigInt and FromBigInt unchanged, trailing zeros included.
//
// # Nil coefficients
//
// A nil *big.Int is zero, like a zero coefficient. pgtype.Numeric stores SQL
// NULL, NaN and the infinities with a nil Int, so converting its Int and Exp
// alone turns those into zero, with no error. FromBigInt cannot see the other
// fields: test Valid, NaN and InfinityModifier before converting.
//
// # Representation
//
// A (coef, exp) pair keeps its trailing zeros: FromBigInt follows
// zerodecimal.New, not the parser, so the pair (150, -2) is 1.50 with
// precision 2. Call Trim on the result for the canonical form that parsing
// produces.
//
// # Allocations
//
// FromBigInt reads the limbs of its argument in place and does not allocate
// when the coefficient fits 128 bits, on success and on error. A wider
// coefficient is only representable after its trailing decimal zeros are
// stripped, which takes its decimal digits and so allocates, at a cost of
// about one decimal conversion of the coefficient. One that is not a multiple
// of ten, or is too large for the exponent, is rejected before that, after at
// most a pass over its limbs. ToBigInt allocates the returned big.Int once,
// limbs included.
//
// # 32-bit platforms
//
// big.Word is 32 bits wide on 386, arm and mips. Both conversions assemble
// limbs of either width.
package zdbig
