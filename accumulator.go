package zerodecimal

import "math/bits"

// Accumulator is a running exact sum. Its zero value is an empty sum, ready to
// use. Terms are aligned to the greatest precision seen and kept in separate
// wide totals per sign, so the result does not depend on the order terms were
// added in, and no partial sum can overflow before it is read. An Accumulator
// is not safe for concurrent use.
//
// Add, Sub and AddMul never fail: only the finishers Sum, Div and DivRound
// report errors. Reading the total does not change it, so a stream may
// continue after a finisher. The total carries the greatest precision of the
// terms added, where an AddMul term has the precision d.Mul(e) has and a zero
// term counts as canonical zero (precision 0), exactly as the package-level
// Sum over the same terms.
//
// Each per-sign total is 256 bits wide. An Add or Sub term aligned to the
// accumulator's precision is below 2^192, so about 2^64 of them fill a total;
// an AddMul term, a product of up to 256 bits before alignment, can fill it
// sooner. A same-sign total that would reach 2^256 sets a sticky overflow
// flag, which every finisher reports as ErrOverflow until Reset. The flag
// depends only on the terms, not on their order: each total only grows, and
// none of its intermediate values exceeds its final one. An Accumulator is 72
// bytes on targets with 8-byte maximum alignment and 68 on gc's 32-bit
// max-align-4 ports; copying one copies the running sum.
type Accumulator struct {
	// tot holds the magnitude totals of the positive (0) and negative (1)
	// terms, aligned to prec.
	tot      [2]u256
	prec     uint8
	overflow bool
}

// accSignIndex maps a sign to its total in Accumulator.tot. The if/else
// compiles to a zero-extension of the bool, so selecting a total takes no
// branch.
func accSignIndex(neg bool) uint8 {
	if neg {
		return 1
	}
	return 0
}

// Add adds d.
func (a *Accumulator) Add(d Decimal) {
	a.add(d.coef, d.neg, d.prec)
}

// Sub subtracts d.
func (a *Accumulator) Sub(d Decimal) {
	a.add(d.coef, !d.neg, d.prec)
}

// add is the shared body of Add, Sub and AddMul's one-limb products: the
// magnitude c at prec joins the total for sign neg. At the accumulator's
// precision that is one or two limb adds with no error branch, and only a
// carry out of them reaches the upper limbs. A coarser term widens by 10^Δ to
// below 2^192; a finer one first raises both totals to its precision. Add and
// Sub inline into the caller, so a term costs one call.
func (a *Accumulator) add(c u128, neg bool, prec uint8) {
	t := &a.tot[accSignIndex(neg)&1]
	var w u256 // what remains to add to t
	switch {
	case prec == a.prec && c.hi == 0:
		lo, carry := bits.Add64(t.d0, c.lo, 0)
		t.d0 = lo
		if carry == 0 {
			return
		}
		w.d1 = 1 // the carry out of bit 63
	case prec == a.prec:
		lo, carry := bits.Add64(t.d0, c.lo, 0)
		hi, carry := bits.Add64(t.d1, c.hi, carry)
		t.d0, t.d1 = lo, hi
		if carry == 0 {
			return
		}
		w.d2 = 1 // the carry out of bit 127
	case c.isZero():
		// A zero term changes no total, and canonical zero (precision 0)
		// must not raise the precision.
		return
	case prec < a.prec:
		w.d2, w.d1, w.d0 = mul128by64to192(c, pow10u64[(a.prec-prec)&31])
	default:
		a.raise(prec)
		w = u256{d0: c.lo, d1: c.hi}
	}
	sum, carry := add256(*t, w)
	*t = sum
	if carry != 0 {
		a.overflow = true
	}
}

// raise rescales both totals from the accumulator's precision up to prec. A
// total that no longer fits 256 bits sets the sticky overflow flag.
//
// PRECONDITION (not checked): a.prec < prec ≤ MaxPrec.
func (a *Accumulator) raise(prec uint8) {
	v := pow10u64[(prec-a.prec)&31]
	pos, posFits := mul256by64(a.tot[0], v)
	neg, negFits := mul256by64(a.tot[1], v)
	a.tot, a.prec = [2]u256{pos, neg}, prec
	if !posFits || !negFits {
		a.overflow = true
	}
}

