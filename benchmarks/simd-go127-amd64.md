# Go 1.27 SIMD arithmetic investigation on amd64

Date: 2026-08-28

Status: **accepted for the opt-in Go 1.27 amd64 build**. The public `Sum` path
is 1.65x to 1.94x faster for 4,096 positive, same-precision decimals on the
measured AVX-512 hosts and 1.66x faster on the measured AVX2 host. It remains
zero-allocation and preserves the scalar contract through exact prefix
continuation. Sums below 32 operands stay scalar.

All production and experiment code is isolated behind:

```text
go1.27 && !go1.28 && goexperiment.simd && amd64
```

It therefore requires Go 1.27 plus `GOEXPERIMENT=simd`, and automatically
falls back to the ordinary implementation on other Go versions and
architectures. Runtime guards select AVX-512 or AVX2 before executing their
instructions. The exact-Go-version gate is intentional because Go's SIMD API
is experimental and unstable.

## Outcome

The useful workload is not one scalar `Decimal.Add`: function dispatch and
packing dominate a single operation. The useful workload is `Sum`, where one
dispatch amortizes over many contiguous 24-byte `Decimal` values.

The candidate kernel shape is:

1. load the existing array-of-structs layout directly;
2. deinterleave coefficient and metadata lanes in registers;
3. validate sign and precision in parallel;
4. accumulate two independent vector chains to shorten the dependency path;
5. reduce once to an exact scalar coefficient;
6. return the exact compatible prefix when a later value needs the scalar
   contract, instead of rescanning that prefix.

No temporary coefficient arrays and no allocations are used.

## Public `Sum` results

The production wrapper, runtime dispatch, layout conversion, metadata checks,
exact reduction, and scalar tail are all inside these measurements:

| Host and selected path | Scalar | Public `Sum` | Speedup |
|---|---:|---:|---:|
| AMD EPYC 9V74, AVX2, ubuntu-latest | 4.747 us | 2.867 us | **1.66x** |
| AMD EPYC 9V74, AVX2, ubuntu-22.04 | 4.759 us | 2.866 us | **1.66x** |
| Intel Xeon Platinum 8573C, AVX-512 | 5.428 us | 3.282 us | **1.65x** |
| Intel Xeon Platinum 8370C, AVX-512 | 5.714 us | 2.946 us | **1.94x** |

