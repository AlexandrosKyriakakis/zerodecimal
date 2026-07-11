# Comparative benchmarks

This module benchmarks [zerodecimal](..) against the other Go decimal
libraries on one shared operation × shape matrix. Published runs use the
current untagged cache-off default.

| key      | library                                                                  | current harness pin |
| -------- | ------------------------------------------------------------------------ | ------------------- |
| `zd`     | github.com/AlexandrosKyriakakis/zerodecimal                              | local checkout      |
| `udec`   | github.com/quagmt/udecimal                                                | v1.10.1             |
| `alpaca` | github.com/alpacahq/alpacadecimal                                         | v0.0.9              |
| `ss`     | github.com/shopspring/decimal                                             | v1.4.0              |
| `eric`   | github.com/ericlagergren/decimal                                          | 00de7ca16731        |
| `dec`    | github.com/jokruger/dec128                                                | v1.0.20             |
| `gv`     | github.com/govalues/decimal                                               | v0.1.36             |

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
| `large`         | `12345678901234567890.123456789`           | `9.876543211`           |
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
| QuoRem                          | `eric` | its mixed-scale remainder is emitted as an exponent-zero unscaled coefficient, violating `x = q*y+r` |
| Mul / `max_prec`, `near_max`    | `dec` | dec128 returns NaN because the exact product is not representable                         |
| RoundBank, Truncate / `large`, `near_max` | `eric` | precision-19 `Quantize` returns invalid-operation NaN                  |

## Semantic asymmetries (deliberate)

These are part of the story the numbers tell, not benchmark bugs:

- **alpaca fallback**: `large` and `near_max` exceed alpacadecimal's optimized
  int64 fixed-point range, so those rows measure its shopspring fallback path.
- **zd error returns**: zerodecimal's fallible ops return `(Decimal, error)`
  and the error is sunk. Every included zerodecimal row succeeds; a fixture
  test enforces this so an error path cannot silently enter the comparison.
- **QuoRem mapping**: each library's closest exact-truncated-quotient API is
  used — zd `QuoRem(e)`, udec `QuoRem(e)`, alpaca/ss `QuoRem(e, 0)`, and dec
  `QuoRem(e)`. Eric's superficially matching API is omitted for the semantic
  defect documented above.
- **eric context and mutability**: every `*decimal.Big` uses the context from
  udecimal's benchmark harness (precision 19, half-even). Results go through
  explicit receiver Bigs; RoundBank and Truncate are `Copy` + `Quantize` on a
  receiver with the matching rounding mode (half-even and to-zero), so the
  copy is part of the measured cost — that is what the API requires. On
  `large` and `near_max` the quantized coefficient exceeds the 19-digit
  context; those individual NaN rows are omitted.
- **eric NewFromFloat**: `SetFloat64` performs an exact binary-to-decimal
  conversion, unlike the shortest-decimal semantics of the other four — fewer
  digits in, sometimes far more digits stored.
- **Div precision**: zd and udec produce up to 19 fractional digits, ss and
  alpaca default to `DivisionPrecision = 16`, eric rounds to 19 significant
  digits. The work compared is each library's own contract.
- **SQL caches**: alpaca has its own small-value cache. Zerodecimal's cache is
  absent by default and enabled only with `zerodecimal_strcache`; the
  published untagged runs measure the production-default cache-off behavior.
- **dec NaN poisoning**: dec128's fallible ops (FromString, Add, Sub, Mul,
  Div, QuoRem, FromFloat64) return a NaN-poisoned `Dec128` instead of a
  `(Decimal, error)` pair (NaN + 1 = NaN). Any individual row that produces
  NaN is omitted rather than timed as successful arithmetic.
- **dec Mul exactness**: dec128's `Mul` returns the exact product or NaN
  (overflow) — it never truncates or rounds to fit. Its `max_prec` and
  `near_max` products are not representable and those rows are omitted;
  `large` uses an operand pair whose exact product fits both libraries.
- **dec AppendText mapping**: dec128 has no `AppendText`, but `StringToBuf`
  is a genuine render-into-caller-buffer text API; it resets the buffer
  (`buf[:0]`) instead of appending. The harness's append buffer is empty, so
  the measured work is identical; the row is a buffer-reuse comparison, not
  an `encoding.TextAppender` contract match.
