# zerodecimal

Zero-allocation, panic-free, fixed-point decimals for latency-critical Go.

**Status: under construction — APIs and numbers below are being landed commit by commit.**

## Why another decimal library

- **Strictly zero heap allocations.** No `big.Int` fallback exists in the
  package; every operation — parse, arithmetic, rounding, comparison,
  encoding — runs on fixed-width 128/256-bit integer math. Enforced by
  `testing.AllocsPerRun` assertions in the default test suite.
- **Faster than everything we could find.** Division by powers of ten — the
  inner loop of decimal rescaling, rounding, and formatting — uses
  precomputed multiply-high reciprocals (Granlund–Montgomery–Warren and
  Möller–Granlund) instead of hardware `DIV`. Comparative benchmarks against
  shopspring/decimal, alpacadecimal, udecimal, and ericlagergren/decimal live
  in `benchmarks/`.
- **Bit-exact.** Every operation is differential-fuzzed against
  shopspring/decimal (unbounded oracle) and quagmt/udecimal, including the
  exactness of the overflow boundary.
- **Panic-free.** Fallible operations return sentinel errors (zero-alloc,
  `errors.Is`-able); `Must*` twins panic for call sites with proven bounds.

## Representation

```go
type Decimal struct {
    coef u128  // |value| * 10^prec, 0 <= coef < 2^128
    neg  bool
    prec uint8 // 0..19
}
```

24 bytes, no pointers, copyable, register-friendly. Domain:
|value| < 2^128 / 10^prec (up to 39 significant digits, up to 19 fractional).
Out-of-domain results return `ErrOverflow` — there is deliberately no slow
arbitrary-precision rescue path.

## Sections to come

- Usage examples
- Error model table (operation → possible sentinels)
- Allocation guarantees table (operation → 0 or 1 allocs)
- Parsing rules and deviations from shopspring/alpacadecimal
- Rounding modes
- Benchmark results (benchstat vs udecimal / alpacadecimal / shopspring)
- PGO guide for consumers
- Build tags: `zerodecimal_prec9`, `zerodecimal_prec12`, `zerodecimal_nostrcache`

## License

MIT
