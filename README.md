# zerodecimal

Zero-allocation, panic-free, fixed-point decimals for latency-critical Go.

## Why another decimal library

- **Strictly zero heap allocations.** Parsing, arithmetic, comparison,
  rounding, conversions, and every Unmarshal/Scan path perform exactly zero
  heap allocations — success and error paths alike — enforced by
  `testing.AllocsPerRun` gates in the default test suite
  ([alloc_test.go](alloc_test.go)).
- **Faster than every Go decimal library we could find.** The committed
  benchstat comparisons show a −35% time geomean against quagmt/udecimal
  (the previous fastest) and −90% against shopspring/decimal
  ([benchmarks/bench-vs-\*.txt](benchmarks/)).
- **Bit-exact.** Every operation is differentially checked against
  shopspring/decimal's unbounded arithmetic — including an *iff* proof for
  every returned overflow — deterministically in the default suite
  ([crosscheck_test.go](crosscheck_test.go)) and by 23 fuzz targets
  ([fuzz_test.go](fuzz_test.go)).
- **Panic-free.** Fallible operations return zero-allocation sentinel errors
  ([errors.go](errors.go)) and the fuzz suite requires every target to be
  total — no input, including garbage binary payloads, may panic the library.

## Install

```sh
go get github.com/AlexandrosKyriakakis/zero-decimal
```

```go
import "github.com/AlexandrosKyriakakis/zero-decimal" // package zerodecimal
```

Requires Go 1.26+. The library has zero runtime dependencies.

```go
price, err := zerodecimal.NewFromString("99.99")
if err != nil {
    return err
}
qty := zerodecimal.NewFromInt(3)

total, err := price.Mul(qty)
if err != nil {
    return err
}
fmt.Println(total)                // 299.97
fmt.Println(total.StringFixed(4)) // 299.9700
```

Runnable examples for parsing, arithmetic, rounding, JSON, and SQL live in
[example_test.go](example_test.go).

## Design

```go
type Decimal struct {
    coef u128  // |value| · 10^prec, 0 ≤ coef < 2^128
    neg  bool
    prec uint8 // fractional digits, 0..19
}
```

A Decimal is a 24-byte pointer-free value: copy it freely, compare it cheaply,
pack it densely. The domain is |value| < 2^128 / 10^prec — up to 39
significant digits with up to 19 fractional. There is **no `big.Int` anywhere
in the package**: every operation runs on fixed-width 128/256-bit integer
math, so nothing can escape to the heap and out-of-domain results return
`ErrOverflow` instead of degrading into arbitrary-precision slowness. The
zero value is the canonical decimal zero, ready to use; no operation produces
a negative zero.

**Reciprocal division is the headline optimization.** Decimal rescaling,
rounding, formatting, and division all reduce to dividing by powers of ten,
and zerodecimal never asks the hardware divider to do it: 64-bit dividends
use precomputed Granlund–Montgomery–Warren multiply-high magics, and
128/256-bit dividends chain Möller–Granlund 2-by-1 steps off a precomputed
reciprocal table ([div10.go](div10.go), tables generated and re-proven
against `bits.Div64` and `big.Int` in [tables_test.go](tables_test.go)).
A multiply-high plus a shift replaces an 18-cycle `DIV` — and for 128-bit
dividends, two *dependent* `DIV`s — which is where most of the headroom over
udecimal comes from.

`Div` uses **adaptive precision**: the result is the exact quotient truncated
at the largest precision ≤ `DefaultPrec` (19 by default) whose coefficient
still fits 128 bits, so huge quotients degrade precision gracefully and
`ErrOverflow` is reserved for integer quotients that genuinely exceed 2^128.

Because `==` compares representations, an arithmetic result of `1.50` differs
from a parsed `1.5` under `==`; use `Equal` or `Cmp` for numeric comparison.
Parsing trims trailing fractional zeros; arithmetic never does (it would tax
the hot path); formatting trims at output.

## Error model

