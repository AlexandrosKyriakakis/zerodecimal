package zerodecimal

import (
	"errors"
	"testing"
)

// BenchmarkMulRoundPow10 covers narrow and wide products, both sides of the
// 19-digit divisor boundary, and paths that do not discard digits. Keep the
// fixtures identical when comparing binaries built before and after a change.
func BenchmarkMulRoundPow10(b *testing.B) {
	for _, tc := range []struct {
		name, a, e string
		places     uint8
		wantErr    error
	}{
		{"price_fee", "1234.5678", "0.00125", 2, nil},
		{"product_128", "123456789.123456789", "7.000000001", 8, nil},
		{"product_192", "34028236692093846346.3374607431768211455", "0.1234567890123456789", 19, nil},
		{"drop_20", "34028236692093846346.3374607431768211455", "0.1234567890123456789", 18, nil},
		{"drop_36", "34028236692093846346.3374607431768211455", "0.1234567890123456789", 2, nil},
		{"drop_38", "34028236692093846346.3374607431768211455", "0.1234567890123456789", 0, nil},
		{"padding", "1234", "7", 2, nil},
		{"same_precision", "1234.56", "7", 2, nil},
		{"sticky_half", "0.0000000000000000001", "500000000000000000.1", 1, nil},
		{"overflow", "34028236692093846346.3374607431768211455", "34028236692093846346.3374607431768211455", 19, ErrOverflow},
	} {
		b.Run(tc.name, func(b *testing.B) {
			d, e := RequireFromString(tc.a), RequireFromString(tc.e)
			if _, err := d.MulRound(e, tc.places, ToNearestEven); !errors.Is(err, tc.wantErr) {
				b.Fatalf("preflight: got %v, want %v", err, tc.wantErr)
			}
			b.ReportAllocs()
			for b.Loop() {
				exactResultSink, errExactSink = d.MulRound(e, tc.places, ToNearestEven)
			}
		})
	}
}
