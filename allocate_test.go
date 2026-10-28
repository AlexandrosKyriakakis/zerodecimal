package zerodecimal

import (
	"math/big"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test names here avoid the TestAllocate prefix: make test-alloc runs
// '^Test.*Alloc', which belongs to the allocation gates in
// allocate_alloc_test.go.

// allocateWant is the math/big model of Allocate: the exact share of each
// weight in units of 10^-places, truncated, with the leftover units handed one
// each to the largest remainders, ties to the lower index. It reports the
// error Allocate must return, in Allocate's precedence order; ErrOverflow
// exactly when a share needs more than 128 bits.
func allocateWant(total Decimal, weights []Decimal, places uint8) ([]Decimal, error) {
	if places > MaxPrec {
		return nil, ErrPrecOutOfRange
	}
	sum := new(big.Rat)
	for _, w := range weights {
		if w.IsNegative() {
			return nil, ErrNegativeWeight
		}
		sum.Add(sum, exactDecimalRat(w))
	}
	if sum.Sign() == 0 {
		return nil, ErrDivideByZero
	}
	units, err := allocateWantUnits(total, places)
	if err != nil {
		return nil, err
	}
	coefs := make([]*big.Int, len(weights))
	rems := make([]*big.Rat, len(weights))
	left := new(big.Int).Set(units)
	for i, w := range weights {
		exact := new(big.Rat).Mul(new(big.Rat).SetInt(units), exactDecimalRat(w))
		exact.Quo(exact, sum)
		coefs[i] = new(big.Int).Quo(exact.Num(), exact.Denom())
		rems[i] = exact.Sub(exact, new(big.Rat).SetInt(coefs[i]))
		left.Sub(left, coefs[i])
	}
	byRem := func(a, b int) int { return rems[b].Cmp(rems[a]) }
	return allocateWantFinish(coefs, byRem, left, total.IsNegative(), places)
}

// allocateWantInt is allocateWant in integer arithmetic: share i truncates to
// ⌊U·a_i/W⌋ with remainder U·a_i mod W, for U = |total|·10^places, the
// weights a_i aligned to their greatest precision and their sum W. It runs
// many times faster than the big.Rat model, which keeps the fuzz targets
// fast; requireAllocateOracle holds the two models to the same answers.
func allocateWantInt(total Decimal, weights []Decimal, places uint8) ([]Decimal, error) {
	if places > MaxPrec {
		return nil, ErrPrecOutOfRange
	}
	for _, w := range weights {
		if w.IsNegative() {
			return nil, ErrNegativeWeight
		}
	}
	aligned, sum := allocateAligned(weights)
	if sum.Sign() == 0 {
		return nil, ErrDivideByZero
	}
	units, err := allocateWantUnits(total, places)
	if err != nil {
		return nil, err
	}
	coefs := make([]*big.Int, len(weights))
	rems := make([]*big.Int, len(weights))
	left := new(big.Int).Set(units)
	for i, a := range aligned {
		coefs[i], rems[i] = new(big.Int).QuoRem(new(big.Int).Mul(units, a), sum, new(big.Int))
		left.Sub(left, coefs[i])
	}
	byRem := func(a, b int) int { return rems[b].Cmp(rems[a]) }
	return allocateWantFinish(coefs, byRem, left, total.IsNegative(), places)
}

// allocateWantUnits returns |total|·10^places, or ErrInexact when that is not
// an integer.
func allocateWantUnits(total Decimal, places uint8) (*big.Int, error) {
	u := new(big.Rat).Abs(exactDecimalRat(total))
	u.Mul(u, new(big.Rat).SetInt(exactPow10(int(places))))
	if !u.IsInt() {
		return nil, ErrInexact
	}
	return u.Num(), nil
}

// allocateWantFinish ranks the shares by remainder with byRem, largest first
// and ties to the lower index (a stable sort keeps index order among equal
// remainders), adds one unit to the first left of them, and builds the
// Decimals, or reports ErrOverflow when a share does not fit 128 bits.
func allocateWantFinish(coefs []*big.Int, byRem func(a, b int) int, left *big.Int, neg bool, places uint8) ([]Decimal, error) {
	order := make([]int, len(coefs))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, byRem)
	for _, i := range order[:left.Int64()] {
		coefs[i].Add(coefs[i], big.NewInt(1))
	}
	out := make([]Decimal, len(coefs))
	for i, c := range coefs {
		if c.BitLen() > 128 {
			return nil, ErrOverflow
		}
		out[i] = newDecimal(exactU128FromBig(c), neg, places)
	}
	return out, nil
}

// requireAllocateOracle holds Allocate to allocateWant: the same error, or the
// same shares in representation. It also pins the two properties the oracle
// implies, independently of it: the shares sum exactly to total, and each
// lies less than one unit of 10^-places from its exact share. The integer
// model the fuzz targets use must agree with the big.Rat one. It reports
// whether the oracle has Allocate succeed.
func requireAllocateOracle(t *testing.T, total Decimal, weights []Decimal, places uint8) bool {
	t.Helper()
	want, wantErr := allocateWant(total, weights, places)
	wantInt, wantIntErr := allocateWantInt(total, weights, places)
	require.Equalf(t, wantErr, wantIntErr, "integer model error: total=%v weights=%v places=%d", total, weights, places)
	require.Equalf(t, want, wantInt, "integer model shares: total=%v weights=%v places=%d", total, weights, places)
	got, err := Allocate(nil, total, weights, places)
	if wantErr != nil {
		require.ErrorIsf(t, err, wantErr, "total=%v weights=%v places=%d", total, weights, places)
		require.Emptyf(t, got, "an error returns dst[:0]: total=%v weights=%v", total, weights)
		return false
	}
	require.NoErrorf(t, err, "total=%v weights=%v places=%d", total, weights, places)
	require.Equalf(t, want, got, "total=%v weights=%v places=%d", total, weights, places)
	requireAllocateProperties(t, total, weights, places, got)
	return true
}

