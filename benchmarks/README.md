# Comparative benchmarks

This module benchmarks [zerodecimal](..) against the other Go decimal
libraries on one shared operation × shape matrix:

| key      | library                                                                  |
| -------- | ------------------------------------------------------------------------ |
| `zd`     | github.com/AlexandrosKyriakakis/zero-decimal                              |
| `udec`   | github.com/quagmt/udecimal                                                |
| `alpaca` | github.com/alpacahq/alpacadecimal                                         |
| `ss`     | github.com/shopspring/decimal                                             |
| `eric`   | github.com/ericlagergren/decimal                                          |

It is a separate Go module (`replace`d onto the parent directory) so the
competitor dependencies never touch the library's own `go.mod`.

## Matrix

Sub-benchmarks are named `Benchmark<Op>/<lib>/<shape>`. The shapes are operand
pairs spanning the representation regimes that matter:

| shape           | a                                          | b                       |
| --------------- | ------------------------------------------ | ----------------------- |
| `small_int`     | `5`                                        | `7`                     |
| `typical_price` | `1234.5678`                                | `8765.4321`             |
| `max_prec`      | `0.1234567890123456789`                    | `0.9876543210987654321` |
| `large`         | `12345678901234567890.123456789`           | `987654321.987654321`   |
| `near_max`      | `17014118346046923173.1687303715884105727` | `1.000000001`           |

`near_max` carries the coefficient 2^127−1 at precision 19 — the widest value
every u128-based library still represents. Ops: Parse, String, Add, Sub, Mul,
Div, QuoRem, Cmp, RoundBank, Truncate, MarshalJSON, UnmarshalJSON,
MarshalBinary, UnmarshalBinary, AppendText, SQLValue, SQLScan, NewFromFloat.
Single-operand ops use column `a`.

Every leaf benchmark reports allocations, runs under `b.Loop`, reads
pre-parsed package-level inputs, and writes results — errors included — into
package-level sinks.

## Skips

Where a library has no genuine equivalent for an op it is skipped, never
approximated:

| op                              | skipped | why                                                                                      |
| ------------------------------- | ------- | ---------------------------------------------------------------------------------------- |
| MarshalJSON                     | `eric`  | `*decimal.Big` has no `MarshalJSON`; `MarshalText` is a different operation               |
| MarshalBinary / UnmarshalBinary | `alpaca` | its binary codec converts to shopspring and delegates, so the `ss` rows already cover it |
| MarshalBinary / UnmarshalBinary | `eric`  | `*decimal.Big` has no binary codec                                                        |
| AppendText                      | `alpaca`, `ss`, `eric` | no append-style text API                                                    |

## Semantic asymmetries (deliberate)

These are part of the story the numbers tell, not benchmark bugs:

- **alpaca fallback**: `large` and `near_max` exceed alpacadecimal's optimized
  int64 fixed-point range, so those rows measure its shopspring fallback path.
- **zd error returns**: zerodecimal's fallible ops return `(Decimal, error)`
  and the error is sunk; on these shapes every op succeeds (`near_max` Mul
  truncated to 19 fractional digits still fits 2^128), but if an op overflowed
  the benchmark would be measuring the error path.
- **QuoRem mapping**: each library's closest exact-truncated-quotient API is
  used — zd `QuoRem(e)`, udec `QuoRem(e)`, alpaca/ss `QuoRem(e, 0)`, eric
  `QuoRem(x, y, r)`.
- **eric context and mutability**: every `*decimal.Big` uses the context from
  udecimal's benchmark harness (precision 19, half-even). Results go through
  explicit receiver Bigs; RoundBank and Truncate are `Copy` + `Quantize` on a
  receiver with the matching rounding mode (half-even and to-zero), so the
  copy is part of the measured cost — that is what the API requires. On
  `large` and `near_max` the quantized coefficient exceeds the 19-digit
  context and `Quantize` takes eric's invalid-operation (NaN) path.
- **eric NewFromFloat**: `SetFloat64` performs an exact binary-to-decimal
  conversion, unlike the shortest-decimal semantics of the other four — fewer
  digits in, sometimes far more digits stored.
- **Div precision**: zd and udec produce up to 19 fractional digits, ss and
  alpaca default to `DivisionPrecision = 16`, eric rounds to 19 significant
  digits. The work compared is each library's own contract.
- **SQL caches**: zd (±1000.00, two decimal places) and alpaca have
  small-value caches, so `small_int` SQLValue/String rows measure cache hits.

## Known trade-offs

Allocation floors that are accepted by design rather than optimized away:

- **String: 1 alloc/op** outside the small-value cache window. A
  string-returning API must allocate the immutable result; the rendering
  itself happens in a stack scratch buffer and the one allocation is exactly
  the string. Inside the ±1000.00 cache window it is 0 allocs.
- **MarshalText / MarshalJSON / MarshalBinary: 1 alloc/op** — the returned
  byte slice the caller owns (callers may mutate marshal results, so sharing
  cached bytes is off the table). The slice is sized exactly: MarshalJSON of
  `5` allocates 3 bytes, not a fixed 48-byte buffer.
- **SQLValue: 2 allocs/op** outside the cache window: the canonical string
  plus boxing its header into the `driver.Value` interface
  (`runtime.convTstring`); the bytes are shared, not copied. There is no
  cheaper portable shape — a `driver.Value` must carry a concrete boxed
  type. Inside the cache window the pre-boxed value makes it 0 allocs.

## Running

```sh
make bench          # quick full sweep, count=1
make bench-zd       # per-library runs, count=10, lib segment stripped
make bench-udec
make bench-alpaca
make bench-ss
make bench-eric
make compare        # benchstat per-pair reports into bench-vs-*.txt
make pgo            # profile the zd benchmarks, re-run with -pgo, benchstat into bench-pgo.txt
```

`compare` and `pgo` need `benchstat`
(`go install golang.org/x/perf/cmd/benchstat@latest`).
The per-library `bench-*.txt` files, `bench-zd-pgo.txt`, and `zd.pprof` are
scratch output (gitignored); the `bench-vs-*.txt` comparisons and
`bench-pgo.txt` are the published artifacts.

`pgo` rebuilds the benchmark binary with the profile it just collected, so the
published delta is what a consumer gets by feeding a production profile to
`go build -pgo`: profile-driven inlining promotes zerodecimal's outlined slow
paths into their hot call sites past the default inlining budget.
