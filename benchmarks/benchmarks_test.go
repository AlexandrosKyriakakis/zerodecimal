// Package benchmarks compares zerodecimal against udecimal, alpacadecimal,
// shopspring/decimal, ericlagergren/decimal, jokruger/dec128, and
// govalues/decimal on a shared op × shape matrix. Sub-benchmarks are named
// Benchmark<Op>/<lib>/<shape> with lib in {zd, udec, alpaca, ss, eric, dec,
// gv} so that per-library runs can be filtered with -bench=/<lib>/ (anchored
// as /^dec$/ for dec, which is a substring of udec) and compared with
// benchstat after stripping the lib segment.
//
// Inputs are parsed once at package level per library type; every leaf
// benchmark reports allocations, uses b.Loop, and writes results (errors
// included) into package-level sinks so no call is dead-code-eliminated.
// Operations a library does not provide are skipped, never approximated;
// shapes a library cannot represent are skipped too (govalues caps at 19
// significant digits, so its large and near_max rows are absent). The README
// lists every skip and semantic asymmetry.
package benchmarks

import (
	"database/sql/driver"
	"strconv"
	"testing"

	alpaca "github.com/alpacahq/alpacadecimal"
	ed "github.com/ericlagergren/decimal"
	ericpg "github.com/ericlagergren/decimal/sql/postgres"
	gv "github.com/govalues/decimal"
	dec "github.com/jokruger/dec128"
	udec "github.com/quagmt/udecimal"
	ss "github.com/shopspring/decimal"

	zd "github.com/AlexandrosKyriakakis/zerodecimal"
)

// roundPlaces is the fractional-digit count used by RoundBank and Truncate.
const roundPlaces = 2

// numShapes is the number of input shapes in the matrix.
const numShapes = 5

// shapes is the operand matrix: each shape is a pair of decimal literals
// chosen to exercise a distinct representation regime, from single-digit
// integers up to coefficients near 2^128.
var shapes = [numShapes]struct {
	name, a, b string
}{
	{"small_int", "5", "7"},
	{"typical_price", "1234.5678", "8765.4321"},
	{"max_prec", "0.1234567890123456789", "0.9876543210987654321"},
	{"large", "12345678901234567890.123456789", "987654321.987654321"},
	{"near_max", "17014118346046923173.1687303715884105727", "1.000000001"},
}

// Pre-parsed operands, one pair per shape per library.
var (
	zdA, zdB         [numShapes]zd.Decimal
	udecA, udecB     [numShapes]udec.Decimal
	alpacaA, alpacaB [numShapes]alpaca.Decimal
	ssA, ssB         [numShapes]ss.Decimal
	ericA, ericB     [numShapes]*ed.Big
	decA, decB       [numShapes]dec.Dec128
	gvA, gvB         [numShapes]gv.Decimal
)

// gvOK[i] is true when govalues can represent both operands of shape i; it
// caps at 19 significant digits, so the large and near_max shapes are absent.
// Every gv sub-benchmark is gated on it.
var gvOK [numShapes]bool

// Pre-encoded inputs for the decode-direction codec and conversion benchmarks.
var (
	floats   [numShapes]float64
	scanSrcs [numShapes]any

	zdJSON, udecJSON, alpacaJSON, ssJSON, ericJSON, decJSON, gvJSON [numShapes][]byte
	zdBin, udecBin, ssBin, decBin, gvBin                            [numShapes][]byte
)

// ericValuers wraps each eric operand for the driver.Valuer benchmarks.
var ericValuers [numShapes]*ericpg.Decimal

// Package-level sinks keep every benchmarked call observable.
var (
	zdSink, zdSink2         zd.Decimal
	udecSink, udecSink2     udec.Decimal
	alpacaSink, alpacaSink2 alpaca.Decimal
	ssSink, ssSink2         ss.Decimal
	decSink, decSink2       dec.Dec128
	gvSink, gvSink2         gv.Decimal
	ericPtrSink             *ed.Big
	ericPtrSink2            *ed.Big

	boolSink  bool
	bytesSink []byte
	errSink   error
	intSink   int
	strSink   string
	valueSink driver.Value
)