Each number is the median of six 200 ms samples. The AVX2 results are from
[run 33184734175](https://github.com/AlexandrosKyriakakis/zerodecimal/actions/runs/33184734175);
the final gated AVX-512 results are from
[run 33185280431](https://github.com/AlexandrosKyriakakis/zerodecimal/actions/runs/33185280431).
Both runs executed the race suite, allocation assertions, differential SIMD
tests, and the end-to-end decision benchmark before reporting success.

The measured public crossover supports a conservative 32-operand gate. On the
two final AVX-512 hosts, 32 values ranged from neutral to 1.05x, 64 values from
1.25x to 1.27x, 128 from 1.52x to 1.56x, and 1,024 from 1.64x to 1.87x.
On AVX2, 32 values were 1.14x faster and the advantage grew to 1.66x at 4,096.
Below the gate the feature check and SIMD call are compiled out of the public
path, so small opt-in sums retain the scalar implementation.

## AVX-512 `Decimal.Sum`

Environment:

- Go 1.27.0 with `GOEXPERIMENT=simd`;
- linux/amd64;
- Intel Xeon Platinum 8573C;
- 10 samples per row, 300 ms per sample;
- [GitHub Actions run 33178359498, job 98872810499](https://github.com/AlexandrosKyriakakis/zerodecimal/actions/runs/33178359498/job/98872810499).

Medians from the raw samples:

| 4,096-value positive shape | Scalar | AVX-512 | Speedup |
|---|---:|---:|---:|
| Arbitrary 128-bit coefficient, general metadata | 4.442 us | 2.370 us | **1.87x** |
| Arbitrary 128-bit coefficient, positive specialization | 4.442 us | 2.051 us | **2.17x** |
| Arbitrary 128-bit coefficient, two chains | 4.442 us | 1.956 us | **2.27x** |

The final two-chain kernel is 55.97% lower latency and remains at zero
allocations. Its hot loop uses three direct ZMM loads per eight decimals,
`VPERMI2Q` deinterleaving, `VPADDQ` coefficient adds, unsigned `VPCMPUQ`
carry/overflow masks, and vector metadata comparisons. Helpers inline out of
the loop.

A mixed-sign AVX-512 prototype improved 4.540 us to 3.721 us, only **1.22x**.
A scalar-metadata variation regressed to 5.773 us. Neither is worth adding to
the production dispatch.

## AVX2 `Decimal.Sum`

The portable amd64 fallback uses four 64-bit lanes. Go 1.27's AVX2 API does
not expose the AVX-512 unsigned compare, so the carry test XOR-biases operands
by `1<<63` and performs a signed comparison. Subtracting the all-ones carry
mask avoids an extra broadcast/AND and removed spills from the two-chain hot
loop.

On the same Intel Xeon Platinum 8573C run:

| 4,096-value positive shape | Scalar | AVX2 | Speedup |
|---|---:|---:|---:|
| Arbitrary 128-bit coefficient | 4.493 us | 3.437 us | **1.31x** |
| Coefficient fits 64 bits, one chain | 4.457 us | 2.761 us | **1.61x** |
| Coefficient fits 64 bits, two chains | 4.457 us | 2.666 us | **1.67x** |

The 64-bit-coefficient specialization is exact to a 128-bit result: the high
vectors accumulate carries, so it does not reduce `Sum` to 64-bit arithmetic.
Real AMD EPYC runners also showed roughly 1.6x to 1.9x at 4,096 values.
Mixed-sign AVX2 variants improved only about 5% to 10% and were rejected.

## Size threshold

A direct AVX2 candidate was measured on an AMD EPYC 7763 with six 200 ms
samples per row:
[run 33179952557, job 98878356437](https://github.com/AlexandrosKyriakakis/zerodecimal/actions/runs/33179952557/job/98878356437).

| Positive values | Scalar | Direct AVX2 | Speedup |
|---:|---:|---:|---:|
| 8 | 14.68 ns | 14.07 ns | 1.04x |
| 16 | 24.66 ns | 18.75 ns | 1.32x |
| 32 | 44.81 ns | 31.33 ns | 1.43x |
| 64 | 90.10 ns | 52.84 ns | 1.70x |
| 128 | 171.0 ns | 96.76 ns | 1.77x |
| 256 | 330.0 ns | 183.6 ns | 1.80x |
| 1,024 | 1.290 us | 0.711 us | 1.81x |
| 4,096 | 5.143 us | 2.809 us | 1.83x |

The direct kernel crosses over before the complete public wrapper does. The
production gate therefore uses the end-to-end evidence above: at least 32
total operands for both AVX2 and AVX-512. This avoids a regression at 16
operands on AVX2 and avoids paying runtime feature dispatch for smaller sums.

## Fallback policy

The first dispatch experiment attempted the SIMD scan and, on a late
incompatible value, restarted the scalar implementation from the beginning.
That policy is rejected:

| 4,096-value late mismatch | Scalar | Restart after SIMD | Regression |
|---|---:|---:|---:|
| Negative last | 5.136 us | 7.966 us | **+55.1%** |
| Different precision last | 5.162 us | 8.106 us | **+57.0%** |
| Wide coefficient last | 5.137 us | 7.944 us | **+54.6%** |

The production candidate instead returns an exact positive prefix and the
first unprocessed index. `Sum` continues its full scalar algorithm from that
prefix, preserving mixed precision, signs, cancellation, canonical zero, and
final-only overflow semantics without paying for a second scan.

If the vector prefix itself exceeds 128 bits, it is discarded and the full
wide scalar implementation runs. This cold fallback is necessary because a
later negative suffix may cancel an overflowing positive intermediate;
`Sum` promises not to report a spurious intermediate overflow.

## Correctness and generated-code checks

Completed locally:

- ordinary Go 1.26.5 full unit tests;
- race, allocation, and precision/cache build-tag matrices;
- Go 1.27 SIMD tests on arm64 (scalar architecture stub);
- Linux/amd64 Go 1.27 SIMD compile and `go vet`;
- differential tests against the scalar implementation for sizes 1 through
  4,096, tails, zeros, signs, mixed precision, wide coefficients, random
  suffixes, overflow, and cancellation;
- ordinary-build disassembly: the tiny stub and dispatch branch are erased;
- SIMD disassembly: no AVX-512 operands in the AVX2 function, and no helper
  calls inside either vector hot loop;
- paired ordinary-build benchmark on Apple M1 Pro: no regression from 2
  through 4,096 operands and zero allocations.
- stripped linux/amd64 size probe: the stable executable is exactly the same
  size as the pre-dispatch baseline; the opt-in SIMD implementation adds
  8 KiB (0.41%) when both are built with Go 1.27 and `GOEXPERIMENT=simd`.
- final public `Sum` race, allocation, differential, wide-coefficient, and
  continuation tests on real AVX2 and AVX-512 hardware;
- final end-to-end benchmarks on two AVX2 jobs and two AVX-512 jobs.

The first real-amd64 production run found a correctness bug in metadata value
zero: it can represent canonical zero, but also a nonzero precision-zero
Decimal. The vector validity test now treats metadata zero as a zero value only
when both coefficient limbs are zero. A deterministic regression case covers
the distinction, and the corrected implementation passed subsequent AVX2 and
AVX-512 race runs.

The installed `golangci-lint` is built with Go 1.26 and cannot parse Go 1.27
SIMD sources; it reports zero issues for the ordinary build, while Go 1.27
`go vet` covers the SIMD build.

## Library integration plan

| Decision | Introduce? | Boundary |
|---|:---:|---|
| Long-input SIMD scanner | Yes | Existing 28-byte gate; Go 1.27 experiment only |
| amd64 SIMD `Sum` | Yes | 32+ operands; positive same-precision prefix |
| AVX-512 and AVX2 runtime dispatch | Yes | AVX-512 preferred; AVX2 is the portable amd64 fallback |
| Exact prefix continuation | Yes | Late sign, precision, or width mismatch continues scalar |
| Permanent SIMD correctness CI | Yes | Go 1.27 experiment on amd64, arm64, and Windows builds |
| Public API or representation change | No | `Decimal`, `Sum`, and allocation contracts stay unchanged |
| Minimum Go-version increase | No | Ordinary Go 1.26 builds retain their original generated path |
| SIMD `Add`, `Sub`, or `Mul` | No | Single operations cannot amortize packing and dispatch |
| Mixed-sign or mixed-precision vector sum | No | Measured gains are too small; continuation is faster and simpler |
| arm64 NEON arithmetic | No | Carry-correct candidates were materially slower than scalar |
| New batch/structure-of-arrays API | No | Not justified by this optimization and would expand the API |
| Naive SIMD-then-rescan fallback | No | Measured 55% to 57% regression on late mismatches |
| Go 1.28 SIMD path | Not yet | Re-enable only after the experimental API and codegen are revalidated |
| Rejected arithmetic kernels | Evidence only | Keep test-only benchmarks; no production references or dispatch |
| Temporary runner probes and rejected-kernel CI | No | Remove after preserving results in this report |

## Decision boundary

**Proven:**

- direct-layout AVX-512 makes a 4,096-value positive `Decimal.Sum` kernel
  2.27x faster on the measured Intel CPU;
- direct-layout AVX2 makes the common 64-bit-coefficient shape 1.67x faster on
  that CPU and materially faster on multiple AMD runners;
- direct array-of-structs loads are essential; scalar packing is slower than
  the original implementation;
- a SIMD-then-restart fallback is unacceptable;
- stable builds can retain their original generated path and allocation count.
- the final public wrapper is 1.65x to 1.94x faster at 4,096 values on the
  measured AVX-512 hosts and 1.66x faster on the measured AVX2 host;
- exact prefix continuation retains a 1.57x to 1.94x gain when the final value
  changes sign, precision, or coefficient width.

**Not proven / deliberately out of scope:**

- portable gains across every AVX2/AVX-512 microarchitecture;
- a worthwhile SIMD path for mixed signs or mixed precision;
- a win for individual scalar `Add`, `Sub`, or `Mul` calls.

## Reproduce

Compile the production candidate without executing it:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd \
  go test -c -o /tmp/zerodecimal-simd.test .
```

On an amd64 machine:

```sh
GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd \
  go test -run '^TestSIMDSumProductionCorrectness$|^TestAVX512SumProductionWideCoefficients$' \
  -count=1 .

GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd \
  go test -run '^$' -bench '^BenchmarkArithmeticSIMDSumDecision' \
  -benchmem -count=10 -benchtime=300ms .
```
