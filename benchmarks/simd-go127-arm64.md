# Go 1.27 SIMD experiment

Measured on 2026-08-28 on darwin/arm64, Apple M1 Pro. The parsing sections
compare the same Go 1.27.0 commit without and with `GOEXPERIMENT=simd`; those
runs were interleaved to reduce temperature and frequency bias. The arithmetic
section states its separate baselines explicitly. All rows remained at 0 B/op
and 0 allocs/op.

## Existing parse matrix

Twelve samples per side, 150 ms per sample:

| Input shape | Scalar ns/op | SIMD ns/op | Delta |
| --- | ---: | ---: | ---: |
| small integer | 4.304 | 4.277 | no significant change |
| typical price | 9.081 | 9.147 | no significant change |
| max precision | 11.74 | 11.79 | no significant change |
| large | 27.43 | 25.86 | -5.71% |
| near max | 32.67 | 31.30 | -4.21% |
| **geomean** | **13.27** | **13.01** | **-1.91%** |

## Long-input matrix

Ten samples per side, 150 ms per sample. The 28-byte cutoff is the measured
crossover where both integer and decimal inputs benefit.

| Input shape | Scalar ns/op | SIMD ns/op | Delta |
| --- | ---: | ---: | ---: |
| 21-digit integer | 22.16 | 22.60 | no significant change |
| 22-digit integer | 22.38 | 22.71 | no significant change |
| 24-digit integer | 21.64 | 21.91 | no significant change |
| 26-digit integer | 23.66 | 23.78 | no significant change |
| 28-digit integer | 22.61 | 22.00 | -2.72% |
| 30-digit integer | 23.80 | 23.25 | -2.31% |
| 32-digit integer | 22.73 | 20.80 | -8.47% |
| 39-digit integer | 31.79 | 29.83 | -6.15% |
| max uint128 | 31.82 | 29.66 | -6.77% |
| 21-byte decimal | 23.26 | 23.41 | no significant change |
| 22-byte decimal | 23.41 | 23.38 | no significant change |
| 24-byte decimal | 23.81 | 23.71 | no significant change |
| 26-byte decimal | 24.21 | 24.59 | +1.57% |
| 28-byte decimal | 24.38 | 23.73 | -2.63% |
| 32-byte decimal | 25.61 | 24.71 | -3.51% |
| 39-byte decimal | 28.27 | 26.47 | -6.37% |
| 40-byte decimal | 32.53 | 30.85 | -5.18% |
| invalid byte at position 40 | 16.77 | 17.06 | +1.73% |
| 80-digit overflow input | 50.97 | 45.88 | -10.00% |
| **geomean** | **25.38** | **24.75** | **-2.47%** |

The below-cutoff differences are code-layout effects from enabling the global
Go experiment; those rows execute the scalar scanner. They are included so the
cutoff decision and tradeoff remain visible rather than being optimized out of
the report.

## Arithmetic experiments

The repository keeps benchmark-only, carry-correct SIMD candidates for
128-bit addition, subtraction, 64x64-to-128 multiplication, and a 4,096-term
128-bit sum. They are excluded from production builds. A deterministic test
cross-checks each primitive against the existing scalar implementation over
100,000 pseudorandom operand pairs before the benchmark runs.

Ten samples per side, 300 ms per sample. These are serial dependency chains,
so the compiler cannot hoist an invariant result out of the timed loop:

| Kernel | Scalar median | SIMD median | SIMD delta |
| --- | ---: | ---: | ---: |
| carry-correct 128-bit add | 2.443 ns/op | 8.816 ns/op | +260.8% (3.61x slower) |
| borrow-correct 128-bit subtract | 2.446 ns/op | 8.083 ns/op | +230.5% (3.31x slower) |
| 64x64-to-128 multiply | 2.948 ns/op | 7.892 ns/op | +167.7% (2.68x slower) |
| carry-correct sum of 4,096 u128 values | 2.167 us/op | 28.602 us/op | +1219.9% (13.20x slower) |

No arithmetic candidate was selected. On arm64, `bits.Add64` and `bits.Sub64`
compile to scalar flag-carry chains (`ADDS`/`ADCS` and `SUBS`/`SBCS`). A
lane-wise SIMD add or subtract must additionally detect the low-limb carry,
move it between lanes, apply it to the high limb, detect the final overflow,
and extract the result. Likewise, `bits.Mul64` compiles to `MUL` plus `UMULH`;
NEON has no integer 64x64-to-128 lane multiply, so the tested SIMD form needs
two 32-bit `UMULL` operations plus cross-product reassembly.

