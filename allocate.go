package zerodecimal

import "math/bits"

const (
	// apportionSmallShares sizes the stack buffer for a few weights, the
	// common case, which is much cheaper to zero than the full one.
	apportionSmallShares = 8
	// apportionStackShares is the most weights Allocate ranks in a stack
	// buffer; more take one scratch allocation.
	apportionStackShares = 64
)

// Allocate splits total into len(weights) shares proportional to weights.
// Each share is rounded to places fractional digits, and the shares sum
// exactly to total (the largest-remainder, or Hamilton, method). Every share
// starts at its exact share truncated toward zero; the leftover units of
// 10^-places go one each to the shares with the largest discarded remainders,
// ties to the lower index. A negative total is allocated by magnitude and
// negated. Weights may have any precision, and a zero weight gets a zero
// share. A nonzero share carries precision places; a zero share is canonical
// Decimal{}.
//
// The shares overwrite dst[:len(weights)], and the result is returned; dst
// grows only when its capacity is too small. dst may be weights itself,
// computing the shares in place, but must not otherwise overlap it.
// Allocate does not allocate when cap(dst) ≥ len(weights) and len(weights)
// ≤ 64; with more weights, ranking the remainders takes one scratch
// allocation, also when the ranking settles an ErrOverflow.
//
// Validation errors take precedence in this order: ErrPrecOutOfRange when
// places > MaxPrec, ErrNegativeWeight for any negative weight,
// ErrDivideByZero when the weights sum to zero or there are none, ErrInexact
// when total has nonzero digits beyond places, and ErrOverflow when a share
// does not fit a 128-bit coefficient at places. A share overflows only if
// total has at least 2^128 units of 10^-places, as no share exceeds total.
// On error the result is dst[:0] and dst is left unwritten.
func Allocate(dst []Decimal, total Decimal, weights []Decimal, places uint8) ([]Decimal, error) {
	if places > MaxPrec {
		return dst[:0], ErrPrecOutOfRange
	}
	if len(weights) == 0 {
		return dst[:0], ErrDivideByZero
	}
	// The exact aggregate aligns every weight to the greatest weight precision
	// and sums negative weights apart, so one call yields the weight sum W,
	// its precision and the negative-weight test.
	agg := accumulateAggregate(weights[0], weights[1:])
	if !u256Zero(agg.neg) {
		return dst[:0], ErrNegativeWeight
	}
	if u256Zero(agg.pos) {
		return dst[:0], ErrDivideByZero
	}
	units, err := apportionUnits(total, places)
	if err != nil {
		return dst[:0], err
	}
	if u256Zero(units) {
		dst = apportionResize(dst, len(weights))
		clear(dst)
		return dst, nil
	}
	p := apportionment{units: units, sum: agg.pos, prec: agg.prec, places: places, neg: total.neg}
	var edge bool
	if units.d2 != 0 {
		var over bool
		if over, edge = p.risk(weights); over {
			return dst[:0], ErrOverflow
		}
	}

	// Only shares with a nonzero remainder are ranked. Two stack tiers keep
	// the buffer a few shares need cheap to zero.
	var ranked []apportionRank
	switch n := len(weights); {
	case n <= apportionSmallShares:
		var buf [apportionSmallShares]apportionRank
		ranked = buf[:0]
	case n <= apportionStackShares:
		var buf [apportionStackShares]apportionRank
		ranked = buf[:0]
	default:
		ranked = make([]apportionRank, 0, n)
	}
	if edge && p.edgeTakesUnit(weights, ranked) {
		return dst[:0], ErrOverflow
	}
	dst = apportionResize(dst, len(weights))
	ranked, given := p.shares(dst, weights, ranked)
	// The truncated shares sum to at most units, and the leftover is below
	// len(ranked) (see apportionChosen), so it fits one limb.
	left := aggregateSub256(units, given)
	for _, r := range apportionChosen(ranked, left.d0) {
		dst[r.i] = Decimal{coef: inc128(dst[r.i].coef), neg: total.neg, prec: places}
	}
	return dst, nil
}

