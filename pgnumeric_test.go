package zerodecimal

import (
	"encoding/binary"
	"encoding/hex"
	"math/big"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// pgNumericPostgresVectors are numeric_send results captured from a
// throwaway local PostgreSQL 18.6 server:
//
//	SELECT v::numeric::text, encode(numeric_send(v::numeric), 'hex')
//
// The text column is PostgreSQL's own rendering, which spells dscale out as
// trailing zeros ("1.50" has dscale 2). The inputs cover zero at several
// scales ('-0' and '-0.00' come back as the positive "0" and "0.00"), the
// special values (PostgreSQL sends dscale 0x20 for ±Infinity), one- and
// two-limb coefficients, the maximum coefficient at every precision 0..19,
// leading zero fraction digits, trailing zero integer digits, the 2^64 and
// 2^128 neighbours, and values past MaxPrec places or past 128 bits.
var pgNumericPostgresVectors = []struct{ text, hex string }{
	{"12345.6789", "0003000100000004000109291a85"},
	{"0.0001", "0001ffff000000040001"},
	{"-1.5", "000200004000000100011388"},
	{"100000000", "00010002000000000001"},
	{"0.000000001", "0001fffd0000000903e8"},
	{"0", "0000000000000000"},
	{"0.00", "0000000000000002"},
	{"NaN", "00000000c0000000"},
	{"Infinity", "00000000d0000020"},
	{"-Infinity", "00000000f0000020"},
	{"1", "00010000000000000001"},
	{"-1", "00010000400000000001"},
	{"9999", "0001000000000000270f"},
	{"10000", "00010001000000000001"},
	{"10001", "000200010000000000010001"},
	{"99999999", "0002000100000000270f270f"},
	{"100000000.5", "00040002000000010001000000001388"},
	{"10000.00001", "000400010000000500010000000003e8"},
	{"0.9999", "0001ffff00000004270f"},
	{"0.99999", "0002ffff00000005270f2328"},
	{"1.50", "000200000000000200011388"},
	{"1.500000", "000200000000000600011388"},
	{"1.5000000000000000000", "000200000000001300011388"},
	{"0.0000000000000000001", "0001fffb00000013000a"},
	{"-0.0000000000000000001", "0001fffb40000013000a"},
	{"0.00000001", "0001fffe000000080001"},
	{"0.00001", "0001fffe0000000503e8"},
	{"0.00010", "0001ffff000000050001"},
	{"0.000100000", "0001ffff000000090001"},
	{"12.345678900", "0003000000000009000c0d801ed2"},
	{"1234.500000000", "000200000000000904d21388"},
	{"1234567.89", "0003000100000002007b11d722c4"},
	{"-1234567.89", "0003000140000002007b11d722c4"},
	{"0.123456789012345678", "0005ffff0000001204d2162e23340d801e78"},
	{"123456789.123456789012345678", "0008000200000012000109291a8504d2162e23340d801e78"},
	{"1.000000000000000000", "00010000000000120001"},
	{"340282366920938463463374607431768211455", "000a00090000000001540b071a2403aa121a18c111ff10dd1aa505af"},
	{"34028236692093846346337460743176821145.5", "000b0009000000010022011a0e55082d210f12191d241d071e0204791388"},
	{"3402823669209384634633746074317682114.55", "000b00090000000200030fbc093e23f90f060d870ea202e706e80842157c"},
	{"340282366920938463463374607431768211.455", "000a0008000000030d4a202c1b0824a818ca0d2e17ba0c68201311c6"},
	{"34028236692093846346337460743176821.1455", "000a00080000000401540b071a2403aa121a18c111ff10dd1aa505af"},
	{"3402823669209384634633746074317682.11455", "000b0008000000050022011a0e55082d210f12191d241d071e0204791388"},
	{"340282366920938463463374607431768.211455", "000b00080000000600030fbc093e23f90f060d870ea202e706e80842157c"},
	{"34028236692093846346337460743176.8211455", "000a0007000000070d4a202c1b0824a818ca0d2e17ba0c68201311c6"},
	{"3402823669209384634633746074317.68211455", "000a00070000000801540b071a2403aa121a18c111ff10dd1aa505af"},
	{"340282366920938463463374607431.768211455", "000b0007000000090022011a0e55082d210f12191d241d071e0204791388"},
	{"34028236692093846346337460743.1768211455", "000b00070000000a00030fbc093e23f90f060d870ea202e706e80842157c"},
	{"3402823669209384634633746074.31768211455", "000a00060000000b0d4a202c1b0824a818ca0d2e17ba0c68201311c6"},
	{"340282366920938463463374607.431768211455", "000a00060000000c01540b071a2403aa121a18c111ff10dd1aa505af"},
	{"34028236692093846346337460.7431768211455", "000b00060000000d0022011a0e55082d210f12191d241d071e0204791388"},
	{"3402823669209384634633746.07431768211455", "000b00060000000e00030fbc093e23f90f060d870ea202e706e80842157c"},
	{"340282366920938463463374.607431768211455", "000a00050000000f0d4a202c1b0824a818ca0d2e17ba0c68201311c6"},
	{"34028236692093846346337.4607431768211455", "000a00050000001001540b071a2403aa121a18c111ff10dd1aa505af"},
	{"3402823669209384634633.74607431768211455", "000b0005000000110022011a0e55082d210f12191d241d071e0204791388"},
	{"340282366920938463463.374607431768211455", "000b00050000001200030fbc093e23f90f060d870ea202e706e80842157c"},
	{"34028236692093846346.3374607431768211455", "000a0004000000130d4a202c1b0824a818ca0d2e17ba0c68201311c6"},
	{"-340282366920938463463374607431768211455", "000a00094000000001540b071a2403aa121a18c111ff10dd1aa505af"},
	{"-34028236692093846346.3374607431768211455", "000a0004400000130d4a202c1b0824a818ca0d2e17ba0c68201311c6"},
	{"18446744073709551615", "000500040000000007341a5802e103bb064f"},
	{"18446744073709551616", "000500040000000007341a5802e103bb0650"},
	{"1844674407370955161.5", "000600040000000100b812420fe91bb714291388"},
	{"1844674407370955161.6", "000600040000000100b812420fe91bb714291770"},
	{"100000000000000000000000000000000000000", "00010009000000000064"},
	{"99999999999999999999999999999999999999", "000a0009000000000063270f270f270f270f270f270f270f270f270f"},
	{"1000000000000000000000000000000000000.1", "000b000900000001000100000000000000000000000000000000000003e8"},
	{"10000000000000000000000000000000000000.1", "000b000900000001000a00000000000000000000000000000000000003e8"},
	{"100000000000000000000000000000000000.0001", "000a00080000000403e8000000000000000000000000000000000001"},
	{"0.1", "0001ffff0000000103e8"},
	{"0.12", "0001ffff0000000204b0"},
	{"0.123", "0001ffff0000000304ce"},
	{"0.1234", "0001ffff0000000404d2"},
	{"0.12345", "0002ffff0000000504d21388"},
	{"0.000000000000000001", "0001fffb000000120064"},
	{"0.0000000000000001", "0001fffc000000100001"},
	{"0.00000000000000000010", "0001fffb00000014000a"},
	{"1.00000000000000000000", "00010000000000140001"},
	{"0.00000000000000000001", "0001fffb000000140001"},
	{"-0.00000000000000000001", "0001fffb400000140001"},
	{"0.0000000000000000000100", "0001fffb000000160001"},
	{"340282366920938463463374607431768211456", "000a00090000000001540b071a2403aa121a18c111ff10dd1aa505b0"},
	{"1000000000000000000000000000000000000000", "000100090000000003e8"},
	{"3402823669209384634633746074317682114.551", "000b00090000000300030fbc093e23f90f060d870ea202e706e808421586"},
	{"1" + strings.Repeat("0", 100), "00010019000000000001"},
	{"0." + strings.Repeat("0", 99) + "1", "0001ffe7000000640001"},
	{"12345678901234567890123456789012345678901234567890", "000d000c00000000000c0d801ed204d2162e23340d801ed204d2162e23340d801ed2"},
	{"0." + strings.Repeat("0", 48) + "01", "0001fff3000000320064"},
	{"5000", "00010000000000001388"},
	{"50000000", "00010001000000001388"},
	{"0.5", "0001ffff000000011388"},
	{"0.05", "0001ffff0000000201f4"},
	{"0.005", "0001ffff000000030032"},
	{"0.0005", "0001ffff000000040005"},
	{"0.00005", "0001fffe000000051388"},
	{"7.0000", "00010000000000040007"},
	{"70000.0000", "00010001000000040007"},
}

// pgBytes decodes a hex vector. Spaces are ignored, so the grouped
// spelling ("0003 0001 …") pastes verbatim.
func pgBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	require.NoError(t, err, "bad hex vector %q", s)
	return b
}