On arm64, the same dependency makes the existing `Sum` and `Avg` accumulator
a poor SIMD target: every 128-bit coefficient needs a carry into its adjacent
limb. Independent batch operations could occupy independent lanes, but that
would require a new batch API or a structure-of-arrays representation. The
separate amd64 report documents why AVX2/AVX-512 deinterleaving and unsigned
lane operations make `Sum` worthwhile there without changing the API or
layout.

### Go 1.27 arithmetic toolchain comparison

For completeness, the existing 38-row public arithmetic suite was compiled
unchanged with Go 1.26.5, then with Go 1.27.0 and `GOEXPERIMENT=simd` (eight
samples per side, 250 ms per sample):

| Existing public path | Go 1.26.5 | Go 1.27 + experiment | Delta |
| --- | ---: | ---: | ---: |
| all 38 rows, geomean | 52.28 ns/op | 51.97 ns/op | -0.60% |
| AvgRound, cancellation-heavy | 39.96 ns/op | 37.43 ns/op | -6.34% |
| Mul, direct bank rounding | 16.14 ns/op | 16.05 ns/op | no significant change |
| Div, direct bank rounding | 13.47 ns/op | 13.28 ns/op | -1.48% |
| Sum, same-precision 4,096 values | 4.016 us/op | 4.091 us/op | +1.87% |
| AvgRound, mixed 4,096 values | 14.76 us/op | 15.32 us/op | +3.81% |

There is no explicit arithmetic SIMD on those public paths, so their mixed
changes are Go compiler and code-layout effects, not SIMD acceleration. The
0.60% geomean is too small and uneven to justify raising the module's minimum
Go version or publishing an arithmetic speed claim.

## Implementation and rejected variants

The selected path scans two 16-byte vectors together, uses one validity
reduction on the success path, and locates a delimiter with the existing SWAR
mask without an extra function call. The generated arm64 code contains NEON
`ldr q`, `sub.16b`, `umax.16b`, and `umaxv.16b` instructions. The Go 1.27
linux/amd64 cross-build contains `vmovdqu`, `vpsubb`, `vpmaxub`, `vpsubusb`,
and `vptest`.

Two broader variants, measured during exploration before the branch was
updated to the current `main`, were rejected:

- SIMD validation and conversion of a fixed 16-digit block was 31.74% slower
  by geomean; its valid cases were 15-17% slower and its invalid case was about
  69% slower.
- Activating the scanner at 21 bytes regressed 21-22 byte integers by 3-4% and
  21-26 byte decimals by roughly 4-5%. The final implementation waits for 28
  bytes.

Only arm64 performance is claimed here. The amd64 implementation is compiled
and correctness-tested, but needs a native amd64 benchmark before publishing
an amd64 speed claim.

## Reproduce

```sh
GOTOOLCHAIN=go1.27.0 go test -run '^$' -bench '^BenchmarkParseLong$' \
  -benchmem -count=10 -benchtime=150ms > scalar.txt
GOEXPERIMENT=simd GOTOOLCHAIN=go1.27.0 go test -run '^$' \
  -bench '^BenchmarkParseLong$' -benchmem -count=10 -benchtime=150ms > simd.txt
benchstat scalar=scalar.txt simd=simd.txt
```

For the existing five-shape matrix, run the same pair from `benchmarks/` with
`-bench='^BenchmarkParse/zd/'`.

Run the rejected, correctness-checked arithmetic candidates with:

```sh
GOEXPERIMENT=simd GOTOOLCHAIN=go1.27.0 go test \
  -run '^TestArithmeticSIMDExperimentCorrectness$' \
  -bench '^BenchmarkArithmeticSIMDExperiment$' \
  -benchmem -count=10 -benchtime=300ms .
```

Compare the unchanged public arithmetic paths across toolchains with:

```sh
GOTOOLCHAIN=go1.26.5 go test -run '^$' \
  -bench '^(BenchmarkExactArithmetic|BenchmarkAggregates|BenchmarkAggregateCommonPrecision|BenchmarkQuoRemAlignmentPaths|BenchmarkModAlignmentPaths)$' \
  -benchmem -count=8 -benchtime=250ms > go126.txt
GOEXPERIMENT=simd GOTOOLCHAIN=go1.27.0 go test -run '^$' \
  -bench '^(BenchmarkExactArithmetic|BenchmarkAggregates|BenchmarkAggregateCommonPrecision|BenchmarkQuoRemAlignmentPaths|BenchmarkModAlignmentPaths)$' \
  -benchmem -count=8 -benchtime=250ms > go127-simd.txt
benchstat go126.txt go127-simd.txt
```