// requireAllocateProperties checks shares against total and weights: every
// nonzero share carries places digits and total's sign, the shares sum
// exactly to total (in representation too, through Sum, when that sum fits a
// Decimal), and each differs from its exact share total·w_i/Σw by less than
// 10^-places.
func requireAllocateProperties(t *testing.T, total Decimal, weights []Decimal, places uint8, shares []Decimal) {
	t.Helper()
	require.Len(t, shares, len(weights))
	sum, shareSum := new(big.Rat), new(big.Rat)
	for i, w := range weights {
		sum.Add(sum, exactDecimalRat(w))
		shareSum.Add(shareSum, exactDecimalRat(shares[i]))
	}
	require.Zerof(t, shareSum.Cmp(exactDecimalRat(total)), "the shares must sum exactly to total: total=%v weights=%v shares=%v", total, weights, shares)
	if wantSum, err := total.Rescale(places); err == nil {
		gotSum, err := Sum(shares[0], shares[1:]...)
		require.NoError(t, err)
		require.Equal(t, wantSum, gotSum)
	}
	unit := new(big.Rat).SetFrac(big.NewInt(1), exactPow10(int(places)))
	for i, s := range shares {
		if !s.IsZero() {
			require.Equalf(t, places, s.Prec(), "share %d precision", i)
			require.Equalf(t, total.IsNegative(), s.IsNegative(), "share %d sign", i)
		}
		exact := new(big.Rat).Mul(exactDecimalRat(total), exactDecimalRat(weights[i]))
		exact.Quo(exact, sum)
		diff := new(big.Rat).Sub(exactDecimalRat(s), exact)
		require.Negativef(t, diff.Abs(diff).Cmp(unit), "share %d=%v is a unit or more from %s", i, s, exact.FloatString(25))
	}
}

// requireAllocateIntProperties is requireAllocateProperties in integer
// arithmetic, cheap enough for the fuzz targets: the share coefficients c_i
// (zero, or at precision places with total's sign) sum to U =
// |total|·10^places, and |c_i·W - U·a_i| < W for the aligned weights a_i and
// their sum W.
func requireAllocateIntProperties(t *testing.T, total Decimal, weights []Decimal, places uint8, shares []Decimal) {
	t.Helper()
	require.Len(t, shares, len(weights))
	units, err := allocateWantUnits(total, places)
	require.NoError(t, err)
	aligned, sum := allocateAligned(weights)
	shareSum := new(big.Int)
	for i, s := range shares {
		if !s.IsZero() {
			require.Equalf(t, places, s.Prec(), "share %d precision", i)
			require.Equalf(t, total.IsNegative(), s.IsNegative(), "share %d sign", i)
		}
		c := exactBigFromU128(s.coef)
		shareSum.Add(shareSum, c)
		diff := new(big.Int).Sub(new(big.Int).Mul(c, sum), new(big.Int).Mul(units, aligned[i]))
		require.Negativef(t, diff.Abs(diff).Cmp(sum), "share %d=%v is a unit or more from its exact share", i, s)
	}
	require.Zerof(t, shareSum.Cmp(units), "the shares must sum exactly to total: total=%v shares=%v", total, shares)
}

// allocateShares parses the expected shares of a known example at places.
func allocateShares(t *testing.T, places uint8, values ...string) []Decimal {
	t.Helper()
	out := make([]Decimal, len(values))
	for i, v := range values {
		d, err := RequireFromString(v).Rescale(places)
		require.NoError(t, err)
		out[i] = d
	}
	return out
}

func allocateDecimals(values ...string) []Decimal {
	out := make([]Decimal, len(values))
	for i, v := range values {
		out[i] = RequireFromString(v)
	}
	return out
}

func allocateInts(values ...int64) []Decimal {
	out := make([]Decimal, len(values))
	for i, v := range values {
		out[i] = NewFromInt(v)
	}
	return out
}

func TestLargestRemainderKnownExamples(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		total   string
		weights []string
		places  uint8
		want    []string
	}{
		{"ten_three_ways", "10.00", []string{"1", "1", "1"}, 2, []string{"3.34", "3.33", "3.33"}},
		{"nickel_three_ways", "0.05", []string{"1", "1", "1"}, 2, []string{"0.02", "0.02", "0.01"}},
		{"negative_total", "-10.00", []string{"1", "1", "1"}, 2, []string{"-3.34", "-3.33", "-3.33"}},
		{"single_weight", "12.34", []string{"0.5"}, 2, []string{"12.34"}},
		{"all_weight_on_one", "12.34", []string{"0", "7", "0"}, 2, []string{"0", "12.34", "0"}},
		{"zero_total", "0", []string{"1", "2"}, 2, []string{"0", "0"}},
		{"integer_total_cents", "10", []string{"1", "1", "1"}, 2, []string{"3.34", "3.33", "3.33"}},
		{"trailing_zeros_past_places", "10.0000", []string{"1", "1", "1"}, 2, []string{"3.34", "3.33", "3.33"}},
		{"rounding_each_overshoots", "1.00", []string{"1", "1", "1", "3"}, 2, []string{"0.17", "0.17", "0.16", "0.50"}},
		{"ties_to_lower_index", "0.05", []string{"1", "2", "1", "2"}, 2, []string{"0.01", "0.02", "0.01", "0.01"}},
		{"proportional_fee", "10.00", []string{"100", "250", "37.5"}, 2, []string{"2.58", "6.45", "0.97"}},
		{"mixed_weight_precision", "1.00", []string{"0.5", "1", "0.25"}, 2, []string{"0.29", "0.57", "0.14"}},
		{"whole_units", "10", []string{"1", "1", "1"}, 0, []string{"4", "3", "3"}},
		{"too_few_units", "0.02", []string{"1", "1", "1"}, 2, []string{"0.01", "0.01", "0"}},
		{"max_places", "1", []string{"1", "1", "1"}, MaxPrec, []string{"0.3333333333333333334", "0.3333333333333333333", "0.3333333333333333333"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			weights := allocateDecimals(tc.weights...)
			got, err := Allocate(nil, RequireFromString(tc.total), weights, tc.places)
			require.NoError(t, err)
			require.Equal(t, allocateShares(t, tc.places, tc.want...), got)
			requireAllocateOracle(t, RequireFromString(tc.total), weights, tc.places)
		})
	}
}

