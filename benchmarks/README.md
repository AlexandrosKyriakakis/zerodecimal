# Comparative benchmarks

This module benchmarks [zerodecimal](..) against the other Go decimal
libraries on one shared operation × shape matrix:

> **Historical artifact notice:** the committed `bench-vs-*.txt`,
> `bench-pgo.txt`, and comparison charts were collected before the
> zerodecimal string cache became opt-in. Their cache-eligible zerodecimal
> `String`/`SQLValue` rows therefore reflect a cache-enabled binary. New
> untagged runs use the current production default (cache absent) and are not
> directly comparable on those rows. The committed artifacts and charts have
> intentionally not been regenerated as part of this methodology update.

| key      | library                                                                  |
| -------- | ------------------------------------------------------------------------ |
| `zd`     | github.com/AlexandrosKyriakakis/zerodecimal                              |
| `udec`   | github.com/quagmt/udecimal                                                |
| `alpaca` | github.com/alpacahq/alpacadecimal                                         |
| `ss`     | github.com/shopspring/decimal                                             |
| `eric`   | github.com/ericlagergren/decimal                                          |
| `dec`    | github.com/jokruger/dec128                                                |
| `gv`     | github.com/govalues/decimal                                               |

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
every u128-based library still represents. `govalues` is the exception: it
caps at 19 significant digits, so it cannot represent `large` (29 digits) or
`near_max` (39 digits) and its rows for those two shapes are absent (gated on
`gvOK` in the harness). Ops: Parse, String, Add, Sub, Mul,
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
  `QuoRem(x, y, r)`, dec `QuoRem(e)`.
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
- **SQL caches**: alpaca has its own small-value cache. Zerodecimal's cache is
  absent by default and enabled only with `zerodecimal_strcache`; the
  committed comparisons predate that default change and their `small_int`
  zerodecimal SQLValue/String rows measure cache hits. Fresh untagged runs
  measure cache misses instead.
- **dec NaN poisoning**: dec128's fallible ops (FromString, Add, Sub, Mul,
  Div, QuoRem, FromFloat64) return a NaN-poisoned `Dec128` instead of a
  `(Decimal, error)` pair (NaN + 1 = NaN). The benchmarks sink the result
  Decimal — there is no error to sink — so its error-path cost is not
  directly comparable to the libraries that construct and return errors.
- **dec Mul exactness**: dec128's `Mul` returns the exact product or NaN
  (overflow) — it never truncates or rounds to fit. On `max_prec`, `large`,
  and `near_max` the exact product needs more than 19 fractional digits or
  128 coefficient bits, so those rows measure the full 256-bit multiply plus
  the failed scale-reduction loop ending in NaN, not a representable product
  (the same way eric's RoundBank/Truncate rows measure its NaN path on
  `large`/`near_max`).
- **dec AppendText mapping**: dec128 has no `AppendText`, but `StringToBuf`
  is a genuine render-into-caller-buffer text API; it resets the buffer
  (`buf[:0]`) instead of appending. The harness's append buffer is empty, so
  the measured work is identical; the row is a buffer-reuse comparison, not
  an `encoding.TextAppender` contract match.
- **gv 19-digit cap**: govalues stores at most 19 significant digits, so the
  `large` and `near_max` operands do not fit and those rows are skipped
  entirely (not approximated) — `bench-vs-govalues.txt` lists those shapes with
  a zerodecimal column only, and the comparison geomean is over the three
  shapes both libraries can represent. It maps cleanly onto every op for those
  three shapes: `Quo` for Div, `Round` for RoundBank (half-even, the same
  mode), `Trunc` for Truncate, and the full `(Decimal, error)` codec/SQL
  surface. Where an exact result needs more than 19 digits it takes govalues's
  internal big-integer path rather than overflowing — `Add`/`Mul` on `max_prec`
  and every `Quo` run there (≈107/132/280 ns/op vs zerodecimal's single-digit
  ns) — but that path is allocation-free in steady state, so govalues stays 0
  B/op throughout, matching zerodecimal on allocations and differing only in
  time.

## Known trade-offs

Allocation floors that are accepted by design rather than optimized away:

- **String: 1 alloc/op for representative multi-byte output** in the default
  cache-off build. A string-returning API must normally allocate the immutable
  result; the rendering itself happens in a stack scratch buffer. Go serves
  one-byte strings from a runtime static table, so values such as `0` can be a
  zero-allocation exception even without the cache. With
  `zerodecimal_strcache`, values inside the ±1000.00 window cost 0 allocs.
- **MarshalText / MarshalJSON / MarshalBinary: 1 alloc/op** — the returned
  byte slice the caller owns (callers may mutate marshal results, so sharing
  cached bytes is off the table). The slice is sized exactly: MarshalJSON of
  `5` allocates 3 bytes, not a fixed 48-byte buffer.
- **SQLValue: at most 2 allocs/op** in the default cache-off build: for a
  representative multi-byte value, the canonical string plus boxing its
  header into the `driver.Value` interface
  (`runtime.convTstring`); the bytes are shared, not copied. There is no
  cheaper portable shape — a `driver.Value` must carry a concrete boxed
  type. With `zerodecimal_strcache`, an eligible pre-boxed value costs 0.

## Running

