package zerodecimal

import "math/bits"

// WeightedAvg is a running weighted mean Σ(value·weight) / Σweight, such as a
// volume-weighted average price. Its zero value is empty and ready to use. It
// is not safe for concurrent use.
//
// The numerator and the total weight are two Accumulators, so Add and
// AddTotal never fail, the result does not depend on the order of the rows,
// and neither sum has to fit 128 bits until a finisher reads it. Each sum is
// the exact sum of its rows' terms. A weight term is the weight as given; a
// numerator term is AddTotal's total as given, or Add's value×weight
// truncated at DefaultPrec places as Mul truncates it. Total and Weight
// return the two sums, Avg divides them with Div's truncation, and AvgRound
// rounds their exact quotient once. Reading does not change the sums, so a
// stream may continue after a finisher. Weights may have either sign; a zero
// total weight, as in an empty WeightedAvg, makes Avg and AvgRound return
// ErrDivideByZero and leaves what an empty mean means to the caller.
type WeightedAvg struct {
	num, weight Accumulator
}

// Add adds a row: the numerator gains value×weight truncated toward zero at
// min(value.Prec()+weight.Prec(), DefaultPrec) places, the value
// value.Mul(weight) has (see Accumulator.AddMul), and the total weight gains
// weight. The truncated digits are lost before they reach the exact sums:
// under zerodecimal_prec9, Add(0.5, 0.000000001) adds 0 to the numerator, so
// AvgRound(2, ToNearestAway) returns 0 rather than 0.5. A caller that needs
// such a product exact can pass an exact one, such as value.MulExact(weight)
// when it succeeds, to AddTotal.
func (w *WeightedAvg) Add(value, weight Decimal) {
	w.num.AddMul(value, weight)
	if !wavgAddNoCarry(&w.weight, weight) {
		w.weight.Add(weight)
	}
}

// AddTotal adds a row whose numerator term the caller has already computed,
// such as price.Mul(multiplier).Mul(qty), or value.MulExact(weight) to keep
// every digit: the numerator gains total as given, and the total weight gains
// weight.
func (w *WeightedAvg) AddTotal(total, weight Decimal) {
	if !wavgAddNoCarry(&w.num, total) {
		w.num.Add(total)
	}
	if !wavgAddNoCarry(&w.weight, weight) {
		w.weight.Add(weight)
	}
}

// wavgAddNoCarry adds d to a when that is the first case of Accumulator.add,
// d at a's precision with a one-limb coefficient, and the add leaves no carry
// out of the total's low limb; it reports whether it did. Otherwise a is
// unchanged and the caller adds d with Accumulator.Add, which handles every
// case. Inlined, it spares an aligned row a call: one in Add, up to two in
// AddTotal. It reads Accumulator's internals (tot, prec, accSignIndex), an
// exception to using only its exported API, and the tests hold the state it
// leaves to that of two Accumulators fed the same rows.
func wavgAddNoCarry(a *Accumulator, d Decimal) bool {
	if d.prec != a.prec || d.coef.hi != 0 {
		return false
	}
	t := &a.tot[accSignIndex(d.neg)&1]
	lo, carry := bits.Add64(t.d0, d.coef.lo, 0)
	if carry != 0 {
		return false
	}
	t.d0 = lo
	return true
}

// Avg returns the numerator over the total weight with Div's legacy contract:
// the exact quotient of the two sums truncated toward zero at the greatest
// precision ≤ DefaultPrec whose coefficient fits 128 bits. It equals Total()
// followed by Decimal.Div by Weight() whenever both sums fit 128 bits, and it
// still succeeds when only the numerator does not.
//
// ErrOverflow when the total weight does not fit 128 bits; otherwise
// ErrDivideByZero when it is zero, then ErrOverflow when the numerator
// outgrew its Accumulator or the quotient does not fit even at 0 places.
func (w *WeightedAvg) Avg() (Decimal, error) {
	den, err := w.weight.Sum()
	if err != nil {
		return Decimal{}, err
	}
	return w.num.Div(den)
}

// AvgRound returns q, the exact quotient of the numerator over the total
// weight, rounded once to places fractional digits in mode, with no
// intermediate truncation even when the numerator passes 128 bits. Only the
// products Add truncates lose digits, before they reach the numerator (see
// Add). A nonzero result carries precision places; zero is canonical
// Decimal{}. It equals Total() followed by Decimal.DivRound by Weight()
// whenever both sums fit 128 bits.
//
// Validation follows DivRound: ErrPrecOutOfRange, then
// ErrInvalidRoundingMode, then the total weight as for Avg (ErrOverflow past
// 128 bits, ErrDivideByZero at zero), then ErrOverflow when the numerator
// outgrew its Accumulator or the rounded coefficient does not fit 128 bits.
//
// Rounding Avg's truncated q later agrees with AvgRound in two modes whenever
// Avg keeps more than places digits, that is, for places < DefaultPrec while
// |q| stays below 2^128/10^(places+1) (about 3.4e21 for 16 places):
// Avg().Round(places) equals AvgRound(places, ToNearestAway), and
// Avg().Truncate(places) equals AvgRound(places, TowardZero). Truncation is
// monotone, and the points these modes round at, the multiples of 10^-places
// and the half-way points (10k+5)·10^-(places+1), lie on Avg's finer grid, so
// truncating to that grid cannot carry q across one. At the default
// DefaultPrec of 19, Avg().Round(16) is therefore AvgRound(16, ToNearestAway)
// below that bound. The other modes can differ by one unit in the last place:
// ToNearestEven when |q| exceeds a tie by less than one unit in Avg's last
// place, and AwayFromZero, TowardPositive and TowardNegative when Avg's digits
// past places are all zero but q's are not.
func (w *WeightedAvg) AvgRound(places uint8, mode RoundingMode) (Decimal, error) {
	// Validate before reading the weight, so a weight past 128 bits cannot
	// pre-empt DivRound's argument order.
	if places > MaxPrec {
		return Decimal{}, ErrPrecOutOfRange
	}
	if !mode.valid() {
		return Decimal{}, ErrInvalidRoundingMode
	}
	den, err := w.weight.Sum()
	if err != nil {
		return Decimal{}, err
	}
	return w.num.DivRound(den, places, mode)
}

// Total returns the numerator, the exact sum of the rows' numerator terms
// (Add's truncated products and AddTotal's totals), with Accumulator.Sum's
// contract: the sum at the greatest precision added, or ErrOverflow when it
// does not fit 128 bits or a same-sign total outgrew the Accumulator's width.
func (w *WeightedAvg) Total() (Decimal, error) {
	return w.num.Sum()
}

// Weight returns the total weight, the exact sum of the rows' weights, with
// Accumulator.Sum's contract.
func (w *WeightedAvg) Weight() (Decimal, error) {
	return w.weight.Sum()
}

// Reset empties both sums.
func (w *WeightedAvg) Reset() {
	*w = WeightedAvg{}
}
