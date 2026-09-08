# PR #8 optimization validation

The branch incorporates main at `d5025f05c91accba1db5f4ca2c3e2284f534827f`,
including the float32 nearest-even correction, strict shortest-decimal oracle,
and expanded security scans. The API, Decimal representation, Go 1.26 language
floor, precision rules, overflow errors, and allocation contracts are retained.

## Accepted changes

- `MulRound` uses generated power-of-ten reciprocals when dropping 1–19
  decimal places. It still derives rounding from the full product, retains
  the exact remainder's nonzero and half-way classification, and checks both
  quotient overflow and rounding-increment overflow. Narrow products avoid
  unnecessary high-limb divisions. Larger divisors keep the existing kernel.
- Go 1.27 SIMD parsing remains enabled on arm64 at 28 remaining input bytes.
  The amd64 parser uses scalar/SWAR code.
- SIMD `Sum` starts at 64 operands on amd64, with AVX-512/AVX2 feature dispatch,
  exact scalar continuation, and full scalar fallback if the positive prefix
  overflows. Mixed signs and precision still retain order-independent exact
  accumulation and cancellation.
- SIMD object loads have compile-time checks for every field offset and the
  stride, plus dirty-padding tests. Direct kernel checks exercise AVX2 even
  when public dispatch chooses AVX-512 and compare with an independent
  exact-integer oracle. Targeted fuzzing constructs long compatible prefixes
  followed by arbitrary suffixes.

All SIMD code remains opt-in and gated to Go 1.27. Unsupported architectures
and later unvalidated toolchains retain scalar implementations.

## Local final implementation measurements

Apple M1 Pro, darwin/arm64, Go 1.26.8, one CPU, cache off. Ten alternating
200 ms samples per side used identical benchmark fixtures and separate
before/after binaries. No validation jobs ran concurrently. All numerical
operations remained at zero allocations; pipeline allocations were unchanged.

| Operation | Main | Optimized | Change |
| --- | ---: | ---: | ---: |
| Price × fee, nearest-even currency rounding | 15.970 ns | 8.057 ns | -49.55% |
| Rounded 128-bit product | 16.01 ns | 10.84 ns | -32.29% |
| Rounded 192-bit product | 17.46 ns | 11.90 ns | -31.84% |
| Sticky-half fixture | 17.000 ns | 7.597 ns | -55.31% |
| Synthetic trade capture | 124.9 ns | 102.1 ns | -18.25% |
| Synthetic portfolio mark | 281.6 ns | 217.2 ns | -22.89% |

The 20/36/38-digit-drop, padding, and same-precision control rows showed no
statistically significant change. [Raw samples, benchstat reports, and environment](optimization-20260907/local-arm64/)
include the complete population, including overflow and control rows.

The separate Go 1.27.1 arm64 parser comparison used six alternating 100 ms
samples. SIMD improved 32/39-digit integers by 5.6–8.5%, the maximum-coefficient
input by 6.2%, and the 80-digit overflow input by 10.3%. Across all 19 shapes the geomean
improved only 0.8%; several 21–26-byte inputs regressed roughly 4–6% from
dispatch/code-generation overhead despite remaining on the scalar scanner.
This remains an opt-in optimization for workloads with sufficiently long input.

## Final amd64 measurements

