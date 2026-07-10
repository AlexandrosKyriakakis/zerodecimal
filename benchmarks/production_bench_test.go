package benchmarks

import (
	"database/sql/driver"
	"errors"
	"runtime"
	"testing"

	zd "github.com/AlexandrosKyriakakis/zerodecimal"
)

const productionMax = "340282366920938463463374607431768211455"

// Every fixture below is immutable and built before benchmark timers start.
// Inputs that benchmark parsing remain strings/bytes by definition; all other
// decimals and interface-valued Scan sources are prebuilt at package init.
var (
	productionParseCases = []struct {
		name string
		text string
		data []byte
	}{
		{name: "canonical_price", text: "12345678901234567890.1234567890123456789", data: []byte("12345678901234567890.1234567890123456789")},
		{name: "scientific", text: "1234567890123456789012345e-5", data: []byte("1234567890123456789012345e-5")},
		{name: "rescue_fraction_zero", text: productionMax + ".0", data: []byte(productionMax + ".0")},
		{name: "rescue_exponent", text: productionMax + "0e-1", data: []byte(productionMax + "0e-1")},
		{name: "rescue_scale_twenty", text: "1.00000000000000000000", data: []byte("1.00000000000000000000")},
	}

	productionMulExactA = zd.RequireFromString("123456789.125")
	productionMulExactB = zd.RequireFromString("8")
	productionMulRoundA = zd.RequireFromString("123456789.123456789")
	productionMulRoundB = zd.RequireFromString("7.000000001")
	productionZero      = zd.Decimal{}
	productionDivExactA = zd.NewFromInt(1)
	productionDivExactB = zd.NewFromInt(8)
	productionDivRoundA = zd.RequireFromString("5000000000000000001")
	productionDivRoundB = zd.RequireFromString("1000000000000000000000")

	productionAggregateMixed = []zd.Decimal{
		zd.RequireFromString("1250000.125"),
		zd.RequireFromString("-249999.875"),
		zd.RequireFromString("17.0000000000000000001"),
		zd.RequireFromString("-3.75"),
		zd.RequireFromString("0.0000000000000000001"),
	}
	productionAggregateCancellation = []zd.Decimal{
		zd.RequireFromString(productionMax),
		zd.RequireFromString(productionMax),
		zd.RequireFromString("-" + productionMax),
		zd.RequireFromString("-" + productionMax),
		zd.RequireFromString("0.01"),
	}
	productionAggregateSamePrecision2 = []zd.Decimal{
		zd.MustNew(123456, -2),
		zd.MustNew(654321, -2),
	}
	productionAggregateSamePrecision10 = func() []zd.Decimal {
		xs := make([]zd.Decimal, 10)
		for i := range xs {
			xs[i] = zd.MustNew(100000+int64(i)*17, -2)
		}
		return xs
	}()
	productionAggregateSamePrecision4096 = func() []zd.Decimal {
		xs := make([]zd.Decimal, 4096)
		for i := range xs {
			xs[i] = zd.MustNew(100000+int64(i%17), -2)
		}
		return xs
	}()
	productionAggregateLateMismatch4096 = func() []zd.Decimal {
		xs := make([]zd.Decimal, len(productionAggregateSamePrecision4096))
		copy(xs, productionAggregateSamePrecision4096)
		xs[len(xs)-1] = zd.MustNew(1000000, -3)
		return xs
	}()
	productionAggregateCases = []struct {
		name  string
		xs    []zd.Decimal
		round bool
	}{
		{name: "mixed_sign_scale", xs: productionAggregateMixed, round: true},
		{name: "cancellation", xs: productionAggregateCancellation, round: true},
		{name: "same_precision_2", xs: productionAggregateSamePrecision2},
		{name: "same_precision_10", xs: productionAggregateSamePrecision10},
		{name: "same_precision_4096", xs: productionAggregateSamePrecision4096},
		{name: "late_mismatch_4096", xs: productionAggregateLateMismatch4096},
	}

	productionQuoRemDividend = zd.RequireFromString("0.0000000000000000001")
	productionQuoRemDivisor  = zd.RequireFromString(productionMax)
	productionQuoRemOrdinary = zd.RequireFromString("9876543210.123456789")
	productionQuoRemUnit     = zd.RequireFromString("3.25")

	productionJSONCases = []struct {
		name string
		data []byte
	}{
		{name: "bare_plain", data: []byte(`1234.5678`)},
		{name: "quoted_plain", data: []byte(`"1234.5678"`)},
		{name: "quoted_escaped_point", data: []byte(`"1234\u002e5678"`)},
		{name: "quoted_all_escaped", data: []byte(`"\u0031\u0032\u0033\u0034\u002e\u0035\u0036\u0037\u0038"`)},
	}
	productionJSONNull = []byte("null")

	productionStrictSources = []struct {
		name string
		src  any
	}{
		{name: "string", src: "12345678901234567890.1234567890123456789"},
		{name: "bytes", src: []byte("-9876543210.0000000000000000001")},
		{name: "int64", src: int64(-9223372036854775808)},
		{name: "uint64", src: uint64(18446744073709551615)},
		{name: "float64_rejected", src: float64(0.5)},
		{name: "null_rejected", src: nil},
	}

	productionFixedCases = []struct {
		name   string
		d      zd.Decimal
		places uint8
	}{
		{name: "currency_2", d: zd.RequireFromString("1234567890.1"), places: 2},
		{name: "max_precision_19", d: zd.RequireFromString("12345678901234567890.1234567890123456789"), places: 19},
		{name: "wide_48", d: zd.RequireFromString(productionMax), places: 48},
		{name: "maximum_255", d: zd.RequireFromString(productionMax), places: 255},
	}

	productionCacheEligible = zd.RequireFromString("999.99")
	productionCacheOutside  = zd.RequireFromString("1234.5678")

	productionErrorParseInvalid  = "not-a-number"
	productionErrorParseOverflow = "340282366920938463463374607431768211456"
	productionErrorParsePrec     = "0.00000000000000000001"
	productionErrorMulA          = zd.RequireFromString("0.0000000000000000001")
	productionErrorMulB          = zd.RequireFromString("1.1")
	productionDivInexactB        = zd.NewFromInt(3)

	productionTradePriceJSON     = []byte(`"1234\u002e5678"`)
	productionTradeQuantity  any = int64(250)
	productionTradeFeeRate       = zd.RequireFromString("0.00125")

	productionPositions = [...]struct {
		quantity zd.Decimal
		price    zd.Decimal
	}{
		{quantity: zd.RequireFromString("1250"), price: zd.RequireFromString("101.125")},
		{quantity: zd.RequireFromString("-750.5"), price: zd.RequireFromString("99.875")},
		{quantity: zd.RequireFromString("0.000000001"), price: zd.RequireFromString("123456789.987654321")},
		{quantity: zd.RequireFromString("2500000"), price: zd.RequireFromString("0.00004125")},
		{quantity: zd.RequireFromString("-17"), price: zd.RequireFromString("8765.4321")},
		{quantity: zd.RequireFromString("999.99"), price: zd.RequireFromString("1.00005")},
		{quantity: zd.RequireFromString("3.141592653"), price: zd.RequireFromString("2718.281828")},
		{quantity: zd.RequireFromString("-0.125"), price: zd.RequireFromString("340282366920938463.4")},
	}
)

