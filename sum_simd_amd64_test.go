//go:build go1.27 && !go1.28 && goexperiment.simd && amd64

package zerodecimal

import (
	"math/rand"
	"simd/archsimd"
	"strconv"
	"testing"
)

func checkSIMDSumAgainstScalar(t *testing.T, ds []Decimal) {
	t.Helper()
	want, wantErr := arithScalarSumReference(ds)
	got, gotErr := Sum(ds[0], ds[1:]...)
	if got != want || gotErr != wantErr {
		t.Fatalf("got (%#v,%v), want (%#v,%v)", got, gotErr, want, wantErr)
	}
}

func TestSIMDSumProductionCorrectness(t *testing.T) {
	if !archsimd.X86.AVX2() {
		t.Skip("CPU does not expose AVX2")
	}

	rng := rand.New(rand.NewSource(3))
	for _, size := range []int{1, 2, 8, 9, 16, 17, 31, 32, 33, 63, 64, 65, 255, 256, 257, 4096} {
		ds := make([]Decimal, size)
		for i := range ds {
			if i%23 == 0 {
				continue
			}
			ds[i] = newDecimal(u128{lo: rng.Uint64()}, false, 4)
		}
		checkSIMDSumAgainstScalar(t, ds)
	}

	base := make([]Decimal, 257)
	for i := range base {
		base[i] = newDecimal(u128{lo: uint64(i) + 1}, false, 4)
	}
	for _, position := range []int{1, 4, 8, 9, 64, 128, 255, 256} {
		t.Run("negative-at-"+strconv.Itoa(position), func(t *testing.T) {
			ds := append([]Decimal(nil), base...)
			ds[position].neg = true
			checkSIMDSumAgainstScalar(t, ds)
			prefix, next, ok := sumSIMDPrefix(ds[0], ds[1:])
			if !ok || prefix.neg || next > position-1 {
				t.Fatalf("invalid continuation: prefix=%#v next=%d ok=%t", prefix, next, ok)
			}
		})

		t.Run("precision-at-"+strconv.Itoa(position), func(t *testing.T) {
			ds := append([]Decimal(nil), base...)
			ds[position].prec = 3
			checkSIMDSumAgainstScalar(t, ds)
			prefix, next, ok := sumSIMDPrefix(ds[0], ds[1:])
			if !ok || prefix.neg || next > position-1 {
				t.Fatalf("invalid continuation: prefix=%#v next=%d ok=%t", prefix, next, ok)
			}
		})

		t.Run("wide-at-"+strconv.Itoa(position), func(t *testing.T) {
			ds := append([]Decimal(nil), base...)
			ds[position].coef.hi = 1
			checkSIMDSumAgainstScalar(t, ds)
		})
	}

	// Exercise arbitrary continuation suffixes, including zeros, both signs,
	// and multiple precisions. Small coefficients keep every reference result
	// representable so failures isolate semantic mismatches rather than bounds.
	for iteration := range 200 {
		ds := make([]Decimal, 257)
		for i := range ds {
			if rng.Intn(11) == 0 {
				continue
			}
			ds[i] = newDecimal(
				u128{lo: uint64(rng.Int63n(1<<40)) + 1},
				rng.Intn(3) == 0,
				uint8(rng.Intn(5)), //nolint:gosec // bounded by construction
			)
		}
		t.Run("mixed-"+strconv.Itoa(iteration), func(t *testing.T) {
			checkSIMDSumAgainstScalar(t, ds)
		})
	}

	// A positive SIMD prefix may itself exceed 128 bits before a negative
	// suffix cancels it. That cold case must abandon the prefix and preserve
	// Sum's wide, order-independent contract.
	maxCoef := u128{hi: ^uint64(0), lo: ^uint64(0)}
	cancel := make([]Decimal, 34)
	for i := range 17 {
		cancel[i] = newDecimal(maxCoef, false, 0)
		cancel[i+17] = newDecimal(maxCoef, true, 0)
	}
	checkSIMDSumAgainstScalar(t, cancel)
}