// Reused destinations for the unmarshal- and scan-direction benchmarks.
var (
	zdDst     zd.Decimal
	udecDst   udec.Decimal
	alpacaDst alpaca.Decimal
	ssDst     ss.Decimal
	decDst    dec.Dec128
	gvDst     gv.Decimal
	ericDst   = newEricSink(ed.ToNearestEven)
	ericPGDst = ericpg.Decimal{V: newEricSink(ed.ToNearestEven)}
)

// Eric result receivers: ericlagergren operations write into an explicit
// destination Big whose Context controls rounding, so each rounding flavor
// needs its own receiver.
var (
	ericSink      = newEricSink(ed.ToNearestEven)
	ericRemSink   = newEricSink(ed.ToNearestEven)
	ericTruncSink = newEricSink(ed.ToZero)
)

// appendBuf backs the AppendText benchmarks so appends never grow a slice.
var appendBuf = make([]byte, 0, 64)

// newEricSink returns an empty ericlagergren Big with the same context the
// udecimal benchmark harness uses (precision 19), varying only the rounding
// mode.
func newEricSink(mode ed.RoundingMode) *ed.Big {
	z := new(ed.Big)
	z.Context.Precision = 19
	z.Context.RoundingMode = mode
	return z
}

// newEric parses s into a fresh ericlagergren Big, panicking on bad fixtures.
func newEric(s string) *ed.Big {
	z := newEricSink(ed.ToNearestEven)
	if _, ok := z.SetString(s); !ok {
		panic("benchmarks: eric cannot parse " + s)
	}
	return z
}

// mustDec parses s into a dec128 Dec128, panicking on bad fixtures: dec128
// reports failure by returning a NaN-poisoned value instead of an error.
func mustDec(s string) dec.Dec128 {
	d := dec.FromString(s)
	if d.IsNaN() {
		panic("benchmarks: dec128 cannot parse " + s)
	}
	return d
}

// mustBytes unwraps a ([]byte, error) fixture constructor, panicking on error.
func mustBytes(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func init() {
	for i, sh := range shapes {
		zdA[i] = zd.RequireFromString(sh.a)
		zdB[i] = zd.RequireFromString(sh.b)
		udecA[i] = udec.MustParse(sh.a)
		udecB[i] = udec.MustParse(sh.b)
		alpacaA[i] = alpaca.RequireFromString(sh.a)
		alpacaB[i] = alpaca.RequireFromString(sh.b)
		ssA[i] = ss.RequireFromString(sh.a)
		ssB[i] = ss.RequireFromString(sh.b)
		ericA[i] = newEric(sh.a)
		ericB[i] = newEric(sh.b)
		decA[i] = mustDec(sh.a)
		decB[i] = mustDec(sh.b)

		// govalues caps at 19 significant digits; the large and near_max
		// operands do not fit, so those shapes stay skipped (gvOK false).
		if ga, ea := gv.Parse(sh.a); ea == nil {
			if gb, eb := gv.Parse(sh.b); eb == nil {
				gvA[i], gvB[i], gvOK[i] = ga, gb, true
			}
		}

		f, err := strconv.ParseFloat(sh.a, 64)
		if err != nil {
			panic(err)
		}
		floats[i] = f
		scanSrcs[i] = sh.a

		zdJSON[i] = mustBytes(zdA[i].MarshalJSON())
		udecJSON[i] = mustBytes(udecA[i].MarshalJSON())
		alpacaJSON[i] = mustBytes(alpacaA[i].MarshalJSON())
		ssJSON[i] = mustBytes(ssA[i].MarshalJSON())
		ericJSON[i] = mustBytes(ericA[i].MarshalText())
		decJSON[i] = mustBytes(decA[i].MarshalJSON())

		zdBin[i] = mustBytes(zdA[i].MarshalBinary())
		udecBin[i] = mustBytes(udecA[i].MarshalBinary())
		ssBin[i] = mustBytes(ssA[i].MarshalBinary())
		decBin[i] = mustBytes(decA[i].MarshalBinary())

		if gvOK[i] {
			gvJSON[i] = mustBytes(gvA[i].MarshalJSON())
			gvBin[i] = mustBytes(gvA[i].MarshalBinary())
		}

		ericValuers[i] = &ericpg.Decimal{V: ericA[i]}
	}
}

func BenchmarkParse(b *testing.B) {
	for i, sh := range shapes {
		s := sh.a
		b.Run("zd/"+sh.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				zdSink, errSink = zd.NewFromString(s)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				udecSink, errSink = udec.Parse(s)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				alpacaSink, errSink = alpaca.NewFromString(s)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ssSink, errSink = ss.NewFromString(s)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ericPtrSink, boolSink = ericDst.SetString(s)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				decSink = dec.FromString(s)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					gvSink, errSink = gv.Parse(s)
				}
			})
		}
	}
}

