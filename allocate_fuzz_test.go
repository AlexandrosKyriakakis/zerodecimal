//go:build fuzz

package zerodecimal

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// allocateFuzzWidths are the coefficient widths a fuzzed weight's meta byte
// selects, so random input reaches all three division paths of Allocate
// instead of nearly always summing past 2^128.
var allocateFuzzWidths = [8]uint{8, 16, 32, 48, 64, 96, 112, 128}

// allocateFuzzWeights decodes up to 80 nonnegative weights, past the 64-weight
// stack buffer, from 17-byte records: a meta byte whose low five bits give the
// precision (mod 20) and whose top three bits pick the coefficient width, then
// the coefficient's 16 bytes. A short last record is zero-padded; empty data
// decodes to no weights at all.
func allocateFuzzWeights(data []byte) []Decimal {
	n := min((len(data)+16)/17, 80)
	ws := make([]Decimal, n)
	for i := range ws {
		var raw [17]byte
		copy(raw[:], data[i*17:min(i*17+17, len(data))])
		width := allocateFuzzWidths[raw[0]>>5]
		coef := u128{hi: binary.LittleEndian.Uint64(raw[1:]), lo: binary.LittleEndian.Uint64(raw[9:])}
		switch { // keep the low width bits; 1<<64 is 0 for a variable shift
		case width <= 64:
			coef = u128{lo: coef.lo & (uint64(1)<<width - 1)}
		case width < 128:
			coef.hi &= uint64(1)<<(width-64) - 1
		}
		ws[i] = newDecimal(coef, false, (raw[0]&0x1f)%(MaxPrec+1))
	}
	return ws
}

func TestLargestRemainderFuzzDecode(t *testing.T) {
	ws := []Decimal{MustNew(12_665, -2), {coef: u128{hi: ^uint64(0), lo: 1}, prec: MaxPrec}, {}}
	require.Equal(t, ws, allocateFuzzWeights(allocateFuzzEncode(ws...)))
	require.Empty(t, allocateFuzzWeights(nil))
	// Width 8 keeps the low byte; width 96 the low 32 bits of the high limb.
	data := allocateFuzzEncode(Decimal{coef: u128{hi: ^uint64(0), lo: ^uint64(0)}})
	data[0] = 0<<5 | 3
	require.Equal(t, []Decimal{{coef: u128{lo: 0xff}, prec: 3}}, allocateFuzzWeights(data))
	data[0] = 5 << 5
	require.Equal(t, []Decimal{{coef: u128{hi: 0xffffffff, lo: ^uint64(0)}}}, allocateFuzzWeights(data))
}

func allocateFuzzEncode(ws ...Decimal) []byte {
	data := make([]byte, 17*len(ws))
	for i, w := range ws {
		data[17*i] = 7<<5 | w.prec // full width
		binary.LittleEndian.PutUint64(data[17*i+1:], w.coef.hi)
		binary.LittleEndian.PutUint64(data[17*i+9:], w.coef.lo)
	}
	return data
}

// allocateFuzzPlaces reduces a fuzzed places byte to 0..MaxPrec; its top bit
// lifts places to at least total's precision, so a total is often exact at
// places instead of almost always failing with ErrInexact.
func allocateFuzzPlaces(pc uint8, total Decimal) uint8 {
	places := (pc & 0x7f) % (MaxPrec + 1)
	if pc&0x80 != 0 {
		places = max(places, total.prec)
	}
	return places
}

// requireAllocateIntOracle is requireAllocateOracle on the integer model
// alone, which the deterministic tests hold equal to the big.Rat model; it
// keeps the fuzzer fast.
func requireAllocateIntOracle(t *testing.T, total Decimal, weights []Decimal, places uint8) {
	t.Helper()
	want, wantErr := allocateWantInt(total, weights, places)
	got, err := Allocate(nil, total, weights, places)
	if wantErr != nil {
		require.ErrorIsf(t, err, wantErr, "total=%v weights=%v places=%d", total, weights, places)
		require.Emptyf(t, got, "an error returns dst[:0]: total=%v weights=%v", total, weights)
		return
	}
	require.NoErrorf(t, err, "total=%v weights=%v places=%d", total, weights, places)
	require.Equalf(t, want, got, "total=%v weights=%v places=%d", total, weights, places)
	requireAllocateIntProperties(t, total, weights, places, got)
}