// Split splits total into n equal shares at places with Allocate's
// guarantees: the shares sum exactly to total, each differs from total/n by
// less than 10^-places, and the leftover units go to the lowest indices. It
// equals Allocate over n equal weights, without a weights slice. Split does
// not allocate when cap(dst) ≥ n.
//
// Validation errors take precedence in this order: ErrPrecOutOfRange when
// places > MaxPrec, ErrDivideByZero when n ≤ 0, then ErrInexact and
// ErrOverflow as in Allocate. On error the result is dst[:0] and dst is left
// unwritten.
func Split(dst []Decimal, total Decimal, n int, places uint8) ([]Decimal, error) {
	if places > MaxPrec {
		return dst[:0], ErrPrecOutOfRange
	}
	if n <= 0 {
		return dst[:0], ErrDivideByZero
	}
	units, err := apportionUnits(total, places)
	if err != nil {
		return dst[:0], err
	}
	var q u128
	var r uint64
	if units.d2 == 0 {
		q, r = quoRem64(units.lo128(), uint64(n))
	} else {
		// The first share, q plus one unit when a remainder is left, is the
		// largest and must fit 128 bits.
		wq, wr := aggregateDiv256by64(units, uint64(n))
		if !wq.isZeroUpper() || (wr != 0 && wq.lo128() == u128{hi: ^uint64(0), lo: ^uint64(0)}) {
			return dst[:0], ErrOverflow
		}
		q, r = wq.lo128(), wr
	}
	dst = apportionResize(dst, n)
	share := newDecimal(q, total.neg, places)
	// q+1 is a share only when a remainder is left, so it cannot wrap then.
	up := Decimal{coef: inc128(q), neg: total.neg, prec: places}
	for i := range dst {
		if r == 0 {
			dst[i] = share
			continue
		}
		dst[i] = up
		r--
	}
	return dst, nil
}

// apportionment is what every share's computation needs: total as a count of
// 10^-places units (below 2^128·10^MaxPrec < 2^192), the weight sum W aligned
// to precision prec, and the places and sign of the shares.
type apportionment struct {
	units  u256
	sum    u256
	prec   uint8
	places uint8
	neg    bool
}

// apportionRank is a share that competes for a leftover unit: its index and
// its discarded remainder, the numerator of a fraction over W.
type apportionRank struct {
	rem u256
	i   int
}

// shares writes the truncated shares ⌊units·a/W⌋ for the aligned weights a
// into dst, or only ranks them when dst is nil. It returns the ranks of the
// shares with a nonzero remainder and the sum of the truncated shares.
//
// PRECONDITION (not checked): no truncated share reaches 2^128 (risk).
func (p *apportionment) shares(dst, weights []Decimal, ranked []apportionRank) ([]apportionRank, u256) {
	if p.sum.d1|p.sum.d2|p.sum.d3 == 0 {
		return p.narrow(dst, weights, ranked)
	}
	return p.wide(dst, weights, ranked)
}

// narrow is shares for W < 2^64, where every aligned weight a fits one limb.
// It splits units = uq·W + ur once, so each share is uq·a + ⌊ur·a/W⌋ with
// remainder ur·a mod W, and ur·a < W² fits 128 bits: one hardware divide
// while it fits a limb, otherwise one reciprocal divide by the normalized W.
// uq·a never exceeds its share, so it fits 128 bits.
func (p *apportionment) narrow(dst, weights []Decimal, ranked []apportionRank) ([]apportionRank, u256) {
	w := p.sum.d0
	var uq u128
	var ur uint64
	if p.units.d2 == 0 {
		uq, ur = quoRem64(p.units.lo128(), w)
	} else {
		// uq ≤ uq·a ≤ a truncated share for any nonzero weight a, and risk
		// keeps every truncated share below 2^128.
		q, r := aggregateDiv256by64(p.units, w)
		uq, ur = q.lo128(), r
	}
	//nolint:gosec // bits.LeadingZeros64 returns 0..63, which always fits uint
	s := uint(bits.LeadingZeros64(w))
	dn := w << s
	var v uint64
	if w > 1<<32 {
		// ur·a ≤ (W-1)·W passes 64 bits only from here on: derive the
		// reciprocal once rather than a 128/64 divide per share.
		v, _ = bits.Div64(^dn, ^uint64(0), dn)
	}
	var given u256
	for i, d := range weights {
		a := d.coef.lo * pow10u64[(p.prec-d.prec)&31] // a ≤ W < 2^64, so exact
		hi, lo := bits.Mul64(ur, a)
		var f, r uint64
		if hi == 0 {
			f, r = lo/w, lo%w
		} else {
			// ur·a < W·2^64, so the normalized high limb is below dn.
			f, r = div2by1(hi<<s|lo>>(64-s), lo<<s, dn, v)
			r >>= s
		}
		b, _ := mul128by64(uq, a)
		b, _ = add128(b, u128{lo: f})
		given = aggregateAdd128(given, b)
		if dst != nil {
			dst[i] = newDecimal(b, p.neg, p.places)
		}
		if r != 0 {
			ranked = append(ranked, apportionRank{rem: u256{d0: r}, i: i})
		}
	}
	return ranked, given
}

