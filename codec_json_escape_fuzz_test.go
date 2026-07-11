//go:build fuzz

package zerodecimal

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzJSONQuotedSemanticEquivalence constructs valid JSON strings with a
// mixture of literal bytes and lower-/upper-case Unicode escapes. The decoder
// must produce exactly the result (or bare parse sentinel) obtained by feeding
// the semantic string directly to parseCore.
func FuzzJSONQuotedSemanticEquivalence(f *testing.F) {
	for _, seed := range []struct {
		literal string
		modes   []byte
	}{
		{literal: "1.5", modes: []byte{0, 1, 0}},
		{literal: "-1.25E+2", modes: []byte{2, 1, 0, 2, 1, 0, 2, 1}},
		{literal: "340282366920938463463374607431768211455", modes: []byte{1, 2}},
		{literal: "0.00000000000000000001", modes: []byte{0, 1, 2}},
		{literal: "", modes: nil},
		{literal: "not-a-decimal", modes: []byte{1}},
	} {
		f.Add([]byte(seed.literal), seed.modes)
	}

	f.Fuzz(func(t *testing.T, literal, modes []byte) {
		if len(literal) > maxParseLen || !utf8.Valid(literal) {
			return
		}
		for _, c := range literal {
			if c >= utf8.RuneSelf {
				return
			}
		}

		encoded := fuzzQuoteASCII(literal, modes)
		want, wantErr := parseCore(literal, false)
		marker := MustNew(424242, -2)

		got := marker
		gotErr := got.UnmarshalJSON(encoded)
		if !fuzzJSONSameError(gotErr, wantErr) {
			t.Fatalf("error mismatch: encoded=%q literal=%q got=%v want=%v", encoded, literal, gotErr, wantErr)
		}
		if gotErr == nil && got != want {
			t.Fatalf("value mismatch: encoded=%q literal=%q got=%+v want=%+v", encoded, literal, got, want)
		}
		if gotErr != nil && got != marker {
			t.Fatalf("failed Decimal decode mutated receiver: encoded=%q got=%+v", encoded, got)
		}

		n := NewNullDecimal(marker)
		nErr := n.UnmarshalJSON(encoded)
		if !fuzzJSONSameError(nErr, wantErr) {
			t.Fatalf("NullDecimal error mismatch: encoded=%q got=%v want=%v", encoded, nErr, wantErr)
		}
		if nErr == nil && n != NewNullDecimal(want) {
			t.Fatalf("NullDecimal value mismatch: encoded=%q got=%+v want=%+v", encoded, n, want)
		}
		if nErr != nil && n != NewNullDecimal(marker) {
			t.Fatalf("failed NullDecimal decode mutated receiver: encoded=%q got=%+v", encoded, n)
		}
	})
}