// Package sinks make every sequential benchmark result observable. Parallel
// leaves use goroutine-local sinks plus runtime.KeepAlive to avoid races and
// false-sharing that belong to the harness rather than the decimal operation.
var (
	productionDecimalSink  zd.Decimal
	productionDecimalSink2 zd.Decimal
	productionStrictSink   zd.StrictSQLDecimal
	productionStringSink   string
	productionValueSink    driver.Value
	errProductionSink      [12]error
	errProductionTradeSink [5]error
	errProductionBookSink  [10]error
)

func productionRequireSuccess(b *testing.B, err error) {
	b.Helper()
	if err != nil {
		b.Fatalf("invalid production benchmark fixture: %v", err)
	}
}

func productionRequireError(b *testing.B, err, want error) {
	b.Helper()
	if !errors.Is(err, want) {
		b.Fatalf("invalid error-path fixture: got %v, want %v", err, want)
	}
}

// BenchmarkProductionMicroParse isolates ordinary, scientific, and
// canonical-rescue parsing. It deliberately does not include pipeline work.
func BenchmarkProductionMicroParse(b *testing.B) {
	for _, tc := range productionParseCases {
		b.Run(tc.name+"/string", func(b *testing.B) {
			_, err := zd.NewFromString(tc.text)
			productionRequireSuccess(b, err)
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.text)))
			b.ResetTimer()
			for b.Loop() {
				productionDecimalSink, errProductionSink[0] = zd.NewFromString(tc.text)
			}
		})

		b.Run(tc.name+"/bytes", func(b *testing.B) {
			_, err := zd.ParseBytes(tc.data)
			productionRequireSuccess(b, err)
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.data)))
			b.ResetTimer()
			for b.Loop() {
				productionDecimalSink, errProductionSink[0] = zd.ParseBytes(tc.data)
			}
		})
	}
}

