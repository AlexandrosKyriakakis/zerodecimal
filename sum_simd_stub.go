//go:build !go1.27 || go1.28 || !goexperiment.simd || !amd64

package zerodecimal

// sumSIMDPrefix reports that no experimental SIMD implementation is present.
// Keeping the stub tiny lets the compiler erase the call from ordinary builds.
func sumSIMDPrefix(Decimal, []Decimal) (Decimal, int, bool) {
	return Decimal{}, 0, false
}
