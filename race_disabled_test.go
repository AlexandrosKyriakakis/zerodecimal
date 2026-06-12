//go:build !race

package zerodecimal

// raceEnabled reports at compile time whether this test binary carries the
// race detector (see race_enabled_test.go); without -race the allocation
// gates in alloc_test.go run for real.
const raceEnabled = false
