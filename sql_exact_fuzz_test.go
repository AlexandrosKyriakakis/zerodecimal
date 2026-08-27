//go:build fuzz

package zerodecimal

import (
	"errors"
	"testing"
	"time"
)

const strictSQLFuzzSourceKinds = 19

// FuzzStrictSQLDecimalScan covers every accepted and rejected source arm.
// Parsing validity is delegated to the parser contract; this target checks
// source policy, exact integer conversion, receiver atomicity, and Valuer
// round trips for every successful scan.
func FuzzStrictSQLDecimalScan(f *testing.F) {
	for sel := uint8(0); sel < strictSQLFuzzSourceKinds; sel++ {
		f.Add(sel, int64(-5), uint64(7), float64(0.5), []byte("1.25"))
	}
	f.Add(uint8(0), int64(0), uint64(0), float64(0), []byte("not-a-number"))
	f.Add(uint8(1), int64(0), uint64(0), float64(0), []byte("0.0000000000000000001"))
	f.Add(uint8(12), int64(0), uint64(0), float64(0.5), []byte("0"))
	f.Add(uint8(13), int64(0), uint64(0), float64(0.5), []byte("0"))
	f.Fuzz(func(t *testing.T, sel uint8, i int64, u uint64, fv float64, raw []byte) {
		kind := sel % strictSQLFuzzSourceKinds
		var src any
		var want Decimal
		var wantErr error
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
			wantErr = ErrScanNil
		case 17:
			src = uintptr(u)
			wantErr = ErrScanType
		case 18:
			src = struct{ value uint64 }{value: u}
			wantErr = ErrScanType
		}

		marker := NewStrictSQLDecimal(MustNew(31337, -3))
		got := marker
		err := got.Scan(src)
		if wantErr != nil {
			if !errors.Is(err, wantErr) {
				t.Fatalf("source kind %d: got error %v, want %v", kind, err, wantErr)
			}
			if got != marker {
				t.Fatalf("source kind %d changed receiver on error: got %v, marker %v", kind, got, marker)
			}
			if errors.Is(err, ErrScanFloat) && errors.Is(err, ErrInexact) {
				t.Fatal("ErrScanFloat must remain distinct from ErrInexact")
			}
			return
		}
		if err != nil {
			t.Fatalf("source kind %d: unexpected error %v", kind, err)
		}
		if got.Decimal != want {
			t.Fatalf("source kind %d: got %v, want %v", kind, got.Decimal, want)
		}

		v, err := got.Value()
		if err != nil {
			t.Fatalf("source kind %d: Value error %v", kind, err)
		}
		var roundTrip StrictSQLDecimal
		if err := roundTrip.Scan(v); err != nil {
			t.Fatalf("source kind %d: Value/Scan round trip error %v", kind, err)
		}
		if roundTrip != got {
			t.Fatalf("source kind %d: Value/Scan got %v, want %v", kind, roundTrip, got)
		}
	})
}
