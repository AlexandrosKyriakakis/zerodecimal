# Go 1.27 AVX-512 arithmetic experiment on amd64

Date: 2026-08-28

Status: **no production arithmetic path accepted**. Eight-wide kernels are
materially faster, but zerodecimal's current scalar APIs cannot consume that
throughput and the first end-to-end `Decimal.Sum` prototype regressed.

The experiment is isolated in `arithmetic_avx512_experiment_test.go` behind:

```text
go1.27 && !go1.28 && goexperiment.simd && amd64
```

Every entry point checks `archsimd.X86.AVX512()` before executing a 512-bit
instruction. In Go 1.27 that guard represents AVX-512F+CD+BW+DQ+VL together.

## What was tested

- eight independent carry-correct 128-bit additions;
- eight independent borrow-correct 128-bit subtractions;
- eight exact 64x64-to-128 products, reconstructing the high halves from
  32-bit partial products;
- accumulation of 4,096 contiguous `u128` coefficients;
- a semantic `Decimal.Sum` prototype that validates precision, preserves
  canonical zero, separates signs, detects narrow overflow, and falls back
  rather than weakening the public contract.

The first four candidates completed differential correctness testing on real
AVX-512 hardware: 12,500 pseudorandom batches, or 100,000 operands per
operation, plus aggregate and tail cases. All benchmarks remained at zero
allocations.

## Eight-wide kernel results

Environment:

- Go 1.27.0 with `GOEXPERIMENT=simd`;
- linux/amd64;
- `Intel(R) Xeon(R) 6973P-C`;
- 10 samples per row, 300 ms per sample;
- [GitHub Actions run 33171502108, job 98849526443](https://github.com/AlexandrosKyriakakis/zerodecimal/actions/runs/33171502108/job/98849526443).

`benchstat` medians:

| Candidate | Scalar | AVX-512 | Change |
|---|---:|---:|---:|
| Add eight, SoA | 8.858 ns | 1.878 ns | **-78.80%** |
| Add eight, `u128` AoS | 8.003 ns | 3.045 ns | **-61.94%** |
| Subtract eight, `u128` AoS | 8.091 ns | 3.043 ns | **-62.39%** |
| Multiply eight 64x64-to-128 | 5.678 ns | 4.756 ns | **-16.23%** |
| Sum 4,096 contiguous `u128`s | 1.987 us | 0.921 us | **-53.65%** |
| Geomean | 23.02 ns | 9.472 ns | **-58.86%** |

Every time change was significant at `p=0.000`, `n=10`.

The generated code contains the intended ZMM operations: `VPERMI2Q` for AoS
deinterleaving, `VPADDQ`/`VPSUBQ`, unsigned `VPCMPUQ` carry masks, and
`VPMULLQ` for low products.

## End-to-end `Decimal.Sum` result

Environment:

- Go 1.27.0 with `GOEXPERIMENT=simd`;
- linux/amd64;
- `INTEL(R) XEON(R) PLATINUM 8573C`;
- 10 samples per row, 300 ms per sample;
- [GitHub Actions run 33171859771, job 98850698090](https://github.com/AlexandrosKyriakakis/zerodecimal/actions/runs/33171859771/job/98850698090).

The first prototype copied the coefficient limbs out of `Decimal`'s 24-byte
AoS layout into temporary SoA vectors while validating signs and precision.
It was correct but decisively slower:

| 4,096-value shape | Existing `Sum` | AVX-512 prototype | Change |
|---|---:|---:|---:|
| Positive, same precision | 5.358 us | 11.290 us | **+110.70%** |
| Mixed signs, same precision | 5.538 us | 11.227 us | **+102.73%** |
| Geomean | 5.447 us | 11.26 us | **+106.68%** |

Both regressions were significant at `p=0.000`, `n=10`, with zero
allocations. Scalar extraction and packing cost more than the vectorized
arithmetic saved.

A follow-up prototype replaces scalar coefficient packing with three direct
ZMM loads and AVX-512 permutations over the existing 24-byte layout. It
cross-compiles for Linux and Windows amd64, and disassembly confirms the
intended loads/permutations. Three replacement hosted-runner allocations did
not expose AVX-512, so its runtime correctness and performance remain
**unmeasured**. It is not a production candidate without that evidence.

## Decision boundary

**Proven:**

- AVX-512 can accelerate batches of eight independent 128-bit operations.
- The contiguous `u128` aggregate kernel is 2.16x faster on the measured CPU.
- The first public-contract-shaped `Decimal.Sum` prototype is about 2x slower.
- GitHub's standard x64 runner pool is heterogeneous; the runtime guard is
  mandatory because some allocations expose AVX-512 and others do not.

**Not proven:**

- a speedup for scalar `Decimal.Add`, `Sub`, or `Mul`;
- an end-to-end win for the direct-layout `Decimal.Sum` follow-up;
- a portable gain across AVX-512 microarchitectures or workload sizes;
- a justification for changing zerodecimal's public API or minimum Go version.

The current library has no batch arithmetic API, so the clearest future use
would require either a deliberately designed batch/SoA API or a separately
proven aggregate fast path. Neither should be inferred from the kernel-only
results.

## Reproduce

Compile the amd64 experiment without executing it:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd \
  go test -c -o /tmp/zerodecimal-avx512.test .
```

On an amd64 machine for which the guard reports AVX-512 support:

```sh
GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd \
  go test -run '^TestArithmeticAVX512ExperimentCorrectness$' -count=1 .

GOTOOLCHAIN=go1.27.0 GOEXPERIMENT=simd \
  go test -run '^$' -bench '^BenchmarkArithmeticAVX512Experiment$' \
  -benchmem -count=10 -benchtime=300ms .
```