// pgWire assembles a wire encoding from raw header words and digits. Nothing
// is validated, so malformed shapes can be built as easily as valid ones.
func pgWire(ndigits, weight, sign, dscale uint16, digits ...uint16) []byte {
	b := make([]byte, 0, pgHeaderLen+2*len(digits))
	for _, w := range append([]uint16{ndigits, weight, sign, dscale}, digits...) {
		b = binary.BigEndian.AppendUint16(b, w)
	}
	return b
}

// pgDigitsWire is pgWire with ndigits taken from the digits and a signed
// weight.
func pgDigitsWire(weight int16, sign, dscale uint16, digits ...uint16) []byte {
	return pgWire(uint16(len(digits)), uint16(weight), sign, dscale, digits...)
}

// pgRefEncodeText is the reference encoder. It follows numeric_send on the
// decimal text rather than on a coefficient: the integer digits are grouped in
// fours leftward from the point and the fraction digits rightward, zero
// groups are stripped at both ends, the weight counts groups from the point,
// and zero is ndigits 0 with weight 0 and a positive sign. s is a plain
// decimal ("-12.5"); any length works, so values no Decimal can hold have
// encodings too.
func pgRefEncodeText(s string, dscale uint16) []byte {
	sign := uint16(0)
	if rest, ok := strings.CutPrefix(s, "-"); ok {
		s, sign = rest, pgSignNeg
	}
	intPart, fracPart, _ := strings.Cut(s, ".")
	intPart = strings.Repeat("0", (4-len(intPart)%4)%4) + intPart
	fracPart += strings.Repeat("0", (4-len(fracPart)%4)%4)
	all := intPart + fracPart
	weight := len(intPart)/4 - 1
	var groups []uint16
	for i := 0; i < len(all); i += 4 {
		g, err := strconv.ParseUint(all[i:i+4], 10, 16)
		if err != nil {
			panic(err)
		}
		groups = append(groups, uint16(g))
	}
	for len(groups) > 0 && groups[0] == 0 {
		groups = groups[1:]
		weight--
	}
	for len(groups) > 0 && groups[len(groups)-1] == 0 {
		groups = groups[:len(groups)-1]
	}
	if len(groups) == 0 {
		weight, sign = 0, 0
	}
	return pgDigitsWire(int16(weight), sign, dscale, groups...)
}

// pgRefEncode is the reference encoding of d: its canonical string grouped
// by pgRefEncodeText, with dscale = d.Prec().
func pgRefEncode(d Decimal) []byte {
	return pgRefEncodeText(d.String(), uint16(d.Prec()))
}

// pgRefDecode is the reference decoder. It validates what numeric_recv
// validates, reads ndigits unsigned as numeric_recv does, and applies
// numeric_recv's truncation (trunc_var) to the decimal text rather than to
// base-10000 digits: it spells every digit out as four decimal places, puts
// the point 4·(weight+1) places in, and cuts the places past dscale. The
// rest is parsed with big.Int under the text parser's range policy, in the
// text parser's order: ErrOverflow when the significant digits exceed 128
// bits, then ErrPrecOutOfRange past MaxPrec places, then ErrOverflow for an
// integer scaled past 128 bits. text is the kept value in scientific
// notation, for cross-checking against NewFromString; it is empty when the
// input is not a finite number.
func pgRefDecode(src []byte) (d Decimal, text string, err error) {
	if len(src) < pgHeaderLen {
		return Decimal{}, "", ErrInvalidBinaryData
	}
	ndigits := int(binary.BigEndian.Uint16(src))
	weight := int(int16(binary.BigEndian.Uint16(src[2:])))
	sign := binary.BigEndian.Uint16(src[4:])
	dscale := int(binary.BigEndian.Uint16(src[6:]))
	if len(src) != pgHeaderLen+2*ndigits || dscale > 0x3FFF {
		return Decimal{}, "", ErrInvalidBinaryData
	}
	switch sign {
	case 0x0000, 0x4000, 0xC000, 0xD000, 0xF000:
	default:
		return Decimal{}, "", ErrInvalidBinaryData
	}
	places := make([]byte, 0, 4*ndigits)
	for i := range ndigits {
		dg := binary.BigEndian.Uint16(src[pgHeaderLen+2*i:])
		if dg > 9999 {
			return Decimal{}, "", ErrInvalidBinaryData
		}
		places = append(places, byte('0'+dg/1000), byte('0'+dg/100%10), byte('0'+dg/10%10), byte('0'+dg%10))
	}
	if sign >= 0xC000 {
		return Decimal{}, "", ErrInvalidFloat
	}

	// value = places·10^exp, with the point 4·(weight+1) places in; the
	// places past dscale are numeric_recv's to truncate.
	point := 4 * (weight + 1)
	if keep := point + dscale; keep < len(places) {
		places = places[:max(keep, 0)]
	}
	exp := point - len(places)
	digits := strings.TrimRight(string(places), "0")
	exp += len(places) - len(digits)
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return Decimal{}, "0", nil
	}
	text = digits + "e" + strconv.Itoa(exp)
	if sign == pgSignNeg {
		text = "-" + text
	}
	if len(digits) > 39 { // at least 10^39 > 2^128
		return Decimal{}, text, ErrOverflow
	}
	num, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		panic(digits)
	}
	switch {
	case num.BitLen() > 128:
		return Decimal{}, text, ErrOverflow
	case exp < -int(MaxPrec):
		return Decimal{}, text, ErrPrecOutOfRange
	case exp > 38: // num ≥ 1, so num·10^exp ≥ 10^39 > 2^128
		return Decimal{}, text, ErrOverflow
	case exp > 0:
		num.Mul(num, bp10(exp))
		if num.BitLen() > 128 {
			return Decimal{}, text, ErrOverflow
		}
		exp = 0
	}
	lo := new(big.Int).And(num, new(big.Int).SetUint64(^uint64(0))).Uint64()
	hi := new(big.Int).Rsh(num, 64).Uint64()
	return newDecimal(u128{hi: hi, lo: lo}, sign == pgSignNeg, uint8(-exp)), text, nil
}