// wide is shares for W ≥ 2^64. Below 2^128 it splits units like narrow,
// with uq < 2^128 as units < 2^192, and divides ur·a < W² ≤ W·2^128 with
// div256by128. From 2^128 on it divides units·a < 2^384 by W directly, below
// W·2^128 because no share reaches 2^128.
func (p *apportionment) wide(dst, weights []Decimal, ranked []apportionRank) ([]apportionRank, u256) {
	w := p.sum
	wideSum := !w.isZeroUpper()
	var uq, ur u128
	if !wideSum {
		uq, ur = div256by128(p.units, w.lo128()) // hi128(units) < 2^64 ≤ W
	}
	var given u256
	for i, d := range weights {
		var b u128
		var r u256
		if wideSum {
			b, r = div384by256(p.product(d), w)
		} else {
			a, _ := mul128by64(d.coef, pow10u64[(p.prec-d.prec)&31]) // a ≤ W < 2^128
			f, rem := div256by128(mulToU256(ur, a), w.lo128())
			// uq·a + f is the truncated share, below 2^128, so the product's
			// upper half is zero.
			b, _ = add128(mulToU256(uq, a).lo128(), f)
			r = u256{d0: rem.lo, d1: rem.hi}
		}
		given = aggregateAdd128(given, b)
		if dst != nil {
			dst[i] = newDecimal(b, p.neg, p.places)
		}
		if !u256Zero(r) {
			ranked = append(ranked, apportionRank{rem: r, i: i})
		}
	}
	return ranked, given
}

// risk decides whether a share overflows 128 bits from the largest weight a
// alone, whose truncated share ⌊units·a/W⌋ is the largest: from 2^128 on it
// overflows (over); at exactly 2^128-1 it overflows only if it takes a
// leftover unit (edge); below that no share can overflow, a share being at
// most one unit above its truncated value.
func (p *apportionment) risk(weights []Decimal) (over, edge bool) {
	x := p.product(Max(weights[0], weights[1:]...))
	if cmp384(x, p.shareBound(false)) >= 0 {
		return true, false
	}
	return false, cmp384(x, p.shareBound(true)) >= 0
}

// edgeTakesUnit settles the case risk leaves open, a largest truncated share
// of exactly 2^128-1: some share overflows if, and only if, a share at
// 2^128-1 takes a leftover unit. It ranks the shares without writing them and
// tests the chosen ones, so an overflow leaves dst unwritten.
func (p *apportionment) edgeTakesUnit(weights []Decimal, ranked []apportionRank) bool {
	ranked, given := p.shares(nil, weights, ranked)
	left := aggregateSub256(p.units, given)
	bound := p.shareBound(true)
	for _, r := range apportionChosen(ranked, left.d0) {
		if cmp384(p.product(weights[r.i]), bound) >= 0 {
			return true
		}
	}
	return false
}

// product returns units·a for the weight d aligned to precision prec: a
// 384-bit value, as units and a are each below 2^192.
func (p *apportionment) product(d Decimal) [6]uint64 {
	a2, a1, a0 := mul128by64to192(d.coef, pow10u64[(p.prec-d.prec)&31])
	return mul192to384([3]uint64{p.units.d0, p.units.d1, p.units.d2}, [3]uint64{a0, a1, a2})
}