// TestLargestRemainderErrors pins every sentinel and their precedence:
// places, then a negative weight, then a zero weight sum, then a total
// inexact at places, then a share past 128 bits. An error leaves dst
// unwritten and returns dst[:0].
func TestLargestRemainderErrors(t *testing.T) {
	t.Parallel()
	one := NewFromInt(1)
	maxInt := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	cases := []struct {
		name    string
		total   Decimal
		weights []Decimal
		places  uint8
		want    error
	}{
		{"places_out_of_range", one, []Decimal{one}, MaxPrec + 1, ErrPrecOutOfRange},
		{"places_before_weights", RequireFromString("0.001"), []Decimal{one.Neg()}, MaxPrec + 1, ErrPrecOutOfRange},
		{"negative_weight", one, []Decimal{one, RequireFromString("-0.5")}, 2, ErrNegativeWeight},
		{"negative_before_zero_sum", one, []Decimal{{}, one.Neg()}, 2, ErrNegativeWeight},
		{"negative_before_total", RequireFromString("0.001"), []Decimal{one.Neg()}, 2, ErrNegativeWeight},
		{"no_weights", one, nil, 2, ErrDivideByZero},
		{"zero_weights", one, []Decimal{{}, {}}, 2, ErrDivideByZero},
		{"zero_sum_before_total", RequireFromString("0.001"), []Decimal{{}}, 2, ErrDivideByZero},
		{"total_past_places", RequireFromString("10.005"), []Decimal{one, one}, 2, ErrInexact},
		{"share_overflows", maxInt, []Decimal{one, one}, 1, ErrOverflow},
		{"single_share_overflows", maxInt.Neg(), []Decimal{one}, MaxPrec, ErrOverflow},
		{"one_heavy_share_overflows", maxInt, allocateInts(1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 10), 1, ErrOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := []Decimal{one, one, one}
			got, err := Allocate(dst, tc.total, tc.weights, tc.places)
			require.ErrorIs(t, err, tc.want)
			require.Empty(t, got)
			require.Equal(t, 3, cap(got), "the result is dst[:0]")
			require.Equal(t, []Decimal{one, one, one}, dst, "dst must not be written on error")
		})
	}
	// The largest total fits at its own precision: (2^128-1)/2 leaves one
	// leftover unit, which goes to the first share.
	got, err := Allocate(nil, maxInt, []Decimal{one, one}, 0)
	require.NoError(t, err)
	require.Equal(t, []Decimal{{coef: u128{hi: 1 << 63}}, {coef: u128{hi: maxUint64 >> 1, lo: maxUint64}}}, got)
}