// requirePgDecode holds ParsePgNumeric to the reference decoder on src: the
// identical value and representation, or the identical sentinel with a zero
// result. When the reference produced a text form within the parser's input
// bound, NewFromString must agree with the reference as well, which pins
// the reference's error precedence to the text protocol's.
func requirePgDecode(t *testing.T, src []byte) {
	t.Helper()
	want, text, wantErr := pgRefDecode(src)
	got, err := ParsePgNumeric(src)
	if wantErr != nil {
		require.ErrorIsf(t, err, wantErr, "src=% x", src)
		require.Equalf(t, Decimal{}, got, "an error returns the zero Decimal: src=% x", src)
	} else {
		require.NoErrorf(t, err, "src=% x", src)
		require.Equalf(t, want, got, "src=% x", src)
	}
	if text == "" || len(text) > maxParseLen {
		return
	}
	fromText, textErr := NewFromString(text)
	if wantErr != nil {
		require.ErrorIsf(t, textErr, wantErr, "text form %q of % x", text, src)
		return
	}
	require.NoErrorf(t, textErr, "text form %q of % x", text, src)
	require.Equalf(t, want, fromText, "text form %q of % x", text, src)
}

// TestPgNumericVectors pins hand-verified vectors in both directions:
// decoding yields the exact canonical value and representation, and encoding
// the value at the vector's dscale yields the exact bytes.
func TestPgNumericVectors(t *testing.T) {
	tests := []struct {
		name  string
		value string // canonical text of the decoded value; "" with err
		prec  uint8  // representation to encode, the vector's dscale
		wire  string
		// decodeOnly marks a dscale no Decimal can carry: canonical zero
		// always has precision 0.
		decodeOnly bool
		err        error
	}{
		{name: "12345.6789", value: "12345.6789", prec: 4, wire: "0003 0001 0000 0004 0001 0929 1A85"},
		{name: "0.0001", value: "0.0001", prec: 4, wire: "0001 FFFF 0000 0004 0001"},
		{name: "-1.5", value: "-1.5", prec: 1, wire: "0002 0000 4000 0001 0001 1388"},
		{name: "100000000", value: "100000000", wire: "0001 0002 0000 0000 0001"},
		{name: "1e-9_prec9", value: "0.000000001", prec: 9, wire: "0001 FFFD 0000 0009 03E8"},
		{name: "zero", value: "0", wire: "0000 0000 0000 0000"},
		{name: "zero_dscale2", value: "0", wire: "0000 0000 0000 0002", decodeOnly: true},
		{name: "nan", wire: "0000 0000 C000 0000", err: ErrInvalidFloat},
		{name: "pos_inf", wire: "0000 0000 D000 0000", err: ErrInvalidFloat},
		{name: "neg_inf", wire: "0000 0000 F000 0000", err: ErrInvalidFloat},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wire := pgBytes(t, tc.wire)
			got, err := ParsePgNumeric(wire)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, Decimal{}, got)
				return
			}
			require.NoError(t, err)
			want := RequireFromString(tc.value)
			require.Equal(t, want, got, "value and canonical representation")
			if tc.decodeOnly {
				return
			}
			d := want.MustRescale(tc.prec)
			require.Equal(t, tc.prec, d.Prec())
			require.Equal(t, wire, d.AppendPgNumeric(nil))
		})
	}
}

// TestPgNumericPostgresVectors replays every captured numeric_send vector.
// Decoding must equal NewFromString of PostgreSQL's text rendering, value,
// representation and sentinel alike, so the binary and text protocols scan to
// == values. Encoding the decoded value at the vector's dscale must
// reproduce PostgreSQL's bytes exactly wherever a Decimal can carry that
// representation.
func TestPgNumericPostgresVectors(t *testing.T) {
	encoded := 0
	for _, v := range pgNumericPostgresVectors {
		t.Run(v.text, func(t *testing.T) {
			wire := pgBytes(t, v.hex)
			requirePgDecode(t, wire)
			got, err := ParsePgNumeric(wire)
			switch v.text {
			case "NaN", "Infinity", "-Infinity":
				require.ErrorIs(t, err, ErrInvalidFloat)
				require.Equal(t, Decimal{}, got)
				return
			}
			want, wantErr := NewFromString(v.text)
			if wantErr != nil {
				require.ErrorIs(t, err, wantErr)
				require.Equal(t, Decimal{}, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, want, got)

			dscale := binary.BigEndian.Uint16(wire[6:])
			if dscale > uint16(MaxPrec) || got.IsZero() && dscale != 0 {
				return // no Decimal has this representation
			}
			d, err := got.Rescale(uint8(dscale))
			if err != nil {
				require.ErrorIs(t, err, ErrOverflow)
				return
			}
			require.Equal(t, wire, d.AppendPgNumeric(nil))
			require.Equal(t, wire, pgRefEncode(d), "the reference encoder must agree with PostgreSQL")
			encoded++
		})
	}
	require.GreaterOrEqual(t, encoded, 70, "too few vectors exercised the encoder")
}

// TestPgNumericLongTextRenderings pins where the two protocols part ways: a
// text rendering longer than ParseBytes' 200-byte limit fails with
// ErrMaxStrLen, while ParsePgNumeric still decodes the binary form or returns
// its range sentinel. The texts and bytes were captured from PostgreSQL 18.6
// (1.5::numeric(1000,500), 0.1::numeric(400,199), 0::numeric(1000,500) and
// 1e-300::numeric).
func TestPgNumericLongTextRenderings(t *testing.T) {
	tests := []struct {
		name string
		text string // PostgreSQL's rendering
		wire string // numeric_send
		want string // canonical decoded value; "" when err is set
		err  error
	}{
		{name: "numeric_1000_500", text: "1.5" + strings.Repeat("0", 499), wire: "00020000000001f400011388", want: "1.5"},
		{name: "numeric_400_199", text: "0.1" + strings.Repeat("0", 198), wire: "0001ffff000000c703e8", want: "0.1"},
		{name: "zero_numeric_1000_500", text: "0." + strings.Repeat("0", 500), wire: "00000000000001f4", want: "0"},
		{name: "1e-300", text: "0." + strings.Repeat("0", 299) + "1", wire: "0001ffb50000012c0001", err: ErrPrecOutOfRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Greater(t, len(tc.text), maxParseLen)
			_, textErr := ParseBytes([]byte(tc.text))
			require.ErrorIs(t, textErr, ErrMaxStrLen)

			wire := pgBytes(t, tc.wire)
			require.Equal(t, wire, pgRefEncodeText(tc.text, binary.BigEndian.Uint16(wire[6:])), "text and bytes must be one value")
			got, err := ParsePgNumeric(wire)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, Decimal{}, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, RequireFromString(tc.want), got)
			}
			requirePgDecode(t, wire)
		})
	}
}

