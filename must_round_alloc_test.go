package zerodecimal

import (
	"errors"
	"testing"
)

// TestMustRoundAllocs gates the success path of every Must twin at exactly
// zero allocations, on the one-limb shape each operation has a fast path for
// and on a wide shape that takes the 256-bit path. The twins add a branch and
// no heap traffic to the operations they wrap (see alloc_test.go for why the
// operands and the sink are package-level).
func TestMustRoundAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	ops := []struct {
		name string
		fn   func()
	}{
		{"must_mul_round_one_limb", func() { mustRoundSink = mustRoundQty.MustMulRound(mustRoundPrice, 2, ToNearestEven) }},
		{"must_mul_round_wide", func() { mustRoundSink = mustRoundWideA.MustMulRound(mustRoundWideB, 2, ToNearestEven) }},
		{"must_div_round_one_limb", func() { mustRoundSink = mustRoundNotional.MustDivRound(mustRoundQty, 2, ToNearestEven) }},
		{"must_div_round_wide", func() { mustRoundSink = mustRoundWideA.MustDivRound(mustRoundWideB, 19, ToNearestEven) }},
		{"must_mul_add_round_one_limb", func() { mustRoundSink = mustRoundQty.MustMulAddRound(mustRoundPrice, mustRoundFee, 2, ToNearestEven) }},
		{"must_mul_add_round_wide", func() { mustRoundSink = mustRoundWideA.MustMulAddRound(mustRoundWideB, mustRoundFee, 2, ToNearestEven) }},
		{"must_mul_div_round_one_limb", func() {
			mustRoundSink = mustRoundNotional.MustMulDivRound(mustRoundPart, mustRoundWhole, 2, ToNearestEven)
		}},
		{"must_mul_div_round_wide", func() {
			mustRoundSink = mustRoundWideA.MustMulDivRound(mustRoundWideB, mustRoundWhole, 2, ToNearestEven)
		}},
		{"must_avg_round", func() { mustRoundSink = MustAvgRound(mustRoundPrices[0], 2, ToNearestEven, mustRoundPrices[1:]...) }},
		{"must_avg_round_long", func() { mustRoundSink = MustAvgRound(mustRoundLong[0], 4, ToNearestEven, mustRoundLong[1:]...) }},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			requireAllocs(t, 0, op.fn)
		})
	}
}

// mustRoundPanicSink receives the recovered panic values of the panic-path
// gate. It is not an error variable: recover returns any.
var mustRoundPanicSink any

// TestMustRoundPanicAllocs gates the panic path at zero allocations as well:
// the panic value is the package's sentinel itself, so nothing is formatted,
// wrapped or copied to the heap on the way to the caller's recover. A twin
// that panicked with fmt.Errorf or errors.New(err.Error()) would allocate here
// and fail TestMustRoundTwinsPanicSentinels' bare-sentinel check as well.
func TestMustRoundPanicAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	ops := []struct {
		name string
		fn   func()
		want error
	}{
		{"must_mul_round_overflow", func() { mustRoundSink = mustRoundMax.MustMulRound(mustRoundMax, 0, ToNearestEven) }, ErrOverflow},
		{"must_div_round_zero_divisor", func() { mustRoundSink = mustRoundQty.MustDivRound(Decimal{}, 2, ToNearestEven) }, ErrDivideByZero},
		{"must_mul_add_round_places", func() {
			mustRoundSink = mustRoundQty.MustMulAddRound(mustRoundPrice, mustRoundFee, MaxPrec+1, ToNearestEven)
		}, ErrPrecOutOfRange},
		{"must_mul_div_round_mode", func() {
			mustRoundSink = mustRoundQty.MustMulDivRound(mustRoundPrice, mustRoundFee, 2, TowardNegative+1)
		}, ErrInvalidRoundingMode},
		{"must_avg_round_overflow", func() {
			mustRoundSink = MustAvgRound(mustRoundMax, 1, ToNearestEven, mustRoundMax, mustRoundMax)
		}, ErrOverflow},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			mustRoundPanicSink = nil
			requireAllocs(t, 0, func() {
				defer func() { mustRoundPanicSink = recover() }()
				op.fn()
			})
			if got, ok := mustRoundPanicSink.(error); !ok || !errors.Is(got, op.want) {
				t.Fatalf("panic value: got %v, want %v", mustRoundPanicSink, op.want)
			}
		})
	}
}