// TestLargestRemainderOverflowBoundary pins ErrOverflow at its exact
// boundary. A share overflows when it needs 2^128 units of 10^-places or
// more, which only a total of 2^128 units or more allows; the largest
// truncated share, ⌊U·a/W⌋ for the largest weight a, decides it, except at
// exactly 2^128-1, where it overflows if and only if a share at 2^128-1 takes
// a leftover unit. Hand-picked cases (found with a big-integer model) cover
// each kind; generated cases put totals c·10^places around the boundary for
// random weights on every division path. Every case runs the oracle, an
// overflow must leave dst unwritten even when dst is the weights slice, and
// every kind must occur on every path.
func TestLargestRemainderOverflowBoundary(t *testing.T) {
	t.Parallel()
	maxInt := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	explicit := []struct {
		name    string
		total   string
		weights []Decimal
		want    error
	}{
		{"truncated_share_2^128", "323268248574891540290205877060179800884", allocateInts(2, 2, 2, 2, 2, 2, 2, 2, 2, 1), ErrOverflow},
		{"edge_share_takes_unit", "323268248574891540290205877060179800883", allocateInts(2, 2, 2, 2, 2, 2, 2, 2, 2, 1), ErrOverflow},
		{"edge_share_takes_unit_alone", "126390593427777143572110568474656764255", allocateInts(7, 4, 3, 2, 3, 3, 4), ErrOverflow},
		{"edge_shares_keep_value", "225437068085121732044485677423546440089", allocateInts(6, 6, 6, 8, 8, 2, 2, 5, 8, 2), nil},
		{"edge_share_exact", "113427455640312821154458202477256070485", allocateInts(3, 7, 1, 2, 9, 2, 6), nil},
		{"two_shares_of_2^128-1", "68056473384187692692674921486353642291", allocateInts(1, 1), nil},
		{"truncated_share_2^128-2", "323268248574891540290205877060179800882", allocateInts(2, 2, 2, 2, 2, 2, 2, 2, 2, 1), nil},
	}
	for _, tc := range explicit {
		t.Run(tc.name, func(t *testing.T) {
			total := RequireFromString(tc.total)
			requireAllocateOracle(t, total, tc.weights, 1)
			inPlace := slices.Clone(tc.weights)
			got, err := Allocate(inPlace, total, inPlace, 1)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Empty(t, got)
				require.Equal(t, tc.weights, inPlace, "an overflow must leave the weights unwritten")
				return
			}
			require.NoError(t, err)
		})
	}
	// Past 2^128 units, shares still fit while each is below 2^128.
	requireAllocateOracle(t, maxInt, allocateInts(1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1), 1)
	requireAllocateOracle(t, maxInt, allocateInts(3, 5, 7, 11, 13, 17, 19, 23, 29, 31), MaxPrec)

	rng := rand.New(rand.NewPCG(0x65646765, 0x626f756e64))
	kinds := map[string]int{}
	for trial := range 3_000 {
		path := [...]string{"W<2^64", "W<2^128", "W>=2^128"}[trial%3]
		n, places, maxWeight := 2+rng.IntN(11), uint8(1), int64(9)
		if trial%10 == 0 {
			n, places, maxWeight = 60+rng.IntN(41), 2, 99 // past the stack buffer
		}
		weights := make([]Decimal, n)
		for i := range weights {
			coef := exactBigFromU128(u128{lo: 1 + rng.Uint64N(uint64(maxWeight))})
			switch path {
			case "W<2^128":
				coef.Lsh(coef, 70)
			case "W>=2^128":
				coef.Lsh(coef, 100)
			}
			weights[i] = newDecimal(exactU128FromBig(coef), false, 0)
		}
		if path == "W>=2^128" {
			// A weight at nineteen places aligns the others by 10^19.
			weights[rng.IntN(n)] = Decimal{coef: u128{lo: 1}, prec: MaxPrec}
		}
		require.Equal(t, path, allocatePathOf(weights))
		aligned, sum := allocateAligned(weights)
		top := slices.MaxFunc(aligned, (*big.Int).Cmp)
		scale := new(big.Int).Mul(exactPow10(int(places)), top) // U·a = c·scale for a total c at precision 0
		for _, target := range []*big.Int{allocateTwoTo128(-2), allocateTwoTo128(-1), allocateTwoTo128(0)} {
			// The c with ⌊c·scale/W⌋ = target: ⌈target·W/scale⌉ ≤ c < ⌈(target+1)·W/scale⌉.
			lo := allocateCeilDiv(new(big.Int).Mul(target, sum), scale)
			hi := allocateCeilDiv(new(big.Int).Mul(new(big.Int).Add(target, big.NewInt(1)), sum), scale)
			for c := lo; c.Cmp(hi) < 0 && c.BitLen() <= 128; c = new(big.Int).Add(c, big.NewInt(1)) {
				total := newDecimal(exactU128FromBig(c), trial%2 == 0, 0)
				ok := requireAllocateOracle(t, total, weights, places)
				units := new(big.Int).Mul(c, exactPow10(int(places)))
				kind := allocateBoundaryKind(target, ok, units, aligned, sum)
				kinds[path+" "+kind]++
				if !ok {
					inPlace := slices.Clone(weights)
					_, err := Allocate(inPlace, total, inPlace, places)
					require.ErrorIs(t, err, ErrOverflow)
					require.Equal(t, weights, inPlace, "an overflow must leave the weights unwritten")
				}
			}
		}
	}
	for _, path := range []string{"W<2^64", "W<2^128", "W>=2^128"} {
		for _, kind := range []string{"below", "edge_overflow", "edge_fits", "certain"} {
			require.Positivef(t, kinds[path+" "+kind], "%s never hit %s: %v", path, kind, kinds)
		}
	}
	t.Logf("boundary cases by path and kind: %v", kinds)
}

// allocateAligned returns the weights as integers aligned to their greatest
// precision, and their sum W.
func allocateAligned(weights []Decimal) ([]*big.Int, *big.Int) {
	prec := uint8(0)
	for _, w := range weights {
		prec = max(prec, w.Prec())
	}
	aligned := make([]*big.Int, len(weights))
	sum := new(big.Int)
	for i, w := range weights {
		aligned[i] = new(big.Int).Mul(exactBigFromU128(w.coef), exactPow10(int(prec-w.Prec())))
		sum.Add(sum, aligned[i])
	}
	return aligned, sum
}

// allocateBoundaryKind names a boundary case by its largest truncated share,
// target, and whether the allocation succeeds (ok): "certain" at 2^128 or
// more; at 2^128-1 "edge_overflow" when a leftover unit overflows it, else
// "edge_fits" ("edge_exact" when no share at 2^128-1 has a remainder);
// "below" under 2^128-1.
func allocateBoundaryKind(target *big.Int, ok bool, units *big.Int, aligned []*big.Int, sum *big.Int) string {
	switch target.Cmp(allocateTwoTo128(-1)) {
	case 1:
		return "certain"
	case -1:
		return "below"
	}
	if !ok {
		return "edge_overflow"
	}
	for _, a := range aligned {
		q, r := new(big.Int).QuoRem(new(big.Int).Mul(units, a), sum, new(big.Int))
		if q.Cmp(target) == 0 && r.Sign() != 0 {
			return "edge_fits"
		}
	}
	return "edge_exact"
}

func allocateTwoTo128(delta int64) *big.Int {
	v := new(big.Int).Lsh(big.NewInt(1), 128)
	return v.Add(v, big.NewInt(delta))
}

