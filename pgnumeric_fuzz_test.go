//go:build fuzz

package zerodecimal

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzParsePgNumeric feeds arbitrary bytes to ParsePgNumeric. It must never
// panic and must agree with the big.Int reference decoder on every input —
// the same value and representation, or the same sentinel with a zero
// result — and, where the reference renders a text form within the parser's
// bound, with NewFromString of that text. Every accepted input re-encodes to
// the reference encoder's bytes, which decode back to the identical Decimal.
func FuzzParsePgNumeric(f *testing.F) {
	for _, v := range pgNumericPostgresVectors {
		b, err := hex.DecodeString(v.hex)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	for _, s := range fuzzCorpus {
		if d, err := NewFromHiLo(s.neg, s.hi, s.lo, s.prec); err == nil {
			f.Add(d.AppendPgNumeric(nil))
		}
	}
	for _, raw := range [][]byte{
		{},
		{0, 1, 0, 0, 0, 0, 0},
		pgWire(0xFFFF, 0, 0, 0),
		pgDigitsWire(0, 0, 0, 10000),
		pgDigitsWire(0, 0x1234, 0, 1),
		pgDigitsWire(0, 0, 0x4000, 1),
		pgDigitsWire(0, 0xC000, 0, 10000),
		pgDigitsWire(-5, 0, 20, 1),
		pgDigitsWire(10, 0, 0, 1),
		pgDigitsWire(9, 0, 0, 35, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1000),
		pgDigitsWire(2, 0x4000, 4, 0, 0, 1, 2345, 0),
		pgDigitsWire(32767, 0, 0, 1),
		pgDigitsWire(-32768, 0, 0x3FFF, 0, 1),
		pgDigitsWire(-4096, 0, 0x3FFF, 10),
		pgDigitsWire(0, 0, 2, 1, 2345),
		pgDigitsWire(0, 0x4000, 9, 1, 0, 0, 5),
		pgDigitsWire(0, 0, 5, 10000, 0, 1),
		pgDigitsWire(0, 0, 4, 1, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999, 9999),
		pgWire(0x8000, 0x7FFF, 0, 0, append(make([]uint16, 0x7FFF), 7)...),
	} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		requirePgDecode(t, src)
		d, err := ParsePgNumeric(src)
		if err != nil {
			return
		}
		enc := d.AppendPgNumeric(nil)
		require.Equalf(t, pgRefEncode(d), enc, "re-encode: src=% x", src)
		back, err := ParsePgNumeric(enc)
		require.NoErrorf(t, err, "re-encoded: src=% x enc=% x", src, enc)
		require.Equalf(t, d, back, "decode of the re-encoding: src=% x enc=% x", src, enc)
	})
}

// FuzzAppendPgNumeric encodes fuzzed operand quadruples — both operands and
// their product, which reaches past raw quadruples — and requires the
// reference encoder's exact bytes within PgNumericMaxLen, a decode back to
// d.Trim() that the reference decoder confirms, and an append that keeps b's
// existing bytes.
func FuzzAppendPgNumeric(f *testing.F) {
	fuzzPairs(f)
	f.Fuzz(func(t *testing.T, aneg bool, ahi, alo uint64, aprec uint8, bneg bool, bhi, blo uint64, bprec uint8) {
		a, b := fuzzOperands(t, aneg, ahi, alo, aprec, bneg, bhi, blo, bprec)
		for _, d := range []Decimal{a, b, fuzzProduct(t, a, b)} {
			enc := d.AppendPgNumeric(nil)
			require.LessOrEqualf(t, len(enc), PgNumericMaxLen, "d=%+v", d)
			require.Equalf(t, pgRefEncode(d), enc, "d=%+v", d)
			got, err := ParsePgNumeric(enc)
			require.NoErrorf(t, err, "d=%+v enc=% x", d, enc)
			require.Equalf(t, d.Trim(), got, "d=%+v enc=% x", d, enc)
			requirePgDecode(t, enc)
			prefixed := d.AppendPgNumeric([]byte{0xAA, 0x55})
			require.Equalf(t, append([]byte{0xAA, 0x55}, enc...), prefixed, "d=%+v", d)
		}
	})
}