// TestPgNumericRoundTrip requires ParsePgNumeric(d.AppendPgNumeric(nil)) to
// be d.Trim() for boundary-biased values (randCrossDecimal) and for
// coefficient widths uniform over 1..128 bits at every precision and both
// signs, and AppendPgNumeric to leave existing bytes in b untouched.
func TestPgNumericRoundTrip(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x7067, 0x726f756e64))
	prefix := []byte("row:")
	for i := range 100_000 {
		d := randCrossDecimal(rng)
		if i%2 == 1 {
			d = shapedDecimal(rng, 128, uint8(rng.Uint64N(uint64(MaxPrec)+1)))
		}
		enc := d.AppendPgNumeric(nil)
		require.LessOrEqualf(t, len(enc), PgNumericMaxLen, "d=%+v", d)
		got, err := ParsePgNumeric(enc)
		require.NoErrorf(t, err, "d=%+v enc=% x", d, enc)
		require.Equalf(t, d.Trim(), got, "d=%+v enc=% x", d, enc)

		appended := d.AppendPgNumeric(append(make([]byte, 0, 64), prefix...))
		require.Equalf(t, append(append([]byte{}, prefix...), enc...), appended, "d=%+v", d)
	}
}

// TestPgNumericReferenceOracle holds both functions to the independent
// reference codec on 150k values from four shapes: boundary-biased values,
// widths uniform over 1..128 bits, money-sized coefficients, and
// coefficients built from a short digit run times a power of ten, which
// produce zero groups inside, before and after the significant digits.
// AppendPgNumeric must emit pgRefEncode's exact bytes; pgRefDecode,
// ParsePgNumeric and NewFromString of the reference text form must all
// return d.Trim().
func TestPgNumericReferenceOracle(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x7067, 0x6f7261636c65))
	randPrec := func() uint8 { return uint8(rng.Uint64N(uint64(MaxPrec) + 1)) }
	gens := []func() Decimal{
		func() Decimal { return randCrossDecimal(rng) },
		func() Decimal { return shapedDecimal(rng, 128, randPrec()) },
		func() Decimal {
			return newDecimal(u128{lo: rng.Uint64N(1e12)}, rng.IntN(2) == 0, uint8(rng.IntN(10)))
		},
		func() Decimal {
			run := 1 + rng.Uint64N(9999)
			coef, _ := mul128by64(pow10u128[rng.IntN(35)], run)
			return newDecimal(coef, rng.IntN(2) == 0, randPrec())
		},
	}
	for i := range 150_000 {
		d := gens[i%len(gens)]()
		enc := d.AppendPgNumeric(nil)
		require.Equalf(t, pgRefEncode(d), enc, "d=%s prec=%d", d, d.Prec())

		ref, text, err := pgRefDecode(enc)
		require.NoErrorf(t, err, "d=%s enc=% x", d, enc)
		require.Equalf(t, d.Trim(), ref, "reference decode: d=%s enc=% x", d, enc)
		got, err := ParsePgNumeric(enc)
		require.NoErrorf(t, err, "d=%s enc=% x", d, enc)
		require.Equalf(t, ref, got, "d=%s enc=% x", d, enc)
		fromText, err := NewFromString(text)
		require.NoErrorf(t, err, "text=%q", text)
		require.Equalf(t, ref, fromText, "text=%q", text)
	}
}

// TestParsePgNumericAgainstReference throws 300k random encodings at the
// decoder: digit counts on both sides of the 11-digit limit, weights around
// the 128-bit and MaxPrec boundaries plus the int16 extremes, all sign words
// that matter, valid and invalid dscales (small ones that truncate, and
// numeric_send's own, which keeps every digit), zero digits padding either
// end, an occasional digit past 9999, and occasional length mismatches.
// Every case must match the reference decoder's value or sentinel exactly.
func TestParsePgNumericAgainstReference(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x7067, 0x72616e646f6d))
	sign := func() uint16 {
		switch rng.IntN(20) {
		case 0:
			return []uint16{0xC000, 0xD000, 0xF000}[rng.IntN(3)]
		case 1:
			return []uint16{0x8000, 0x4001, 0x1234, 0xE000}[rng.IntN(4)]
		default:
			return uint16(rng.IntN(2)) << 14 // positive or negative
		}
	}
	for range 300_000 {
		n := rng.IntN(14)
		if rng.IntN(16) == 0 {
			n = 12 + rng.IntN(20)
		}
		digits := make([]uint16, n)
		for i := range digits {
			switch rng.IntN(10) {
			case 0:
				digits[i] = 0
			case 1:
				digits[i] = 9999
			case 2:
				digits[i] = uint16(1+rng.IntN(9)) * uint16(pow10u64[rng.IntN(4)])
			default:
				digits[i] = uint16(rng.IntN(10000))
			}
		}
		if n > 0 && rng.IntN(20) == 0 {
			digits[rng.IntN(n)] = uint16(10000 + rng.IntN(55536))
		}
		if rng.IntN(5) == 0 {
			digits = append(make([]uint16, 1+rng.IntN(3)), digits...)
		}
		if rng.IntN(5) == 0 {
			digits = append(digits, make([]uint16, 1+rng.IntN(3))...)
		}
		// Mostly weights that keep the value near the representable window
		// (the first digit between 10^-24 and 10^36), sometimes far outside.
		weight := int16(rng.IntN(16) - 6)
		switch rng.IntN(32) {
		case 0:
			weight = 32767
		case 1:
			weight = -32768
		case 2, 3, 4, 5:
			weight = int16(rng.IntN(31) - 15)
		}
		dscale := uint16(rng.IntN(40))
		switch rng.IntN(32) {
		case 0:
			dscale = 0x3FFF
		case 1:
			dscale = 0x4000 + uint16(rng.IntN(0xC000))
		case 2, 3, 4, 5, 6, 7, 8, 9, 10, 11:
			// The places down to the last digit, as numeric_send sets it.
			dscale = uint16(min(max(-4*(int(weight)-len(digits)+1), 0), 0x3FFF))
		}
		src := pgDigitsWire(weight, sign(), dscale, digits...)
		switch rng.IntN(40) {
		case 0:
			src = src[:len(src)-1-rng.IntN(min(len(src), 3))]
		case 1:
			src = append(src, byte(rng.IntN(256)))
		case 2:
			binary.BigEndian.PutUint16(src, uint16(len(digits)+1))
		}
		requirePgDecode(t, src)
	}
}