// BenchmarkProductionMicroArithmetic separates exact arithmetic from direct
// rounding; none of these rows format, parse, or aggregate.
func BenchmarkProductionMicroArithmetic(b *testing.B) {
	b.Run("MulExact/terminating", func(b *testing.B) {
		_, err := productionMulExactA.MulExact(productionMulExactB)
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, errProductionSink[0] = productionMulExactA.MulExact(productionMulExactB)
		}
	})

	b.Run("MulRound/nearest_even_8", func(b *testing.B) {
		_, err := productionMulRoundA.MulRound(productionMulRoundB, 8, zd.ToNearestEven)
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, errProductionSink[0] = productionMulRoundA.MulRound(productionMulRoundB, 8, zd.ToNearestEven)
		}
	})

	b.Run("DivExact/terminating", func(b *testing.B) {
		_, err := productionDivExactA.DivExact(productionDivExactB)
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, errProductionSink[0] = productionDivExactA.DivExact(productionDivExactB)
		}
	})

	b.Run("DivRound/sticky_nearest_even_2", func(b *testing.B) {
		_, err := productionDivRoundA.DivRound(productionDivRoundB, 2, zd.ToNearestEven)
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, errProductionSink[0] = productionDivRoundA.DivRound(productionDivRoundB, 2, zd.ToNearestEven)
		}
	})
}

func BenchmarkProductionMicroAggregates(b *testing.B) {
	for _, tc := range productionAggregateCases {
		b.Run("Sum/"+tc.name, func(b *testing.B) {
			_, err := zd.Sum(tc.xs[0], tc.xs[1:]...)
			productionRequireSuccess(b, err)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				productionDecimalSink, errProductionSink[0] = zd.Sum(tc.xs[0], tc.xs[1:]...)
			}
		})

		b.Run("Avg/"+tc.name, func(b *testing.B) {
			_, err := zd.Avg(tc.xs[0], tc.xs[1:]...)
			productionRequireSuccess(b, err)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				productionDecimalSink, errProductionSink[0] = zd.Avg(tc.xs[0], tc.xs[1:]...)
			}
		})

		if tc.round {
			b.Run("AvgRound/"+tc.name, func(b *testing.B) {
				_, err := zd.AvgRound(tc.xs[0], 8, zd.ToNearestEven, tc.xs[1:]...)
				productionRequireSuccess(b, err)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					productionDecimalSink, errProductionSink[0] = zd.AvgRound(tc.xs[0], 8, zd.ToNearestEven, tc.xs[1:]...)
				}
			})
		}
	}
}