// AddMul adds d×e truncated toward zero at min(d.Prec()+e.Prec(), DefaultPrec)
// places. That is the value d.Mul(e) has when it succeeds, but it is kept
// wide, so it never fails where Mul would return ErrOverflow.
func (a *Accumulator) AddMul(d, e Decimal) {
	neg := d.neg != e.neg
	pSum := d.prec + e.prec // 0..2*MaxPrec
	if d.coef.hi|e.coef.hi == 0 {
		// One-limb operands, the money shape: the product is one multiply, and
		// a truncation by at most 10^MaxPrec is one reciprocal divide whose
		// quotient fits 128 bits.
		hi, lo := bits.Mul64(d.coef.lo, e.coef.lo)
		if pSum <= DefaultPrec {
			a.add(u128{hi: hi, lo: lo}, neg, pSum)
			return
		}
		if k := pSum - DefaultPrec; k <= MaxPrec {
			if hi == 0 {
				q, _ := divmod64Pow10(lo, k)
				a.add(u128{lo: q}, neg, DefaultPrec)
				return
			}
			q, _ := divmod128Pow10Slow(u128{hi: hi, lo: lo}, k)
			a.add(q, neg, DefaultPrec)
			return
		}
	}
	a.addMulWide(d, e, neg, pSum)
}

// addMulWide is AddMul for two-limb operands, and for one-limb operands whose
// truncation exceeds 10^MaxPrec (reduced DefaultPrec builds only). It drops
// pSum - DefaultPrec digits from the 256-bit product as Mul's rescale does,
// in two reciprocal passes past 10^MaxPrec (⌊⌊u/a⌋/b⌋ == ⌊u/(a·b)⌋), but keeps
// the full-width quotient.
func (a *Accumulator) addMulWide(d, e Decimal, neg bool, pSum uint8) {
	prod := mulToU256(d.coef, e.coef)
	prec := pSum
	if pSum > DefaultPrec {
		k := pSum - DefaultPrec // 1..2*MaxPrec-DefaultPrec
		if k > MaxPrec {
			prod, _ = divmodU256Pow10Wide(prod, MaxPrec)
			k -= MaxPrec
		}
		prod, _ = divmodU256Pow10Wide(prod, k)
		prec = DefaultPrec
	}
	a.addWide(prod, neg, prec)
}

// addWide adds a 256-bit term at prec, as add does for 128-bit ones.
// Widening a coarser term by 10^Δ can pass 256 bits; that sets the sticky
// flag, because the same-sign total would then pass 256 bits too.
func (a *Accumulator) addWide(w u256, neg bool, prec uint8) {
	if u256Zero(w) {
		return
	}
	switch {
	case prec < a.prec:
		var fits bool
		if w, fits = mul256by64(w, pow10u64[(a.prec-prec)&31]); !fits {
			a.overflow = true
			return
		}
	case prec > a.prec:
		a.raise(prec)
	}
	t := &a.tot[accSignIndex(neg)&1]
	sum, carry := add256(*t, w)
	*t = sum
	if carry != 0 {
		a.overflow = true
	}
}

// total returns the magnitude mag and sign neg of the signed total
// tot[0] - tot[1]; ok is false once the sticky overflow flag is set.
func (a *Accumulator) total() (mag u256, neg, ok bool) {
	if a.overflow {
		return u256{}, false, false
	}
	diff, borrow := sub256(a.tot[0], a.tot[1])
	if borrow != 0 {
		return neg256(diff), true, true
	}
	return diff, false, true
}

// Sum returns the exact total at the greatest precision added, with Sum's
// contract (canonical zero). ErrOverflow when the total does not fit 128 bits,
// or when a same-sign total outgrew the accumulator's width.
func (a *Accumulator) Sum() (Decimal, error) {
	t, neg, ok := a.total()
	if !ok || !t.isZeroUpper() {
		return Decimal{}, ErrOverflow
	}
	return newDecimal(t.lo128(), neg, a.prec), nil
}

