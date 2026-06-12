package zerodecimal

import (
	"strconv"
	"unsafe"
)

// digitPairs is the two-digits-per-entry ASCII lookup table: value i in
// 0..99 occupies bytes 2i and 2i+1 ("00", "01", ..., "99"), so the formatter
// emits two digits per division step instead of one.
const digitPairs = "00010203040506070809" +
	"10111213141516171819" +
	"20212223242526272829" +
	"30313233343536373839" +
	"40414243444546474849" +
	"50515253545556575859" +
	"60616263646566676869" +
	"70717273747576777879" +
	"80818283848586878889" +
	"90919293949596979899"

// appendCanonical appends the canonical decimal rendering of d to dst and
// returns the extended slice. Canonical form: optional leading minus, integer
// digits without leading zeros (a single "0" when the integer part is empty —
// "0.5", never ".5"), then the fractional digits with trailing zeros trimmed
// and no trailing point; zero is "0". Digits are written right to left into a
// stack scratch buffer — [48]byte covers the 41-byte worst case of sign plus
// 39 digits plus point — and reach dst with a single append.
func appendCanonical(dst []byte, d Decimal) []byte {
	var scratch [48]byte
	pos := len(scratch)
	end := len(scratch)

	q, r := divmod128Pow10(d.coef, d.prec)

	// Fractional part. r == 0 covers both prec == 0 and an all-zero
	// fraction, which trims away entirely, point included.
	if r != 0 {
		pos = writePadded(scratch[:], pos, r, int(d.prec))
		// r != 0 guarantees a nonzero digit, so the trim loop terminates.
		for scratch[end-1] == '0' {
			end--
		}
		pos--
		scratch[pos] = '.'
	}

	// Integer part, least-significant 19-digit chunk first, so only the
	// final (most significant) chunk needs leading-zero suppression.
	if q.isZero() {
		pos--
		scratch[pos] = '0'
	} else {
		for {
			next, chunk := divmod128Pow10(q, 19)
			if next.isZero() {
				pos = writeUnpadded(scratch[:], pos, chunk)
				break
			}
			pos = writePadded(scratch[:], pos, chunk, 19)
			q = next
		}
	}

	if d.neg {
		pos--
		scratch[pos] = '-'
	}
	return append(dst, scratch[pos:end]...)
}

// writePadded writes exactly n decimal digits of v right to left into buf,
// ending just before pos and zero-padding on the left, and returns the index
// of the first digit written.
//
// PRECONDITIONS (not checked): v < 10^n, 1 ≤ n ≤ 19, and pos ≥ n.
func writePadded(buf []byte, pos int, v uint64, n int) int {
	for n >= 2 {
		pair := v % 100 * 2
		v /= 100
		pos -= 2
		buf[pos] = digitPairs[pair]
		buf[pos+1] = digitPairs[pair+1]
		n -= 2
	}
	if n == 1 {
		pos--
		buf[pos] = byte('0' + v%10)
	}
	return pos
}

// writeUnpadded writes the significant decimal digits of v right to left
// into buf without leading zeros, ending just before pos, and returns the
// index of the first digit written.
//
// PRECONDITIONS (not checked): 0 < v < 10^20 and pos ≥ 20.
func writeUnpadded(buf []byte, pos int, v uint64) int {
	for v >= 100 {
		pair := v % 100 * 2
		v /= 100
		pos -= 2
		buf[pos] = digitPairs[pair]
		buf[pos+1] = digitPairs[pair+1]
	}
	if v >= 10 {
		pair := v * 2
		pos -= 2
		buf[pos] = digitPairs[pair]
		buf[pos+1] = digitPairs[pair+1]
		return pos
	}
	pos--
	buf[pos] = byte('0' + v)
	return pos
}

// String returns the canonical decimal representation of d (the exact form
// appendCanonical produces). Values inside the small-value cache window
// return the precomputed string with zero allocations; everything else costs
// exactly one string allocation.
func (d Decimal) String() string {
	if s, ok := cachedString(d); ok {
		return s
	}
	var buf [48]byte
	return string(appendCanonical(buf[:0], d))
}

// AppendText appends the canonical decimal representation of d to b and
// returns the extended slice, matching the encoding.TextAppender shape. The
// error is always nil. It allocates only if b lacks capacity.
func (d Decimal) AppendText(b []byte) ([]byte, error) {
	return appendCanonical(b, d), nil
}

// InexactFloat64 returns the float64 nearest to d. The conversion goes
// through strconv.ParseFloat over the canonical string for correct
// round-to-nearest-even; every Decimal is finite and |d| < 2^128 stays well
// inside float64 range, so the parse cannot fail.
func (d Decimal) InexactFloat64() float64 {
	var buf [48]byte
	b := appendCanonical(buf[:0], d)
	// The unsafe string view keeps buf on the stack: ParseFloat leaks its
	// argument only into a potential error value, which canonical input
	// never produces (and is discarded regardless), so nothing retains the
	// buffer past this call — a plain string(b) conversion would
	// heap-allocate solely to feed that dead error path.
	f, _ := strconv.ParseFloat(unsafe.String(&b[0], len(b)), 64)
	return f
}