func BenchmarkProductionMicroQuoRem(b *testing.B) {
	b.Run("ordinary", func(b *testing.B) {
		_, _, err := productionQuoRemOrdinary.QuoRem(productionQuoRemUnit)
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, productionDecimalSink2, errProductionSink[0] = productionQuoRemOrdinary.QuoRem(productionQuoRemUnit)
		}
	})

	b.Run("scaled_divisor_wider_than_u128", func(b *testing.B) {
		_, _, err := productionQuoRemDividend.QuoRem(productionQuoRemDivisor)
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, productionDecimalSink2, errProductionSink[0] = productionQuoRemDividend.QuoRem(productionQuoRemDivisor)
		}
	})
}

func BenchmarkProductionMicroJSON(b *testing.B) {
	for _, tc := range productionJSONCases {
		b.Run(tc.name, func(b *testing.B) {
			var d zd.Decimal
			productionRequireSuccess(b, d.UnmarshalJSON(tc.data))
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.data)))
			b.ResetTimer()
			for b.Loop() {
				errProductionSink[0] = productionDecimalSink.UnmarshalJSON(tc.data)
			}
		})
	}

	b.Run("null_rejected", func(b *testing.B) {
		d := zd.NewFromInt(1)
		productionRequireError(b, d.UnmarshalJSON(productionJSONNull), zd.ErrJSONNull)
		b.ReportAllocs()
		b.SetBytes(int64(len(productionJSONNull)))
		b.ResetTimer()
		for b.Loop() {
			errProductionSink[0] = productionDecimalSink.UnmarshalJSON(productionJSONNull)
		}
	})
}

func BenchmarkProductionMicroStrictSQL(b *testing.B) {
	for _, tc := range productionStrictSources {
		b.Run(tc.name, func(b *testing.B) {
			var d zd.StrictSQLDecimal
			err := d.Scan(tc.src)
			switch tc.name {
			case "float64_rejected":
				productionRequireError(b, err, zd.ErrScanFloat)
			case "null_rejected":
				productionRequireError(b, err, zd.ErrScanNil)
			default:
				productionRequireSuccess(b, err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				errProductionSink[0] = productionStrictSink.Scan(tc.src)
			}
		})
	}
}

func BenchmarkProductionMicroStringFixed(b *testing.B) {
	for _, tc := range productionFixedCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				productionStringSink = tc.d.StringFixed(tc.places)
			}
		})
	}
}

// BenchmarkProductionMicroCache uses one cache-eligible and one ineligible
// value. Run it both untagged and with zerodecimal_strcache; the benchmark
// names describe eligibility, not whether the current binary contains cache.
func BenchmarkProductionMicroCache(b *testing.B) {
	b.Run("String/eligible_window", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			productionStringSink = productionCacheEligible.String()
		}
	})
	b.Run("String/outside_window", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			productionStringSink = productionCacheOutside.String()
		}
	})
	b.Run("Value/eligible_window", func(b *testing.B) {
		_, err := productionCacheEligible.Value()
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionValueSink, errProductionSink[0] = productionCacheEligible.Value()
		}
	})
	b.Run("Value/outside_window", func(b *testing.B) {
		_, err := productionCacheOutside.Value()
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionValueSink, errProductionSink[0] = productionCacheOutside.Value()
		}
	})
}