func BenchmarkString(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d := zdA[i]
			b.ReportAllocs()
			for b.Loop() {
				strSink = d.String()
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d := udecA[i]
			b.ReportAllocs()
			for b.Loop() {
				strSink = d.String()
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d := alpacaA[i]
			b.ReportAllocs()
			for b.Loop() {
				strSink = d.String()
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d := ssA[i]
			b.ReportAllocs()
			for b.Loop() {
				strSink = d.String()
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			d := ericA[i]
			b.ReportAllocs()
			for b.Loop() {
				strSink = d.String()
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d := decA[i]
			b.ReportAllocs()
			for b.Loop() {
				strSink = d.String()
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d := gvA[i]
				b.ReportAllocs()
				for b.Loop() {
					strSink = d.String()
				}
			})
		}
	}
}

func BenchmarkAdd(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d, e := zdA[i], zdB[i]
			b.ReportAllocs()
			for b.Loop() {
				zdSink, errSink = d.Add(e)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d, e := udecA[i], udecB[i]
			b.ReportAllocs()
			for b.Loop() {
				udecSink = d.Add(e)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d, e := alpacaA[i], alpacaB[i]
			b.ReportAllocs()
			for b.Loop() {
				alpacaSink = d.Add(e)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d, e := ssA[i], ssB[i]
			b.ReportAllocs()
			for b.Loop() {
				ssSink = d.Add(e)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			d, e := ericA[i], ericB[i]
			b.ReportAllocs()
			for b.Loop() {
				ericPtrSink = ericSink.Add(d, e)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d, e := decA[i], decB[i]
			b.ReportAllocs()
			for b.Loop() {
				decSink = d.Add(e)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d, e := gvA[i], gvB[i]
				b.ReportAllocs()
				for b.Loop() {
					gvSink, errSink = d.Add(e)
				}
			})
		}
	}
}

func BenchmarkSub(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d, e := zdA[i], zdB[i]
			b.ReportAllocs()
			for b.Loop() {
				zdSink, errSink = d.Sub(e)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d, e := udecA[i], udecB[i]
			b.ReportAllocs()
			for b.Loop() {
				udecSink = d.Sub(e)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d, e := alpacaA[i], alpacaB[i]
			b.ReportAllocs()
			for b.Loop() {
				alpacaSink = d.Sub(e)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d, e := ssA[i], ssB[i]
			b.ReportAllocs()
			for b.Loop() {
				ssSink = d.Sub(e)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			d, e := ericA[i], ericB[i]
			b.ReportAllocs()
			for b.Loop() {
				ericPtrSink = ericSink.Sub(d, e)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d, e := decA[i], decB[i]
			b.ReportAllocs()
			for b.Loop() {
				decSink = d.Sub(e)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d, e := gvA[i], gvB[i]
				b.ReportAllocs()
				for b.Loop() {
					gvSink, errSink = d.Sub(e)
				}
			})
		}
	}
}

func BenchmarkMul(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d, e := zdA[i], zdB[i]
			b.ReportAllocs()
			for b.Loop() {
				zdSink, errSink = d.Mul(e)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d, e := udecA[i], udecB[i]
			b.ReportAllocs()
			for b.Loop() {
				udecSink = d.Mul(e)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d, e := alpacaA[i], alpacaB[i]
			b.ReportAllocs()
			for b.Loop() {
				alpacaSink = d.Mul(e)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d, e := ssA[i], ssB[i]
			b.ReportAllocs()
			for b.Loop() {
				ssSink = d.Mul(e)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			d, e := ericA[i], ericB[i]
			b.ReportAllocs()
			for b.Loop() {
				ericPtrSink = ericSink.Mul(d, e)
			}
		})
		// dec on max_prec, large, near_max: measures the overflow-to-NaN
		// path — dec128 only returns exact products (see README).
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d, e := decA[i], decB[i]
			b.ReportAllocs()
			for b.Loop() {
				decSink = d.Mul(e)
			}
		})
		// gv on max_prec rounds the product half-even to fit 19 digits (the
		// exact product needs more scale); see README.
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d, e := gvA[i], gvB[i]
				b.ReportAllocs()
				for b.Loop() {
					gvSink, errSink = d.Mul(e)
				}
			})
		}
	}
}

func BenchmarkDiv(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d, e := zdA[i], zdB[i]
			b.ReportAllocs()
			for b.Loop() {
				zdSink, errSink = d.Div(e)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d, e := udecA[i], udecB[i]
			b.ReportAllocs()
			for b.Loop() {
				udecSink, errSink = d.Div(e)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d, e := alpacaA[i], alpacaB[i]
			b.ReportAllocs()
			for b.Loop() {
				alpacaSink = d.Div(e)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d, e := ssA[i], ssB[i]
			b.ReportAllocs()
			for b.Loop() {
				ssSink = d.Div(e)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			d, e := ericA[i], ericB[i]
			b.ReportAllocs()
			for b.Loop() {
				ericPtrSink = ericSink.Quo(d, e)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d, e := decA[i], decB[i]
			b.ReportAllocs()
			for b.Loop() {
				decSink = d.Div(e)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d, e := gvA[i], gvB[i]
				b.ReportAllocs()
				for b.Loop() {
					gvSink, errSink = d.Quo(e)
				}
			})
		}
	}
}

func BenchmarkQuoRem(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d, e := zdA[i], zdB[i]
			b.ReportAllocs()
			for b.Loop() {
				zdSink, zdSink2, errSink = d.QuoRem(e)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d, e := udecA[i], udecB[i]
			b.ReportAllocs()
			for b.Loop() {
				udecSink, udecSink2, errSink = d.QuoRem(e)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d, e := alpacaA[i], alpacaB[i]
			b.ReportAllocs()
			for b.Loop() {
				alpacaSink, alpacaSink2 = d.QuoRem(e, 0)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d, e := ssA[i], ssB[i]
			b.ReportAllocs()
			for b.Loop() {
				ssSink, ssSink2 = d.QuoRem(e, 0)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			d, e := ericA[i], ericB[i]
			b.ReportAllocs()
			for b.Loop() {
				ericPtrSink, ericPtrSink2 = ericSink.QuoRem(d, e, ericRemSink)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d, e := decA[i], decB[i]
			b.ReportAllocs()
			for b.Loop() {
				decSink, decSink2 = d.QuoRem(e)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d, e := gvA[i], gvB[i]
				b.ReportAllocs()
				for b.Loop() {
					gvSink, gvSink2, errSink = d.QuoRem(e)
				}
			})
		}
	}
}

func BenchmarkCmp(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d, e := zdA[i], zdB[i]
			b.ReportAllocs()
			for b.Loop() {
				intSink = d.Cmp(e)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d, e := udecA[i], udecB[i]
			b.ReportAllocs()
			for b.Loop() {
				intSink = d.Cmp(e)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d, e := alpacaA[i], alpacaB[i]
			b.ReportAllocs()
			for b.Loop() {
				intSink = d.Cmp(e)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d, e := ssA[i], ssB[i]
			b.ReportAllocs()
			for b.Loop() {
				intSink = d.Cmp(e)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			d, e := ericA[i], ericB[i]
			b.ReportAllocs()
			for b.Loop() {
				intSink = d.Cmp(e)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d, e := decA[i], decB[i]
			b.ReportAllocs()
			for b.Loop() {
				intSink = d.Compare(e)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d, e := gvA[i], gvB[i]
				b.ReportAllocs()
				for b.Loop() {
					intSink = d.Cmp(e)
				}
			})
		}
	}
}

func BenchmarkRoundBank(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d := zdA[i]
			b.ReportAllocs()
			for b.Loop() {
				zdSink = d.RoundBank(roundPlaces)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d := udecA[i]
			b.ReportAllocs()
			for b.Loop() {
				udecSink = d.RoundBank(roundPlaces)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d := alpacaA[i]
			b.ReportAllocs()
			for b.Loop() {
				alpacaSink = d.RoundBank(roundPlaces)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d := ssA[i]
			b.ReportAllocs()
			for b.Loop() {
				ssSink = d.RoundBank(roundPlaces)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			d := ericA[i]
			b.ReportAllocs()
			for b.Loop() {
				ericPtrSink = ericSink.Copy(d).Quantize(roundPlaces)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d := decA[i]
			b.ReportAllocs()
			for b.Loop() {
				decSink = d.RoundBank(roundPlaces)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d := gvA[i]
				b.ReportAllocs()
				for b.Loop() {
					gvSink = d.Round(roundPlaces)
				}
			})
		}
	}
}

func BenchmarkTruncate(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d := zdA[i]
			b.ReportAllocs()
			for b.Loop() {
				zdSink = d.Truncate(roundPlaces)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d := udecA[i]
			b.ReportAllocs()
			for b.Loop() {
				udecSink = d.Trunc(roundPlaces)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d := alpacaA[i]
			b.ReportAllocs()
			for b.Loop() {
				alpacaSink = d.Truncate(roundPlaces)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d := ssA[i]
			b.ReportAllocs()
			for b.Loop() {
				ssSink = d.Truncate(roundPlaces)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			d := ericA[i]
			b.ReportAllocs()
			for b.Loop() {
				ericPtrSink = ericTruncSink.Copy(d).Quantize(roundPlaces)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d := decA[i]
			b.ReportAllocs()
			for b.Loop() {
				decSink = d.Trunc(roundPlaces)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d := gvA[i]
				b.ReportAllocs()
				for b.Loop() {
					gvSink = d.Trunc(roundPlaces)
				}
			})
		}
	}
}

func BenchmarkMarshalJSON(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d := zdA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.MarshalJSON()
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d := udecA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.MarshalJSON()
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d := alpacaA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.MarshalJSON()
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d := ssA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.MarshalJSON()
			}
		})
		// eric: skipped, *decimal.Big has no MarshalJSON (MarshalText is a
		// different operation).
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d := decA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.MarshalJSON()
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d := gvA[i]
				b.ReportAllocs()
				for b.Loop() {
					bytesSink, errSink = d.MarshalJSON()
				}
			})
		}
	}
}

func BenchmarkUnmarshalJSON(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			data := zdJSON[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = zdDst.UnmarshalJSON(data)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			data := udecJSON[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = udecDst.UnmarshalJSON(data)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			data := alpacaJSON[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = alpacaDst.UnmarshalJSON(data)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			data := ssJSON[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = ssDst.UnmarshalJSON(data)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			data := ericJSON[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = ericDst.UnmarshalJSON(data)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			data := decJSON[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = decDst.UnmarshalJSON(data)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				data := gvJSON[i]
				b.ReportAllocs()
				for b.Loop() {
					errSink = gvDst.UnmarshalJSON(data)
				}
			})
		}
	}
}

func BenchmarkMarshalBinary(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d := zdA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.MarshalBinary()
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d := udecA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.MarshalBinary()
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d := ssA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.MarshalBinary()
			}
		})
		// alpaca: skipped, its binary codec converts to shopspring and
		// delegates, so the ss rows already measure that path.
		// eric: skipped, *decimal.Big has no binary codec.
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d := decA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.MarshalBinary()
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d := gvA[i]
				b.ReportAllocs()
				for b.Loop() {
					bytesSink, errSink = d.MarshalBinary()
				}
			})
		}
	}
}

