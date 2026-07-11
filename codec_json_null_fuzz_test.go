//go:build fuzz

package zerodecimal

import (
	"encoding/json"
	"errors"
	"testing"
)

// FuzzJSONNullPolicy proves that encoding/json's permitted surrounding
// whitespace cannot turn null into a silent Decimal no-op, while NullDecimal
// continues to clear to its invalid state. Direct calls remain strict and
// recognize only the exact four-byte token.
func FuzzJSONNullPolicy(f *testing.F) {
	for _, seed := range []struct {
		prefix []byte
		suffix []byte
	}{
		{},
		{prefix: []byte(" ")},
		{suffix: []byte("\n")},
		{prefix: []byte("\t\r"), suffix: []byte(" \n")},
	} {
		f.Add(seed.prefix, seed.suffix)
	}

	f.Fuzz(func(t *testing.T, prefix, suffix []byte) {
		if len(prefix)+len(suffix) > 64 || !jsonWhitespace(prefix) || !jsonWhitespace(suffix) {
			return
		}

		wire := make([]byte, 0, len(prefix)+len(jsonNull)+len(suffix))
		wire = append(wire, prefix...)
		wire = append(wire, jsonNull...)
		wire = append(wire, suffix...)
		marker := MustNew(424242, -2)

		d := marker
		err := json.Unmarshal(wire, &d)
		if !errors.Is(err, ErrJSONNull) || d != marker {
			t.Fatalf("Decimal null must fail closed: wire=%q err=%v got=%+v", wire, err, d)
		}

		n := NewNullDecimal(marker)
		if err := json.Unmarshal(wire, &n); err != nil || n != (NullDecimal{}) {
			t.Fatalf("NullDecimal null must clear: wire=%q err=%v got=%+v", wire, err, n)
		}

		d = marker
		directErr := d.UnmarshalJSON(wire)
		if len(prefix) == 0 && len(suffix) == 0 {
			if !errors.Is(directErr, ErrJSONNull) {
				t.Fatalf("exact direct null error: got=%v", directErr)
			}
		} else if !errors.Is(directErr, ErrInvalidFormat) {
			t.Fatalf("direct whitespace must remain strict: wire=%q got=%v", wire, directErr)
		}
		if d != marker {
			t.Fatalf("direct null attempt mutated Decimal: wire=%q got=%+v", wire, d)
		}
	})
}

func jsonWhitespace(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\r', '\n':
		default:
			return false
		}
	}
	return true
}