func BenchmarkProductionMicroErrors(b *testing.B) {
	b.Run("Parse/invalid", func(b *testing.B) {
		_, err := zd.NewFromString(productionErrorParseInvalid)
		productionRequireError(b, err, zd.ErrInvalidFormat)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, errProductionSink[0] = zd.NewFromString(productionErrorParseInvalid)
		}
	})
	b.Run("Parse/overflow", func(b *testing.B) {
		_, err := zd.NewFromString(productionErrorParseOverflow)
		productionRequireError(b, err, zd.ErrOverflow)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, errProductionSink[0] = zd.NewFromString(productionErrorParseOverflow)
		}
	})
	b.Run("Parse/precision", func(b *testing.B) {
		_, err := zd.NewFromString(productionErrorParsePrec)
		productionRequireError(b, err, zd.ErrPrecOutOfRange)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, errProductionSink[0] = zd.NewFromString(productionErrorParsePrec)
		}
	})
	b.Run("MulExact/inexact", func(b *testing.B) {
		_, err := productionErrorMulA.MulExact(productionErrorMulB)
		productionRequireError(b, err, zd.ErrInexact)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, errProductionSink[0] = productionErrorMulA.MulExact(productionErrorMulB)
		}
	})
	b.Run("DivExact/inexact", func(b *testing.B) {
		_, err := productionDivExactA.DivExact(productionDivInexactB)
		productionRequireError(b, err, zd.ErrInexact)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, errProductionSink[0] = productionDivExactA.DivExact(productionDivInexactB)
		}
	})
	b.Run("DivRound/divide_by_zero", func(b *testing.B) {
		_, err := productionDivExactA.DivRound(productionZero, 2, zd.ToNearestEven)
		productionRequireError(b, err, zd.ErrDivideByZero)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			productionDecimalSink, errProductionSink[0] = productionDivExactA.DivRound(productionZero, 2, zd.ToNearestEven)
		}
	})
}

// productionTradeCapture composes escaped JSON ingestion, strict integer SQL
// ingestion, direct monetary rounding, aggregation, and currency formatting.
func productionTradeCapture() (zd.Decimal, string, [5]error) {
	var errs [5]error
	var price zd.Decimal
	errs[0] = price.UnmarshalJSON(productionTradePriceJSON)
	var quantity zd.StrictSQLDecimal
	errs[1] = quantity.Scan(productionTradeQuantity)
	gross, err := price.MulRound(quantity.Decimal, 2, zd.ToNearestEven)
	errs[2] = err
	fee, err := gross.MulRound(productionTradeFeeRate, 2, zd.ToNearestEven)
	errs[3] = err
	total, err := zd.Sum(gross, fee)
	errs[4] = err
	return total, total.StringFixed(2), errs
}

func productionPortfolioMark() (zd.Decimal, zd.Decimal, string, [10]error) {
	var legs [len(productionPositions)]zd.Decimal
	var errs [10]error
	for i, position := range productionPositions {
		legs[i], errs[i] = position.quantity.MulRound(position.price, 2, zd.ToNearestEven)
	}
	total, err := zd.Sum(legs[0], legs[1:]...)
	errs[8] = err
	average, err := zd.AvgRound(legs[0], 2, zd.ToNearestEven, legs[1:]...)
	errs[9] = err
	return total, average, total.StringFixed(2), errs
}

