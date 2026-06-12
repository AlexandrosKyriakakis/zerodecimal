//go:build !zerodecimal_nostrcache

package zerodecimal

import "database/sql/driver"

// cacheSpan is the magnitude of the largest cached value in hundredths: the
// caches cover -1000.00 .. +1000.00 in steps of 0.01, indexed by
// cacheSpan + cents.
const cacheSpan = 100000

// strCacheEnabled records at compile time that the small-value string cache
// is present in this build; the zerodecimal_nostrcache tag selects the
// constant-false twin in cache_off.go.
const strCacheEnabled = true

// stringCache maps every hundredths offset in [-cacheSpan, +cacheSpan] to
// its canonical string. It is built in init through appendCanonical, so a
// cached result is byte-identical to a computed one by construction; the
// ~8 MB are paid once at startup, leaving no first-hit jitter. Build with
// -tags zerodecimal_nostrcache to compile the caches out.
var stringCache [2*cacheSpan + 1]string

// valueCache boxes the exact strings of stringCache as driver.Value, so SQL
// Value calls on cached decimals allocate nothing either.
var valueCache [2*cacheSpan + 1]driver.Value

// init renders every cached entry through the canonical formatter and shares
// each resulting string between both caches.
func init() {
	var buf [16]byte
	for i := range stringCache {
		cents := int64(i) - cacheSpan
		neg := cents < 0
		if neg {
			cents = -cents
		}
		//nolint:gosec // cents ≥ 0 after the negation above
		d := newDecimal(u128{lo: uint64(cents)}, neg, 2)
		s := string(appendCanonical(buf[:0], d))
		stringCache[i] = s
		valueCache[i] = s
	}
}

// cachedString returns the canonical string of d from the small-value cache,
// or ok == false when d lies outside the cached window.
func cachedString(d Decimal) (string, bool) {
	idx, ok := cacheIndex(d)
	if !ok {
		return "", false
	}
	return stringCache[idx], true
}

// cachedValue returns the canonical string of d boxed as a driver.Value,
// sharing the cached string's backing, or ok == false when d lies outside
// the cached window.
func cachedValue(d Decimal) (driver.Value, bool) {
	idx, ok := cacheIndex(d)
	if !ok {
		return nil, false
	}
	return valueCache[idx], true
}

// cacheIndex maps d to its cache index. A hit requires the coefficient to
// fit one limb, prec ≤ 2, and the value scaled to hundredths to lie within
// ±cacheSpan. The lo > cacheSpan pre-check also keeps the 10^(2-prec)
// scaling multiply from wrapping, since any larger coefficient can only
// scale further out of range.
func cacheIndex(d Decimal) (uint64, bool) {
	if d.coef.hi != 0 || d.prec > 2 || d.coef.lo > cacheSpan {
		return 0, false
	}
	scaled := d.coef.lo * pow10u64[(2-d.prec)&31]
	if scaled > cacheSpan {
		return 0, false
	}
	if d.neg {
		return cacheSpan - scaled, true
	}
	return cacheSpan + scaled, true
}
