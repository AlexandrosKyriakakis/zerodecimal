//go:build fuzz

package zerodecimal

import (
	"encoding/binary"
	"testing"

	shopspring "github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func aggregateFuzzEncode(xs ...Decimal) []byte {
	data := make([]byte, 17*len(xs))
	for i, d := range xs {
		meta := d.prec
		if d.neg {
			meta |= 0x80
		}
		data[17*i] = meta
		binary.LittleEndian.PutUint64(data[17*i+1:], d.coef.hi)
		binary.LittleEndian.PutUint64(data[17*i+9:], d.coef.lo)
	}
	return data
}

func aggregateFuzzDecode(data []byte) []Decimal {
	if len(data) == 0 {
		return []Decimal{{}}
	}
	n := (len(data) + 16) / 17
	if n > 32 {
		n = 32
	}
	xs := make([]Decimal, n)
	for i := range xs {
		var raw [17]byte
		start := i * 17
		end := min(start+17, len(data))
		copy(raw[:], data[start:end])
		prec := raw[0] % (MaxPrec + 1)
		d, err := NewFromHiLo(raw[0]&0x80 != 0, binary.LittleEndian.Uint64(raw[1:]), binary.LittleEndian.Uint64(raw[9:]), prec)
		if err != nil {
			panic(err)
		}
		xs[i] = d
	}
	return xs
}

// FuzzWideAggregates checks Sum, Avg, AvgExact, and AvgRound against exact
// math/big oracles over variable-length slices. shopspring/decimal is a
// second implementation oracle for every successful operation and for
// nearest-away direct rounding.
func FuzzWideAggregates(f *testing.F) {
	maxValue, err := NewFromHiLo(false, ^uint64(0), ^uint64(0), 0)
	if err != nil {
		panic(err)
	}
	minQuantum, err := NewFromHiLo(false, 0, 1, MaxPrec)
	if err != nil {
		panic(err)
	}
	f.Add(aggregateFuzzEncode(Zero), uint8(0), uint8(ToNearestEven))
	f.Add(aggregateFuzzEncode(maxValue, maxValue, maxValue.Neg()), uint8(19), uint8(ToNearestAway))
	f.Add(aggregateFuzzEncode(maxValue, maxValue, maxValue.Neg(), One), uint8(0), uint8(AwayFromZero))
	f.Add(aggregateFuzzEncode(minQuantum, Zero), uint8(19), uint8(TowardNegative))
	f.Add(aggregateFuzzEncode(One, NewFromInt(2), NewFromInt(4)), uint8(2), uint8(ToNearestEven))

	f.Fuzz(func(t *testing.T, data []byte, pc, mc uint8) {
		xs := aggregateFuzzDecode(data)
		places := pc % (MaxPrec + 1)
		mode := RoundingMode(mc % uint8(TowardNegative+1))

		requireAggregateSumOracle(t, xs)
		requireAggregateAvgOracle(t, xs)
		requireAggregateExactOracle(t, xs)
		requireAggregateRoundOracle(t, xs, places, mode)

		ssSum := shopspring.Zero
		for _, d := range xs {
			ssSum = ssSum.Add(ssOf(d))
		}
		ssCount := shopspring.NewFromInt(int64(len(xs)))

		gotSum, sumErr := Sum(xs[0], xs[1:]...)
		if sumErr == nil {
			require.True(t, ssSum.Equal(ssOf(gotSum)), "shopspring sum: xs=%+v", xs)
		}

		gotAvg, avgErr := Avg(xs[0], xs[1:]...)
		require.NoError(t, avgErr)
		ssAvg, _ := ssSum.QuoRem(ssCount, int32(gotAvg.prec))
		require.True(t, ssAvg.Equal(ssOf(gotAvg)), "shopspring truncated average: xs=%+v", xs)

		gotExact, exactErr := AvgExact(xs[0], xs[1:]...)
		if exactErr == nil {
			require.True(t, ssOf(gotExact).Mul(ssCount).Equal(ssSum), "shopspring exact average: xs=%+v", xs)
		}

		gotRound, roundErr := AvgRound(xs[0], places, mode, xs[1:]...)
		if mode == ToNearestAway && roundErr == nil {
			ssRound := ssSum.DivRound(ssCount, int32(places))
			require.True(t, ssRound.Equal(ssOf(gotRound)), "shopspring rounded average: xs=%+v", xs)
		}
	})
}
