package zerodecimal

import (
	"errors"
	"testing"
)

var (
	aggregateBenchResult Decimal
	errAggregateBench    error

	aggregateBenchOrdinary = []Decimal{
		RequireFromString("1250000.125"),
		RequireFromString("-249999.875"),
		RequireFromString("17.0000000000000000001"),
		RequireFromString("3.75"),
	}
	aggregateBenchCancellation = func() []Decimal {
		maxValue, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 0)
		if err != nil {
			panic(err)
		}
		return []Decimal{maxValue, maxValue, maxValue.Neg(), maxValue.Neg(), RequireFromString("0.01")}
	}()
	aggregateBenchWide = func() []Decimal {
		maxValue, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 0)
		if err != nil {
			panic(err)
		}
		return []Decimal{maxValue, maxValue}
	}()
	aggregateBenchLong = func() []Decimal {
		xs := make([]Decimal, 4096)
		for i := range xs {
			switch i % 4 {
			case 0:
				xs[i] = RequireFromString("1000000.0000000000000000001")
			case 1:
				xs[i] = RequireFromString("-999999.9999999999999999999")
			case 2:
				xs[i] = RequireFromString("17.25")
			default:
				// Each four-value block sums to exactly 0.125. Keeping the
				// long AvgExact row exact prevents it from silently measuring
				// ErrInexact under a success-looking benchmark name.
				xs[i] = RequireFromString("-17.1250000000000000002")
			}
		}
		return xs
	}()
)

func aggregateBenchOutcome(wantErr error) string {
	switch {
	case wantErr == nil:
		return "success"
	case errors.Is(wantErr, ErrOverflow):
		return "overflow"
	case errors.Is(wantErr, ErrInexact):
		return "inexact"
	default:
		return "error"
	}
}

func requireAggregateBenchError(b *testing.B, got, want error) {
	b.Helper()
	if want == nil {
		if got != nil {
			b.Fatalf("preflight: unexpected error: %v", got)
		}
		return
	}
	if !errors.Is(got, want) {
		b.Fatalf("preflight: got %v, want %v", got, want)
	}
}

func BenchmarkAggregates(b *testing.B) {
	shapes := []struct {
		name                                  string
		xs                                    []Decimal
		sumErr, avgErr, roundErr, avgExactErr error
	}{
		{name: "ordinary", xs: aggregateBenchOrdinary, avgExactErr: ErrInexact},
		{name: "cancellation_heavy", xs: aggregateBenchCancellation},
		{name: "wide_intermediate", xs: aggregateBenchWide, sumErr: ErrOverflow, roundErr: ErrOverflow},
		{name: "long_4096", xs: aggregateBenchLong},
	}
	for _, shape := range shapes {
		b.Run("Sum/"+shape.name+"/"+aggregateBenchOutcome(shape.sumErr), func(b *testing.B) {
			_, err := Sum(shape.xs[0], shape.xs[1:]...)
			requireAggregateBenchError(b, err, shape.sumErr)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				aggregateBenchResult, errAggregateBench = Sum(shape.xs[0], shape.xs[1:]...)
			}
		})
		b.Run("Avg/"+shape.name+"/"+aggregateBenchOutcome(shape.avgErr), func(b *testing.B) {
			_, err := Avg(shape.xs[0], shape.xs[1:]...)
			requireAggregateBenchError(b, err, shape.avgErr)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				aggregateBenchResult, errAggregateBench = Avg(shape.xs[0], shape.xs[1:]...)
			}
		})
		b.Run("AvgRound/"+shape.name+"/"+aggregateBenchOutcome(shape.roundErr), func(b *testing.B) {
			_, err := AvgRound(shape.xs[0], 8, ToNearestEven, shape.xs[1:]...)
			requireAggregateBenchError(b, err, shape.roundErr)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				aggregateBenchResult, errAggregateBench = AvgRound(shape.xs[0], 8, ToNearestEven, shape.xs[1:]...)
			}
		})
		b.Run("AvgExact/"+shape.name+"/"+aggregateBenchOutcome(shape.avgExactErr), func(b *testing.B) {
			_, err := AvgExact(shape.xs[0], shape.xs[1:]...)
			requireAggregateBenchError(b, err, shape.avgExactErr)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				aggregateBenchResult, errAggregateBench = AvgExact(shape.xs[0], shape.xs[1:]...)
			}
		})
	}
}