func BenchmarkUnmarshalBinary(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			data := zdBin[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = zdDst.UnmarshalBinary(data)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			data := udecBin[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = udecDst.UnmarshalBinary(data)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			data := ssBin[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = ssDst.UnmarshalBinary(data)
			}
		})
		// alpaca, eric: skipped for the same reasons as MarshalBinary.
		b.Run("dec/"+sh.name, func(b *testing.B) {
			data := decBin[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = decDst.UnmarshalBinary(data)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				data := gvBin[i]
				b.ReportAllocs()
				for b.Loop() {
					errSink = gvDst.UnmarshalBinary(data)
				}
			})
		}
	}
}

func BenchmarkAppendText(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d := zdA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.AppendText(appendBuf)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d := udecA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink, errSink = d.AppendText(appendBuf)
			}
		})
		// alpaca, ss, eric: skipped, no append-style text API.
		// dec: StringToBuf is its buffer-reuse text renderer; it resets the
		// buffer instead of appending, identical work on an empty buffer
		// (see README).
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d := decA[i]
			b.ReportAllocs()
			for b.Loop() {
				bytesSink = d.StringToBuf(appendBuf)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d := gvA[i]
				b.ReportAllocs()
				for b.Loop() {
					bytesSink, errSink = d.AppendText(appendBuf)
				}
			})
		}
	}
}