func allocateCeilDiv(a, b *big.Int) *big.Int {
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

// TestLargestRemainderAgainstBigRat runs the oracle over random totals,
// places and weight slices: zero weights, coefficient widths from 1 to 128
// bits at every precision (so aligned weights differ by up to 2^128·10^19),
// now and then a negative weight, totals that are usually exact at places,
// and slices past the 64-weight stack buffer. Each successful case is
// classified by the width of the aligned weight sum W, and all three division
// paths must occur often.
func TestLargestRemainderAgainstBigRat(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x616c6c6f, 0x63617465))
	paths := map[string]int{}
	for i := range 20_000 {
		n := 1 + rng.IntN(8)
		switch i % 10 {
		case 0:
			n = 1 + rng.IntN(70)
		case 1:
			n = 60 + rng.IntN(10)
		}
		maxBits := [...]int{8, 24, 40, 64, 96, 128}[rng.IntN(6)]
		weights := make([]Decimal, n)
		for j := range weights {
			if rng.IntN(5) != 0 {
				weights[j] = newDecimal(shapedCoef(rng, maxBits), false, uint8(rng.UintN(uint(MaxPrec)+1)))
			}
		}
		if i%97 == 0 {
			weights[rng.IntN(n)] = NewFromInt(-1)
		}
		places := uint8(rng.UintN(uint(MaxPrec) + 1))
		total := allocateRandomTotal(rng, places)
		requireAllocateOracle(t, total, weights, places)
		if _, err := Allocate(nil, total, weights, places); err == nil {
			paths[allocatePathOf(weights)]++
		}
	}
	for _, p := range []string{"W<2^64", "W<2^128", "W>=2^128"} {
		require.Greaterf(t, paths[p], 500, "path %s rarely taken: %v", p, paths)
	}
	t.Logf("successful allocations by path: %v", paths)
}

// allocateRandomTotal draws a total whose coefficient width and precision are
// spread out: usually exact at places, sometimes not, and often with 2^128
// units of 10^-places or more.
func allocateRandomTotal(rng *rand.Rand, places uint8) Decimal {
	prec := uint8(rng.UintN(uint(MaxPrec) + 1))
	coef := shapedCoef(rng, 128)
	if prec > places && rng.IntN(5) != 0 {
		// Exact at places: a narrower coefficient times 10^(prec-places).
		coef, _ = mul128by64(shapedCoef(rng, 64), pow10u64[prec-places])
	}
	return newDecimal(coef, rng.IntN(2) == 0, prec)
}

// allocatePathOf names the division path Allocate takes for weights by the
// width of their sum aligned to the greatest weight precision.
func allocatePathOf(weights []Decimal) string {
	_, sum := allocateAligned(weights)
	switch {
	case sum.BitLen() <= 64:
		return "W<2^64"
	case sum.BitLen() <= 128:
		return "W<2^128"
	}
	return "W>=2^128"
}

// TestLargestRemainderNarrowDivides covers both divides of the W < 2^64
// path: the hardware divide when Ur·w_i fits one limb, and the reciprocal
// divide when it does not (possible once W > 2^32), with totals wide enough
// that the whole part Uq of total/W is nonzero too.
func TestLargestRemainderNarrowDivides(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6e6172726f77, 0x646976))
	for range 5_000 {
		weights := make([]Decimal, 1+rng.IntN(6))
		for j := range weights {
			weights[j] = newDecimal(u128{lo: rng.Uint64() >> (3 + rng.UintN(40))}, false, 0)
		}
		total := newDecimal(shapedCoef(rng, 128), rng.IntN(2) == 0, 0)
		requireAllocateOracle(t, total, weights, uint8(rng.UintN(3)))
	}
	// W = 2^63+2^62, where Ur·w_i passes 2^64. W = 2^32+1 is the narrowest
	// sum that needs the reciprocal: Ur = 2^32 times the weight 2^32 is 2^64,
	// which leaves remainder 1, while at W = 2^32 every Ur·w_i stays below 2^64.
	requireAllocateOracle(t, Decimal{coef: u128{hi: 5, lo: 12345}},
		[]Decimal{{coef: u128{lo: 1 << 63}}, {coef: u128{lo: 1 << 62}}}, 0)
	requireAllocateOracle(t, Decimal{coef: u128{lo: 1 << 32}},
		[]Decimal{{coef: u128{lo: 1 << 32}}, {coef: u128{lo: 1}}}, 0)
	requireAllocateOracle(t, Decimal{coef: u128{lo: 1<<32 - 1}},
		[]Decimal{{coef: u128{lo: 1<<32 - 3}}, {coef: u128{lo: 3}}}, 0)
}

// TestLargestRemainderWideWeights exercises W ≥ 2^128: a huge whole weight
// beside one at nineteen places, several maximal weights, and a sum just past
// 2^128, against totals of every width and sign.
func TestLargestRemainderWideWeights(t *testing.T) {
	t.Parallel()
	maxInt := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	tiny := Decimal{coef: u128{lo: 1}, prec: MaxPrec}
	cases := [][]Decimal{
		{RequireFromString("1e30"), tiny},
		{maxInt, maxInt, maxInt},
		{maxInt, NewFromInt(1)},
		{maxInt, tiny, tiny, NewFromInt(7)},
		{RequireFromString("123456789012345678901234567890"), RequireFromString("0.0000000000000000003"), RequireFromString("98765432109876543210.123456789")},
	}
	totals := []Decimal{
		NewFromInt(1), RequireFromString("10.00"), RequireFromString("-0.05"),
		RequireFromString("12345678901234567890123456789.01"), maxInt, maxInt.Neg(),
	}
	for _, weights := range cases {
		require.Equal(t, "W>=2^128", allocatePathOf(weights))
		for _, total := range totals {
			for _, places := range []uint8{0, 2, 9, MaxPrec} {
				requireAllocateOracle(t, total, weights, places)
			}
		}
	}
}

// TestLargestRemainderDeterministic pins that a repeated call returns the
// same shares and that equal weights rank by index alone: the leftover units
// go to the first shares, exactly as Split assigns them.
func TestLargestRemainderDeterministic(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x64657465, 0x726d))
	for range 2_000 {
		n := 1 + rng.IntN(80)
		w := newDecimal(shapedCoef(rng, 128), false, uint8(rng.UintN(uint(MaxPrec)+1)))
		weights := make([]Decimal, n)
		for j := range weights {
			weights[j] = w
		}
		places := uint8(rng.UintN(uint(MaxPrec) + 1))
		total := allocateRandomTotal(rng, places)
		first, err := Allocate(nil, total, weights, places)
		second, err2 := Allocate(nil, total, weights, places)
		require.Equal(t, err, err2)
		require.Equal(t, first, second)
		split, splitErr := Split(nil, total, n, places)
		require.Equal(t, err, splitErr)
		require.Equal(t, split, first, "equal weights must allocate exactly as Split")
	}
}

