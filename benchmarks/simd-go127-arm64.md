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
| small integer | 4.264 | 4.292 | no significant change |
| typical price | 9.092 | 9.235 | no significant change |
| max precision | 11.75 | 11.77 | no significant change |
| large | 27.32 | 25.83 | -5.45% |
| near max | 32.61 | 30.98 | -5.01% |
| **geomean** | **13.23** | **13.01** | **-1.66%** |

## Long-input matrix

Ten samples per side, 150 ms per sample. The 28-byte cutoff is the measured
crossover where both integer and decimal inputs benefit.

| Input shape | Scalar ns/op | SIMD ns/op | Delta |
| --- | ---: | ---: | ---: |
| 21-digit integer | 21.89 | 22.19 | no significant change |
| 22-digit integer | 22.19 | 22.56 | +1.67% |
| 24-digit integer | 21.34 | 21.72 | +1.78% |
| 26-digit integer | 23.26 | 23.80 | +2.30% |
| 28-digit integer | 22.46 | 21.98 | -2.14% |
| 30-digit integer | 23.57 | 23.11 | -1.97% |
| 32-digit integer | 22.56 | 20.76 | -7.98% |
| 39-digit integer | 31.37 | 29.88 | -4.73% |
| max uint128 | 31.47 | 30.04 | -4.54% |
| 21-byte decimal | 22.85 | 23.19 | +1.51% |
| 22-byte decimal | 23.09 | 23.38 | no significant change |
| 24-byte decimal | 23.50 | 23.62 | no significant change |
| 26-byte decimal | 24.11 | 24.25 | no significant change |
| 28-byte decimal | 24.32 | 23.38 | -3.89% |
| 32-byte decimal | 25.65 | 24.41 | -4.81% |
| 39-byte decimal | 28.20 | 26.34 | -6.56% |
| 40-byte decimal | 32.77 | 31.13 | -5.00% |
| invalid byte at position 40 | 48.51 | 49.45 | no significant change |
| 80-digit overflow input | 69.23 | 67.44 | -2.59% |
| **geomean** | **27.07** | **26.61** | **-1.70%** |

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

Two broader variants were rejected:

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
