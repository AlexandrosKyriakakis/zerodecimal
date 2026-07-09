# zerodecimal

Fixed-width decimal arithmetic for latency-sensitive Go services, with a
pointer-free representation, allocation-free arithmetic and parsing, explicit
rounding APIs, and checked overflow.

## Install

```sh
go get github.com/AlexandrosKyriakakis/zerodecimal
```

```go
import "github.com/AlexandrosKyriakakis/zerodecimal"
```

The package requires Go 1.26.5 or later and has no runtime dependencies.

For a money calculation, select the output scale and rounding mode at the
operation that discards digits:

```go
price, err := zerodecimal.NewFromString("99.99")
if err != nil {
	return err
}
quantity := zerodecimal.NewFromInt(3)

total, err := price.MulRound(quantity, 2, zerodecimal.ToNearestEven)
if err != nil {
	return err
}
fmt.Println(total.StringFixed(2)) // 299.97
```

See [example_test.go](example_test.go) for runnable examples and
[MIGRATION.md](MIGRATION.md) before upgrading an existing deployment.

## Representation and domain

Conceptually:

```go
value = (negative ? -1 : +1) * coefficient / 10^precision
```

The coefficient is an unsigned 128-bit integer and precision is between 0 and
`MaxPrec` (19). The representable domain is
`|value| < 2^128 / 10^precision`, or at most 39 significant decimal digits.
There is no `big.Int` fallback: an unrepresentable magnitude returns
`ErrOverflow`.

`Decimal` contains no pointers. It is 24 bytes on targets with 8-byte maximum
alignment and 20 bytes on gc's 32-bit max-align-4 ports (`386`, `arm`, `mips`,
and `mipsle`). The zero value `Decimal{}` is canonical zero and is ready to
use. Negative zero is never produced.

Arithmetic retains representation precision unless its documented semantics
require a different scale. Consequently, `==` is representation equality:
`1.5` and `1.50` may compare unequal. Use `Equal` or `Cmp` for numeric
comparison. Use `Trim` when canonical representation equality or stable map
keys are required, and `Rescale` when a fixed representation scale is
required.

## Choose arithmetic semantics explicitly

The package retains `Mul` and `Div` as fast, compatibility-oriented
operations. They can discard digits without returning `ErrInexact`. New code
that moves or records money should normally use an exact or directly rounded
variant.

| Intent | Multiplication/division | Aggregate mean |
| --- | --- | --- |
| Compatibility truncation | `Mul`, `Div` | `Avg` |
| Require exact representation | `MulExact`, `DivExact` | `AvgExact` |
| Round once to a requested scale | `MulRound`, `DivRound` | `AvgRound` |

### Legacy truncating operations

- `Mul` preserves the natural product precision until it exceeds
  `DefaultPrec`, then truncates excess fractional digits toward zero. A tiny
  nonzero product can therefore become zero with a nil error.
- `Div` chooses the greatest precision at or below `DefaultPrec` whose
  truncated coefficient fits 128 bits. It truncates toward zero and may lower
  precision for a large quotient.
- `Avg` divides the exact wide aggregate at adaptive precision up to
  `DefaultPrec`, truncating toward zero.

Calling `RoundBank` after `Mul`, `Div`, or `Avg` cannot restore digits already
discarded by the first operation and can double-round. Use the corresponding
`*Round` method when a rounded result is required.

### Exact and directly rounded operations

- `MulExact`, `DivExact`, and `AvgExact` succeed only when the mathematical
  result has an exact `Decimal` representation. They can return `ErrInexact`,
  `ErrUnderflow`, or `ErrOverflow`; division can also return
  `ErrDivideByZero`.
- `MulRound`, `DivRound`, and `AvgRound` retain the full product or quotient
  remainder until the rounding decision. They accept an exact output
  `places` value and one of six `RoundingMode` constants.
- Exact and directly rounded operations use `MaxPrec`, not `DefaultPrec`, and
  therefore keep the same semantic ceiling under the `zerodecimal_prec9` and
  `zerodecimal_prec12` build tags.

Rounding modes are `ToNearestAway`, `ToNearestEven`, `AwayFromZero`,
`TowardZero`, `TowardPositive`, and `TowardNegative`. A rounded zero is a
successful canonical zero; it is not `ErrUnderflow` because the caller
explicitly requested loss at that scale.

### Sums, means, quotient, and remainder

