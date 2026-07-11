//go:build !zerodecimal_strcache || zerodecimal_nostrcache

package zerodecimal

import "database/sql/driver"

// cacheSpan documents the window the compiled-out caches would cover (see
// cache.go); kept here so both build modes expose the same constant.
const cacheSpan = 100000

// strCacheEnabled records at compile time that the small-value string cache
// is compiled out of this build. The cache is opt-in through
// zerodecimal_strcache; zerodecimal_nostrcache remains an overriding explicit
// off switch for compatibility with existing build configurations.
const strCacheEnabled = false

// cachedString always reports a miss: the small-value string cache is absent
// by default and when zerodecimal_nostrcache is set. The constant false lets
// callers' cache probes fold away.
func cachedString(Decimal) (string, bool) {
	return "", false
}

// cachedValue always reports a miss (see cachedString).
func cachedValue(Decimal) (driver.Value, bool) {
	return nil, false
}
