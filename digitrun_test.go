package zerodecimal

import "testing"

func TestDigitRunLen(t *testing.T) {
	bad := [...]byte{0, '/', '.', ':', 0x80, 0xff}
	for n := 0; n <= maxParseLen; n++ {
		for stop := 0; stop <= n; stop++ {
			for _, c := range bad {
				b := make([]byte, n+2)
				for i := range b {
					b[i] = '7'
				}
				if stop < n {
					b[2+stop] = c
				}

				if got := testDigitRunLen(b, 2); got != stop {
					t.Fatalf("digit run ([]byte, 2) = %d, want %d (len=%d, bad=%#x)", got, stop, n, c)
				}
				if got := testDigitRunLen(string(b), 2); got != stop {
					t.Fatalf("digit run (string, 2) = %d, want %d (len=%d, bad=%#x)", got, stop, n, c)
				}
			}
		}
	}
}

func testDigitRunLen[T string | []byte](s T, i int) int {
	if digitRunWideEnabled && len(s)-i >= 28 && s[len(s)-1]-'0' <= 9 {
		return digitRunLenWide(s, i)
	}
	return digitRunLen(s, i)
}
