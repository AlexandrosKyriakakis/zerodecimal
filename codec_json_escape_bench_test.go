package zerodecimal

import (
	"errors"
	"testing"
)

var (
	jsonEscapeBenchDecimal Decimal
	errJSONEscapeBench     error
)

// BenchmarkUnmarshalJSONSpellings keeps the bare-number path, the package's
// ordinary quoted wire form, and progressively escaped interoperable forms
// visible independently.
func BenchmarkUnmarshalJSONSpellings(b *testing.B) {
	cases := []struct {
		name    string
		data    []byte
		wantErr error
	}{
		{name: "bare", data: []byte(`1234.5678`)},
		{name: "bare_zero_fraction", data: []byte(`0.125`)},
		{name: "bare_plus_rejected", data: []byte(`+1`), wantErr: ErrInvalidFormat},
		{name: "bare_leading_zero_rejected", data: []byte(`-00`), wantErr: ErrInvalidFormat},
		{name: "quoted_plain", data: []byte(`"1234.5678"`)},
		{name: "quoted_escaped_point", data: []byte(`"1234\u002e5678"`)},
		{name: "quoted_all_escaped", data: []byte(`"\u0031\u0032\u0033\u0034\u002e\u0035\u0036\u0037\u0038"`)},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			var d Decimal
			if err := d.UnmarshalJSON(tc.data); !errors.Is(err, tc.wantErr) {
				b.Fatalf("benchmark fixture: got %v, want %v", err, tc.wantErr)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.data)))
			for b.Loop() {
				errJSONEscapeBench = jsonEscapeBenchDecimal.UnmarshalJSON(tc.data)
			}
		})
	}
}
