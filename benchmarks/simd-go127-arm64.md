# Go 1.27 SIMD experiment

Measured on 2026-08-28 with Go 1.27.0 on darwin/arm64, Apple M1 Pro.
"Scalar" is the same commit without `GOEXPERIMENT=simd`; "SIMD" sets
`GOEXPERIMENT=simd`. Runs were interleaved to reduce temperature and frequency
bias, then compared with `benchstat`. All rows remained at 0 B/op and
0 allocs/op.

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