// TestParsePgNumericMalformed pins every ErrInvalidBinaryData shape and the
// precedence of malformed input over NaN, ±Infinity and range errors: a
// malformed encoding is reported as such whatever it would have encoded.
func TestParsePgNumericMalformed(t *testing.T) {
	bigDigits := make([]uint16, 20)
	bigDigits[0] = 1
	tests := []struct {
		name string
		src  []byte
	}{
		{name: "nil", src: nil},
		{name: "len_1", src: []byte{0}},
		{name: "len_2", src: []byte{0, 0}},
		{name: "len_3", src: []byte{0, 0, 0}},
		{name: "len_4", src: []byte{0, 0, 0, 0}},
		{name: "len_5", src: []byte{0, 0, 0, 0, 0}},
		{name: "len_6", src: []byte{0, 0, 0, 0, 0, 0}},
		{name: "len_7", src: []byte{0, 0, 0, 0, 0, 0, 0}},
		{name: "ndigits_1_no_digits", src: pgWire(1, 0, 0, 0)},
		{name: "ndigits_2_one_digit", src: pgWire(2, 0, 0, 0, 1)},
		{name: "ndigits_0_one_digit", src: pgWire(0, 0, 0, 0, 1)},
		{name: "odd_trailing_byte", src: append(pgWire(1, 0, 0, 0, 1), 0)},
		{name: "ndigits_0xFFFF_no_digits", src: pgWire(0xFFFF, 0, 0, 0)},
		{name: "ndigits_0x8000_one_short", src: pgWire(0x8000, 0, 0, 0, make([]uint16, 0x7FFF)...)},
		{name: "digit_10000", src: pgDigitsWire(0, 0, 0, 10000)},
		{name: "digit_0xFFFF", src: pgDigitsWire(0, 0, 0, 0xFFFF)},
		{name: "digit_10000_first", src: pgDigitsWire(1, 0, 4, 10000, 1, 1)},
		{name: "digit_10000_middle", src: pgDigitsWire(1, 0, 4, 1, 10000, 1)},
		{name: "digit_10000_after_zeros", src: pgDigitsWire(1, 0, 4, 0, 0, 10000)},
		{name: "digit_10000_second_chunk", src: pgDigitsWire(6, 0, 0, 1, 2, 3, 4, 5, 10000, 7)},
		{name: "digit_10000_third_chunk", src: pgDigitsWire(9, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 10000, 9)},
		{name: "digit_10000_last", src: pgDigitsWire(9, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10000)},
		{name: "digit_10000_with_overflow", src: pgDigitsWire(19, 0, 0, append(append([]uint16{}, bigDigits[:19]...), 10000)...)},
		{name: "digit_10000_with_prec_error", src: pgDigitsWire(-6, 0, 0, 10000, 1)},
		{name: "digit_10000_negative_zero", src: pgDigitsWire(0, 0x4000, 0, 10000)},
		// numeric_recv validates every digit before it truncates to dscale.
		{name: "digit_10000_last_past_dscale", src: pgDigitsWire(0, 0, 0, 1, 10000)},
		{name: "digit_10000_past_dscale", src: pgDigitsWire(0, 0, 0, 1, 10000, 1)},
		{name: "digit_10000_all_past_dscale", src: pgDigitsWire(-1, 0, 0, 10000, 1)},
		{name: "digit_10000_last_kept", src: pgDigitsWire(1, 0, 0, 1, 10000, 1, 1)},
		{name: "digit_10000_kept_middle", src: pgDigitsWire(2, 0, 0, 1, 10000, 1, 1)},
		{name: "digit_10000_exposed_by_cut", src: pgDigitsWire(0, 0, 5, 10000, 0, 1)},
		{name: "sign_0x1234", src: pgDigitsWire(0, 0x1234, 0, 1)},
		{name: "sign_0x8000", src: pgDigitsWire(0, 0x8000, 0, 1)},
		{name: "sign_0x4001", src: pgDigitsWire(0, 0x4001, 0, 1)},
		{name: "sign_0xC001", src: pgWire(0, 0, 0xC001, 0)},
		{name: "sign_0xE000", src: pgWire(0, 0, 0xE000, 0)},
		{name: "sign_0xFFFF", src: pgWire(0, 0, 0xFFFF, 0)},
		{name: "dscale_0x4000", src: pgDigitsWire(0, 0, 0x4000, 1)},
		{name: "dscale_0xFFFF", src: pgDigitsWire(0, 0, 0xFFFF, 1)},
		{name: "zero_dscale_0x4000", src: pgWire(0, 0, 0, 0x4000)},
		{name: "nan_bad_digit", src: pgDigitsWire(0, 0xC000, 0, 10000)},
		{name: "nan_short", src: pgWire(1, 0, 0xC000, 0)},
		{name: "nan_dscale_0x4000", src: pgWire(0, 0, 0xC000, 0x4000)},
		{name: "inf_bad_digit", src: pgDigitsWire(0, 0xD000, 0, 0, 0xFFFF)},
		{name: "neg_inf_long", src: append(pgWire(0, 0, 0xF000, 0x20), 0, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePgNumeric(tc.src)
			require.ErrorIs(t, err, ErrInvalidBinaryData)
			require.Equal(t, Decimal{}, got)
			requirePgDecode(t, tc.src)
		})
	}
}

// TestParsePgNumericLargeDigitCounts pins ndigits as unsigned, as
// numeric_recv reads it. numeric_send writes counts of 0x8000..0x9000 for
// values of 131,072 digits or more; no Decimal holds them, so they return
// ErrOverflow like any other value past 128 bits. The first two encodings are
// numeric_send's bytes for repeat('1', 131072) and for that plus a
// 16383-digit fraction of ones, rebuilt by the reference encoder and held to
// the header, length and tail captured from PostgreSQL 18.6. The zero-padded
// encodings are ones PostgreSQL 18.6 accepted through binary COPY, storing 0,
// 7 and 0.
func TestParsePgNumericLargeDigitCounts(t *testing.T) {
	ones := strings.Repeat("1", 131072)
	seven := make([]uint16, 0x8000)
	seven[len(seven)-1] = 7
	tests := []struct {
		name       string
		src        []byte
		head, tail string
		length     int
		want       string // canonical decoded value; "" when err is set
		err        error
	}{
		{name: "ndigits_0x8000", src: pgRefEncodeText(ones, 0), head: "80007fff0000000004570457", tail: "04570457", length: 65544, err: ErrOverflow},
		{name: "ndigits_0x9000", src: pgRefEncodeText(ones+"."+strings.Repeat("1", 16383), 0x3FFF), head: "90007fff00003fff04570457", tail: "04570456", length: 73736, err: ErrOverflow},
		{name: "ndigits_0x8000_zeros", src: pgWire(0x8000, 0, 0, 0, make([]uint16, 0x8000)...), head: "800000000000000000000000", tail: "00000000", length: 65544, want: "0"},
		{name: "ndigits_0x8000_leading_zeros", src: pgWire(0x8000, 0x7FFF, 0, 0, seven...), head: "80007fff0000000000000000", tail: "00000007", length: 65544, want: "7"},
		{name: "ndigits_0xFFFF_zeros", src: pgWire(0xFFFF, 0xFFFF, 0, 0, make([]uint16, 0xFFFF)...), head: "ffffffff0000000000000000", tail: "00000000", length: 131078, want: "0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Len(t, tc.src, tc.length)
			require.Equal(t, tc.head, hex.EncodeToString(tc.src[:12]))
			require.Equal(t, tc.tail, hex.EncodeToString(tc.src[len(tc.src)-4:]))
			got, err := ParsePgNumeric(tc.src)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, Decimal{}, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, RequireFromString(tc.want), got)
			}
			requirePgDecode(t, tc.src)
		})
	}
}

