//go:build race

package zdbig

// raceEnabled reports at compile time whether this test binary carries the
// race detector. The race runtime instruments memory accesses and performs
// its own bookkeeping allocations, which makes testing.AllocsPerRun counts
// meaningless, so the allocation gates skip when it is on (see the root
// package's race_enabled_test.go).
const raceEnabled = true
