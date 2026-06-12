//go:build zerodecimal_nostrcache

package zerodecimal

import "database/sql/driver"

// cacheSpan documents the window the compiled-out caches would cover (see
// cache.go); kept here so both build modes expose the same constant.
const cacheSpan = 100000

// cachedString always reports a miss: the small-value string cache is
// compiled out under the zerodecimal_nostrcache build tag, and the constant
// false folds every cache probe away.
func cachedString(Decimal) (string, bool) {
	return "", false
}

// cachedValue always reports a miss (see cachedString).
func cachedValue(Decimal) (driver.Value, bool) {
	return nil, false
}
