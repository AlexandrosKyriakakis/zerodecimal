//go:build fuzz

package zerodecimal

import "testing"

// FuzzDigitRun compares the selected scanner against a byte-at-a-time oracle,
// including arbitrary non-ASCII bytes and unaligned offsets in both APIs.
func FuzzDigitRun(f *testing.F) {
	f.Add("123456789012345678901234567890123456789", uint8(0))
	f.Add("1234567890123456.1234567890123456789", uint8(1))
	f.Add("123456789012345678901234567890\xff1234567", uint8(3))
	f.Fuzz(func(t *testing.T, text string, offset uint8) {
		if len(text) > maxParseLen {
			text = text[:maxParseLen]
		}
		start := int(offset) % (len(text) + 1)
		end := start
		for end < len(text) && text[end] >= '0' && text[end] <= '9' {
			end++
		}
		want := end - start
		if got := testDigitRunLen(text, start); got != want {
			t.Fatalf("string scanner: start=%d text=%q: got %d, want %d", start, text, got, want)
		}
		if got := testDigitRunLen([]byte(text), start); got != want {
			t.Fatalf("byte scanner: start=%d text=%q: got %d, want %d", start, text, got, want)
		}
	})
}

// FuzzSumPositivePrefix deliberately reaches the vectorized prefix before
// introducing arbitrary zeros, signs, precisions, and coefficient widths.
// Unstructured short inputs predominantly stop SIMD at the first operand.
func FuzzSumPositivePrefix(f *testing.F) {
	f.Add(uint16(0), uint64(1), uint8(4), uint16(31), []byte{})
	f.Add(uint16(225), ^uint64(0), uint8(19), uint16(129), aggregateFuzzEncode(NewFromInt(7)))
	f.Add(uint16(33), ^uint64(0), uint8(4), uint16(64), aggregateFuzzEncode(MustNew(-1, -4)))
	f.Add(uint16(225), ^uint64(0), uint8(0x80|19), uint16(250), aggregateFuzzEncode(RequireFromString(exactMaxString)))
	f.Fuzz(func(t *testing.T, size uint16, coefficient uint64, meta uint8, position uint16, suffix []byte) {
		xs := make([]Decimal, 32+int(size%226))
		prec := (meta & 0x7f) % (MaxPrec + 1)
		for i := range xs {
			hi := uint64(0)
			if meta&0x80 != 0 {
				hi = uint64(i + 1)
			}
			xs[i] = newDecimal(u128{hi: hi, lo: coefficient + uint64(i)}, false, prec)
		}
		if len(suffix) > 0 {
			copy(xs[int(position)%len(xs):], aggregateFuzzDecode(suffix))
		}
		requireAggregateSumOracle(t, xs)
	})
}
