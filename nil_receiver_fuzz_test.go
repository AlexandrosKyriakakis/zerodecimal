//go:build fuzz

package zerodecimal

import (
	"errors"
	"testing"
)

// FuzzNilReceiverEntryPoints feeds arbitrary bytes and representative Scan
// source categories to every fallible pointer-receiver boundary. Every call
// must be total, input-independent, and return ErrNilReceiver.
func FuzzNilReceiverEntryPoints(f *testing.F) {
	for _, seed := range []struct {
		raw  []byte
		kind uint8
	}{
		{raw: nil},
		{raw: []byte("null"), kind: 1},
		{raw: []byte(`"1\u002e5"`), kind: 2},
		{raw: []byte{0xFF, 0x00, 0x80}, kind: 3},
		{raw: []byte("1.5"), kind: 4},
	} {
		f.Add(seed.raw, seed.kind)
	}

	f.Fuzz(func(t *testing.T, raw []byte, kind uint8) {
		var src any
		switch kind % 5 {
		case 0:
			src = nil
		case 1:
			src = raw
		case 2:
			src = true
		case 3:
			src = float64(1.5)
		case 4:
			if len(raw) > maxParseLen {
				src = "over-limit"
			} else {
				src = string(raw)
			}
		}

		var decimal *Decimal
		var strict *StrictSQLDecimal
		var nullable *NullDecimal
		calls := []func() error{
			func() error { return decimal.UnmarshalText(raw) },
			func() error { return decimal.UnmarshalJSON(raw) },
			func() error { return decimal.UnmarshalBinary(raw) },
			func() error { return decimal.Scan(src) },
			func() error { return strict.Scan(src) },
			func() error { return strict.UnmarshalText(raw) },
			func() error { return strict.UnmarshalJSON(raw) },
			func() error { return strict.UnmarshalBinary(raw) },
			func() error { return nullable.Scan(src) },
			func() error { return nullable.UnmarshalText(raw) },
			func() error { return nullable.UnmarshalJSON(raw) },
		}
		for i, call := range calls {
			if err := call(); !errors.Is(err, ErrNilReceiver) {
				t.Fatalf("call %d: got %v, want ErrNilReceiver; raw=%q kind=%d", i, err, raw, kind)
			}
		}
	})
}

// FuzzStrictSQLDecimalCodecForwarders locks the explicit wrapper methods to
// Decimal's codec errors and receiver-state transitions for arbitrary input.
func FuzzStrictSQLDecimalCodecForwarders(f *testing.F) {
	validBinary, err := RequireFromString("1234.5678").MarshalBinary()
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{
		nil,
		[]byte("null"),
		[]byte(`"1\u002e5"`),
		[]byte("1.5"),
		{0xff, 0x00, 0x80},
		validBinary,
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		tests := []struct {
			name        string
			strictCall  func(*StrictSQLDecimal) error
			decimalCall func(*Decimal) error
		}{
			{name: "text", strictCall: func(d *StrictSQLDecimal) error { return d.UnmarshalText(raw) }, decimalCall: func(d *Decimal) error { return d.UnmarshalText(raw) }},
			{name: "json", strictCall: func(d *StrictSQLDecimal) error { return d.UnmarshalJSON(raw) }, decimalCall: func(d *Decimal) error { return d.UnmarshalJSON(raw) }},
			{name: "binary", strictCall: func(d *StrictSQLDecimal) error { return d.UnmarshalBinary(raw) }, decimalCall: func(d *Decimal) error { return d.UnmarshalBinary(raw) }},
		}
		marker := NewStrictSQLDecimal(MustNew(31337, -3))
		for _, tc := range tests {
			got := marker
			want := marker.Decimal
			wantErr := tc.decimalCall(&want)
			gotErr := tc.strictCall(&got)
			if !errors.Is(gotErr, wantErr) || !errors.Is(wantErr, gotErr) {
				t.Fatalf("%s: got error %v, want exact delegated error %v; raw=%q", tc.name, gotErr, wantErr, raw)
			}
			if got.Decimal != want {
				t.Fatalf("%s: got Decimal %v, want delegated state %v; raw=%q", tc.name, got.Decimal, want, raw)
			}
			if gotErr != nil && got != marker {
				t.Fatalf("%s: error changed receiver from %v to %v; raw=%q", tc.name, marker, got, raw)
			}
		}
	})
}