// TestLargestRemainderDst pins the dst contract: the shares overwrite
// dst[:n] when its capacity suffices, a short dst is replaced by a new slice,
// dst may be the weights slice itself, and stale values never survive.
func TestLargestRemainderDst(t *testing.T) {
	t.Parallel()
	weights := allocateDecimals("100", "250", "37.5", "0", "12.25")
	total := RequireFromString("-1234.56")
	want, err := Allocate(nil, total, weights, 2)
	require.NoError(t, err)

	buf := make([]Decimal, 2, 8)
	got, err := Allocate(buf, total, weights, 2)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Same(t, &buf[:1][0], &got[0], "a large enough dst is reused")

	short := make([]Decimal, 4)
	got, err = Allocate(short, total, weights, 2)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.NotSame(t, &short[0], &got[0], "a short dst is replaced")

	inPlace := slices.Clone(weights)
	got, err = Allocate(inPlace, total, inPlace, 2)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Same(t, &inPlace[0], &got[0])

	// Stale values in a reused dst never survive: not at a zero weight's
	// share, and not when the whole total is zero.
	stale := func() []Decimal { return slices.Repeat([]Decimal{NewFromInt(-9)}, len(weights)) }
	got, err = Allocate(stale(), total, weights, 2)
	require.NoError(t, err)
	require.Equal(t, want, got)
	got, err = Allocate(stale(), Decimal{}, weights, 2)
	require.NoError(t, err)
	require.Equal(t, make([]Decimal, len(weights)), got)
	got, err = Split(stale(), Decimal{}, len(weights), 2)
	require.NoError(t, err)
	require.Equal(t, make([]Decimal, len(weights)), got)

	// In place past the stack buffer, on the narrow and the wide path.
	rng := rand.New(rand.NewPCG(0x696e706c, 0x616365))
	for _, maxBits := range []int{40, 128} {
		many := make([]Decimal, 100)
		for j := range many {
			many[j] = newDecimal(shapedCoef(rng, maxBits), false, uint8(rng.UintN(uint(MaxPrec)+1)))
		}
		want, err = Allocate(nil, total, many, 2)
		require.NoError(t, err)
		got, err = Allocate(many, total, many, 2)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}

// TestDiv384by256 holds the Knuth division behind the W ≥ 2^128 path to
// math/big, for three- and four-limb divisors and dividends below d·2^128.
// Random cases mix random limbs with 0, 1, 2^63 and 2^64-1; constructed cases
// force each kind of quotient digit. Every digit is classified by how its
// trial digit relates to the true one, and all three kinds must occur: the
// clamped digit (the window's top two limbs equal the divisor's), an exact
// trial, and a trial one too large that the add-back corrects.
func TestDiv384by256(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6b6e757468, 0x44))
	limb := func() uint64 {
		switch rng.IntN(6) {
		case 0:
			return 0
		case 1:
			return 1
		case 2:
			return 1 << 63
		case 3:
			return maxUint64
		case 4:
			return maxUint64 - rng.Uint64N(4)
		}
		return rng.Uint64()
	}
	two128 := new(big.Int).Lsh(big.NewInt(1), 128)
	digits := map[string]int{}
	check := func(nBig, dBig *big.Int) {
		t.Helper()
		var n [6]uint64
		copy(n[:], allocateLimbs(nBig, 6))
		d := allocateLimbs(dBig, 4)
		q, r := div384by256(n, u256{d0: d[0], d1: d[1], d2: d[2], d3: d[3]})
		wantQ, wantR := new(big.Int).QuoRem(nBig, dBig, new(big.Int))
		require.Equalf(t, wantQ.String(), exactBigFromU128(q).String(), "quotient: n=%x d=%x", nBig, dBig)
		require.Equalf(t, wantR.String(), allocateBigFromLimbs(r.d0, r.d1, r.d2, r.d3).String(), "remainder: n=%x d=%x", nBig, dBig)
		for _, c := range allocateClassifyDigits(nBig, dBig) {
			digits[c]++
		}
	}
	for i := range 50_000 {
		d := u256{d0: limb(), d1: limb(), d2: limb(), d3: limb()}
		if i%2 == 0 {
			d.d3 = 0
		}
		if d.d2|d.d3 == 0 {
			d.d2 = 1 + rng.Uint64N(maxUint64)
		}
		dBig := allocateBigFromLimbs(d.d0, d.d1, d.d2, d.d3)
		limit := new(big.Int).Mul(dBig, two128)
		nBig := allocateBigFromLimbs(limb(), limb(), limb(), limb(), limb(), limb())
		if nBig.Cmp(limit) >= 0 {
			nBig.Mod(nBig, limit)
		}
		if i%5 == 0 {
			nBig.Sub(limit, big.NewInt(1+rng.Int64N(3)))
		}
		check(nBig, dBig)
	}
	// Constructed digits for a normalized divisor top·2^(64(m-2)) + low whose
	// low limbs are all ones. A dividend t·top·2^(64(m-1)) makes the first
	// window's 3-by-2 trial digit t, one more than the true t-1, and
	// t·top·2^(64(m-2)) does the same for the second window: add-backs. A
	// dividend top·2^(64(m-1)) + x puts top in the second window's top two
	// limbs: a clamped digit.
	for m := 3; m <= 4; m++ {
		scale := new(big.Int).Lsh(big.NewInt(1), uint(64*(m-2)))
		for range 200 {
			top := allocateBigFromLimbs(rng.Uint64(), rng.Uint64()|1<<63)
			dBig := new(big.Int).Mul(top, scale)
			dBig.Add(dBig, scale).Sub(dBig, big.NewInt(1))
			trial := new(big.Int).SetUint64(2 + rng.Uint64N(maxUint64-2))
			second := new(big.Int).Mul(new(big.Int).Mul(trial, top), scale)
			check(second, dBig)
			check(new(big.Int).Lsh(second, 64), dBig)
			clamped := new(big.Int).Mul(top, scale)
			clamped.Lsh(clamped, 64).Add(clamped, new(big.Int).SetUint64(rng.Uint64()))
			check(clamped, dBig)
		}
	}
	for _, c := range []string{"clamped", "exact", "add_back"} {
		require.Greaterf(t, digits[c], 100, "digit kind %s rarely occurred: %v", c, digits)
	}
	t.Logf("quotient digits by kind: %v", digits)
}

