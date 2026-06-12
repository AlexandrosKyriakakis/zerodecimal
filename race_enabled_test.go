//go:build race

package zerodecimal

// raceEnabled reports at compile time whether this test binary carries the
// race detector. The race runtime instruments memory accesses and performs
// its own bookkeeping allocations, which makes testing.AllocsPerRun counts
// meaningless, so the allocation gates in alloc_test.go skip when it is on.
const raceEnabled = true