// TestParsePgNumericDscaleTruncation pins numeric_recv's truncation to
// dscale: a nonzero digit past dscale places, which numeric_send never sends
// but another encoder may, is cut toward zero before PostgreSQL stores the
// value, and ParsePgNumeric must decode the bytes to the stored value. Each
// input went to PostgreSQL 18.6 through binary COPY; text and send are what
// it stored, captured with
//
//	SELECT v::text, encode(numeric_send(v), 'hex')
//
// Decoding the input must equal decoding send and, where the rendering fits
// ParseBytes' limit, parsing text, sentinel included. Truncation comes before
// the range checks, so a value past 128 bits or MaxPrec places can decode
// once cut, while a kept digit past either limit still fails.
func TestParsePgNumericDscaleTruncation(t *testing.T) {
	nines := []uint16{1}
	for range 11 {
		nines = append(nines, 9999)
	}
	zeros10 := make([]uint16, 10)
	tests := []struct {
		name string
		src  []byte
		text string // PostgreSQL's rendering of the stored value
		send string // numeric_send of the stored value
		want string // canonical decoded value; "" when err is set
		err  error
	}{
		{name: "fraction_past_dscale_0", src: pgDigitsWire(0, 0, 0, 1, 5000), text: "1", send: "00010000000000000001", want: "1"},
		{name: "cut_inside_digit", src: pgDigitsWire(0, 0, 2, 1, 2345), text: "1.23", send: "0002000000000002000108fc", want: "1.23"},
		{name: "negative_cut_toward_zero", src: pgDigitsWire(0, 0x4000, 2, 1, 2345), text: "-1.23", send: "0002000040000002000108fc", want: "-1.23"},
		{name: "cut_never_rounds", src: pgDigitsWire(0, 0, 1, 1, 9999), text: "1.9", send: "000200000000000100012328", want: "1.9"},
		{name: "cut_leaves_zeros", src: pgDigitsWire(0, 0, 3, 1, 1), text: "1.000", send: "00010000000000030001", want: "1"},
		{name: "whole_digit_dropped", src: pgDigitsWire(0, 0, 6, 1, 2345, 6789), text: "1.234567", send: "0003000000000006000109291a2c", want: "1.234567"},
		{name: "digit_boundary", src: pgDigitsWire(0, 0, 4, 1, 2345, 6789), text: "1.2345", send: "000200000000000400010929", want: "1.2345"},
		{name: "integer_kept", src: pgDigitsWire(1, 0, 0, 12, 3456, 7890), text: "123456", send: "0002000100000000000c0d80", want: "123456"},
		{name: "negative_integer_kept", src: pgDigitsWire(2, 0x4000, 0, 1, 2345, 6789, 1234), text: "-123456789", send: "0003000240000000000109291a85", want: "-123456789"},
		{name: "cut_inside_second_digit", src: pgDigitsWire(0, 0, 3, 9999, 9999), text: "9999.999", send: "0002000000000003270f2706", want: "9999.999"},
		{name: "leading_zero_digit", src: pgDigitsWire(0, 0, 6, 0, 1, 2345), text: "0.000123", send: "0002ffff00000006000108fc", want: "0.000123"},
		{name: "cleared_digit_exposes_zeros", src: pgDigitsWire(0, 0, 9, 1, 0, 0, 5), text: "1.000000000", send: "00010000000000090001", want: "1"},
		{name: "small_fraction_kept", src: pgDigitsWire(-4, 0, 16, 1, 2), text: "0.0000000000000001", send: "0001fffc000000100001", want: "0.0000000000000001"},
		{name: "all_past_dscale", src: pgDigitsWire(-1, 0, 2, 99), text: "0.00", send: "0000000000000002", want: "0"},
		{name: "negative_all_past_dscale", src: pgDigitsWire(-1, 0x4000, 2, 99), text: "0.00", send: "0000000000000002", want: "0"},
		{name: "zero_digits_then_past_dscale", src: pgDigitsWire(-1, 0x4000, 5, 0, 0, 1234), text: "0.00000", send: "0000000000000005", want: "0"},
		{name: "cleared_only_digit", src: pgDigitsWire(0, 0x4000, 3, 0, 1, 2345), text: "0.000", send: "0000000000000003", want: "0"},
		{name: "past_128_bits_until_cut", src: pgDigitsWire(0, 0, 4, nines...), text: "1.9999", send: "00020000000000040001270f", want: "1.9999"},
		{name: "past_128_bits_kept", src: pgDigitsWire(0, 0, 44, append(append([]uint16{1}, zeros10...), 1)...), text: "1." + strings.Repeat("0", 43) + "1", send: "000c00000000002c0001" + strings.Repeat("0000", 10) + "0001", err: ErrOverflow},
		{name: "past_128_bits_cut_to_one", src: pgDigitsWire(0, 0, 43, append(append([]uint16{1}, zeros10...), 1)...), text: "1." + strings.Repeat("0", 43), send: "000100000000002b0001", want: "1"},
		{name: "integer_fits_once_cut", src: pgDigitsWire(9, 0, 0, 35, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1000), text: "35" + strings.Repeat("0", 36), send: "00010009000000000023", want: "35" + strings.Repeat("0", 36)},
		{name: "past_max_prec_until_cut", src: pgDigitsWire(-5, 0, 19, 1), text: "0." + strings.Repeat("0", 19), send: "0000000000000013", want: "0"},
		{name: "past_max_prec_kept", src: pgDigitsWire(-5, 0, 20, 1, 1), text: "0." + strings.Repeat("0", 19) + "1", send: "0001fffb000000140001", err: ErrPrecOutOfRange},
		{name: "weight_min_dscale_max", src: pgDigitsWire(-32768, 0, 0x3FFF, 1), text: "0." + strings.Repeat("0", 16383), send: "0000000000003fff", want: "0"},
		{name: "deepest_place_past_dscale_max", src: pgDigitsWire(-4096, 0, 0x3FFF, 1), text: "0." + strings.Repeat("0", 16383), send: "0000000000003fff", want: "0"},
		{name: "deepest_place_dscale_keeps", src: pgDigitsWire(-4096, 0, 0x3FFF, 10), text: "0." + strings.Repeat("0", 16382) + "1", send: "0001f00000003fff000a", err: ErrPrecOutOfRange},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePgNumeric(tc.src)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, Decimal{}, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, RequireFromString(tc.want), got)
			}
			stored, storedErr := ParsePgNumeric(pgBytes(t, tc.send))
			require.Equal(t, got, stored, "the input and the stored value must decode alike")
			require.Equal(t, err, storedErr, "the input and the stored value must fail alike")
			if len(tc.text) <= maxParseLen {
				fromText, textErr := NewFromString(tc.text)
				require.Equal(t, got, fromText, "binary and text protocols must agree")
				if tc.err != nil {
					require.ErrorIs(t, textErr, tc.err)
				} else {
					require.NoError(t, textErr)
				}
			}
			requirePgDecode(t, tc.src)
		})
	}
}

