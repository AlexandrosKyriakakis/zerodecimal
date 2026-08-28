//go:build !go1.27 || go1.28 || !goexperiment.simd || (!amd64 && !arm64)

package zerodecimal

const digitRunWideEnabled = false

// digitRunLenWide is the portable fallback used by direct tests. Production
// scalar builds compile the false digitRunWideEnabled branch out entirely.
func digitRunLenWide[T string | []byte](s T, i int) int {
	return digitRunLen(s, i)
}