// BenchmarkProductionPipelineTradeCapture is intentionally end-to-end. Its
// StringFixed result allocation is part of the workflow, unlike the micro
// rows that isolate each component.
func BenchmarkProductionPipelineTradeCapture(b *testing.B) {
	_, _, errs := productionTradeCapture()
	for _, err := range errs {
		productionRequireSuccess(b, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		productionDecimalSink, productionStringSink, errProductionTradeSink = productionTradeCapture()
	}
}

// BenchmarkProductionPipelinePortfolioMark represents a pre-parsed hot book:
// eight mixed-sign positions are directly rounded, summed, averaged, and
// formatted. Parsing and database work are deliberately absent.
func BenchmarkProductionPipelinePortfolioMark(b *testing.B) {
	_, _, _, errs := productionPortfolioMark()
	for _, err := range errs {
		productionRequireSuccess(b, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		productionDecimalSink, productionDecimalSink2, productionStringSink, errProductionBookSink = productionPortfolioMark()
	}
}

// BenchmarkProductionParallel exercises immutable operands through
// testing.RunParallel. Collect with multiple -cpu values; each worker keeps
// its own sinks so the rows measure library scaling, not a harness data race.
func BenchmarkProductionParallel(b *testing.B) {
	b.Run("Parse/canonical", func(b *testing.B) {
		text := productionParseCases[0].text
		_, err := zd.NewFromString(text)
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			var d zd.Decimal
			var err error
			for pb.Next() {
				d, err = zd.NewFromString(text)
			}
			runtime.KeepAlive(d)
			runtime.KeepAlive(err)
		})
	})

	b.Run("MulRound/nearest_even", func(b *testing.B) {
		_, err := productionMulRoundA.MulRound(productionMulRoundB, 8, zd.ToNearestEven)
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			var d zd.Decimal
			var err error
			for pb.Next() {
				d, err = productionMulRoundA.MulRound(productionMulRoundB, 8, zd.ToNearestEven)
			}
			runtime.KeepAlive(d)
			runtime.KeepAlive(err)
		})
	})

	b.Run("DivRound/sticky_nearest_even", func(b *testing.B) {
		_, err := productionDivRoundA.DivRound(productionDivRoundB, 2, zd.ToNearestEven)
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			var d zd.Decimal
			var err error
			for pb.Next() {
				d, err = productionDivRoundA.DivRound(productionDivRoundB, 2, zd.ToNearestEven)
			}
			runtime.KeepAlive(d)
			runtime.KeepAlive(err)
		})
	})

	b.Run("Sum/cancellation", func(b *testing.B) {
		xs := productionAggregateCancellation
		_, err := zd.Sum(xs[0], xs[1:]...)
		productionRequireSuccess(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			var d zd.Decimal
			var err error
			for pb.Next() {
				d, err = zd.Sum(xs[0], xs[1:]...)
			}
			runtime.KeepAlive(d)
			runtime.KeepAlive(err)
		})
	})

	b.Run("StrictSQL/string", func(b *testing.B) {
		src := productionStrictSources[0].src
		var preflight zd.StrictSQLDecimal
		productionRequireSuccess(b, preflight.Scan(src))
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			var d zd.StrictSQLDecimal
			var err error
			for pb.Next() {
				err = d.Scan(src)
			}
			runtime.KeepAlive(d)
			runtime.KeepAlive(err)
		})
	})

	b.Run("String/cache_eligible", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			var s string
			for pb.Next() {
				s = productionCacheEligible.String()
			}
			runtime.KeepAlive(s)
		})
	})
}