// TestPgNumericSpecialValueSentinels pins the sentinel for every special
// value in both PostgreSQL protocols, so a driver codec can map them
// consistently. Binary NaN, +Infinity and -Infinity return ErrInvalidFloat
// whenever the rest of the encoding is well formed: the minimal dscale-0
// spelling, PostgreSQL's actual dscale 0x20 for the infinities, and digits,
// weights and dscales numeric_recv would ignore (a malformed special value is
// ErrInvalidBinaryData; see TestParsePgNumericMalformed). PostgreSQL's text
// renderings of the same values, "NaN", "Infinity" and "-Infinity", fail
// ParseBytes with ErrInvalidFormat.
func TestPgNumericSpecialValueSentinels(t *testing.T) {
	binaryTests := []struct {
		name string
		src  []byte
	}{
		{name: "nan", src: pgWire(0, 0, 0xC000, 0)},
		{name: "pos_inf", src: pgWire(0, 0, 0xD000, 0)},
		{name: "neg_inf", src: pgWire(0, 0, 0xF000, 0)},
		{name: "pos_inf_postgres_dscale", src: pgWire(0, 0, 0xD000, 0x20)},
		{name: "neg_inf_postgres_dscale", src: pgWire(0, 0, 0xF000, 0x20)},
		{name: "nan_with_digits", src: pgDigitsWire(3, 0xC000, 7, 1, 9999, 0)},
		{name: "nan_huge_digits", src: pgDigitsWire(32767, 0xC000, 0x3FFF, make([]uint16, 40)...)},
		{name: "inf_weight_min", src: pgWire(0, 0x8000, 0xD000, 0)},
	}
	for _, tc := range binaryTests {
		t.Run("binary_"+tc.name, func(t *testing.T) {
			got, err := ParsePgNumeric(tc.src)
			require.ErrorIs(t, err, ErrInvalidFloat)
			require.Equal(t, Decimal{}, got)
			requirePgDecode(t, tc.src)
		})
	}
	for _, text := range []string{"NaN", "Infinity", "-Infinity"} {
		t.Run("text_"+text, func(t *testing.T) {
			got, err := ParseBytes([]byte(text))
			require.ErrorIs(t, err, ErrInvalidFormat)
			require.Equal(t, Decimal{}, got)
		})
	}
}

// TestParsePgNumericRange pins the range edges: the 128-bit coefficient
// limit, the MaxPrec limit (with zero digits past it ignored), the error
// precedence shared with the text parser, encodings that keep zero digits
// at either end (pgx's pgtype.Numeric writes such encodings), the int16
// weight extremes, and dscale's own limit, past which every digit truncates
// away.
func TestParsePgNumericRange(t *testing.T) {
	forty := pgRefEncodeText("1234567890123456789012345678901234567890", 0)
	tests := []struct {
		name string
		src  []byte
		want string // canonical decoded value; "" when err is set
		err  error
	}{
		{name: "forty_digit_integer", src: forty, err: ErrOverflow},
		{name: "weight_10_digit_1", src: pgDigitsWire(10, 0, 0, 1), err: ErrOverflow},
		{name: "weight_9_digit_340", src: pgDigitsWire(9, 0, 0, 340), want: "340" + strings.Repeat("0", 36)},
		{name: "weight_9_digit_341", src: pgDigitsWire(9, 0, 0, 341), err: ErrOverflow},
		{name: "two_pow_128", src: pgRefEncodeText("340282366920938463463374607431768211456", 0), err: ErrOverflow},
		{name: "two_pow_128_minus_1", src: pgRefEncodeText("340282366920938463463374607431768211455", 0), want: "340282366920938463463374607431768211455"},
		{name: "twelve_digits", src: pgDigitsWire(0, 0, 44, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1), err: ErrOverflow},
		{name: "eleven_digits_fit", src: pgDigitsWire(9, 0, 1, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1000), want: "10" + strings.Repeat("0", 36) + ".1"},
		{name: "eleven_digits_overflow", src: pgDigitsWire(9, 0, 1, 35, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1000), err: ErrOverflow},
		{name: "1e-19", src: pgDigitsWire(-5, 0, 19, 10), want: "0.0000000000000000001"},
		{name: "1e-17", src: pgDigitsWire(-5, 0, 17, 1000), want: "0.00000000000000001"},
		{name: "1e-20", src: pgDigitsWire(-5, 0, 20, 1), err: ErrPrecOutOfRange},
		{name: "15e-20", src: pgDigitsWire(-5, 0, 20, 15), err: ErrPrecOutOfRange},
		{name: "1_point_24_zeros", src: pgDigitsWire(0, 0, 24, 1, 0, 0, 0, 0, 0, 0), want: "1"},
		{name: "1_prec_20_trailing_zero", src: pgDigitsWire(-5, 0, 20, 10), want: "0.0000000000000000001"},
		// The text parser reports the significant digits' overflow first.
		{name: "overflow_beats_prec", src: pgRefEncodeText("1234567890123456789012345678901234567890.00000000000000000001", 20), err: ErrOverflow},
		{name: "prec_error_after_one", src: pgRefEncodeText("1.00000000000000000001", 20), err: ErrPrecOutOfRange},
		{name: "leading_zero_digits", src: pgDigitsWire(2, 0, 4, 0, 0, 1, 2345), want: "1.2345"},
		{name: "trailing_zero_digits", src: pgDigitsWire(0, 0, 4, 1, 2345, 0, 0), want: "1.2345"},
		{name: "pgx_style_integer", src: pgDigitsWire(2, 0, 0, 1, 0, 0), want: "100000000"},
		{name: "pgx_style_fraction", src: pgDigitsWire(-1, 0, 8, 0, 1), want: "0.00000001"},
		{name: "all_zero_digits", src: pgDigitsWire(5, 0x4000, 3, 0, 0, 0), want: "0"},
		{name: "negative_zero", src: pgWire(0, 0, 0x4000, 2), want: "0"},
		{name: "weight_max_digit_1", src: pgDigitsWire(32767, 0, 0, 1), err: ErrOverflow},
		// No dscale reaches 10000^-4096 = 10^-16384, so every digit that far
		// down truncates away; at 10^-16383, the deepest place dscale keeps,
		// a digit is kept and is a precision error.
		{name: "weight_min_digit_1", src: pgDigitsWire(-32768, 0, 0x3FFF, 1), want: "0"},
		{name: "weight_minus_32767_digit_1", src: pgDigitsWire(-32767, 0x4000, 0x3FFF, 1), want: "0"},
		{name: "dscale_max_past_deepest_place", src: pgDigitsWire(-4096, 0, 0x3FFF, 1), want: "0"},
		{name: "dscale_max_deepest_place", src: pgDigitsWire(-4096, 0, 0x3FFF, 10), err: ErrPrecOutOfRange},
		{name: "weight_32766_digit_9999", src: pgDigitsWire(32766, 0x4000, 0, 9999), err: ErrOverflow},
		{name: "weight_min_leading_zeros", src: pgDigitsWire(-32768, 0, 0x3FFF, 0, 0, 1), want: "0"},
		{name: "weight_max_trailing_zeros", src: pgDigitsWire(32767, 0, 0, 1, 0, 0), err: ErrOverflow},
		{name: "weight_max_zero", src: pgWire(0, 0x7FFF, 0, 0), want: "0"},
		{name: "weight_min_zero", src: pgWire(0, 0x8000, 0x4000, 0), want: "0"},
		{name: "dscale_max", src: pgDigitsWire(0, 0, 0x3FFF, 7), want: "7"},
		{name: "integer_trailing_decimal_zeros", src: pgDigitsWire(0, 0x4000, 0, 1000), want: "-1000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePgNumeric(tc.src)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, Decimal{}, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, RequireFromString(tc.want), got)
			}
			requirePgDecode(t, tc.src)
		})
	}
}

