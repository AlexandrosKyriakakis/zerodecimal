package zdbig

// Allocation gates for the two conversions. The rules are those of the root
// package's alloc_test.go, and they are load-bearing: inputs are
// PACKAGE-LEVEL variables parsed or built once at init (a local literal could
// be constant-folded, measuring nothing); every result goes to a package-level
// sink (a discarded result lets the call be deleted); the measured closures
// hold the call and the sink stores only, and are built before
// testing.AllocsPerRun starts. Under -race every count is skipped: the race
// runtime allocates on instrumented accesses.

import (
	"math"
	"math/big"
	"testing"

	"github.com/AlexandrosKyriakakis/zerodecimal"
)

// allocRuns is the invocation count handed to testing.AllocsPerRun; it
// performs one more, unmeasured, warm-up call first.
const allocRuns = 500

// Package-level sinks (see the rules above).
var (
	sinkDecimal zerodecimal.Decimal
	sinkBig     *big.Int
	sinkExp     int32
	errSink     error
)

// requireAllocs fails the test unless fn averages exactly want allocations
// per run. It is deliberately testify-free: the assertion path of a gate must
// stay trivially allocation-transparent.
func requireAllocs(t *testing.T, want float64, fn func()) {
	t.Helper()
	if got := testing.AllocsPerRun(allocRuns, fn); got != want {
		t.Fatalf("allocations per run: got %v, want exactly %v", got, want)
	}
}

// allocCase is one FromBigInt input, built at init.
type allocCase struct {
	name string
	coef *big.Int
	exp  int32
}

// fits128 are coefficients that fit 128 bits, with exponents spanning every
// path through from128, successes and each of the two errors: FromBigInt must
// not allocate on any of them.
var fits128 = []allocCase{
	{"nil", nil, -2},
	{"zero", new(big.Int), 7},
	{"zero_min_exp", new(big.Int), math.MinInt32},
	{"one_limb", big.NewInt(1_234_567), -2},
	{"one_limb_negative", big.NewInt(-1_234_567), -2},
	{"exp_0", big.NewInt(1_234_567), 0},
	{"max_uint64_prec19", new(big.Int).SetUint64(math.MaxUint64), -19},
	{"two_limbs", pow10Big(30), -19},
	{"max128", max128, 0},
	{"max128_prec1", max128, -1},
	{"scale_up", big.NewInt(12_345), 20},
	{"scale_up_overflow", max128, 1},
	{"scale_up_exp_39", big.NewInt(1), 39},
	{"scale_up_exp_max", big.NewInt(1), math.MaxInt32},
	{"strip", pow10Big(30), -30},
	{"strip_two_divisions", pow10Big(38), -57},
	{"strip_negative", new(big.Int).Neg(pow10Big(30)), -30},
	{"strip_inexact", max128, -20},
	{"strip_past_the_digits", pow10Big(38), -58},
	{"min_exp", big.NewInt(1), math.MinInt32},
}

// TestFromBigIntAllocs pins the documented contract: no allocation when |coef|
// fits 128 bits.
func TestFromBigIntAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, tc := range fits128 {
		t.Run(tc.name, func(t *testing.T) {
			coef, exp := tc.coef, tc.exp
			requireAllocs(t, 0, func() { sinkDecimal, errSink = FromBigInt(coef, exp) })
		})
	}
}

// wideRejects are coefficients past 128 bits that FromBigInt turns away
// before it converts anything to decimal: no room to strip (exp ≥ 0), a value
// too large for any amount of stripping, or no zero to strip (not a multiple
// of ten). The last class is odd, or even but not a multiple of five, and the
// latter slips past a parity test alone. This is an implementation property
// rather than a documented one: it keeps hostile oversized input cheap.
var wideRejects = []allocCase{
	{"exp_0", two128, 0},
	{"exp_positive", pow10Big(45), 3},
	{"exp_max", pow10Big(45), math.MaxInt32},
	{"odd", new(big.Int).Add(pow10Big(45), bigOne), -40},
	{"odd_min_exp", new(big.Int).Add(pow10Big(45), bigOne), math.MinInt32},
	{"value_too_large", pow10Big(45), -5},
	{"huge", new(big.Int).Lsh(bigOne, 100_000), -2},
	{"even_not_a_multiple_of_ten", new(big.Int).Add(pow10Big(45), big.NewInt(2)), -40},
	{"power_of_two", new(big.Int).Lsh(bigOne, 200), -22},
	{"huge_power_of_two", new(big.Int).Lsh(bigOne, 100_000), -30_100},
}

// TestFromBigIntWideRejectionAllocs pins the cheap exits.
func TestFromBigIntWideRejectionAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, tc := range wideRejects {
		t.Run(tc.name, func(t *testing.T) {
			coef, exp := tc.coef, tc.exp
			requireAllocs(t, 0, func() { sinkDecimal, errSink = FromBigInt(coef, exp) })
		})
	}
}

// toBigIntCases are the Decimals ToBigInt is gated on: each call allocates
// exactly one object, the returned big.Int with its limbs.
var toBigIntCases = []struct {
	name string
	d    zerodecimal.Decimal
}{
	{"zero", zerodecimal.Decimal{}},
	{"one_limb", zerodecimal.MustNew(-1_234_567, -2)},
	{"max_uint64", zerodecimal.NewFromUint64(math.MaxUint64)},
	{"two_limbs", zerodecimal.RequireFromString("1234567890123456789012345.6789")},
	{"max_negative_prec19", func() zerodecimal.Decimal {
		d, err := zerodecimal.NewFromHiLo(true, math.MaxUint64, math.MaxUint64, 19)
		if err != nil {
			panic(err)
		}
		return d
	}()},
}

// TestToBigIntAllocs pins ToBigInt at exactly one allocation: the big.Int and
// its limbs share it.
func TestToBigIntAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, tc := range toBigIntCases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.d
			requireAllocs(t, 1, func() { sinkBig, sinkExp = ToBigInt(d) })
		})
	}
}