`Sum` and every mean operation accumulate through a fixed-width wide signed
accumulator. Cancellation is independent of operand order, so a final
coefficient that fits at the greatest input precision is not rejected merely
because a left-to-right intermediate would exceed 128 bits. `Sum` evaluates
the final coefficient at that precision (a nonzero result retains it; zero is
canonical) and returns `ErrOverflow` when the coefficient does not fit there,
even if lowering precision could express the same mathematical integer.

`QuoRem` implements truncated/T-division:

```text
q = trunc(d/e)
r = d - q*e
```

It guarantees `d = q*e + r`, `|r| < |e|`, and a remainder with the sign of
`d`. When precision alignment makes the divisor wider than 128 bits, the
implementation recognizes that it exceeds the 128-bit numerator and returns
`q = 0, r = d` instead of reporting a spurious overflow. `Mod` returns the
same remainder.

## Parsing and canonicalization

The strict grammar is:

```text
['+'|'-'] digits ['.' digits] [('e'|'E') ['+'|'-'] digits]
```

Input is ASCII and limited to 200 bytes. Both sides of a decimal point must
contain a digit, so `"1."` and `".1"` are rejected. Whitespace, underscores,
non-ASCII digits, NaN, and infinities are rejected.

Strict parsing validates the canonical mathematical value, not the raw
mantissa width or lexical number of fractional digits. Exponents are folded
and trailing fractional zeros are removed before range checks. For example,
all of the following exact values are accepted:

```text
340282366920938463463374607431768211455.0
3402823669209384634633746074317682114550e-1
1.00000000000000000000
```

The first two canonicalize to the maximum coefficient and the third to `1`.
An effective precision above `MaxPrec` with discarded nonzero digits returns
`ErrPrecOutOfRange`; a canonical coefficient above `2^128-1` returns
`ErrOverflow`.

`NewFromStringTrunc` and `ParseBytesTrunc` instead truncate digits below
`MaxPrec` toward zero. They still enforce the grammar, 200-byte work bound,
and final coefficient range.

## JSON, text, and binary boundaries

`MarshalJSON` emits a quoted canonical decimal string to avoid accidental
float64 conversion by downstream systems. `UnmarshalJSON` accepts both bare
JSON numbers and quoted strings. Bare input follows JSON number syntax, so a
leading plus or integer leading zeros are rejected; quoted content follows the
decimal-literal grammar, where `"+1"` and `"01"` are valid. Quoted strings are
decoded according to the JSON string grammar before decimal parsing, so
`"1\u002e5"` is identical to `"1.5"`; malformed escapes, invalid UTF-8, and
invalid surrogate pairs are rejected. The decoded content remains subject to
the 200-byte parser limit.

JSON null is fail-closed for a required value:

- `Decimal.UnmarshalJSON(null)` returns `ErrJSONNull` and leaves the receiver
  unchanged.
- `NullDecimal.UnmarshalJSON(null)` and
  `StrictNullDecimal.UnmarshalJSON(null)` clear the value and return nil.

Use a `Decimal` value field for a required JSON amount. A `*Decimal` field
follows `encoding/json` pointer semantics, under which null can set the
pointer to nil without calling `Decimal.UnmarshalJSON`. Use `NullDecimal` or
`StrictNullDecimal` when null is part of the schema.

Text decoding uses the same strict decimal parser. The compact binary format
is 10 or 18 bytes and validates length, flags, precision, and canonical limb
layout.

Every fallible pointer-receiver decoder and scanner returns `ErrNilReceiver`
before inspecting input when called on nil. Non-nil decode failures leave a
`Decimal` unchanged. `NullDecimal.Scan`, by design, clears its receiver after
a non-nil SQL conversion error so it cannot retain a stale nullable row.

## SQL boundaries

Choose the scanner based on provenance and nullability:

- `StrictSQLDecimal` is the recommended required-value boundary for exact
  `NUMERIC`/`DECIMAL` columns. It accepts strings, byte slices, and ordinary
  signed/unsigned integer widths; rejects float32/float64 with `ErrScanFloat`;
  and rejects SQL NULL with `ErrScanNil`.
- `StrictNullDecimal` is the recommended nullable exact boundary. SQL NULL
  clears it without error; non-null sources use the same strict policy as
  `StrictSQLDecimal`; every failed Scan clears stale nullable state.
- `Decimal.Scan` is the v1 compatibility scanner. It accepts float64 through
  `NewFromFloat` in addition to exact textual/integer sources. That cannot
  recover precision already lost by a database driver.
- `NullDecimal` represents SQL NULL, but otherwise delegates to the legacy
  float64-accepting `Decimal.Scan` source policy.

`Decimal`, `StrictSQLDecimal`, and valid nullable `Value` methods emit
canonical strings rather than floats; invalid nullable values emit SQL NULL.
Exercise the real driver in integration tests: drivers differ in how they
expose decimal columns.