// allocateClassifyDigits replays the two digit steps of div384by256 in
// math/big and names each by its trial digit: "clamped" when the window's top
// two limbs equal the divisor's, else "exact" or "add_back" as the 3-by-2
// trial quotient equals or exceeds the true digit.
func allocateClassifyDigits(n, d *big.Int) []string {
	m := (d.BitLen() + 63) / 64
	s := uint(64*m - d.BitLen())
	dn := new(big.Int).Lsh(d, s)
	un := new(big.Int).Lsh(n, s)
	b := new(big.Int).Lsh(big.NewInt(1), 64)
	scale := new(big.Int).Lsh(big.NewInt(1), uint(64*(m-2))) // 2^(64·(m-2))
	d2 := new(big.Int).Quo(dn, scale)
	low := new(big.Int).Mod(un, b)
	window := new(big.Int).Quo(un, b)
	out := make([]string, 0, 2)
	for range 2 {
		q, rem := new(big.Int).QuoRem(window, dn, new(big.Int))
		t3 := new(big.Int).Quo(window, scale)
		switch {
		case new(big.Int).Quo(t3, b).Cmp(d2) == 0:
			out = append(out, "clamped")
		case new(big.Int).Quo(t3, d2).Cmp(q) == 0:
			out = append(out, "exact")
		default:
			out = append(out, "add_back")
		}
		window = rem.Mul(rem, b).Add(rem, low)
		low = new(big.Int)
	}
	return out
}

func allocateBigFromLimbs(limbs ...uint64) *big.Int {
	v := new(big.Int)
	for k := len(limbs) - 1; k >= 0; k-- {
		v.Lsh(v, 64).Add(v, new(big.Int).SetUint64(limbs[k]))
	}
	return v
}

// allocateLimbs returns the low count 64-bit limbs of v, least significant first.
func allocateLimbs(v *big.Int, count int) []uint64 {
	out := make([]uint64, count)
	w := new(big.Int).Set(v)
	mask := new(big.Int).SetUint64(maxUint64)
	for k := range out {
		out[k] = new(big.Int).And(w, mask).Uint64()
		w.Rsh(w, 64)
	}
	return out
}

// TestMul192to384 holds the 192-bit product behind the wide path to math/big.
func TestMul192to384(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x6d756c, 0x313932))
	for i := range 20_000 {
		var x, y [3]uint64
		for k := range 3 {
			x[k], y[k] = rng.Uint64(), rng.Uint64()
			if i%3 == 0 {
				x[k], y[k] = maxUint64, maxUint64-rng.Uint64N(2)
			}
		}
		want := new(big.Int).Mul(allocateBigFromLimbs(x[:]...), allocateBigFromLimbs(y[:]...))
		got := mul192to384(x, y)
		require.Equal(t, want.String(), allocateBigFromLimbs(got[:]...).String())
	}
}

// splitWant is the math/big model of Split: ⌊U/n⌋ units of 10^-places per
// share and one more for each of the first U mod n shares, U =
// |total|·10^places; ErrOverflow when the first share needs more than 128
// bits.
func splitWant(total Decimal, n int, places uint8) ([]Decimal, error) {
	if places > MaxPrec {
		return nil, ErrPrecOutOfRange
	}
	if n <= 0 {
		return nil, ErrDivideByZero
	}
	units, err := allocateWantUnits(total, places)
	if err != nil {
		return nil, err
	}
	q, r := new(big.Int).QuoRem(units, big.NewInt(int64(n)), new(big.Int))
	out := make([]Decimal, n)
	for i := range out {
		c := new(big.Int).Set(q)
		if int64(i) < r.Int64() {
			c.Add(c, big.NewInt(1))
		}
		if c.BitLen() > 128 {
			return nil, ErrOverflow
		}
		out[i] = newDecimal(exactU128FromBig(c), total.IsNegative(), places)
	}
	return out, nil
}

// requireSplitOracle holds Split to splitWant, and a successful result to the
// sum and bound properties of an allocation over n equal weights (in integer
// arithmetic, which keeps FuzzSplit fast for large n).
func requireSplitOracle(t *testing.T, total Decimal, n int, places uint8) {
	t.Helper()
	want, wantErr := splitWant(total, n, places)
	got, err := Split(nil, total, n, places)
	if wantErr != nil {
		require.ErrorIsf(t, err, wantErr, "total=%v n=%d places=%d", total, n, places)
		require.Empty(t, got)
		return
	}
	require.NoErrorf(t, err, "total=%v n=%d places=%d", total, n, places)
	require.Equalf(t, want, got, "total=%v n=%d places=%d", total, n, places)
	weights := make([]Decimal, n)
	for i := range weights {
		weights[i] = NewFromInt(1)
	}
	requireAllocateIntProperties(t, total, weights, places, got)
}

