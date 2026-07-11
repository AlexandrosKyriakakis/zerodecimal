package zerodecimal

import "testing"

// BenchmarkParseCanonicalRescue separates the ordinary specialized paths,
// ordinary strict-general parsing, and the canonical-rescue shapes. This
// keeps the correctness fix's cold-path cost visible without allowing the
// common short-literal path to hide it in an aggregate benchmark.
func BenchmarkParseCanonicalRescue(b *testing.B) {
	cases := []struct {
		name string
		in   string
	}{
		{"ordinary_short", "1234.5678"},
		{"ordinary_scientific_short_integer", "1e2"},
		{"ordinary_scientific_short_fraction", "2.5E8"},
		{"ordinary_scientific_short_negative_exp", "1.5e-7"},
		{"ordinary_scientific_long_integer", "1234567890123456e2"},
		{"ordinary_max", maxCoefficientText},
		{"ordinary_scientific", "12345678901234567890.12345e1"},
		{"rescue_fraction_zero", maxCoefficientText + ".0"},
		{"rescue_exponent", maxCoefficientText + "0e-1"},
		{"rescue_scale_twenty", "1.00000000000000000000"},
	}

	for _, tc := range cases {
		b.Run(tc.name+"/string", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				canonicalParseSink, errCanonicalParseSink = NewFromString(tc.in)
			}
		})

		in := []byte(tc.in)
		b.Run(tc.name+"/bytes", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				canonicalParseSink, errCanonicalParseSink = ParseBytes(in)
			}
		})
	}
}

func BenchmarkParseTruncScientific(b *testing.B) {
	for _, in := range []string{"1e2", "2.5E8", "1.5e-7"} {
		b.Run(in, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				canonicalParseSink, errCanonicalParseSink = NewFromStringTrunc(in)
			}
		})
	}
}