- **gv 19-digit cap**: govalues stores at most 19 significant digits, so the
  `large` and `near_max` operands do not fit and those rows are skipped
  entirely (not approximated), and the comparison geomean is restricted to
  the three shapes both libraries can represent. It maps cleanly onto every
  op for those three shapes: `Quo` for Div, `Round` for RoundBank (half-even,
  the same mode), `Trunc` for Truncate, and the full `(Decimal, error)`
  codec/SQL surface. Where an exact result needs more than 19 digits it takes
  govalues's internal big-integer path rather than overflowing — `Add`/`Mul`
  on `max_prec` and every `Quo` run (about 103/127/275–290 ns/op versus
  zerodecimal's single-digit ns) — but that path is allocation-free in steady
  state, so govalues stays 0 B/op throughout, matching zerodecimal on
  allocations and differing only in time.

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
make verify         # fixture-contract and benchmark-generator tests
make collect        # unified count=10 collection, comparisons, PGO, and charts
make bench-all      # one unified, same-process comparative collection
make bench-split    # derive per-library raw files from bench-all.txt
make bench-zd       # per-library runs, count=10, lib segment stripped
make bench-udec
make bench-alpaca
make bench-ss
make bench-eric
make bench-dec      # anchored -bench=/^dec$/ so it does not also match udec
make bench-gv       # govalues; only the three shapes it can represent
make compare        # intersect supported rows, then write benchstat reports
make pgo            # profile the zd benchmarks, re-run with -pgo, benchstat into bench-pgo.txt
make chart          # verify committed hashes, then deterministically re-render charts
make production-smoke    # every production row once, default and strcache
make production-default  # production suite, current cache-off default
make production-strcache # same suite with the opt-in cache
make production-micro    # primitives and error paths only
make production-pipeline # composed monetary workflows only
make production-parallel          # RunParallel at 1,2,4,8 CPUs, cache off
make production-parallel-strcache # same RunParallel rows, cache on
```

`compare` and `pgo` need the benchmark pipeline's pinned `benchstat`
(`go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260610192853-712aea8b4705`).
Per-library/intersection files, raw-with-library-segment PGO files, and
`zd.pprof` are scratch output (gitignored). The unified `bench-all.txt`, exact
filtered `bench-zd-pgo-default.txt` and `bench-zd-pgo.txt` populations,
`bench-vs-*.txt` comparisons, `bench-pgo.txt`, charts, and
`benchmark-provenance.txt` are tracked published artifacts.
`collect` runs every library in one benchmark process, then intersects each
competitor with zerodecimal before invoking benchstat. This prevents omitted
APIs, unsupported shapes, and NaN rows from contaminating either geomean.
The chart uses each pair's relative geomean because absolute geomeans over
different API/shape subsets are not comparable across libraries. It is
explicitly a pairwise native-API comparison: shared benchmark names do not
erase the precision, rounding, float-conversion, or fallback differences
listed under semantic asymmetries.

The committed comparative run uses a GitHub-hosted `macos-15` Apple M1
(Virtual) runner. The workflow fixes the source identity, toolchain, build
environment, process shape, duration, and sample count, but hosted virtualized
hardware is neither exclusive nor guaranteed idle. Inspect confidence
intervals and raw samples; do not treat a noisy individual row as a production
latency guarantee.

At the end of `collect`, its private publication phase records the source
state, tool versions, raw-collection SHA-256, and SHA-256 of every published
benchstat input before rendering. Standalone `make publish` is deliberately
disabled: an old raw collection cannot be rebound to the current source.
Ordinary `make chart` reads committed provenance, verifies the current source
identity and every published input hash (including the tracked raw
populations), and only then re-renders.

`collect` refuses to publish unless the source worktree was clean when make
started. It embeds a deterministic benchmark-source SHA-256 in the raw output
before measurement and verifies the same identity again before publication.
That content identity survives rebases and squash merges; the commit ID remains
additional traceability rather than the validity key. Provenance also binds
the synthetic profile plus both raw-with-segment and filtered standalone
default/PGO populations that feed `bench-pgo.txt`. The filtered populations are
tracked so the published statistical inputs remain directly inspectable.

When CI detects a changed source identity on a same-repository PR, it
recollects and uploads the whole evidence bundle, then intentionally fails if
those refreshed tracked files are not in the PR. Download the bundle, replace
the tracked artifacts unchanged, and commit them. The next run verifies the
source and artifact hashes without recollecting. Fork PRs run cheap Linux
detection/verification only; changed benchmark source must be reproduced on a
maintainer-controlled branch before merge.

The Makefile exports `GOENV=off`, `GOWORK=off`, and an empty `GOFLAGS` for every
command, and every comparative/PGO `go test` also spells out `GOFLAGS=`. This
prevents process/persisted flags and an ignored parent `go.work` from injecting
module replacements, `zerodecimal_strcache`, precision tags, or `-pgo` into the
claimed untagged cache-off population. Provenance records that enforcement
together with `GOEXPERIMENT`, `GOMAXPROCS`, `GOGC`, `GOMEMLIMIT`, and `GODEBUG`.

The PGO comparison is separately configured and isolated: after profile
collection it runs standalone default and PGO zerodecimal populations with
identical benchmark selection, benchtime, count, and process shape. The unified
competitor baseline is never reused for PGO because its interleaved library
order is a different measurement context.

`pgo` is intentionally only an in-sample experiment: it profiles this
synthetic benchmark binary and rebuilds that same benchmark binary with the
profile. The published delta is evidence about this harness, not a prediction
for an application built from a production profile. Applications that use PGO
must collect their own representative profile and measure their own binary.

## Production benchmark methodology

The production suite is zerodecimal-only and deliberately separate from the
competitor matrix. Names make the measurement boundary explicit:

- `BenchmarkProductionMicro*` measures one API family at a time: canonical,
  scientific, and rescue parsing; exact/direct-round multiplication and
  division; same-precision 2/10/4096-item aggregates, their late-mismatch
  fallback, and mixed-sign/mixed-scale and cancellation-heavy aggregates; the
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

The comparative harness has a fixed library order, and Go emits the requested
`-count` samples consecutively for each leaf benchmark. The samples are not
randomized or temporally paired, so thermal or background-load drift can bias
very small rows even when benchstat reports significance. Collect on an
otherwise idle host, inspect confidence intervals, and rerun before treating a
small delta as actionable.

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