func TestSplitKnownExamples(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		total  string
		n      int
		places uint8
		want   []string
	}{
		{"ten_three_ways", "10.00", 3, 2, []string{"3.34", "3.33", "3.33"}},
		{"nickel_three_ways", "0.05", 3, 2, []string{"0.02", "0.02", "0.01"}},
		{"negative_nickel", "-0.05", 3, 2, []string{"-0.02", "-0.02", "-0.01"}},
		{"too_few_units", "0.02", 3, 2, []string{"0.01", "0.01", "0"}},
		{"one_share", "7", 1, 2, []string{"7.00"}},
		{"zero_total", "0", 4, 2, []string{"0", "0", "0", "0"}},
		{"whole_units", "100", 7, 0, []string{"15", "15", "14", "14", "14", "14", "14"}},
		{"max_places", "2", 3, MaxPrec, []string{"0.6666666666666666667", "0.6666666666666666667", "0.6666666666666666666"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Split(nil, RequireFromString(tc.total), tc.n, tc.places)
			require.NoError(t, err)
			require.Equal(t, allocateShares(t, tc.places, tc.want...), got)
			requireSplitOracle(t, RequireFromString(tc.total), tc.n, tc.places)
		})
	}
}

// TestSplitErrors pins Split's sentinels and their precedence: places, then a
// nonpositive n, then a total inexact at places, then a share past 128 bits.
func TestSplitErrors(t *testing.T) {
	t.Parallel()
	one := NewFromInt(1)
	maxInt := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	cases := []struct {
		name   string
		total  Decimal
		n      int
		places uint8
		want   error
	}{
		{"places_out_of_range", one, 3, MaxPrec + 1, ErrPrecOutOfRange},
		{"places_before_n", one, 0, MaxPrec + 1, ErrPrecOutOfRange},
		{"zero_n", one, 0, 2, ErrDivideByZero},
		{"negative_n", one, -3, 2, ErrDivideByZero},
		{"n_before_total", RequireFromString("0.001"), 0, 2, ErrDivideByZero},
		{"total_past_places", RequireFromString("0.001"), 3, 2, ErrInexact},
		{"share_overflows", maxInt, 3, 1, ErrOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := []Decimal{one, one}
			got, err := Split(dst, tc.total, tc.n, tc.places)
			require.ErrorIs(t, err, tc.want)
			require.Empty(t, got)
			require.Equal(t, 2, cap(got), "the result is dst[:0]")
			require.Equal(t, []Decimal{one, one}, dst, "dst must not be written on error")
		})
	}
}

// TestSplitOverflowBoundary pins Split's exact ErrOverflow: the first share,
// ⌊U/n⌋ plus one unit when a remainder is left, must fit 128 bits. Totals
// c·10 put ⌊U/n⌋ on 2^128-2, 2^128-1 and 2^128 for every n up to 10.
func TestSplitOverflowBoundary(t *testing.T) {
	t.Parallel()
	maxInt := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	// U = 2·(2^128-1): two shares of exactly 2^128-1.
	requireSplitOracle(t, RequireFromString("68056473384187692692674921486353642291"), 2, 1)
	// U = 7·(2^128-1) + 5: the first five shares would need 2^128.
	requireSplitOracle(t, RequireFromString("238197656844656924424362225202237748019"), 7, 1)
	requireSplitOracle(t, maxInt, 11, 1)
	kinds := map[string]int{}
	ten := big.NewInt(10)
	for n := 2; n <= 10; n++ {
		nb := big.NewInt(int64(n))
		for _, target := range []*big.Int{allocateTwoTo128(-2), allocateTwoTo128(-1), allocateTwoTo128(0)} {
			lo := allocateCeilDiv(new(big.Int).Mul(target, nb), ten)
			hi := allocateCeilDiv(new(big.Int).Mul(new(big.Int).Add(target, big.NewInt(1)), nb), ten)
			for c := lo; c.Cmp(hi) < 0 && c.BitLen() <= 128; c = new(big.Int).Add(c, big.NewInt(1)) {
				total := newDecimal(exactU128FromBig(c), n%2 == 0, 0)
				requireSplitOracle(t, total, n, 1)
				if _, err := splitWant(total, n, 1); err != nil {
					kinds["overflow"]++
				} else {
					kinds["fits"]++
				}
			}
		}
	}
	require.Positive(t, kinds["overflow"])
	require.Positive(t, kinds["fits"])
}

// TestSplitAgainstBigInt runs the oracle over random totals, share counts and
// places, including counts above the total's unit count.
func TestSplitAgainstBigInt(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x73706c6974, 0x6f7261))
	for i := range 10_000 {
		n := 1 + rng.IntN(10)
		if i%10 == 0 {
			n = 1 + rng.IntN(120)
		}
		places := uint8(rng.UintN(uint(MaxPrec) + 1))
		total := allocateRandomTotal(rng, places)
		if i%7 == 0 {
			total = newDecimal(u128{lo: rng.Uint64N(uint64(2 * n))}, rng.IntN(2) == 0, places)
		}
		requireSplitOracle(t, total, n, places)
	}
	got, err := Split(nil, NewFromInt(1), 100_000, 0)
	require.NoError(t, err)
	require.Equal(t, NewFromInt(1), got[0])
	require.Equal(t, Decimal{}, got[1])
	require.Equal(t, Decimal{}, got[99_999])
}

func TestSplitDst(t *testing.T) {
	t.Parallel()
	total := RequireFromString("10.00")
	buf := make([]Decimal, 0, 4)
	got, err := Split(buf, total, 3, 2)
	require.NoError(t, err)
	require.Equal(t, allocateShares(t, 2, "3.34", "3.33", "3.33"), got)
	require.Same(t, &buf[:1][0], &got[0], "a large enough dst is reused")

	short := make([]Decimal, 2)
	got, err = Split(short, total, 3, 2)
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.NotSame(t, &short[0], &got[0], "a short dst is replaced")
}
