//go:build fuzz

package zerodecimal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// wavgFuzzMaxRows bounds a decoded stream so every input stays fast.
const wavgFuzzMaxRows = 32

// wavgFuzzDecode turns bytes into rows. Each row is a flag byte (bit 0:
// AddTotal rather than Add) followed by its value and weight operands. An
// operand is a meta byte (bit 7 sign, low seven bits precision mod 20), a
// length byte (mod 17) and that many big-endian coefficient bytes, so short
// inputs give small coefficients and 16 bytes give any 128-bit one. Missing
// bytes read as zero.
func wavgFuzzDecode(data []byte) []wavgRow {
	next := func() byte {
		if len(data) == 0 {
			return 0
		}
		b := data[0]
		data = data[1:]
		return b
	}
	operand := func() Decimal {
		meta := next()
		var c u128
		for range next() % 17 {
			c.hi = c.hi<<8 | c.lo>>56
			c.lo = c.lo<<8 | uint64(next())
		}
		return newDecimal(c, meta&0x80 != 0, (meta&0x7f)%(MaxPrec+1))
	}
	var rows []wavgRow
	for len(data) > 0 && len(rows) < wavgFuzzMaxRows {
		r := wavgRow{total: next()&1 != 0}
		r.value = operand()
		r.weight = operand()
		rows = append(rows, r)
	}
	return rows
}

// wavgFuzzEncode is the inverse of wavgFuzzDecode, for seeds.
func wavgFuzzEncode(rows ...wavgRow) []byte {
	var data []byte
	operand := func(d Decimal) {
		meta := d.prec
		if d.neg {
			meta |= 0x80
		}
		data = append(data, meta, 16)
		for shift := 56; shift >= 0; shift -= 8 {
			data = append(data, byte(d.coef.hi>>shift))
		}
		for shift := 56; shift >= 0; shift -= 8 {
			data = append(data, byte(d.coef.lo>>shift))
		}
	}
	for _, r := range rows {
		var flag byte
		if r.total {
			flag = 1
		}
		data = append(data, flag)
		operand(r.value)
		operand(r.weight)
	}
	return data
}

func TestWeightedAvgFuzzCodecRoundTrip(t *testing.T) {
	rows := []wavgRow{
		{value: MustNew(1025, -2), weight: MustNew(-15, -1)},
		{total: true, value: MustNew(-1, -int32(MaxPrec)), weight: Decimal{}},
		{value: Decimal{coef: u128{hi: maxUint64, lo: 7}, prec: 3}, weight: wavgMax},
	}
	require.Equal(t, rows, wavgFuzzDecode(wavgFuzzEncode(rows...)))
}

// FuzzWeightedAvg streams a byte-driven sequence of Add and AddTotal rows,
// then holds Total, Weight, Avg and AvgRound to the exact big.Int/big.Rat
// oracle (and to Total followed by Div and DivRound by Weight wherever both
// sums fit), pins the internal state and Avg and AvgRound to two raw
// Accumulators fed the same terms, and requires the reversed stream to give
// identical results. It must never panic.
func FuzzWeightedAvg(f *testing.F) {
	d := RequireFromString
	ulp := Decimal{coef: u128{lo: 1}, prec: MaxPrec}
	seeds := [][]wavgRow{
		nil,
		// A VWAP of three fills, bought and then sold.
		{{value: d("10.25"), weight: NewFromInt(100)}, {value: d("10.40"), weight: NewFromInt(50)}, {value: d("10.10"), weight: NewFromInt(25)}},
		{{value: d("10.25"), weight: NewFromInt(-100)}, {total: true, value: NewFromInt(-520), weight: NewFromInt(-50)}},
		// Weights that cancel.
		{{value: NewFromInt(10), weight: NewFromInt(5)}, {value: NewFromInt(11), weight: NewFromInt(-5)}},
		// A wide numerator whose mean fits only at 0 places.
		{{total: true, value: wavgMax, weight: NewFromInt(2)}, {total: true, value: wavgMax, weight: NewFromInt(2)}},
		// A total weight of 2^128.
		{{total: true, value: One, weight: wavgMax}, {total: true, value: One, weight: One}},
		// A sticky numerator over a zero weight.
		{
			{value: wavgMax, weight: wavgMax},
			{value: wavgMax, weight: wavgMax},
			{total: true, weight: wavgMax.Neg()},
			{total: true, weight: wavgMax.Neg()},
		},
		// No precision holds the quotient.
		{{total: true, value: wavgMax, weight: ulp}},
		// A truncated product, and a quotient just past a tie.
		{{value: d("1.234567891"), weight: d("101.1234567890123456")}, {total: true, value: d("0.375").MustAdd(ulp), weight: NewFromInt(3)}},
	}
	for i, rows := range seeds {
		f.Add(wavgFuzzEncode(rows...), uint8(2*i), uint8(i))
	}
	f.Add(wavgFuzzEncode(seeds[1]...), MaxPrec+1, uint8(TowardNegative+1))

	f.Fuzz(func(t *testing.T, data []byte, pc, mc uint8) {
		rows := wavgFuzzDecode(data)
		places := pc % (MaxPrec + 2)                       // MaxPrec+1 exercises validation
		mode := RoundingMode(mc % uint8(TowardNegative+2)) // so does TowardNegative+1
		w := wavgRun(rows)
		requireWavgFinishers(t, w, wavgOracleRun(rows), places, mode, rows)

		var num, weight Accumulator
		for _, r := range rows {
			if r.total {
				num.Add(r.value)
			} else {
				num.AddMul(r.value, r.weight)
			}
			weight.Add(r.weight)
		}
		require.Equal(t, num, w.num, "numerator state")
		require.Equal(t, weight, w.weight, "weight state")
		if den, err := weight.Sum(); err == nil {
			want, wantErr := num.Div(den)
			got, gotErr := w.Avg()
			requireWavgResult(t, "Avg vs Accumulators", want, wantErr, got, gotErr, rows)
			want, wantErr = num.DivRound(den, places, mode)
			got, gotErr = w.AvgRound(places, mode)
			requireWavgResult(t, "AvgRound vs Accumulators", want, wantErr, got, gotErr, rows)
		}

		reversed := make([]wavgRow, len(rows))
		for i, r := range rows {
			reversed[len(rows)-1-i] = r
		}
		b := wavgRun(reversed)
		for _, finisher := range []struct {
			name string
			read func(*WeightedAvg) (Decimal, error)
		}{
			{"Total", (*WeightedAvg).Total},
			{"Weight", (*WeightedAvg).Weight},
			{"Avg", (*WeightedAvg).Avg},
			{"AvgRound", func(x *WeightedAvg) (Decimal, error) { return x.AvgRound(places, mode) }},
		} {
			want, wantErr := finisher.read(w)
			got, gotErr := finisher.read(b)
			requireWavgResult(t, finisher.name+" reversed", want, wantErr, got, gotErr, rows)
		}
	})
}