// FuzzAllocate checks Allocate against the math/big oracle on fuzzed totals,
// places and slices of up to 80 weights, one of which may be negated: the
// error and its precedence, or the exact shares, which also must sum exactly
// to total and lie within one unit of 10^-places of their exact values.
// Allocating in place must agree, and must leave the weights unwritten on
// error.
func FuzzAllocate(f *testing.F) {
	maxCoef := u128{hi: ^uint64(0), lo: ^uint64(0)}
	seeds := []struct {
		weights []Decimal
		total   Decimal
		places  uint8
		negAt   uint8
	}{
		{allocateInts(1, 1, 1), MustNew(1_000, -2), 0x80 | 2, 255},
		{allocateInts(1, 1, 1, 3), MustNew(100, -2), 0x80 | 2, 255},
		{allocateDecimals("100", "250", "37.5"), MustNew(12_665, -2), 0x80 | 2, 255},
		{allocateDecimals("1e30", "0.0000000000000000003"), MustNew(-5, -2), 2, 255},
		{allocateInts(2, 2, 2, 2, 2, 2, 2, 2, 2, 1), RequireFromString("323268248574891540290205877060179800883"), 1, 255},
		{allocateInts(6, 6, 6, 8, 8, 2, 2, 5, 8, 2), RequireFromString("225437068085121732044485677423546440089"), 1, 255},
		{[]Decimal{{coef: maxCoef}, {coef: maxCoef}}, Decimal{coef: maxCoef}, 19, 255},
		{allocateInts(1, 2), MustNew(1, 0), 2, 1},
		{[]Decimal{{}, {}}, MustNew(1, 0), 2, 255},
		{allocateAllocWeights(70), MustNew(1_000, -2), 0x80 | 2, 255},
	}
	for _, s := range seeds {
		_, hi, lo, prec := s.total.ToHiLo()
		meta := prec
		if s.total.IsNegative() {
			meta |= 0x80
		}
		f.Add(allocateFuzzEncode(s.weights...), hi, lo, meta, s.places, s.negAt)
	}
	f.Fuzz(func(t *testing.T, data []byte, thi, tlo uint64, tmeta, pc, negAt uint8) {
		weights := allocateFuzzWeights(data)
		if int(negAt) < len(weights) {
			weights[negAt] = weights[negAt].Neg()
		}
		total := exactFuzzDecimal(thi, tlo, tmeta)
		places := allocateFuzzPlaces(pc, total)
		requireAllocateIntOracle(t, total, weights, places)

		want, err := Allocate(nil, total, weights, places)
		inPlace := slices.Clone(weights)
		got, inPlaceErr := Allocate(inPlace, total, inPlace, places)
		require.Equal(t, err, inPlaceErr)
		if err != nil {
			require.Equal(t, weights, inPlace, "an error must leave the weights unwritten")
			return
		}
		require.Equal(t, want, got)
	})
}

// FuzzSplit checks Split against its math/big oracle on fuzzed totals, places
// and share counts, nonpositive ones included, and against Allocate over
// equal weights.
func FuzzSplit(f *testing.F) {
	f.Add(uint64(0), uint64(1_000), uint8(2), uint8(0x80|2), int16(3))
	f.Add(uint64(0), uint64(5), uint8(0x80|2), uint8(2), int16(3))
	f.Add(uint64(0), uint64(1), uint8(0), uint8(0), int16(0))
	f.Add(uint64(0), uint64(1), uint8(0), uint8(0), int16(-7))
	f.Add(uint64(0x3333333333333333), uint64(0x3333333333333333), uint8(0), uint8(1), int16(2))
	f.Add(^uint64(0), ^uint64(0), uint8(0), uint8(19), int16(300))
	f.Fuzz(func(t *testing.T, thi, tlo uint64, tmeta, pc uint8, n int16) {
		total := exactFuzzDecimal(thi, tlo, tmeta)
		places := allocateFuzzPlaces(pc, total)
		requireSplitOracle(t, total, int(n), places)
		if n <= 0 || n > 300 {
			return
		}
		weights := make([]Decimal, n)
		for i := range weights {
			weights[i] = NewFromInt(1)
		}
		split, err := Split(nil, total, int(n), places)
		shares, allocErr := Allocate(nil, total, weights, places)
		require.Equal(t, allocErr, err)
		require.Equal(t, shares, split)
	})
}
