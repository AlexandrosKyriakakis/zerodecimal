package zerodecimal

import "testing"

// pgBenchShape is one typical column shape in both PostgreSQL protocols:
// d is the value at the column's scale, text the text protocol's bytes
// (PostgreSQL renders dscale as trailing zeros) and wire the binary
// protocol's (numeric_send at the same dscale).
type pgBenchShape struct {
	name string
	d    Decimal
	text []byte
	wire []byte
}

func newPgBenchShape(name, text string, scale uint8) pgBenchShape {
	d := RequireFromString(text).MustRescale(scale)
	return pgBenchShape{name: name, d: d, text: []byte(text), wire: d.AppendPgNumeric(nil)}
}

// Package-level inputs and sinks for BenchmarkPgNumeric (see alloc_test.go
// for why neither may be local).
var (
	pgBenchShapes = []pgBenchShape{
		newPgBenchShape("price_9dp", "12.345678900", 9),
		newPgBenchShape("qty_9dp", "1234.500000000", 9),
		newPgBenchShape("cost_2dp", "1234567.89", 2),
		newPgBenchShape("crypto_qty_18dp", "1234.567890123456789012", 18),
		newPgBenchShape("max_coefficient", "34028236692093846346.3374607431768211455", 19),
	}
	pgBenchResult Decimal
	errPgBench    error
	pgBenchBuf    = make([]byte, 0, 64)
	pgBenchOut    []byte
)

// BenchmarkPgNumeric times the binary NUMERIC codec against the text
// protocol path it lets pgx skip, ParseBytes and AppendText of the same
// value, for typical column shapes. Sub-benchmark names are key=value
// pairs, so `benchstat -col /proto` compares the protocols row by row. The
// pgtype.Numeric path the binary codec replaces cannot be measured here
// (the root module has no pgx dependency); it costs at least one *big.Int
// allocation per value.
func BenchmarkPgNumeric(b *testing.B) {
	for _, s := range pgBenchShapes {
		wire, text, d := s.wire, s.text, s.d
		b.Run("op=Parse/proto=binary/shape="+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				pgBenchResult, errPgBench = ParsePgNumeric(wire)
			}
		})
		b.Run("op=Parse/proto=text/shape="+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				pgBenchResult, errPgBench = ParseBytes(text)
			}
		})
		b.Run("op=Append/proto=binary/shape="+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				pgBenchOut = d.AppendPgNumeric(pgBenchBuf[:0])
			}
		})
		b.Run("op=Append/proto=text/shape="+s.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				pgBenchOut, errPgBench = d.AppendText(pgBenchBuf[:0])
			}
		})
	}
}