## Error model

Errors are package sentinels and should be matched with `errors.Is`. Hot paths
return them bare. The legal-but-unsupported `bool` and `time.Time` SQL source
errors are precomputed wrappers matching `ErrScanType`, and `database/sql`
may add its own wrapping around scanner errors. The table covers every public
API family that can currently return a non-nil package error.

| API family | Errors to handle |
| --- | --- |
| `NewFromString*` / `ParseBytes*` | `ErrEmptyString`, `ErrMaxStrLen`, `ErrInvalidFormat`, `ErrOverflow`, `ErrPrecOutOfRange` |
| Text / JSON decode | The same parse sentinels for decimal input; `Decimal.UnmarshalJSON` also returns `ErrJSONNull` for JSON null (nullable wrappers accept null, and empty text, as invalid) |
| `New` / `NewFromHiLo` | `ErrOverflow`, `ErrPrecOutOfRange` (`NewFromHiLo` only returns the latter) |
| `NewFromFloat` / `NewFromFloat32` | `ErrInvalidFloat`, `ErrOverflow`, `ErrPrecOutOfRange` |
| `Rescale` | `ErrPrecOutOfRange`, `ErrOverflow` |
| `IntPart` | `ErrIntPartOverflow` |
| `UnmarshalBinary` | `ErrInvalidBinaryData` |
| `Add` / `Sub` / `Sum` | `ErrOverflow` |
| Legacy `Mul` / `Div` / `Avg` | `ErrOverflow`; division also `ErrDivideByZero` |
| `MulExact` / `DivExact` / `AvgExact` | `ErrOverflow`, `ErrInexact`, `ErrUnderflow`; division also `ErrDivideByZero` |
| `MulRound` / `DivRound` / `AvgRound` | `ErrPrecOutOfRange`, `ErrInvalidRoundingMode`, `ErrOverflow`; division also `ErrDivideByZero` |
| `QuoRem` / `Mod` | `ErrDivideByZero`, `ErrOverflow` |
| `Decimal.Scan` / `NullDecimal.Scan` | The parse sentinels above; `ErrInvalidFloat`, `ErrScanType`, and `ErrScanNil` for `Decimal` only |
| `StrictSQLDecimal.Scan` / `StrictNullDecimal.Scan` | The parse sentinels above; `ErrScanFloat`, `ErrScanType`, and `ErrScanNil` for `StrictSQLDecimal` only |
| Nil decoder/scanner receiver | `ErrNilReceiver` before every input-specific error |

`MarshalText`, `MarshalJSON`, `MarshalBinary`, `AppendText`, `AppendBinary`, and
the SQL `Value` methods expose an `error` result for standard interfaces or API
symmetry but currently return it as nil.

`errors.Is(ErrUnderflow, ErrInexact)` is true. A caller can therefore handle
all exactness failures together while still testing `ErrUnderflow` first when
the below-minimum-quantum distinction matters.

The `Must*` and `Require*` helpers deliberately panic on error and should be
reserved for constants, tests, or bounds proven by the caller.

## Allocation contracts and optional cache

Allocation behavior is asserted with exact `testing.AllocsPerRun` gates:

| Contract | APIs |
| --- | --- |
| Exactly 0 | Parsing; legacy, exact, rounded, and aggregate arithmetic; comparisons; rounding; `Trim`; Unmarshal and Scan methods on success/error/nil-receiver paths; `Append*` with sufficient caller capacity |
| Exactly 1 | `StringFixed` for every `uint8` places value; each `Decimal.MarshalText`, `Decimal.MarshalJSON`, and `Decimal.MarshalBinary` result slice |
| Normally 1 | Uncached multi-byte `String` results; the Go runtime may serve one-byte strings from its static table |
| Exactly 2 in the gated uncached multi-byte case | `Value`: canonical string plus boxing into `driver.Value` |
| Exactly 0 when enabled and hit | `String` for cache-window Decimals; `Value` for `Decimal`, `StrictSQLDecimal`, and valid `NullDecimal`/`StrictNullDecimal` cache-window values |

The string/`driver.Value` cache is absent by default. Building with
`zerodecimal_strcache` eagerly materializes 200,001 canonical strings and
boxed values for `-1000.00..+1000.00` at up to two decimal places. Hits avoid
result allocation and reduce formatting latency, but the cache adds startup
work, resident memory, heap objects, and GC roots; misses still pay a probe.
Enable it only when representative production measurements show a useful hit
rate. `zerodecimal_nostrcache` explicitly forces it off and wins if both cache
tags are present.