// TestParsePgNumericCoefficientLimit decodes the largest coefficient and
// the next integer at every precision and in both signs, alongside the 10^k
// and 10^k - 1 neighbours a carry crosses: the 128-bit boundary must fall
// exactly where the text parser puts it.
func TestParsePgNumericCoefficientLimit(t *testing.T) {
	coefs := []string{
		"340282366920938463463374607431768211455",
		"340282366920938463463374607431768211456",
		"100000000000000000000000000000000000000",
		"99999999999999999999999999999999999999",
		"18446744073709551615",
		"18446744073709551616",
	}
	for _, c := range coefs {
		for p := 0; p <= int(MaxPrec); p++ {
			text := c
			if p > 0 {
				text = c[:len(c)-p] + "." + c[len(c)-p:]
			}
			for _, s := range []string{text, "-" + text} {
				src := pgRefEncodeText(s, uint16(p))
				want, wantErr := NewFromString(s)
				got, err := ParsePgNumeric(src)
				if wantErr != nil {
					require.ErrorIsf(t, err, wantErr, "%s", s)
					continue
				}
				require.NoErrorf(t, err, "%s", s)
				require.Equalf(t, want, got, "%s", s)
				requirePgDecode(t, src)
			}
		}
	}
}

// TestParsePgNumericZeroPadding pads canonical encodings with zero digits
// at either end — the weight rising by one per leading zero — and requires
// the same value: numeric_recv accepts such encodings, and pgx writes them.
func TestParsePgNumericZeroPadding(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(0x7067, 0x706164))
	for range 20_000 {
		d := randCrossDecimal(rng)
		enc := d.AppendPgNumeric(nil)
		ndigits := int(binary.BigEndian.Uint16(enc))
		weight := int16(binary.BigEndian.Uint16(enc[2:]))
		digits := make([]uint16, ndigits)
		for i := range digits {
			digits[i] = binary.BigEndian.Uint16(enc[pgHeaderLen+2*i:])
		}
		lead, trail := rng.IntN(4), rng.IntN(4)
		padded := append(append(make([]uint16, lead), digits...), make([]uint16, trail)...)
		if ndigits == 0 {
			weight = int16(rng.IntN(9) - 4) // any weight: zero digits are zero
		}
		src := pgDigitsWire(weight+int16(lead), binary.BigEndian.Uint16(enc[4:]), binary.BigEndian.Uint16(enc[6:]), padded...)
		got, err := ParsePgNumeric(src)
		require.NoErrorf(t, err, "src=% x", src)
		require.Equalf(t, d.Trim(), got, "src=% x", src)
	}
}

// TestPgNumericMaxLen proves PgNumericMaxLen. Before zero digits are
// stripped, a value with L coefficient digits at precision P has
// ⌈I/4⌉ + ⌈P/4⌉ base-10000 digits, I = max(L-P, 0) integer digits, so the
// count depends on (L, P) alone, and stripping only lowers it. The
// coefficient 10^(L-1) + 1 strips nothing whenever I > 0 (its first and last
// decimal digits are nonzero), so encoding it for every L in 1..39 and P in
// 0..MaxPrec reaches the maximum over all Decimals; with I + P = L ≤ 39 that
// maximum is ⌊(39+6)/4⌋ = 11 digits, 8 + 2·11 = 30 bytes, and it is
// attained. The maximum coefficient at every precision is checked as well.
func TestPgNumericMaxLen(t *testing.T) {
	longest := 0
	for p := uint8(0); p <= MaxPrec; p++ {
		for l := 1; l <= 39; l++ {
			coef := inc128(pow10u128[l-1])
			for _, neg := range []bool{false, true} {
				d := newDecimal(coef, neg, p)
				enc := d.AppendPgNumeric(nil)
				i := max(l-int(p), 0)
				digits := (i+3)/4 + (int(p)+3)/4
				if i == 0 {
					digits = int(binary.BigEndian.Uint16(enc)) // leading zero digits stripped
				}
				require.Equalf(t, pgHeaderLen+2*digits, len(enc), "L=%d P=%d", l, p)
				require.LessOrEqualf(t, len(enc), PgNumericMaxLen, "L=%d P=%d", l, p)
				longest = max(longest, len(enc))
			}
		}
		maxCoef := newDecimal(u128{hi: maxUint64, lo: maxUint64}, true, p)
		require.LessOrEqual(t, len(maxCoef.AppendPgNumeric(nil)), PgNumericMaxLen)
	}
	require.Equal(t, PgNumericMaxLen, longest, "the bound must be tight")
}

// TestAppendPgNumericCapacity checks that AppendPgNumeric writes in place
// when b has room, so a reused PgNumericMaxLen buffer never reallocates.
func TestAppendPgNumericCapacity(t *testing.T) {
	buf := make([]byte, 0, PgNumericMaxLen)
	d := RequireFromString("-34028236692093846346.3374607431768211455")
	out := d.AppendPgNumeric(buf)
	require.Same(t, &buf[:1][0], &out[0], "must reuse b's backing array")
	require.Equal(t, pgRefEncode(d), out)
}