// FuzzJSONQuotedGarbage drives arbitrary byte slices through Decimal and
// NullDecimal to prove panic freedom and receiver atomicity. encoding/json
// supplies the independent syntax oracle for bare JSON numbers and the
// semantic decode oracle for quoted strings.
func FuzzJSONQuotedGarbage(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`0`),
		[]byte(`-0.25e+2`),
		[]byte(`+1`),
		[]byte(`01`),
		[]byte(`-00`),
		[]byte(`00.1`),
		[]byte(` 1`),
		[]byte(`1 `),
		[]byte(`"1\u002e5"`),
		[]byte(`"+1"`),
		[]byte(`"01"`),
		[]byte(`"-00"`),
		[]byte(`"\u002d1\u0045\u002b2"`),
		[]byte(`"1\uD800"`),
		[]byte(`"1\uD83D\uDE00"`),
		[]byte(`"` + strings.Repeat("0", maxParseLen-utf8.UTFMax) + `\uD83D\uDE00"`),
		[]byte(`"1\x32"`),
		[]byte(`"1\`),
		[]byte("\"1\n2\""),
		[]byte("null"),
		nil,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		marker := MustNew(424242, -2)
		got := marker
		gotErr := got.UnmarshalJSON(raw)

		n := NewNullDecimal(marker)
		nErr := n.UnmarshalJSON(raw)
		if string(raw) == jsonNull {
			if !errors.Is(gotErr, ErrJSONNull) || got != marker {
				t.Fatalf("Decimal null semantics changed: err=%v got=%+v", gotErr, got)
			}
			if nErr != nil || n != (NullDecimal{}) {
				t.Fatalf("NullDecimal null semantics changed: err=%v got=%+v", nErr, n)
			}
			return
		}
		if !fuzzJSONSameError(gotErr, nErr) {
			t.Fatalf("Decimal/NullDecimal error mismatch: raw=%q decimal=%v null=%v", raw, gotErr, nErr)
		}
		if gotErr != nil {
			if got != marker || n != NewNullDecimal(marker) {
				t.Fatalf("failed decode mutated receiver: raw=%q decimal=%+v null=%+v", raw, got, n)
			}
		} else if !n.Valid || n.Decimal != got {
			t.Fatalf("successful decoders disagree: raw=%q decimal=%+v null=%+v", raw, got, n)
		}

		// Direct UnmarshalJSON calls deliberately preserve the historical
		// contract of accepting exactly one token, without encoding/json's
		// surrounding-whitespace preprocessing.
		if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
			if len(raw) > maxParseLen {
				if gotErr == nil {
					t.Fatalf("accepted over-limit bare input: raw_len=%d", len(raw))
				}
				return
			}
			if !fuzzValidBareJSONNumber(raw) {
				if gotErr == nil {
					t.Fatalf("accepted non-JSON bare number: raw=%q got=%+v", raw, got)
				}
				return
			}
			want, wantErr := parseCore(raw, false)
			if !fuzzJSONSameError(gotErr, wantErr) {
				t.Fatalf("bare error mismatch: raw=%q got=%v want=%v", raw, gotErr, wantErr)
			}
			if gotErr == nil && got != want {
				t.Fatalf("bare value mismatch: raw=%q got=%+v want=%+v", raw, got, want)
			}
			return
		}
		if len(raw) > maxQuotedJSONLen {
			return
		}
		var semantic string
		if err := json.Unmarshal(raw, &semantic); err != nil {
			return
		}
		if len(semantic) > maxParseLen {
			if gotErr == nil {
				t.Fatalf("accepted over-limit semantic string: raw=%q decoded_len=%d", raw, len(semantic))
			}
			return
		}
		want, wantErr := parseCore(semantic, false)
		if wantErr == nil {
			if gotErr != nil || got != want {
				t.Fatalf("semantic mismatch: raw=%q semantic=%q got=%+v err=%v want=%+v", raw, semantic, got, gotErr, want)
			}
		} else if gotErr == nil {
			t.Fatalf("accepted invalid semantic decimal: raw=%q semantic=%q got=%+v", raw, semantic, got)
		}
	})
}

func fuzzValidBareJSONNumber(raw []byte) bool {
	if len(raw) == 0 || fuzzJSONSpace(raw[0]) || fuzzJSONSpace(raw[len(raw)-1]) {
		return false
	}
	first := raw[0]
	if first != '-' && (first < '0' || first > '9') {
		return false
	}
	return json.Valid(raw)
}

func fuzzJSONSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func fuzzQuoteASCII(literal, modes []byte) []byte {
	encoded := make([]byte, 0, 2+6*len(literal))
	encoded = append(encoded, '"')
	const lowerHex = "0123456789abcdef"
	const upperHex = "0123456789ABCDEF"
	for i, c := range literal {
		mode := byte(0)
		if len(modes) != 0 {
			mode = modes[i%len(modes)] % 3
		}
		if mode != 0 || c < 0x20 {
			hex := lowerHex
			if mode == 2 {
				hex = upperHex
			}
			encoded = append(encoded, '\\', 'u', '0', '0', hex[c>>4], hex[c&0x0F])
			continue
		}
		switch c {
		case '"', '\\':
			encoded = append(encoded, '\\', c)
		default:
			encoded = append(encoded, c)
		}
	}
	return append(encoded, '"')
}

func fuzzJSONSameError(got, want error) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return errors.Is(got, want) && errors.Is(want, got)
}