// Div returns total/den with Div's legacy contract: truncation toward zero at
// the greatest precision ≤ DefaultPrec whose coefficient fits. It equals
// a.Sum() followed by Decimal.Div whenever Sum succeeds, and it still succeeds
// when only the intermediate total exceeds 128 bits.
//
// ErrDivideByZero takes precedence for a zero den, then ErrOverflow for a
// total that outgrew the accumulator's width. An empty or zero total returns
// canonical Decimal{}.
func (a *Accumulator) Div(den Decimal) (Decimal, error) {
	if den.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	t, neg, ok := a.total()
	if !ok {
		return Decimal{}, ErrOverflow
	}
	if t.isZeroUpper() {
		// The total is a Decimal, so Decimal.Div is the contract itself.
		return newDecimal(t.lo128(), neg, a.prec).Div(den)
	}
	return accumulatorDivWide(t, a.prec, den, neg != den.neg)
}

// accumulatorDivWide is Div for a total magnitude t of 128 bits or more at
// precision tp. Fit is monotone in the precision, because the truncated
// quotient never decreases as it gains digits, so it probes DefaultPrec, the
// common answer, and only a degraded quotient pays for a binary search over
// the lower precisions.
func accumulatorDivWide(t u256, tp uint8, den Decimal, neg bool) (Decimal, error) {
	if q, _, _, fits := accumulatorQuoAt(t, tp, den, DefaultPrec); fits {
		return newDecimal(q, neg, DefaultPrec), nil
	}
	var best u128
	var bestPrec uint8
	found := false
	lo, hi := uint8(0), DefaultPrec-1
	for lo <= hi {
		mid := (lo + hi) / 2
		q, _, _, fits := accumulatorQuoAt(t, tp, den, mid)
		if fits {
			best, bestPrec, found = q, mid, true
			lo = mid + 1
			continue
		}
		if mid == 0 {
			break
		}
		hi = mid - 1
	}
	if !found {
		return Decimal{}, ErrOverflow
	}
	return newDecimal(best, neg, bestPrec), nil
}

// DivRound returns total/den rounded once to places, with DivRound's contract
// and validation order (places, mode, zero divisor). A total that outgrew the
// accumulator's width returns ErrOverflow after those checks; otherwise
// ErrOverflow means the rounded coefficient does not fit 128 bits. The total
// is never rounded or truncated first, even past 128 bits, so this is the
// n-ary form of (a+b)/c rounded once. An empty or zero total returns canonical
// Decimal{}.
func (a *Accumulator) DivRound(den Decimal, places uint8, mode RoundingMode) (Decimal, error) {
	if places > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	if den.coef.isZero() {
		return Decimal{}, ErrDivideByZero
	}
	t, neg, ok := a.total()
	if !ok {
		return Decimal{}, ErrOverflow
	}
	if t.isZeroUpper() {
		// The total is a Decimal, so Decimal.DivRound is the contract itself.
		return newDecimal(t.lo128(), neg, a.prec).DivRound(den, places, mode)
	}
	neg = neg != den.neg
	q, rem, halfCmp, fits := accumulatorQuoAt(t, a.prec, den, places)
	if !fits {
		return Decimal{}, ErrOverflow
	}
	q, err := roundQuotient(q, rem, halfCmp, neg, mode)
	if err != nil {
		return Decimal{}, err
	}
	return newDecimal(q, neg, places), nil
}

// accumulatorQuoAt is divRoundAt for a 256-bit total magnitude t at precision
// tp: q = ⌊t·10^f / den⌋ with f = places + den.prec - tp, whether the
// remainder is nonzero, the sign of twice the remainder against the divisor,
// and whether q fits 128 bits. A nonnegative f scales t, and a scaled t past
// 2^256 already exceeds den·2^128. A negative f scales den instead, which
// stays below 2^192; past 2^128 divRoundPow10By divides by the power of ten
// and by den in turn, and the quotient then fits because t < 2^256.
func accumulatorQuoAt(t u256, tp uint8, den Decimal, places uint8) (q u128, rem bool, halfCmp int, fits bool) {
	up := places + den.prec // 0..2*MaxPrec
	if up >= tp {
		num, ok := mul256Pow10(t, up-tp)
		if !ok {
			return u128{}, false, 0, false
		}
		return divRoundWide(num, den.coef)
	}
	g := tp - up // 1..MaxPrec
	if scaled := mulToU256(den.coef, pow10u128[g&63]); scaled.isZeroUpper() {
		return divRoundWide(t, scaled.lo128())
	}
	q, rem, halfCmp = divRoundPow10By(t, g, den.coef)
	return q, rem, halfCmp, true
}

// Reset empties the accumulator.
func (a *Accumulator) Reset() {
	*a = Accumulator{}
}