```sh
make bench          # quick full sweep, count=1
make bench-zd       # per-library runs, count=10, lib segment stripped
make bench-udec
make bench-alpaca
make bench-ss
make bench-eric
make bench-dec      # anchored -bench=/^dec$/ so it does not also match udec
make bench-gv       # govalues; only the three shapes it can represent
make compare        # benchstat per-pair reports into bench-vs-*.txt
make pgo            # profile the zd benchmarks, re-run with -pgo, benchstat into bench-pgo.txt
make chart          # render comparison-{light,dark}.svg from the committed bench-vs-*.txt + bench-pgo.txt geomeans
make production-smoke    # every production row once, default and strcache
make production-default  # production suite, current cache-off default
make production-strcache # same suite with the opt-in cache
make production-micro    # primitives and error paths only
make production-pipeline # composed monetary workflows only
make production-parallel          # RunParallel at 1,2,4,8 CPUs, cache off
make production-parallel-strcache # same RunParallel rows, cache on
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

## Production benchmark methodology

The production suite is zerodecimal-only and deliberately separate from the
competitor matrix. Names make the measurement boundary explicit:

- `BenchmarkProductionMicro*` measures one API family at a time: canonical,
  scientific, and rescue parsing; exact/direct-round multiplication and
  division; mixed-sign/mixed-scale and cancellation-heavy aggregates; the
  wide-scaled-divisor QuoRem path; escaped JSON and JSON-null rejection;
  `StrictSQLDecimal`; narrow and very wide `StringFixed`; cache-eligible versus
  ineligible String/Value; and representative sentinel error paths.
- `BenchmarkProductionPipelineTradeCapture` composes escaped JSON price
  ingestion, strict integer SQL ingestion, direct currency rounding, fee
  aggregation, and fixed-width output. Its result-string allocation is part of
  the measured workflow.
- `BenchmarkProductionPipelinePortfolioMark` directly rounds eight pre-parsed
  mixed-sign positions, then sums, averages, and formats the book.
- `BenchmarkProductionParallel*` uses `RunParallel` with immutable fixtures and
  goroutine-local sinks. It covers parse, direct Mul/Div rounding,
  cancellation-heavy Sum, strict SQL scanning, and cache-eligible String.

Every non-parse decimal fixture and every interface-valued Scan source is
built before timing. Sequential results and errors are written to package
sinks. Parallel workers retain local result/error sinks with
`runtime.KeepAlive`, avoiding a shared-sink race or false-sharing bottleneck.
Every row reports allocations. `TestProductionBenchmarkFixtures` verifies
that success rows succeed, error rows hit their intended sentinel, the wide
QuoRem result is exact, and both pipelines are valid.

### Reproducible collection

Use an otherwise idle target-class host, pin the repository revision, and
record at least:

```sh
git rev-parse HEAD
git status --short
go version
go env GOOS GOARCH GOAMD64 GOEXPERIMENT CGO_ENABLED
uname -a
env | grep -E '^(GOGC|GOMEMLIMIT|GOMAXPROCS)=' || true
# Linux: lscpu; macOS: sysctl -n machdep.cpu.brand_string
```

The benchmark output records GOOS, GOARCH, CPU, ns/op, bytes/op, and allocs/op.
The `-cpu` suffix records the selected GOMAXPROCS. For a stable single-thread
baseline and a separately visible cache experiment:

```sh
make production-default  PRODUCTION_BENCHTIME=2s PRODUCTION_COUNT=10 PRODUCTION_CPU=1 | tee /tmp/zd-production-default.txt
make production-strcache PRODUCTION_BENCHTIME=2s PRODUCTION_COUNT=10 PRODUCTION_CPU=1 | tee /tmp/zd-production-strcache.txt
benchstat default=/tmp/zd-production-default.txt strcache=/tmp/zd-production-strcache.txt
```

For scaling, choose CPU counts that exist on the deployment host rather than
blindly using the default list:

```sh
make production-parallel PRODUCTION_BENCHTIME=2s PRODUCTION_COUNT=10 PRODUCTION_PARALLEL_CPU=1,2,4,8 | tee /tmp/zd-production-parallel-default.txt
make production-parallel-strcache PRODUCTION_BENCHTIME=2s PRODUCTION_COUNT=10 PRODUCTION_PARALLEL_CPU=1,2,4,8 | tee /tmp/zd-production-parallel-strcache.txt
```

Run at least ten samples for benchstat, keep Go version/build tags/GOAMD64,
GOMAXPROCS, power mode, and background load fixed within a comparison, and
compare like-named rows only. Preserve raw output outside the repository; the
targets never overwrite committed comparison results or charts. Benchmark PGO
and non-PGO binaries as separate populations if production uses PGO. The
targets above use the default precision; if production uses
`zerodecimal_prec9` or `zerodecimal_prec12`, repeat the underlying `go test`
command with that tag (comma-combined with `zerodecimal_strcache` when needed)
and keep it as a separately labeled population. Never combine the two mutually
exclusive precision tags.

### Limitations

- These are synthetic in-process throughput benchmarks, not proof of database,
  network, scheduler, NUMA, GC, or whole-application tail latency.
- `RunParallel` reports aggregate steady-state throughput. It is not a p99/p999
  latency or fairness measurement and contains no application locks.
- Cache-on steady-state hits do not include the eager cache's process startup,
  resident-memory, GC-root scanning, or deployment-density cost. Measure those
  in the actual service before enabling `zerodecimal_strcache`.
- A 1x smoke run includes benchmark-harness startup allocations, especially in
  `RunParallel`; use timed multi-sample runs for allocation conclusions.
- Exact, direct-round, and legacy truncating APIs have different semantics.
  Their timings are not interchangeable unless the application's required
  rounding and error policy is also the same.
- Microbenchmark ns/op values do not establish safe capacity for a high-value
  money-moving system. Validate representative request mixes, failure rates,
  GC settings, target CPUs, and latency percentiles in an application-level
  soak before setting production limits.