## Build tags and architecture checks

| Tag | Effect |
| --- | --- |
| `zerodecimal_prec9` | Sets `DefaultPrec` to 9 |
| `zerodecimal_prec12` | Sets `DefaultPrec` to 12 |
| `zerodecimal_strcache` | Enables the eager small-value cache |
| `zerodecimal_nostrcache` | Forces the cache off |

The two precision tags are mutually exclusive. They change the truncation cap
for legacy `Mul`, `Div`, and `Avg`; `MaxPrec`, strict parsing, exact APIs, and
directly rounded APIs remain at 19 places.

CI runs the full test suite—not compile checks alone—across all 12
precision/cache combinations. It also executes the Linux/386 suite. Locally,
`make test-matrix` runs the tag matrix and compiles/vets Linux `386` and `arm`
test binaries.

## Concurrency, constants, and ownership

`Decimal` is a value type. Public arithmetic and formatting methods do not
mutate their operands, so independent copies can be used concurrently.
Pointer-receiver Unmarshal and Scan methods mutate their destination and need
the same synchronization as any other shared Go variable. Caller-owned
destination slices passed to `Append*` must likewise not be mutated
concurrently.

The exported `Zero` and `One` variables remain only as v1 compatibility
shims. Exported Go variables are mutable and can be reassigned or raced by an
application. They are deprecated: use `Decimal{}` for zero and
`NewFromInt(1)` for one. Package internals do not read either variable.

## Production integration checklist

- Choose `MulExact`/`DivExact`/`AvgExact` when loss is forbidden, or
  `MulRound`/`DivRound`/`AvgRound` with an explicitly reviewed scale and
  rounding mode. Do not rely on legacy truncation followed by a second round.
- Use `StrictSQLDecimal` for required exact database values and
  `StrictNullDecimal` for nullable exact values. Use legacy `Decimal` or
  `NullDecimal` Scan only when float64 provenance is intentional.
- Model required JSON values as `Decimal`, nullable values as `NullDecimal` or
  `StrictNullDecimal`, and treat `ErrJSONNull` as a schema violation.
- Match sentinels with `errors.Is`, including errors wrapped by
  `database/sql`. Never ignore Unmarshal or Scan errors on reused objects.
- Pin and test the exact build-tag set used by production. Precision tags
  change legacy arithmetic results; the cache tag changes memory, startup,
  allocation, and latency behavior.
- Treat `Decimal` as an immutable value after publication. Synchronize
  pointer-receiver writes and do not assign to deprecated `Zero` or `One`.
- Benchmark representative signs, scales, cache-hit distribution, error
  paths, chained operations, concurrency, startup, and GC behavior on target
  hardware. The repository's microbenchmarks are not a capacity plan.
- Run differential tests or a second implementation for the firm's critical
  calculation paths, then stage and soak the pinned build before rollout.

## Verification and benchmarks

The default suite includes deterministic differential checks against
shopspring/decimal and quagmt/udecimal, fixed-width primitive oracles,
allocation gates, codec/SQL integration tests, architecture layout guards,
and build-tagged fuzz targets covering arithmetic, parsing, codecs, SQL,
nil-receiver behavior, and invariants. Run:

```sh
make test
make test-matrix
make fuzz-all 20
```

The comparative benchmark suite is a separate module under
[benchmarks/](benchmarks/). Its committed results describe the recorded
hardware, inputs, build configuration, and semantics; they do not establish
application throughput or tail latency. Re-run the suite and add an
application-level benchmark before making deployment claims.

## Limitations

- The domain is bounded to a 128-bit coefficient and 19 fractional places;
  there is no arbitrary-precision fallback.
- Negative `places` values are unsupported because places is `uint8`.
- There is no runtime division-precision global. `DefaultPrec` is selected at
  build time, while direct rounding chooses scale per operation.
- `Pow`, `Sqrt`, and transcendental functions are not provided.
- Parsing deliberately rejects leading/trailing decimal points and other
  relaxed forms accepted by some decimal packages.

## Acknowledgements

The fixed-width arithmetic, reciprocal division, parsing, and formatting
paths are implemented in this repository. `dbox.go` is ported from Go's
BSD-3-Clause implementation of Junekey Jeon's Dragonbox algorithm; the notice
and attribution are in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
shopspring/decimal and quagmt/udecimal are used as correctness oracles. The
benchmark harness also measures the libraries listed in
[benchmarks/README.md](benchmarks/README.md).

## License

[MIT](LICENSE). Third-party notices are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
