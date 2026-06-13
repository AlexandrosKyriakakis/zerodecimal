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

// scratchLen is the size of the right-to-left formatting scratch buffer and
// scratchMask its index mask. The widest rendering — sign, 39 coefficient
// digits, decimal point — is 41 bytes; sizing the buffer to the next power
// of two lets every cursor index be masked with scratchMask, which proves it
// in range to the compiler so the digit writers carry no bounds checks (the
// pow10Tab[k&31] pattern). Under the documented cursor preconditions every
// mask is the identity.
const (
	scratchLen  = 64
	scratchMask = scratchLen - 1
)

// appendCanonical appends the canonical decimal rendering of d to dst and
// returns the extended slice. Canonical form: optional leading minus, integer
// digits without leading zeros (a single "0" when the integer part is empty —
// "0.5", never ".5"), then the fractional digits with trailing zeros trimmed
// and no trailing point; zero is "0". Digits are written right to left into a
// stack scratch buffer and reach dst with a single append.
func appendCanonical(dst []byte, d Decimal) []byte {
	var scratch [scratchLen]byte
	pos := len(scratch)
	end := len(scratch)

	// Precision 0 needs no split at all; skipping the divmod call keeps the
	// pure-integer rendering path call-free up to the digit writers.
	q, r := d.coef, uint64(0)
	if d.prec != 0 {
		// Open-code the one-limb dispatch the non-inlinable divmod128Pow10
		// wrapper performs (div10.go:106-107): the dominant coef.hi == 0 case
		// inlines divmod64Pow10 directly, and the two-limb shape jumps straight
		// to the outlined Slow path — both saving the dispatcher call. q == coef
		// here, so q.hi == 0 already reconstructs the fast-path quotient.
		if q.hi == 0 {
			q.lo, r = divmod64Pow10(q.lo, d.prec)
		} else {
			q, r = divmod128Pow10Slow(q, d.prec)
		}
	}

	// Fractional part. r == 0 covers both prec == 0 and an all-zero
	// fraction, which trims away entirely, point included.
	if r != 0 {
		pos = writePadded(&scratch, pos, r, int(d.prec))
		// r != 0 guarantees a nonzero digit, so the trim loop terminates.
		for scratch[(end-1)&scratchMask] == '0' {
			end--
		}
		pos--
		scratch[pos&scratchMask] = '.'
	}

	pos = writeIntPart(&scratch, pos, q)

	if d.neg {
		pos--
		scratch[pos&scratchMask] = '-'
	}
	// 0 ≤ pos ≤ end ≤ len(scratch) holds on every path (at most 41 of the
	// 64 bytes are consumed); restating it in this checkable form lets the
	// compiler drop the slice bounds check, and the impossible branch keeps
	// dst untouched.
	//nolint:gosec // deliberate: the uint view sends negative cursors above len, failing the guard
	if uint(end) > uint(len(scratch)) || uint(pos) > uint(end) {
		return dst
	}
	return append(dst, scratch[pos:end]...)
}

// writeIntPart writes the decimal digits of q right to left into buf, ending
// just before pos, without leading zeros (a single "0" when q is zero), and
// returns the index of the first digit written. It walks least-significant
// 19-digit chunks first, so only the final (most significant) chunk needs
// leading-zero suppression.
//
// PRECONDITION (not checked): pos leaves room for up to 39 digits. Stores
// are masked with scratchMask — in range by the precondition, and proven in
// range to the compiler — so a violation would wrap within buf, not panic.
func writeIntPart(buf *[scratchLen]byte, pos int, q u128) int {
	// One-limb fast path: any uint64 is below 10^20, writeUnpadded's exact
	// domain, so the dominant case needs no division at all.
	if q.hi == 0 {
		if q.lo == 0 {
			pos--
			buf[pos&scratchMask] = '0'
			return pos
		}
		return writeUnpadded(buf, pos, q.lo)
	}
	for q.hi != 0 {
		var chunk uint64
		q, chunk = divmod128Pow10Slow(q, 19)
		if q.isZero() {
			return writeUnpadded(buf, pos, chunk)
		}
		pos = writePadded(buf, pos, chunk, 19)
	}
	// The remaining most significant digits fit one limb (and are nonzero,
	// or the loop would have returned above).
	return writeUnpadded(buf, pos, q.lo)
}