All sentinels live in [errors.go](errors.go), are returned bare (never
wrapped, except `Scan`'s unsupported-type message), and match with
`errors.Is`. The constructors and arithmetic operations have panicking twins
for call sites with proven bounds; rows marked `—` below have none.

| Operation | Possible sentinels | Panicking twin |
| --- | --- | --- |
| `New` | `ErrOverflow`, `ErrPrecOutOfRange` | `MustNew` |
| `NewFromString`, `ParseBytes` | `ErrEmptyString`, `ErrMaxStrLen`, `ErrInvalidFormat`, `ErrOverflow`, `ErrPrecOutOfRange` | `RequireFromString` |
| `NewFromStringTrunc`, `ParseBytesTrunc` | `ErrEmptyString`, `ErrMaxStrLen`, `ErrInvalidFormat`, `ErrOverflow` | — |
| `NewFromFloat`, `NewFromFloat32` | `ErrInvalidFloat`, `ErrOverflow`, `ErrPrecOutOfRange` | `RequireFromFloat` |
| `NewFromHiLo` | `ErrPrecOutOfRange` | — |
| `Add`, `Sub`, `Mul` | `ErrOverflow` | `MustAdd`, `MustSub`, `MustMul` |
| `Div` | `ErrDivideByZero`, `ErrOverflow` | `MustDiv` |
| `QuoRem`, `Mod` | `ErrDivideByZero`, `ErrOverflow` | `MustQuoRem`, `MustMod` |
| `Sum`, `Avg` | `ErrOverflow` | `MustSum`, `MustAvg` |
| `IntPart` | `ErrIntPartOverflow` | — |
| `UnmarshalText`, `UnmarshalJSON` | the parse sentinels | — |
| `UnmarshalBinary` | `ErrInvalidBinaryData` | — |
| `Scan` | the parse sentinels, `ErrInvalidFloat`, `ErrScanNil`, `ErrScanType` | — |

Everything else is infallible: `NewFromInt`/`NewFromInt32`/`NewFromUint64`,
`Neg`, `Abs`, `Sign`, the `Is*` predicates, `Cmp` and the comparison family,
`Min`/`Max`, the entire rounding family, `Prec`, `ToHiLo`, `String`,
`StringFixed`, `AppendFixed`, and `InexactFloat64`. `AppendText`,
`AppendBinary`, the `Marshal*` methods, and `Value` return an error only to
satisfy their interfaces — it is always nil.

## Allocation guarantees

Exactly what [alloc_test.go](alloc_test.go) enforces with
`testing.AllocsPerRun` on every `make test` run, across six value shapes
(small integers, typical prices, full 19-digit precision, extreme precision
mismatch, near-2^128 coefficients, negatives), on success *and* error paths:

| Allocations | Operations | Gate |
| --- | --- | --- |
| **exactly 0** | `NewFromString`, `ParseBytes`, `Add`, `Sub`, `Mul`, `Div`, `QuoRem`, `Mod`, `Cmp`, `Equal`, `Neg`, `Abs`, `Sign`, `Round`, `RoundBank`, `RoundUp`, `RoundDown`, `RoundCeil`, `RoundFloor`, `Truncate`, `Floor`, `Ceil`, `IntPart`, `InexactFloat64`, `NewFromFloat`, `AppendText`, `AppendFixed`, `AppendBinary`, `Min`, `Max`, `MustAdd`, `UnmarshalText`, `UnmarshalJSON`, `UnmarshalBinary`, `Scan` (string and `[]byte`) | `TestAllocsZero` |
| **exactly 1** | `String` (outside the cache window), `StringFixed` — the returned string itself | `TestAllocsOne` |
| **exactly 1** | `MarshalText`, `MarshalJSON`, `MarshalBinary` — the returned slice, sized exactly | `TestAllocsCodecMarshal` |
| **exactly 0** | `String` and `Value` on values inside the small-value cache window (−1000.00..+1000.00, ≤ 2 places) | `TestAllocsStringCached`, `TestAllocsSQLValueCached` |
| **exactly 2** | `Value` outside the cache window — the canonical string plus boxing it into `driver.Value` | `TestAllocsSQLValueUncached` |

The counts are asserted as *exact*, not upper bounds, so a regression in
either direction fails the suite. Since the steady state allocates nothing,
zerodecimal generates no GC pressure regardless of `GOGC`.

## Parsing rules

Grammar: `['+'|'-'] digits ['.' digits] [('e'|'E') ['+'|'-'] digits]`, ASCII
only, at most 200 bytes.

Accepted:

- plain literals: `"123"`, `"-4.20"`, `"+1"`, redundant zeros (`"00012.3400"` → `12.34`)
- scientific notation: `"1.23e4"` → `12300`, `"1E-7"` → `0.0000001` (required for JSON float interop)
- up to 39 significant digits: `"340282366920938463463374607431768211455"` (= 2^128−1) parses; one more unit is `ErrOverflow`

Rejected:

- `""` → `ErrEmptyString`; input over 200 bytes → `ErrMaxStrLen`
- `"1."` and `".1"` → `ErrInvalidFormat`: **both sides of the dot need a digit** (deliberately stricter than shopspring), as do `"."`, `"-"`, `"1..2"`, `"1e"`, `"1e+"`
- whitespace, underscores, non-ASCII digits, `"NaN"`, `"Inf"` → `ErrInvalidFormat`
- more than 19 fractional digits → `ErrPrecOutOfRange` (strict variants)

The `Trunc` variants (`NewFromStringTrunc`, `ParseBytesTrunc`) replace
`ErrPrecOutOfRange` with truncation toward zero at 19 fractional digits
(possibly to exactly zero) and accept any mantissa within the 200-byte input
cap (`ErrMaxStrLen` still applies) whenever the truncated value is
representable; grammar violations and genuinely unrepresentable values still
error. Results are always canonical: trailing
fractional zeros are trimmed (`"1.500"` parses identically to `"1.5"`) and
parsing never allocates — not even on failure.

## Rounding modes

`places` counts fractional digits; `places ≥ d.Prec()` returns `d` unchanged.
The whole family is infallible — the increment can never overflow — and
rounding a negative value to zero yields the canonical unsigned zero.

| Method | Mode | `2.5` → | `3.5` → | `-2.5` → |
| --- | --- | --- | --- | --- |
| `Round(0)` | half away from zero (shopspring `Round`) | `3` | `4` | `-3` |
| `RoundBank(0)` | half to even (banker's) | `2` | `4` | `-2` |
| `RoundUp(0)` | away from zero | `3` | `4` | `-3` |
| `RoundDown(0)` / `Truncate(0)` | toward zero | `2` | `3` | `-2` |
| `RoundCeil(0)` | toward +∞ | `3` | `4` | `-2` |
| `RoundFloor(0)` | toward −∞ | `2` | `3` | `-3` |

`Floor()` and `Ceil()` are `RoundFloor(0)` and `RoundCeil(0)`. Every mode is
pinned tie-by-tie against its shopspring equivalent in
[crosscheck_test.go](crosscheck_test.go) and fuzzed in
[fuzz_test.go](fuzz_test.go).

## Benchmarks

The comparative suite lives in [benchmarks/](benchmarks/) — a **separate Go
module**, so the competitor dependencies never touch the library's `go.mod`.
Full committed results: [bench-vs-udecimal.txt](benchmarks/bench-vs-udecimal.txt),
[bench-vs-shopspring.txt](benchmarks/bench-vs-shopspring.txt),
[bench-vs-alpacadecimal.txt](benchmarks/bench-vs-alpacadecimal.txt),
[bench-vs-ericlagergren.txt](benchmarks/bench-vs-ericlagergren.txt);
methodology and the deliberate semantic asymmetries are documented in
[benchmarks/README.md](benchmarks/README.md).

Against quagmt/udecimal — the fastest existing Go decimal — zerodecimal is
faster on 89 of the 90 op × shape rows and statistically tied on the
remaining one (`MarshalJSON/small_int`):

```
goos: darwin
goarch: arm64
cpu: Apple M1 Pro
                          │   udecimal   │             zerodecimal             │
                          │    sec/op    │   sec/op     vs base                │
Add/typical_price-10         4.673n ± 0%   2.375n ± 0%  -49.18% (p=0.000 n=10)
Mul/typical_price-10         6.234n ± 0%   2.350n ± 0%  -62.29% (p=0.000 n=10)
Div/typical_price-10         12.93n ± 1%   11.70n ± 1%   -9.51% (p=0.000 n=10)
QuoRem/typical_price-10     13.455n ± 2%   3.155n ± 0%  -76.56% (p=0.000 n=10)
Cmp/typical_price-10         5.291n ± 0%   3.108n ± 2%  -41.26% (p=0.000 n=10)
Parse/typical_price-10       14.32n ± 0%   12.75n ± 0%  -10.96% (p=0.000 n=10)
String/typical_price-10      32.46n ± 1%   25.89n ± 1%  -20.24% (p=0.000 n=10)
geomean                      15.50n        10.07n       -35.03%
```

Against shopspring/decimal, the de-facto standard:

```
                          │  shopspring   │             zerodecimal             │
                          │    sec/op     │   sec/op     vs base                │
Add/typical_price-10        39.975n ± 2%   2.375n ± 0%  -94.06% (p=0.000 n=10)
Mul/typical_price-10        40.375n ± 1%   2.350n ± 0%  -94.18% (p=0.000 n=10)
Div/typical_price-10        210.35n ± 1%   11.70n ± 1%  -94.44% (p=0.000 n=10)
RoundBank/typical_price-10 353.250n ± 2%   4.677n ± 1%  -98.68% (p=0.000 n=10)
Parse/typical_price-10       75.92n ± 2%   12.75n ± 0%  -83.21% (p=0.000 n=10)
String/typical_price-10     106.50n ± 2%   25.89n ± 1%  -75.69% (p=0.000 n=10)
geomean                      93.42n        10.07n       -89.59%
```

Allocations are 0 on every row where any competitor manages 0, and 0 on many
where they do not (e.g. udecimal's `Mul/large` allocates 160 B/op across 4
allocations; zerodecimal allocates nothing).

### Known trade-offs

Allocation floors accepted by design (from
[benchmarks/README.md](benchmarks/README.md)):

- **`String`: 1 alloc** outside the cache window — a string-returning API
  must allocate its immutable result; the rendering itself is a stack buffer.
- **`MarshalText`/`MarshalJSON`/`MarshalBinary`: 1 alloc** — callers own and
  may mutate marshal results, so sharing cached bytes is off the table; the
  slice is sized exactly.
- **`Value`: 2 allocs** outside the cache window — the canonical string plus
  boxing into the `driver.Value` interface; there is no cheaper portable shape.

## PGO

PGO attaches to binaries, not libraries — so zerodecimal cannot ship it, but
your build can claim it. The hot paths are written PGO-friendly: no
interfaces or indirect calls anywhere (devirtualization is never needed), and
the slow arms (`addSlow`, `mulSlow`, the multi-limb division bodies) are
deliberately outlined into small functions that profile-driven inlining can
promote straight into *your* hot loops past the default inlining budget.

1. Collect a CPU profile from production or a representative load:
   `pprof.StartCPUProfile` / `curl .../debug/pprof/profile > default.pgo`.
2. Drop it at your main package root as `default.pgo` (picked up by
   `go build` automatically, i.e. `-pgo=auto`) or pass `-pgo=/path/to.pprof`.
3. Rebuild and ship.

The committed [benchmarks/bench-pgo.txt](benchmarks/bench-pgo.txt) shows what
the benchmark binary itself gains when rebuilt against its own profile
(`make bench-pgo`): a −7.5% time geomean, with the arithmetic core improving
the most because its outlined slow arms inline into the measured call sites.
Three op families honestly regress where PGO's layout choices cost a little:
every `Cmp` shape (+7% to +10%), the short `Parse` inputs (+2% to +5%), and
`NewFromFloat/typical_price` (+2.5%) — the excerpt shows the worst such row:

```
                          │   default   │                 pgo                 │
                          │   sec/op    │   sec/op     vs base                │
Add/typical_price-10        2.375n ± 0%   2.022n ± 0%  -14.88% (p=0.000 n=10)
Sub/typical_price-10        3.743n ± 0%   3.119n ± 1%  -16.68% (p=0.000 n=10)
Mul/large-10                4.429n ± 1%   3.520n ± 0%  -20.52% (p=0.000 n=10)
Div/typical_price-10        11.70n ± 1%   10.24n ± 0%  -12.48% (p=0.000 n=10)
QuoRem/typical_price-10     3.155n ± 0%   2.651n ± 1%  -15.95% (p=0.000 n=10)
RoundBank/typical_price-10  4.677n ± 1%   3.730n ± 1%  -20.26% (p=0.000 n=10)
Cmp/typical_price-10        3.108n ± 2%   3.417n ± 0%   +9.96% (p=0.000 n=10)
geomean                     10.07n        9.313n        -7.50%
```

On amd64 deployments also consider `GOAMD64=v3`: the BMI2/ADX instructions
materially speed the `bits.Mul64`/`bits.Add64` carry chains that dominate the
primitives (arm64 needs no flag).

## Build tags

| Tag | Effect |
| --- | --- |
| `zerodecimal_prec9` | lowers the compile-time `DefaultPrec` to 9 fractional digits (nanos), trading fractional resolution for integer range in division results |
| `zerodecimal_prec12` | lowers `DefaultPrec` to 12, matching alpacadecimal's fixed scale |
| `zerodecimal_nostrcache` | compiles out the ~8 MB small-value string/`driver.Value` cache (−1000.00..+1000.00) built at init |

`DefaultPrec` is a compile-time constant by design — never a mutable global —
so precision checks fold into immediate compares.

The full test suites assume `DefaultPrec` = 19. Compile + `go vet` is the
supported verification level for the `zerodecimal_prec9` and
`zerodecimal_prec12` configurations in v1.

## How correctness is enforced

- **Deterministic cross-check in the default suite**
  ([crosscheck_test.go](crosscheck_test.go)): every arithmetic, comparison,
  rounding, parsing, and formatting result is checked against
  shopspring/decimal's unbounded big.Int arithmetic over an exhaustive
  boundary-value pair sweep plus 30,000 fixed-seed boundary-biased random
  pairs. The overflow oracle is *iff*: every `ErrOverflow` must be proven
  exact (the true coefficient really is ≥ 2^128) and every fitting result
  must be returned — a spurious error fails as loudly as a wrong value.
- **23 differential fuzz targets** ([fuzz_test.go](fuzz_test.go), `make
  fuzz-all`): parse round trips and raw-string parsing, Add/Sub/Mul/Div/
  QuoRem/Mod/Cmp with the same iff overflow proofs, all seven rounding modes
  pinned to their shopspring equivalents, StringFixed, JSON/binary/SQL round
  trips, garbage binary input (which must never panic), float conversion, and
  a structural-invariant target. quagmt/udecimal serves as a second,
  bit-compatible oracle for Add/Sub/Mul.
- **6.5+ million fixed-seed primitive cases**: the u128/u256 primitives and
  every reciprocal-division path are verified against `bits.Div64` and
  `big.Int` at carry, limb, power-of-ten, and exact-overflow boundaries plus
  millions of shaped random cases per run ([u128_test.go](u128_test.go),
  [u256_test.go](u256_test.go), [div10_test.go](div10_test.go) — the loop
  counts sum past 6.5 million), and the generated magic-constant tables are
  recomputed from their definitions in [tables_test.go](tables_test.go).
- **Codegen gates**: the inlining shape of the hot paths (what must inline,
  what must stay outlined, cost ceilings against compiler drift) is asserted
  from the compiler's own `-m=2` report in the default suite.

## Limitations vs shopspring/decimal

- **Bounded domain.** |value| < 2^128 / 10^prec — at most 39 significant
  digits and 19 fractional digits. There is no arbitrary-precision fallback;
  out-of-domain results return `ErrOverflow`. shopspring is unbounded.
- **`places` is `uint8`.** Negative places (rounding at tens/hundreds
  positions, shopspring's `Round(-2)`) are unsupported by design — this is
  what keeps the entire rounding family infallible.
- **Division precision is compile-time.** `Div` truncates at adaptive
  precision up to `DefaultPrec` (19, or 9/12 via build tags); there is no
  runtime `DivisionPrecision` knob and no `DivRound`.
- **No `Pow`, `Sqrt`, or transcendental functions yet.**
- **Stricter parsing.** `"1."` and `".1"` are rejected; shopspring accepts
  both.
- **No exotic float forms.** `NewFromFloat` rejects NaN/±Inf with
  `ErrInvalidFloat` rather than panicking, and converts via the shortest
  decimal representation (like shopspring) — floats outside the domain
  error instead of rounding silently.

## License

[MIT](LICENSE)