The final runtime source, `9035fa2f427b885ea567891b3967cb4765cab194`, was measured
against main with Go 1.27.1 on separate Linux and Windows AMD EPYC 7763 runners.
[Run 34172185961](https://github.com/AlexandrosKyriakakis/zerodecimal/actions/runs/34172185961)
contains the original artifacts; the
[measurement workflow at that revision](https://github.com/AlexandrosKyriakakis/zerodecimal/blob/9035fa2f427b885ea567891b3967cb4765cab194/.github/workflows/test.yaml)
records exact build and alternating-sampling commands. Temporary measurement
jobs were removed after collection; the correctness checks remain permanent.

| Operation | Linux main → optimized | Windows main → optimized |
| --- | ---: | ---: |
| Price × fee, nearest-even currency rounding | 21.52 → 13.15 ns (-38.87%) | 22.10 → 13.47 ns (-39.05%) |
| Rounded 128-bit product | 21.48 → 16.58 ns (-22.83%) | 22.15 → 16.98 ns (-23.36%) |
| Rounded 192-bit product | 22.89 → 18.79 ns (-17.89%) | 23.23 → 19.18 ns (-17.40%) |
| Synthetic trade capture | 186.2 → 172.2 ns (-7.52%) | 186.2 → 185.6 ns (not significant) |
| Synthetic portfolio mark | 362.4 → 286.2 ns (-21.03%) | 376.3 → 302.4 ns (-19.65%) |

Wide divisors, padding, and same-precision controls show no significant
regression. Numerical allocations remain zero; pipeline allocations remain
unchanged. Windows intervals are wider, and its trade-capture result supports
no speedup claim. Full [Linux](optimization-20260907/final-linux-amd64/) and
[Windows](optimization-20260907/final-windows-amd64/) samples and benchstat
reports retain every measured row.

With the final 64-operand gate, public SIMD sums are 1.37x/1.48x as fast at 64
operands and 1.66x/1.57x at 4,096 on Linux/Windows, respectively. Late-mismatch
continuation remains 1.44–1.66x as fast. All sum samples allocate zero bytes.
The x86 parser regression disappears when both builds use the scalar scanner.

## Measurement-driven exclusions

The initial hosted revalidation at `91e2f8c` used Go 1.27.1 on separate Linux
and Windows AMD EPYC 7763 runners. [Run 34171378814](https://github.com/AlexandrosKyriakakis/zerodecimal/actions/runs/34171378814)
contains raw `optimization-ubuntu-latest` and `optimization-windows-latest`
artifacts. Base/head rounded-multiplication and pipeline samples alternated
order ten times at 200 ms per sample with one CPU. Scalar/SIMD parser samples
alternated six times at 100 ms; public sum measurements used six 200 ms samples.
The decision inputs are also retained under
[initial Linux](optimization-20260907/initial-linux-amd64/) and
[initial Windows](optimization-20260907/initial-windows-amd64/).

The x86 SIMD parser regressed the 19-shape geomean by 4.50% on Linux and 4.35%
on Windows. The 28/32-byte decimal cases regressed about 10–12%; the 80-digit
case regressed 12–18%. It was removed. Its use of AVX instructions had also
required a runtime feature guard absent in the original PR; removing the
production path removes that CPU-compatibility hazard altogether.

At 32 sum operands, SIMD was 1.09x as fast on Linux but 0.98x on Windows. At
64 it was 1.39x and 1.30x, respectively, justifying the more conservative
64-operand threshold. At 4,096 it was 1.67x and 1.47x, with zero allocations.
Late sign, precision, and coefficient-width mismatches retained 1.41–1.67x
speedups through exact continuation. Both runners used AVX2; the earlier
AVX-512 measurements remain separately documented in
[the original study](simd-go127-amd64.md).

A local prototype replaced 20–38-digit division with two reciprocal passes
and an exact sticky remainder. Although correct in the exercised cases, its
38-digit-drop fixture regressed 37%, so it was rejected. The production
implementation retains the original wide-divisor algorithm.

## Verification scope

The numerical additions check all 19 one-pass divisors against `math/big`,
including both sides of the exact quotient-overflow threshold, exact halves,
quotient parity, and 64/128/192/256-bit dividends. Another 36,000 rounded-product
comparisons cover all six modes with deliberately varied operand widths,
using an independent exact rational oracle.

Permanent SIMD CI uses Go 1.27.1 on Linux, macOS, and Windows, including race,
allocation, pointer instrumentation, precision/cache configurations, fuzz seeds,
targeted parser/prefix fuzzing, and CPU feature fallback configurations.
The Linux SIMD job also scans its test-aware dependency graph for vulnerabilities.
Ordinary CI retains Go 1.26.8 and stable coverage, all 12 precision/cache
combinations, linux/386 execution, lint, benchmark fixture tests, and the full
fuzz sweep. Benchmarks are bounded synthetic measurements, not application
latency guarantees.

Local validation passed all 51 fuzz targets, the cache-enabled variant, and
additional focused rounding/SIMD runs: 25,917,222 executions across 54 successful
runs. One aggregate fuzz attempt ended with a coordinator deadline, without
an assertion or saved failing input; its fixed-count rerun passed 250,000
inputs. The final rounded-arithmetic helper passed a separate 1,000,000-input
run. Local logs and the separate deadline attempt are retained in
`reviews/2026-09-07/pr8-validation/` outside the PR's tracked publication files.

The pinned golangci-lint checks ordinary Go 1.26 builds. Its dependency reader
cannot decode Go 1.27's export format even after rebuilding the binary; native
Go 1.27 vet passed for both amd64 and arm64 SIMD builds. Experimental paths
also retain their compiler, numerical, race, pointer, fuzz, and vulnerability
checks in CI.

The comparative publication was refreshed by
[run 34172185974](https://github.com/AlexandrosKyriakakis/zerodecimal/actions/runs/34172185974)
on Go 1.26.8, Apple M1 (Virtual), cache off. Its source identity is
`b7bbbf004e1f0dcf06f5de42fa0476eb95081624f569f98ce63022a466bdfc15`.
All artifact and profile-input hashes were verified before import, and the
charts regenerate byte-for-byte. Its first check intentionally failed only
at the requirement to commit that fresh evidence; collection and chart
verification both passed.