// writePadded writes exactly n decimal digits of v right to left into buf,
// ending just before pos and zero-padding on the left, and returns the index
// of the first digit written.
//
// PRECONDITIONS (not checked): v < 10^n, 1 ≤ n ≤ 19, and pos ≥ n. Stores are
// masked with scratchMask (see writeIntPart).
func writePadded(buf *[scratchLen]byte, pos int, v uint64, n int) int {
	for n >= 2 {
		pair := v % 100 * 2
		v /= 100
		pos -= 2
		buf[pos&scratchMask] = digitPairs[pair]
		buf[(pos+1)&scratchMask] = digitPairs[pair+1]
		n -= 2
	}
	if n == 1 {
		pos--
		buf[pos&scratchMask] = byte('0' + v%10)
	}
	return pos
}

// writeUnpadded writes the significant decimal digits of v right to left
// into buf without leading zeros, ending just before pos, and returns the
// index of the first digit written.
//
// PRECONDITIONS (not checked): 0 < v < 10^20 and pos ≥ 20. Stores are masked
// with scratchMask (see writeIntPart).
func writeUnpadded(buf *[scratchLen]byte, pos int, v uint64) int {
	for v >= 100 {
		pair := v % 100 * 2
		v /= 100
		pos -= 2
		buf[pos&scratchMask] = digitPairs[pair]
		buf[(pos+1)&scratchMask] = digitPairs[pair+1]
	}
	if v >= 10 {
		pair := v * 2
		pos -= 2
		buf[pos&scratchMask] = digitPairs[pair]
		buf[(pos+1)&scratchMask] = digitPairs[pair+1]
		return pos
	}
	pos--
	buf[pos&scratchMask] = byte('0' + v)
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

// zeroRun is the padding source for AppendFixed's trailing zeros: 32 zeros,
// a power of two, so the final partial pad masks its length into provably
// valid slice range, and any padding within MaxPrec costs a single append.
const zeroRun = "00000000000000000000000000000000"

// StringFixed returns d rounded to places fractional digits (ties away from
// zero, like Round) and formatted with EXACTLY places fractional digits —
// zero-padded, never trimmed — so StringFixed(3) of 1.5 is "1.500". With
// places 0 the result has no decimal point. It costs one string allocation.
func (d Decimal) StringFixed(places uint8) string {
	var buf [48]byte
	return string(d.AppendFixed(buf[:0], places))
}

// AppendFixed appends the StringFixed rendering of d to b and returns the
// extended slice. It allocates only if b lacks capacity.
func (d Decimal) AppendFixed(b []byte, places uint8) []byte {
	d = d.Round(places)
	// After rounding d.prec ≤ places, so the fraction is the full d.prec
	// digits of the remainder followed by places - d.prec padding zeros.
	var scratch [scratchLen]byte
	pos := len(scratch)

	// Open-code the divmod128Pow10 dispatch (see appendCanonical). Unlike that
	// path, d.prec can legitimately be 0 here, so the k == 0 short-circuit is
	// preserved up front (the table magic is meaningless for k == 0).
	q, r := d.coef, uint64(0)
	if d.prec > 0 {
		if q.hi == 0 {
			q.lo, r = divmod64Pow10(q.lo, d.prec)
		} else {
			q, r = divmod128Pow10Slow(q, d.prec)
		}
		pos = writePadded(&scratch, pos, r, int(d.prec))
	}
	if places > 0 {
		pos--
		scratch[pos&scratchMask] = '.'
	}
	pos = writeIntPart(&scratch, pos, q)
	if d.neg {
		pos--
		scratch[pos&scratchMask] = '-'
	}
	// 0 ≤ pos ≤ len(scratch) holds on every path (see appendCanonical); the
	// restatement drops the slice bounds check, and the impossible branch
	// skips straight to the padding.
	//nolint:gosec // deliberate: the uint view sends negative cursors above len, failing the guard
	if uint(pos) <= uint(len(scratch)) {
		b = append(b, scratch[pos:]...)
	}

	// Rounding capped d.prec at places, so pad ≥ 0; after the run loop it is
	// below len(zeroRun), making the closing mask the identity — its only
	// job is proving the slice in range.
	pad := int(places) - int(d.prec)
	for pad >= len(zeroRun) {
		b = append(b, zeroRun...)
		pad -= len(zeroRun)
	}
	return append(b, zeroRun[:pad&(len(zeroRun)-1)]...)
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
	// heap-allocate solely to feed that dead error path. SliceData rather
	// than &b[0] spares the formatter's one remaining bounds check (b is
	// never empty: zero renders as "0").
	f, _ := strconv.ParseFloat(unsafe.String(unsafe.SliceData(b), len(b)), 64)
	return f
}
