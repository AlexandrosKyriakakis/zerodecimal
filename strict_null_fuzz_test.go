//go:build fuzz

package zerodecimal

import (
	"errors"
	"testing"
	"time"
)

const strictNullFuzzSourceKinds = 19

// FuzzStrictNullDecimalScan covers every exact-source success and rejection,
// SQL NULL, stale-state clearing, and Value/Scan round trips.
func FuzzStrictNullDecimalScan(f *testing.F) {
	for sel := uint8(0); sel < strictNullFuzzSourceKinds; sel++ {
		f.Add(sel, int64(-5), uint64(7), float64(0.5), []byte("1.25"))
	}
	f.Add(uint8(0), int64(0), uint64(0), float64(0), []byte("not-a-number"))
	f.Add(uint8(1), int64(0), uint64(0), float64(0), []byte("0.0000000000000000001"))
	f.Add(uint8(12), int64(0), uint64(0), float64(0.5), []byte("0"))
	f.Add(uint8(13), int64(0), uint64(0), float64(0.5), []byte("0"))
	f.Fuzz(func(t *testing.T, sel uint8, i int64, u uint64, fv float64, raw []byte) {
		kind := sel % strictNullFuzzSourceKinds
		var src any
		var want Decimal
		var wantErr error
		null := false
		switch kind {
		case 0:
			src = string(raw)
			want, wantErr = NewFromString(string(raw))
		case 1:
			src = raw
			want, wantErr = NewFromString(string(raw))
		case 2:
			src = int(i)
			want = NewFromInt(int64(int(i)))
		case 3:
			src = int8(i)
			want = NewFromInt(int64(int8(i)))
		case 4:
			src = int16(i)
			want = NewFromInt(int64(int16(i)))
		case 5:
			src = int32(i)
			want = NewFromInt32(int32(i))
		case 6:
			src = i
			want = NewFromInt(i)
		case 7:
			src = uint(u)
			want = NewFromUint64(uint64(uint(u)))
		case 8:
			src = uint8(u)
			want = NewFromUint64(uint64(uint8(u)))
		case 9:
			src = uint16(u)
			want = NewFromUint64(uint64(uint16(u)))
		case 10:
			src = uint32(u)
			want = NewFromUint64(uint64(uint32(u)))
		case 11:
			src = u
			want = NewFromUint64(u)
		case 12:
			src = float32(fv)
			wantErr = ErrScanFloat
		case 13:
			src = fv
			wantErr = ErrScanFloat
		case 14:
			src = u&1 != 0
			wantErr = ErrScanType
		case 15:
			src = time.Unix(i, int64(uint32(u)))
			wantErr = ErrScanType
		case 16:
			src = nil
			null = true
		case 17:
			src = uintptr(u)
			wantErr = ErrScanType
		case 18:
			src = struct{ value uint64 }{value: u}
			wantErr = ErrScanType
		}

		marker := NewStrictNullDecimal(MustNew(31337, -3))
		got := marker
		err := got.Scan(src)
		if wantErr != nil {
			if !errors.Is(err, wantErr) {
				t.Fatalf("source kind %d: got error %v, want %v", kind, err, wantErr)
			}
			if got != (StrictNullDecimal{}) {
				t.Fatalf("source kind %d retained stale state: got %+v", kind, got)
			}
			return
		}
		if err != nil {
			t.Fatalf("source kind %d: unexpected error %v", kind, err)
		}
		if null {
			if got != (StrictNullDecimal{}) {
				t.Fatalf("SQL NULL did not clear: got %+v", got)
			}
		} else if got != NewStrictNullDecimal(want) {
			t.Fatalf("source kind %d: got %+v, want %+v", kind, got, NewStrictNullDecimal(want))
		}

		v, err := got.Value()
		if err != nil {
			t.Fatalf("source kind %d: Value error %v", kind, err)
		}
		var roundTrip StrictNullDecimal
		if err := roundTrip.Scan(v); err != nil {
			t.Fatalf("source kind %d: Value/Scan error %v", kind, err)
		}
		if roundTrip != got {
			t.Fatalf("source kind %d: Value/Scan got %+v, want %+v", kind, roundTrip, got)
		}
	})
}

// FuzzStrictNullDecimalCodecs proves the strict nullable wrapper is exactly a
// codec view of NullDecimal, including null clearing, parse-error atomicity,
// escaped JSON, marshal output, and nil-receiver precedence.
func FuzzStrictNullDecimalCodecs(f *testing.F) {
	for _, raw := range [][]byte{
		nil,
		[]byte(jsonNull),
		[]byte(`"1.5"`),
		[]byte(`"1\u002e5"`),
		[]byte(`"x"`),
		[]byte("-0.0000000000000000001"),
		{0xFF, 0x00, 0x80},
	} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		marker := RequireFromString("42.25")

		legacyJSON := NewNullDecimal(marker)
		strictJSON := NewStrictNullDecimal(marker)
		legacyErr := legacyJSON.UnmarshalJSON(raw)
		strictErr := strictJSON.UnmarshalJSON(raw)
		requireMatchingStrictNullCodec(t, "json", legacyJSON, legacyErr, strictJSON, strictErr, raw)
		legacyWire, legacyMarshalErr := legacyJSON.MarshalJSON()
		strictWire, strictMarshalErr := strictJSON.MarshalJSON()
		if !strictNullFuzzErrorsMatch(legacyMarshalErr, strictMarshalErr) || string(legacyWire) != string(strictWire) {
			t.Fatalf("json marshal mismatch: legacy=%q/%v strict=%q/%v", legacyWire, legacyMarshalErr, strictWire, strictMarshalErr)
		}

		legacyText := NewNullDecimal(marker)
		strictText := NewStrictNullDecimal(marker)
		legacyErr = legacyText.UnmarshalText(raw)
		strictErr = strictText.UnmarshalText(raw)
		requireMatchingStrictNullCodec(t, "text", legacyText, legacyErr, strictText, strictErr, raw)
		legacyWire, legacyMarshalErr = legacyText.MarshalText()
		strictWire, strictMarshalErr = strictText.MarshalText()
		if !strictNullFuzzErrorsMatch(legacyMarshalErr, strictMarshalErr) || string(legacyWire) != string(strictWire) {
			t.Fatalf("text marshal mismatch: legacy=%q/%v strict=%q/%v", legacyWire, legacyMarshalErr, strictWire, strictMarshalErr)
		}

		var nilStrict *StrictNullDecimal
		for i, err := range []error{
			nilStrict.Scan(raw),
			nilStrict.UnmarshalJSON(raw),
			nilStrict.UnmarshalText(raw),
		} {
			if !errors.Is(err, ErrNilReceiver) {
				t.Fatalf("nil receiver call %d: got %v", i, err)
			}
		}
	})
}

func requireMatchingStrictNullCodec(t *testing.T, name string, legacy NullDecimal, legacyErr error, strict StrictNullDecimal, strictErr error, raw []byte) {
	t.Helper()
	if (legacyErr == nil) != (strictErr == nil) {
		t.Fatalf("%s error presence mismatch: raw=%q legacy=%v strict=%v", name, raw, legacyErr, strictErr)
	}
	if legacyErr != nil && legacyErr.Error() != strictErr.Error() {
		t.Fatalf("%s error mismatch: raw=%q legacy=%v strict=%v", name, raw, legacyErr, strictErr)
	}
	if strict != strictNullDecimalFromNull(legacy) {
		t.Fatalf("%s value mismatch: raw=%q legacy=%+v strict=%+v", name, raw, legacy, strict)
	}
}

func strictNullFuzzErrorsMatch(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Error() == b.Error()
}
