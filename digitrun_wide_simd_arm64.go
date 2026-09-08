//go:build go1.27 && !go1.28 && goexperiment.simd && arm64

package zerodecimal

import (
	"math/bits"
	"simd/archsimd"
	"unsafe"
)

const digitRunWideEnabled = true

// digitRunLenWide scans a long ASCII digit run 16 bytes per iteration with
// arm64 NEON. Subtracting '0' maps digits to 0..9 and wraps every byte below
// '0' to a value above 9, so the horizontal maximum validates both range ends.
// A block containing a delimiter or malformed byte falls back to the scalar
// scanner to locate the exact byte.
//
// parseLongPlain calls this only when at least 28 bytes remain and the final
// byte is a digit, keeping setup off shorter and malformed trailing inputs.
func digitRunLenWide[T string | []byte](s T, i int) int {
	var b []byte
	switch s := any(s).(type) {
	case string:
		// Read-only view: LoadUint8x16 never retains or mutates its input.
		b = unsafe.Slice(unsafe.StringData(s), len(s))
	case []byte:
		b = s
	}

	j := i
	zero := archsimd.BroadcastUint8x16('0')
	for len(s)-j >= 32 {
		lo := archsimd.LoadUint8x16(b[j:]).Sub(zero)
		hi := archsimd.LoadUint8x16(b[j+16:]).Sub(zero)
		if lo.Max(hi).ReduceMax() > 9 {
			if lo.ReduceMax() > 9 {
				if m := nonDigitMask(le64(s[j:])); m != 0 {
					return j - i + bits.TrailingZeros64(m)>>3
				}
				m := nonDigitMask(le64(s[j+8:]))
				return j + 8 - i + bits.TrailingZeros64(m)>>3
			}
			if m := nonDigitMask(le64(s[j+16:])); m != 0 {
				return j + 16 - i + bits.TrailingZeros64(m)>>3
			}
			m := nonDigitMask(le64(s[j+24:]))
			return j + 24 - i + bits.TrailingZeros64(m)>>3
		}
		j += 32
	}
	for len(s)-j >= 16 {
		digits := archsimd.LoadUint8x16(b[j:]).Sub(zero)
		if digits.ReduceMax() > 9 {
			if m := nonDigitMask(le64(s[j:])); m != 0 {
				return j - i + bits.TrailingZeros64(m)>>3
			}
			m := nonDigitMask(le64(s[j+8:]))
			return j + 8 - i + bits.TrailingZeros64(m)>>3
		}
		j += 16
	}
	for len(s)-j >= 8 {
		if m := nonDigitMask(le64(s[j:])); m != 0 {
			return j - i + bits.TrailingZeros64(m)>>3
		}
		j += 8
	}
	for j < len(s) && s[j]-'0' <= 9 {
		j++
	}
	return j - i
}
