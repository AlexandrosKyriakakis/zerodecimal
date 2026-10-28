//go:build fuzz

package zerodecimal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// accFuzzMaxOps bounds a decoded stream so every input stays fast.
const accFuzzMaxOps = 64

// accFuzzDecode turns bytes into an op stream. Each op is an opcode byte (low
// two bits: Add, Sub, AddMul, Add) followed by its operands. An operand is a
// meta byte (bit 7 sign, low seven bits precision mod 20), a length byte (mod
// 17) and that many big-endian coefficient bytes, so short inputs give small
// coefficients and 16 bytes give any 128-bit one. Missing bytes read as zero.
func accFuzzDecode(data []byte) []accOp {
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
	var ops []accOp
	for len(data) > 0 && len(ops) < accFuzzMaxOps {
		op := accOp{kind: accOpKind(next() & 3)}
		if op.kind > accAddMul {
			op.kind = accAdd
		}
		op.d = operand()
		if op.kind == accAddMul {
			op.e = operand()
		}
		ops = append(ops, op)
	}
	return ops
}

// accFuzzEncode is the inverse of accFuzzDecode, for seeds.
func accFuzzEncode(ops ...accOp) []byte {
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
	for _, op := range ops {
		data = append(data, byte(op.kind))
		operand(op.d)
		if op.kind == accAddMul {
			operand(op.e)
		}
	}
	return data
}

func TestAccumulatorFuzzCodecRoundTrip(t *testing.T) {
	ops := []accOp{
		{kind: accAdd, d: MustNew(50025, -2)},
		{kind: accSub, d: MustNew(-1, -int32(MaxPrec))},
		{kind: accAddMul, d: Decimal{coef: u128{hi: maxUint64, lo: 7}, prec: 3}, e: MustNew(-15, -1)},
	}
	require.Equal(t, ops, accFuzzDecode(accFuzzEncode(ops...)))
}

// FuzzAccumulator streams a byte-driven sequence of Add, Sub and AddMul
// terms, then checks Sum, Div and DivRound against the exact big.Int/big.Rat
// oracle (sticky 256-bit overflow included), pins Div and DivRound to Sum
// followed by the Decimal methods wherever Sum succeeds, and requires the
// reversed stream to give identical results. It must never panic.
func FuzzAccumulator(f *testing.F) {
	maxValue := Decimal{coef: u128{hi: maxUint64, lo: maxUint64}}
	tiny := Decimal{coef: u128{lo: 1}, prec: MaxPrec}
	seeds := [][]accOp{
		nil,
		{{kind: accAdd, d: maxValue}, {kind: accAdd, d: maxValue}, {kind: accSub, d: maxValue}},
		{{kind: accAdd, d: maxValue}, {kind: accAdd, d: tiny}},
		{{kind: accAddMul, d: maxValue, e: maxValue}, {kind: accAddMul, d: maxValue, e: maxValue}},
		{{kind: accAdd, d: tiny}, {kind: accAddMul, d: maxValue, e: maxValue}},
		{
			{kind: accAddMul, d: maxValue, e: maxValue},
			{kind: accAdd, d: maxValue},
			{kind: accAdd, d: maxValue},
			{kind: accSub, d: One},
		},
		{
			{kind: accAddMul, d: RequireFromString("100"), e: RequireFromString("10.25")},
			{kind: accAddMul, d: RequireFromString("50"), e: RequireFromString("10.40")},
			{kind: accAddMul, d: RequireFromString("25"), e: RequireFromString("10.10")},
		},
		{
			{kind: accAddMul, d: RequireFromString("1.234567891"), e: RequireFromString("101.1234567890123456")},
			{kind: accSub, d: RequireFromString("0.01")},
			{kind: accAdd, d: RequireFromString("-7.5")},
		},
	}
	for i, ops := range seeds {
		f.Add(accFuzzEncode(ops...), uint8(i), uint64(0), uint64(7+i), uint8(2*i), uint8(i))
	}
	f.Add(accFuzzEncode(seeds[2]...), uint8(0), uint64(0), uint64(0), uint8(2), uint8(1))
	f.Add(accFuzzEncode(seeds[6]...), uint8(0), uint64(0), uint64(175), MaxPrec+1, uint8(TowardNegative+1))

	f.Fuzz(func(t *testing.T, data []byte, denMeta uint8, denHi, denLo uint64, pc, mc uint8) {
		ops := accFuzzDecode(data)
		den := exactFuzzDecimal(denHi, denLo, denMeta)
		places := pc % (MaxPrec + 2)                       // MaxPrec+1 exercises validation
		mode := RoundingMode(mc % uint8(TowardNegative+2)) // so does TowardNegative+1
		a := accRun(ops)
		requireAccFinishers(t, a, accOracleRun(ops), den, places, mode, ops, den)

		reversed := make([]accOp, len(ops))
		for i, op := range ops {
			reversed[len(ops)-1-i] = op
		}
		b := accRun(reversed)
		wantSum, wantSumErr := a.Sum()
		gotSum, gotSumErr := b.Sum()
		require.Equal(t, wantSumErr, gotSumErr)
		require.Equal(t, wantSum, gotSum)
		wantDiv, wantDivErr := a.Div(den)
		gotDiv, gotDivErr := b.Div(den)
		require.Equal(t, wantDivErr, gotDivErr)
		require.Equal(t, wantDiv, gotDiv)
		wantRound, wantRoundErr := a.DivRound(den, places, mode)
		gotRound, gotRoundErr := b.DivRound(den, places, mode)
		require.Equal(t, wantRoundErr, gotRoundErr)
		require.Equal(t, wantRound, gotRound)
	})
}