// TestProductionBenchmarkFixtures prevents -run-enabled development runs from
// accepting a benchmark that silently measures an unintended error path.
func TestProductionBenchmarkFixtures(t *testing.T) {
	requireSuccess := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	requireError := func(name string, err, want error) {
		t.Helper()
		if !errors.Is(err, want) {
			t.Fatalf("%s: got %v, want %v", name, err, want)
		}
	}

	for _, tc := range productionParseCases {
		_, err := zd.NewFromString(tc.text)
		requireSuccess("parse/string/"+tc.name, err)
		_, err = zd.ParseBytes(tc.data)
		requireSuccess("parse/bytes/"+tc.name, err)
	}

	_, err := productionMulExactA.MulExact(productionMulExactB)
	requireSuccess("MulExact", err)
	_, err = productionMulRoundA.MulRound(productionMulRoundB, 8, zd.ToNearestEven)
	requireSuccess("MulRound", err)
	_, err = productionDivExactA.DivExact(productionDivExactB)
	requireSuccess("DivExact", err)
	rounded, err := productionDivRoundA.DivRound(productionDivRoundB, 2, zd.ToNearestEven)
	requireSuccess("DivRound", err)
	if !rounded.Equal(zd.RequireFromString("0.01")) {
		t.Fatalf("DivRound sticky fixture: got %s, want 0.01", rounded)
	}

	for _, tc := range productionAggregateCases {
		_, err = zd.Sum(tc.xs[0], tc.xs[1:]...)
		requireSuccess("Sum/"+tc.name, err)
		_, err = zd.Avg(tc.xs[0], tc.xs[1:]...)
		requireSuccess("Avg/"+tc.name, err)
		if tc.round {
			_, err = zd.AvgRound(tc.xs[0], 8, zd.ToNearestEven, tc.xs[1:]...)
			requireSuccess("AvgRound/"+tc.name, err)
		}
	}

	q, r, err := productionQuoRemDividend.QuoRem(productionQuoRemDivisor)
	if err != nil || !q.IsZero() || !r.Equal(productionQuoRemDividend) {
		t.Fatalf("wide QuoRem fixture: q=%s r=%s err=%v", q, r, err)
	}
	_, _, err = productionQuoRemOrdinary.QuoRem(productionQuoRemUnit)
	requireSuccess("ordinary QuoRem", err)

	for _, tc := range productionJSONCases {
		var d zd.Decimal
		requireSuccess("JSON/"+tc.name, d.UnmarshalJSON(tc.data))
	}
	d := zd.NewFromInt(7)
	requireError("JSON/null", d.UnmarshalJSON(productionJSONNull), zd.ErrJSONNull)

	for _, tc := range productionStrictSources {
		var strict zd.StrictSQLDecimal
		err = strict.Scan(tc.src)
		switch tc.name {
		case "float64_rejected":
			requireError("StrictSQL/float64", err, zd.ErrScanFloat)
		case "null_rejected":
			requireError("StrictSQL/null", err, zd.ErrScanNil)
		default:
			requireSuccess("StrictSQL/"+tc.name, err)
		}
	}

	_, err = zd.NewFromString(productionErrorParseInvalid)
	requireError("error/parse_invalid", err, zd.ErrInvalidFormat)
	_, err = zd.NewFromString(productionErrorParseOverflow)
	requireError("error/parse_overflow", err, zd.ErrOverflow)
	_, err = zd.NewFromString(productionErrorParsePrec)
	requireError("error/parse_precision", err, zd.ErrPrecOutOfRange)
	_, err = productionErrorMulA.MulExact(productionErrorMulB)
	requireError("error/mul_inexact", err, zd.ErrInexact)
	_, err = productionDivExactA.DivExact(productionDivInexactB)
	requireError("error/div_inexact", err, zd.ErrInexact)
	_, err = productionDivExactA.DivRound(productionZero, 2, zd.ToNearestEven)
	requireError("error/divide_by_zero", err, zd.ErrDivideByZero)

	tradeTotal, tradeWire, tradeErrs := productionTradeCapture()
	for i, err := range tradeErrs {
		if err != nil {
			t.Fatalf("trade pipeline step %d: %v", i, err)
		}
	}
	if !tradeTotal.Equal(zd.RequireFromString("309027.75")) || tradeWire != "309027.75" {
		t.Fatalf("trade pipeline result: total=%s wire=%q", tradeTotal, tradeWire)
	}
	bookTotal, bookAverage, bookWire, portfolioErrs := productionPortfolioMark()
	for i, err := range portfolioErrs {
		if err != nil {
			t.Fatalf("portfolio pipeline step %d: %v", i, err)
		}
	}
	wantBookTotal := zd.RequireFromString("-42535295865205227.20")
	wantBookAverage := zd.RequireFromString("-5316911983150653.40")
	if !bookTotal.Equal(wantBookTotal) || !bookAverage.Equal(wantBookAverage) || bookWire != "-42535295865205227.20" {
		t.Fatalf("portfolio pipeline result: total=%s average=%s wire=%q", bookTotal, bookAverage, bookWire)
	}
}