// shareBound returns W·2^128, or W·(2^128-1) when minusOne: a truncated share
// ⌊units·a/W⌋ reaches 2^128 (or 2^128-1) exactly when units·a reaches it.
// W < 2^255 keeps both within 384 bits.
func (p *apportionment) shareBound(minusOne bool) [6]uint64 {
	w := p.sum
	t := [6]uint64{0, 0, w.d0, w.d1, w.d2, w.d3}
	if minusOne {
		var b uint64
		t[0], b = bits.Sub64(t[0], w.d0, 0)
		t[1], b = bits.Sub64(t[1], w.d1, b)
		t[2], b = bits.Sub64(t[2], w.d2, b)
		t[3], b = bits.Sub64(t[3], w.d3, b)
		t[4], b = bits.Sub64(t[4], 0, b)
		t[5], _ = bits.Sub64(t[5], 0, b)
	}
	return t
}

// apportionChosen returns the entries of ranked that take the left leftover
// units: the largest remainders, ties to the lower index. The leftover is the
// sum of the remainders divided by W and each is below W, so left <
// len(ranked): some ranked share always keeps its truncated value, no
// zero-remainder share is ever chosen, and a chosen share stays at most
// units. When left is more than half of len(ranked), it selects the shares
// that keep their value instead, which takes fewer steps. It reorders ranked.
func apportionChosen(ranked []apportionRank, left uint64) []apportionRank {
	if left == 0 {
		return nil
	}
	m := uint64(len(ranked))
	invert := left > m-left
	k := left
	if invert {
		k = m - left
	}
	cut := apportionSelect(ranked, k, invert)
	if invert {
		return ranked[:cut]
	}
	return ranked[cut:]
}

// apportionSelect moves the k entries of ranked that come first by
// apportionAbove, or last when invert, to its end and returns where they
// start. It is a partial heapsort: O(len(ranked) + k·log len(ranked))
// comparisons at worst, in place.
func apportionSelect(ranked []apportionRank, k uint64, invert bool) int {
	n := len(ranked)
	for root := n/2 - 1; root >= 0; root-- {
		apportionSift(ranked, root, n, invert)
	}
	for ; k > 0; k-- {
		n--
		ranked[0], ranked[n] = ranked[n], ranked[0]
		apportionSift(ranked, 0, n, invert)
	}
	return n
}

// apportionSift restores the heap below ranked[root] within ranked[:n]: each
// parent comes before its children by apportionAbove, or after when invert.
func apportionSift(ranked []apportionRank, root, n int, invert bool) {
	for {
		child := 2*root + 1
		if child >= n {
			return
		}
		if child+1 < n && apportionAbove(&ranked[child+1], &ranked[child]) != invert {
			child++
		}
		if apportionAbove(&ranked[root], &ranked[child]) != invert {
			return
		}
		ranked[root], ranked[child] = ranked[child], ranked[root]
		root = child
	}
}

// apportionAbove reports whether share x takes a leftover unit before share
// y: a larger remainder, or an equal one at a lower index. Distinct shares
// never tie.
func apportionAbove(x, y *apportionRank) bool {
	if c := cmp256(x.rem, y.rem); c != 0 {
		return c > 0
	}
	return x.i < y.i
}

// apportionUnits returns |total| as a count of 10^-places units, below
// 2^128·10^MaxPrec < 2^192, or ErrInexact when total has nonzero digits
// beyond places.
func apportionUnits(total Decimal, places uint8) (u256, error) {
	if total.prec > places {
		q, r := divmod128Pow10(total.coef, total.prec-places)
		if r != 0 {
			return u256{}, ErrInexact
		}
		return u256{d0: q.lo, d1: q.hi}, nil
	}
	d2, d1, d0 := mul128by64to192(total.coef, pow10u64[(places-total.prec)&31])
	return u256{d0: d0, d1: d1, d2: d2}, nil
}

// apportionResize returns dst resliced to n shares, or a new slice when its
// capacity is too small.
func apportionResize(dst []Decimal, n int) []Decimal {
	if cap(dst) < n {
		return make([]Decimal, n)
	}
	return dst[:n]
}

