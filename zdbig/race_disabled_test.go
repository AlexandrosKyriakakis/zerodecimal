//go:build !race

package zdbig

// raceEnabled reports at compile time whether this test binary carries the
// race detector (see race_enabled_test.go); without -race the allocation
// gates run for real.
const raceEnabled = false