func BenchmarkSQLValue(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			d := zdA[i]
			b.ReportAllocs()
			for b.Loop() {
				valueSink, errSink = d.Value()
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			d := udecA[i]
			b.ReportAllocs()
			for b.Loop() {
				valueSink, errSink = d.Value()
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			d := alpacaA[i]
			b.ReportAllocs()
			for b.Loop() {
				valueSink, errSink = d.Value()
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			d := ssA[i]
			b.ReportAllocs()
			for b.Loop() {
				valueSink, errSink = d.Value()
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			d := ericValuers[i]
			b.ReportAllocs()
			for b.Loop() {
				valueSink, errSink = d.Value()
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			d := decA[i]
			b.ReportAllocs()
			for b.Loop() {
				valueSink, errSink = d.Value()
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				d := gvA[i]
				b.ReportAllocs()
				for b.Loop() {
					valueSink, errSink = d.Value()
				}
			})
		}
	}
}

func BenchmarkSQLScan(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			src := scanSrcs[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = zdDst.Scan(src)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			src := scanSrcs[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = udecDst.Scan(src)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			src := scanSrcs[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = alpacaDst.Scan(src)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			src := scanSrcs[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = ssDst.Scan(src)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			src := scanSrcs[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = ericPGDst.Scan(src)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			src := scanSrcs[i]
			b.ReportAllocs()
			for b.Loop() {
				errSink = decDst.Scan(src)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				src := scanSrcs[i]
				b.ReportAllocs()
				for b.Loop() {
					errSink = gvDst.Scan(src)
				}
			})
		}
	}
}

func BenchmarkNewFromFloat(b *testing.B) {
	for i, sh := range shapes {
		b.Run("zd/"+sh.name, func(b *testing.B) {
			f := floats[i]
			b.ReportAllocs()
			for b.Loop() {
				zdSink, errSink = zd.NewFromFloat(f)
			}
		})
		b.Run("udec/"+sh.name, func(b *testing.B) {
			f := floats[i]
			b.ReportAllocs()
			for b.Loop() {
				udecSink, errSink = udec.NewFromFloat64(f)
			}
		})
		b.Run("alpaca/"+sh.name, func(b *testing.B) {
			f := floats[i]
			b.ReportAllocs()
			for b.Loop() {
				alpacaSink = alpaca.NewFromFloat(f)
			}
		})
		b.Run("ss/"+sh.name, func(b *testing.B) {
			f := floats[i]
			b.ReportAllocs()
			for b.Loop() {
				ssSink = ss.NewFromFloat(f)
			}
		})
		b.Run("eric/"+sh.name, func(b *testing.B) {
			f := floats[i]
			b.ReportAllocs()
			for b.Loop() {
				ericPtrSink = ericSink.SetFloat64(f)
			}
		})
		b.Run("dec/"+sh.name, func(b *testing.B) {
			f := floats[i]
			b.ReportAllocs()
			for b.Loop() {
				decSink = dec.FromFloat64(f)
			}
		})
		if gvOK[i] {
			b.Run("gv/"+sh.name, func(b *testing.B) {
				f := floats[i]
				b.ReportAllocs()
				for b.Loop() {
					gvSink, errSink = gv.NewFromFloat64(f)
				}
			})
		}
	}
}