// cmp384 compares two 384-bit values in little-endian limbs.
func cmp384(a, b [6]uint64) int {
	for k := 5; k >= 0; k-- {
		if a[k] != b[k] {
			if a[k] < b[k] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// mul192to384 returns the 384-bit product of two 192-bit values, all in
// little-endian limbs: the schoolbook method, whose every step x·y + z + c
// stays below 2^128.
func mul192to384(x, y [3]uint64) [6]uint64 {
	var z [6]uint64
	for i := range 3 {
		var carry uint64
		for j := range 3 {
			hi, lo := bits.Mul64(x[i], y[j])
			var c uint64
			lo, c = bits.Add64(lo, z[i+j], 0)
			hi += c
			lo, c = bits.Add64(lo, carry, 0)
			hi += c
			z[i+j], carry = lo, hi
		}
		z[i+3] = carry
	}
	return z
}

// div384by256 returns ⌊n/d⌋ and n mod d for a 384-bit n in little-endian
// limbs and a divisor d ≥ 2^128: Knuth's Algorithm D (TAOCP 4.3.1) with
// 64-bit digits, d normalized to m = 3 or 4 limbs and two quotient digits.
// The trial digit is the 3-by-2 quotient of the window's top three limbs by
// d's top two (div3by2). It is never below the true digit and at most one
// above it, because the normalized top two limbs are at least 2^127, so one
// add-back corrects it. When the window's top two limbs equal d's, div3by2's
// precondition fails, but the true digit is then exactly 2^64-1.
//
// PRECONDITIONS (not checked): d.d2|d.d3 != 0, and n < d·2^128, so that the
// quotient fits 128 bits.
func div384by256(n [6]uint64, d u256) (u128, u256) {
	m := 3
	if d.d3 != 0 {
		m = 4
	}
	dv := [4]uint64{d.d0, d.d1, d.d2, d.d3}
	//nolint:gosec // bits.LeadingZeros64 returns 0..63, which always fits uint
	s := uint(bits.LeadingZeros64(dv[m-1]))
	var dn [4]uint64
	for k := m - 1; k > 0; k-- {
		dn[k] = dv[k]<<s | dv[k-1]>>(64-s)
	}
	dn[0] = dv[0] << s
	// n·2^s < dn·2^128 < 2^(64·(m+2)): the shifted dividend fits un[0..m+1],
	// the bits shifted out of n[5] are zero, and the top m limbs of the first
	// window, un[2..m+1], are below dn.
	var un [6]uint64
	for k := 5; k > 0; k-- {
		un[k] = n[k]<<s | n[k-1]>>(64-s)
	}
	un[0] = n[0] << s

	top := u128{hi: dn[m-1], lo: dn[m-2]}
	var q [2]uint64
	for j := 1; j >= 0; j-- {
		qhat := ^uint64(0)
		if un[j+m] != top.hi || un[j+m-1] != top.lo {
			qhat, _ = div3by2(un[j+m], un[j+m-1], un[j+m-2], top)
		}
		// un[j..j+m] -= qhat·dn; a borrow out means qhat was one too large.
		var carry, borrow uint64
		for k := range m {
			ph, pl := bits.Mul64(qhat, dn[k])
			pl, c := bits.Add64(pl, carry, 0)
			carry = ph + c // ph ≤ 2^64-2, so this cannot wrap
			un[j+k], borrow = bits.Sub64(un[j+k], pl, borrow)
		}
		un[j+m], borrow = bits.Sub64(un[j+m], carry, borrow)
		if borrow != 0 {
			qhat--
			var c uint64
			for k := range m {
				un[j+k], c = bits.Add64(un[j+k], dn[k], c)
			}
			un[j+m] += c
		}
		q[j] = qhat
	}
	// The remainder is below dn, in un[0..m-1] with un[m] zero; shift it back.
	var r [4]uint64
	for k := range m {
		r[k] = un[k]>>s | un[k+1]<<(64-s)
	}
	return u128{hi: q[1], lo: q[0]}, u256{d0: r[0], d1: r[1], d2: r[2], d3: r[3]}
}
