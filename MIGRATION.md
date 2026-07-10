# Production migration notes

Review these behavior and deployment changes before replacing an earlier v1
build. They are intentionally fail-closed where silent loss or stale values
were possible.

## Arithmetic

`Mul`, `Div`, and `Avg` remain compatibility operations. They truncate toward
zero at `DefaultPrec` (with adaptive precision for division and averages) and
do not report discarded digits.

For new calculation paths:

- use `MulExact`, `DivExact`, or `AvgExact` when any loss must be an error;
- use `MulRound`, `DivRound`, or `AvgRound` when the output scale and rounding
  policy are known;
- do not round a legacy truncated result and assume it is equivalent to direct
  rounding.

`Sum` and averages now use a wide signed accumulator. Their result no longer
depends on operand order or fails merely because a cancellation whose final
coefficient fits at the greatest input precision passed through a 128-bit
intermediate. `Sum` still checks and preserves that precision for nonzero
results (zero remains canonical) and can return `ErrOverflow` even when
lowering precision would express the same integer.

`QuoRem` and `Mod` now recognize an aligned divisor wider than 128 bits. When
that divisor exceeds the numerator they return the exact `q = 0, r = d`
result instead of `ErrOverflow`.

## Parsing

Strict parsing now range-checks the canonical value after folding the exponent
and removing trailing fractional zeros. Representable values such as
`maxCoefficient.0`, `maxCoefficient0e-1`, and
`1.00000000000000000000` are accepted and canonicalized. The grammar remains
strict; this is an acceptance expansion, not relaxed syntax.

Strict general parsing also validates the complete grammar before reporting a
range failure. Malformed input whose prefix is already wider than the 128-bit
coefficient can therefore return the more specific `ErrInvalidFormat` where an
older release returned `ErrOverflow`.

There is a second range-sentinel change for grammatically valid input. When a
raw mantissa is wider than 128 bits but canonicalization reduces its
coefficient into range while leaving more than `MaxPrec` fractional places,
strict parsing now returns `ErrPrecOutOfRange` instead of the earlier
`ErrOverflow`. For example,
`100000000000000000000.00000000000000000000e-40` denotes `1e-20`: its
canonical coefficient fits, but its precision does not. Update callers that
branch on exact sentinels; `errors.Is` remains the supported matching
mechanism.

## JSON

Quoted JSON strings are semantically unescaped before decimal parsing.
`"1\u002e5"` now decodes as `1.5`; malformed escapes and surrogate pairs are
rejected.

Direct `UnmarshalJSON` calls now enforce JSON number syntax on bare input:
leading-plus and integer-leading-zero spellings such as `+1`, `01`, and `-00`
return `ErrInvalidFormat`. Their quoted decimal-string forms remain accepted.
Calls routed through `encoding/json` already rejected those invalid bare tokens.

`Decimal.UnmarshalJSON(null)` now returns `ErrJSONNull` and leaves the
receiver unchanged. It no longer succeeds as a no-op. Change nullable JSON
fields to `NullDecimal` or `StrictNullDecimal`. Keep required fields as
`Decimal`, not `*Decimal`, if null must reach this policy: `encoding/json`
handles pointer nulls itself.

## SQL

`Decimal.Scan` still accepts float64 for v1 compatibility. New required exact
database fields should use `StrictSQLDecimal`, which rejects float32/float64
with `ErrScanFloat` and SQL NULL with `ErrScanNil`.

This float rejection is a Go-side source-type policy, not proof of database
provenance. A driver that returns a floating column as decimal text is
indistinguishable from an exact text value. The end-to-end guarantee therefore
requires `NUMERIC`/`DECIMAL` columns plus integration tests against the actual
driver, protocol, casts, and schema. Also reject nil `*StrictSQLDecimal` bind
parameters in application validation: `database/sql` converts such a pointer
to SQL NULL without calling `Value`.

Use `StrictNullDecimal` for nullable exact values. SQL NULL clears it; every
non-null source follows `StrictSQLDecimal` policy; and every failed Scan clears
stale nullable state. `NullDecimal` remains available for compatibility and
inherits the legacy float64-accepting `Decimal.Scan` policy.

## Nil receivers

Fallible Unmarshal and Scan methods now return the bare `ErrNilReceiver`
sentinel when called on a nil receiver. This error takes precedence over
input-specific validation and replaces prior nil-pointer panics.

## String cache and layout

The eager string/`driver.Value` cache is off by default. This changes an
untagged upgrade from the earlier cache-on default: an eligible multi-byte
`String` call moves from zero allocations to normally one, and the gated
ordinary `Value` case moves from zero to exactly two. Latency can rise by an
order of magnitude on those paths; an illustrative Apple M1 / Go 1.26.5 review
run measured `String` at about 2.2 ns to 18.3 ns and `Value` at about 2.3 ns to
33 ns. These are not portable capacity figures, so remeasure the application's
actual value distribution. Enable `zerodecimal_strcache` only after weighing
the recovered hit latency against its startup, resident-memory, heap-object,
and GC-root cost. `zerodecimal_nostrcache` overrides the enable tag.

`Decimal` is 24 bytes on max-align-8 targets and 20 bytes on gc's max-align-4
32-bit targets. Do not bake a universal 24-byte size into unsafe interop.

The module declares the Go 1.26 language floor; a `go 1.26` directive cannot
enforce a patch release. Earlier Go 1.26 patch releases contain
standard-library vulnerabilities fixed in 1.26.5, so production build and
runtime images must use Go 1.26.5 or a newer patched toolchain.

## Deprecated variables

`Zero` and `One` remain exported for source compatibility but are mutable Go
variables and are deprecated. Replace reads with `Decimal{}` and
`NewFromInt(1)`, and remove all assignments to them. Package internals do not
depend on either variable.

## Deployment gate

Before rollout:

1. Audit every `Mul`, `Div`, and `Avg` call for an explicit loss policy.
2. Audit JSON and SQL fields for required versus nullable semantics; use the
   strict SQL wrappers unless float64 provenance is deliberate.
3. Match errors with `errors.Is` and stop on decode/scan failures.
4. Pin the precision and cache build tags and run `make test-matrix`.
5. Benchmark the pinned configuration on target hardware with production-like
   scales, signs, cache distribution, concurrency, and GC settings.
6. Differentially verify critical formulas and perform a staged soak.
