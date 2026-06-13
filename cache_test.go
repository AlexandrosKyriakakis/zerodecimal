//go:build !zerodecimal_nostrcache

package zerodecimal

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCacheMissSentinelOutOfRange locks in the contract that powers the
// branch-free callers: the miss sentinel must be >= len of both caches, so a
// single bounds comparison in cachedString/cachedValue is simultaneously the
// miss test and the bounds proof the prove pass discharges.
func TestCacheMissSentinelOutOfRange(t *testing.T) {
	require.GreaterOrEqual(t, cacheMiss, uint64(len(stringCache)),
		"cacheMiss must be out of range for stringCache")
	require.GreaterOrEqual(t, cacheMiss, uint64(len(valueCache)),
		"cacheMiss must be out of range for valueCache")
}

// TestCacheIndexScaleConstant couples the prec→hundredths scaling factor to
// the cache's 2-decimal-place window: scaling by 10^(2-prec) is what maps a
// prec-0/1/2 coefficient onto the shared hundredths index. The factor depends
// only on that fixed window, not on cacheSpan's magnitude.
func TestCacheIndexScaleConstant(t *testing.T) {
	for p := uint8(0); p <= 2; p++ {
		assert.Equal(t, pow10u64[2-p], pow10u64[(2-p)&31],
			"hundredths scale for prec %d", p)
	}
	assert.Equal(t, uint64(100), pow10u64[2], "prec 0 scales by 100")
	assert.Equal(t, uint64(10), pow10u64[1], "prec 1 scales by 10")
	assert.Equal(t, uint64(1), pow10u64[0], "prec 2 scales by 1")
}

// TestCacheIndexMissReturnsSentinel verifies cacheIndex returns the sentinel
// (not an in-range value) for every miss class the callers rely on.
func TestCacheIndexMissReturnsSentinel(t *testing.T) {
	misses := []Decimal{
		{coef: u128{lo: 100001}, prec: 2},            // +1000.01, above span
		{coef: u128{lo: 100001}, neg: true, prec: 2}, // -1000.01, above span
		{coef: u128{lo: 1500}, prec: 3},              // prec > 2
		{coef: u128{hi: 1, lo: 5}, prec: 2},          // hi limb set
		{coef: u128{lo: 1<<63 + 100}},                // scaling-wrap guard
	}
	for _, d := range misses {
		assert.Equal(t, cacheMiss, cacheIndex(d), "cacheIndex(%+v) must miss", d)
	}

	hits := []Decimal{
		Zero,
		{coef: u128{lo: 100000}, prec: 2}, // +1000.00
		{coef: u128{lo: 100000}, neg: true, prec: 2}, // -1000.00
	}
	for _, d := range hits {
		assert.Less(t, cacheIndex(d), uint64(len(stringCache)),
			"cacheIndex(%+v) must hit in range", d)
	}
}
