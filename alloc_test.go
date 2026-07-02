package zerodecimal

// Zero-allocation enforcement gate. Every operation the package advertises
// as allocation-free is measured with testing.AllocsPerRun over a matrix of
// value shapes, and the gate fails on the first stray allocation — error
// paths included, since the sentinels in errors.go must reach callers
// without a byte of garbage.
//
// Measuring allocations honestly requires defeating the optimizer without
// perturbing the measurement; the rules this file follows are load-bearing:
//
//   - Inputs are PACKAGE-LEVEL variables (allocShapes), parsed once at init.
//     Local literals could be constant-folded or have the operation under
//     test partially evaluated at compile time, measuring nothing.
//   - Every result is stored to a PACKAGE-LEVEL sink. Discarding a result
//     lets dead-code elimination delete the call under test.
//   - Measured closures contain the operation and the sink stores, nothing
//     else: no fmt, no logging, and no interface conversions — boxing a
//     Decimal into any/error inside the closure would charge the gate with
//     the harness's own allocations. errSink only ever receives the
//     ready-made sentinel values, which is a pointer copy, not a boxing
//     allocation.
//   - Closures are built OUTSIDE testing.AllocsPerRun (the closure object
//     and its captures are allocated once, before measurement starts).
//   - Under -race every count is skipped via the raceEnabled build-tag
//     constant: the race runtime allocates shadow state on instrumented
//     accesses, making the numbers meaningless (see race_enabled_test.go).

import (
	"database/sql/driver"
	"testing"
	"time"
)

// allocRuns is the invocation count handed to testing.AllocsPerRun for every
// gate; AllocsPerRun additionally performs one warm-up call, so lazily
// initialized runtime state never pollutes the measured runs.
const allocRuns = 500

// Package-level sinks. Every measured closure stores its results here so the
// compiler can prove neither the calls nor their results dead.
var (
	sinkDecimal  Decimal
	sinkDecimal2 Decimal
	errSink      error
	sinkBool     bool
	sinkInt      int
	sinkInt64    int64
	sinkFloat64  float64
	sinkString   string
	sinkBytes    []byte
	sinkValue    driver.Value
)

// Error-path and NullDecimal fixtures, pre-boxed once so the measured
// closures load package state only (boxing into any allocates; see aStrAny).
var (
	sinkNull NullDecimal

	allocInvalidStr   = "not-a-number"
	allocInvalidBytes = []byte("1..2")
	allocPrecStr      = "0.00000000000000000001" // > MaxPrec fractional digits
	allocValidStr     = "1.5"
	allocValidBytes   = []byte("1.5")
	allocInvalidJSON  = []byte(`"abc"`)
	allocInvalidBin   = []byte{0x00, 0x00, 0x00} // wrong length → ErrInvalidBinaryData
	allocInvalidText  = []byte("abc")

	allocScanBadStr   any = "not-a-number"
	allocScanBadBytes any = []byte("1..2")
	allocScanValidStr any = "1.5"
	allocScanBool     any = true
	allocScanTime     any = time.Unix(0, 0)
	allocScanUnknown  any = struct{}{} // legal Go value, not a driver.Value
	allocScanNil      any

	allocNullValid = NewNullDecimal(RequireFromString("1.5")) // 1.5 is in the cache window
)

// allocSinkBuf is the pre-allocated destination for the Append* gates: each
// append runs into allocSinkBuf[:0], and the capacity exceeds the widest
// possible rendering, so a correct implementation can never grow the slice.
var allocSinkBuf = make([]byte, 0, 128)

// allocShape is one row of the input matrix: a pair of values in every input
// form an operation under the gate consumes (string, bytes, float64, parsed
// Decimal), precomputed so the measured closures only load package state.
type allocShape struct {
	name   string
	aStr   string
	bStr   string
	aBytes []byte
	bBytes []byte
	aFloat float64
	bFloat float64
	a, b   Decimal
	// Codec inputs: the JSON and binary encodings of a, and a/aBytes pre-boxed
	// as any — boxing a string or slice into an interface allocates, and doing
	// it here keeps that harness cost out of the measured Scan closures.
	aJSON     []byte
	aBin      []byte
	aStrAny   any
	aBytesAny any
}

// newAllocShape parses one matrix row up front. Precomputing every input form
// here keeps conversions like []byte(s) out of the measured closures and
// pins all inputs in package variables.
func newAllocShape(name, aStr, bStr string) allocShape {
	a := RequireFromString(aStr)
	b := RequireFromString(bStr)
	aJSON, _ := a.MarshalJSON()
	aBin, _ := a.MarshalBinary()
	aBytes := []byte(aStr)
	return allocShape{
		name:      name,
		aStr:      aStr,
		bStr:      bStr,
		aBytes:    aBytes,
		bBytes:    []byte(bStr),
		aFloat:    a.InexactFloat64(),
		bFloat:    b.InexactFloat64(),
		a:         a,
		b:         b,
		aJSON:     aJSON,
		aBin:      aBin,
		aStrAny:   aStr,
		aBytesAny: aBytes,
	}
}

// allocShapes is the shape matrix every gate runs over: one-limb integers,
// realistic prices, full 19-digit precision, extreme precision mismatch,
// coefficients near the 2^128 ceiling, and negatives.
var allocShapes = []allocShape{
	newAllocShape("small_int", "5", "7"),
	newAllocShape("typical_price", "1234.5678", "8765.4321"),
	newAllocShape("max_prec", "0.1234567890123456789", "0.9876543210987654321"),
	newAllocShape("mixed_prec", "12345678901234567890.12345", "0.0000000000000000001"),
	newAllocShape("near_max", "17014118346046923173.1687303715884105727", "1.0000000000000000001"),
	newAllocShape("negative", "-1234.5678", "-0.0000000000000000001"),
}

// requireAllocs fails the test unless fn averages exactly want allocations
// per run. It is deliberately testify-free: the gate's own assertion path
// must stay trivially allocation-transparent, and an exact comparison keeps
// the contract sharp — "at most one" would let regressions hide.
func requireAllocs(t *testing.T, want float64, fn func()) {
	t.Helper()
	if got := testing.AllocsPerRun(allocRuns, fn); got != want {
		t.Fatalf("allocations per run: got %v, want exactly %v", got, want)
	}
}

// allocOps binds every zero-allocation operation to a shape outside the
// measured closure, so the closure body is the operation plus sink stores
// and nothing else. Fallible operations store the error too; shapes for
// which an operation legitimately fails (IntPart on mixed_prec and near_max
// overflows int64) are measured all the same — the error path is under the
// exact same zero-allocation contract as success.
var allocOps = []struct {
	name string
	bind func(s allocShape) func()
}{
	{name: "new_from_string", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal, errSink = NewFromString(s.aStr)
			sinkDecimal2, errSink = NewFromString(s.bStr)
		}
	}},
	{name: "parse_bytes", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal, errSink = ParseBytes(s.aBytes)
			sinkDecimal2, errSink = ParseBytes(s.bBytes)
		}
	}},
	{name: "add", bind: func(s allocShape) func() {
		return func() { sinkDecimal, errSink = s.a.Add(s.b) }
	}},
	{name: "sub", bind: func(s allocShape) func() {
		return func() { sinkDecimal, errSink = s.a.Sub(s.b) }
	}},
	{name: "mul", bind: func(s allocShape) func() {
		return func() { sinkDecimal, errSink = s.a.Mul(s.b) }
	}},
	{name: "div", bind: func(s allocShape) func() {
		return func() { sinkDecimal, errSink = s.a.Div(s.b) }
	}},
	{name: "quo_rem", bind: func(s allocShape) func() {
		return func() { sinkDecimal, sinkDecimal2, errSink = s.a.QuoRem(s.b) }
	}},
	{name: "mod", bind: func(s allocShape) func() {
		return func() { sinkDecimal, errSink = s.a.Mod(s.b) }
	}},
	{name: "cmp", bind: func(s allocShape) func() {
		return func() { sinkInt = s.a.Cmp(s.b) }
	}},
	{name: "equal", bind: func(s allocShape) func() {
		return func() { sinkBool = s.a.Equal(s.b) }
	}},
	{name: "neg", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.Neg()
			sinkDecimal2 = s.b.Neg()
		}
	}},
	{name: "abs", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.Abs()
			sinkDecimal2 = s.b.Abs()
		}
	}},
	{name: "sign", bind: func(s allocShape) func() {
		return func() {
			sinkInt = s.a.Sign()
			sinkInt = s.b.Sign()
		}
	}},
	{name: "round", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.Round(2)
			sinkDecimal2 = s.b.Round(2)
		}
	}},
	{name: "round_bank", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.RoundBank(2)
			sinkDecimal2 = s.b.RoundBank(2)
		}
	}},
	{name: "round_up", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.RoundUp(2)
			sinkDecimal2 = s.b.RoundUp(2)
		}
	}},
	{name: "round_down", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.RoundDown(2)
			sinkDecimal2 = s.b.RoundDown(2)
		}
	}},
	{name: "round_ceil", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.RoundCeil(2)
			sinkDecimal2 = s.b.RoundCeil(2)
		}
	}},
	{name: "round_floor", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.RoundFloor(2)
			sinkDecimal2 = s.b.RoundFloor(2)
		}
	}},
	{name: "truncate", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.Truncate(2)
			sinkDecimal2 = s.b.Truncate(2)
		}
	}},
	{name: "floor", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.Floor()
			sinkDecimal2 = s.b.Floor()
		}
	}},
	{name: "ceil", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.Ceil()
			sinkDecimal2 = s.b.Ceil()
		}
	}},
	{name: "int_part", bind: func(s allocShape) func() {
		return func() {
			sinkInt64, errSink = s.a.IntPart()
			sinkInt64, errSink = s.b.IntPart()
		}
	}},
	{name: "inexact_float64", bind: func(s allocShape) func() {
		return func() {
			sinkFloat64 = s.a.InexactFloat64()
			sinkFloat64 = s.b.InexactFloat64()
		}
	}},
	{name: "new_from_float", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal, errSink = NewFromFloat(s.aFloat)
			sinkDecimal2, errSink = NewFromFloat(s.bFloat)
		}
	}},
	{name: "append_text", bind: func(s allocShape) func() {
		return func() {
			sinkBytes, errSink = s.a.AppendText(allocSinkBuf[:0])
			sinkBytes, errSink = s.b.AppendText(allocSinkBuf[:0])
		}
	}},
	{name: "append_fixed", bind: func(s allocShape) func() {
		return func() {
			sinkBytes = s.a.AppendFixed(allocSinkBuf[:0], 4)
			sinkBytes = s.b.AppendFixed(allocSinkBuf[:0], 4)
		}
	}},
	{name: "trim", bind: func(s allocShape) func() {
		return func() {
			sinkDecimal = s.a.Trim()
			sinkDecimal2 = s.b.Trim()
		}
	}},
	{name: "rescale", bind: func(s allocShape) func() {
		// Lowering and raising by shape (2 sits below and above the shapes'
		// precisions), plus the ErrPrecOutOfRange path at 20.
		return func() {
			sinkDecimal, errSink = s.a.Rescale(2)
			sinkDecimal2, errSink = s.b.Rescale(20)
		}
	}},
	{name: "min", bind: func(s allocShape) func() {
		return func() { sinkDecimal = Min(s.a, s.b) }
	}},
	{name: "max", bind: func(s allocShape) func() {
		return func() { sinkDecimal = Max(s.a, s.b) }
	}},
	{name: "must_add", bind: func(s allocShape) func() {
		// No shape pair overflows Add, so the panic twin is safe to gate.
		return func() { sinkDecimal = s.a.MustAdd(s.b) }
	}},
	{name: "append_binary", bind: func(s allocShape) func() {
		return func() {
			sinkBytes, errSink = s.a.AppendBinary(allocSinkBuf[:0])
			sinkBytes, errSink = s.b.AppendBinary(allocSinkBuf[:0])
		}
	}},
	{name: "unmarshal_text", bind: func(s allocShape) func() {
		return func() { errSink = sinkDecimal.UnmarshalText(s.aBytes) }
	}},
	{name: "unmarshal_json", bind: func(s allocShape) func() {
		return func() { errSink = sinkDecimal.UnmarshalJSON(s.aJSON) }
	}},
	{name: "unmarshal_binary", bind: func(s allocShape) func() {
		return func() { errSink = sinkDecimal.UnmarshalBinary(s.aBin) }
	}},
	{name: "scan_string", bind: func(s allocShape) func() {
		// s.aStrAny is the string pre-boxed as any (see allocShape): the
		// closure measures Scan alone, not the caller-side boxing.
		return func() { errSink = sinkDecimal.Scan(s.aStrAny) }
	}},
	{name: "scan_bytes", bind: func(s allocShape) func() {
		return func() { errSink = sinkDecimal.Scan(s.aBytesAny) }
	}},
}

// TestAllocsZero asserts that every operation in allocOps performs exactly
// zero heap allocations per call on every shape, success and error paths
// alike.
func TestAllocsZero(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, op := range allocOps {
		t.Run(op.name, func(t *testing.T) {
			for _, s := range allocShapes {
				t.Run(s.name, func(t *testing.T) {
					requireAllocs(t, 0, op.bind(s))
				})
			}
		})
	}
}

// allocErrOps binds the error and NullDecimal paths whose zero-allocation
// contract does not vary by value shape (rejections, Trunc parsers, invalid
// Unmarshal/Scan inputs, NullDecimal rows). doc.go promises these allocate
// nothing on the error path just as the success paths do.
var allocErrOps = []struct {
	name string
	fn   func()
}{
	{name: "new_from_string_invalid", fn: func() { sinkDecimal, errSink = NewFromString(allocInvalidStr) }},
	{name: "new_from_string_prec", fn: func() { sinkDecimal, errSink = NewFromString(allocPrecStr) }},
	{name: "new_from_string_trunc_ok", fn: func() { sinkDecimal, errSink = NewFromStringTrunc(allocValidStr) }},
	{name: "new_from_string_trunc_invalid", fn: func() { sinkDecimal, errSink = NewFromStringTrunc(allocInvalidStr) }},
	{name: "parse_bytes_trunc_ok", fn: func() { sinkDecimal, errSink = ParseBytesTrunc(allocValidBytes) }},
	{name: "parse_bytes_trunc_invalid", fn: func() { sinkDecimal, errSink = ParseBytesTrunc(allocInvalidBytes) }},
	{name: "unmarshal_text_invalid", fn: func() { errSink = sinkDecimal.UnmarshalText(allocInvalidText) }},
	{name: "unmarshal_json_invalid", fn: func() { errSink = sinkDecimal.UnmarshalJSON(allocInvalidJSON) }},
	{name: "unmarshal_binary_invalid", fn: func() { errSink = sinkDecimal.UnmarshalBinary(allocInvalidBin) }},
	{name: "scan_string_invalid", fn: func() { errSink = sinkDecimal.Scan(allocScanBadStr) }},
	{name: "scan_bytes_invalid", fn: func() { errSink = sinkDecimal.Scan(allocScanBadBytes) }},
	{name: "scan_bool", fn: func() { errSink = sinkDecimal.Scan(allocScanBool) }},
	{name: "scan_time", fn: func() { errSink = sinkDecimal.Scan(allocScanTime) }},
	{name: "scan_unknown", fn: func() { errSink = sinkDecimal.Scan(allocScanUnknown) }},
	{name: "scan_nil", fn: func() { errSink = sinkDecimal.Scan(allocScanNil) }},
	{name: "null_scan_valid", fn: func() { errSink = sinkNull.Scan(allocScanValidStr) }},
	{name: "null_scan_nil", fn: func() { errSink = sinkNull.Scan(allocScanNil) }},
	{name: "null_scan_invalid", fn: func() { errSink = sinkNull.Scan(allocScanBadStr) }},
	{name: "null_value_invalid", fn: func() { sinkValue, errSink = (NullDecimal{}).Value() }},
}

// TestAllocsErrorPaths asserts every rejection and NullDecimal path allocates
// exactly zero — the error-path half of doc.go's Scan/Unmarshal contract.
func TestAllocsErrorPaths(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, op := range allocErrOps {
		t.Run(op.name, func(t *testing.T) {
			requireAllocs(t, 0, op.fn)
		})
	}
}

// TestAllocsNullValueCached asserts NullDecimal.Value on a cache-window value
// forwards to Decimal.Value's zero-allocation cached path.
func TestAllocsNullValueCached(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	if !strCacheEnabled {
		t.Skip("string cache compiled out by zerodecimal_nostrcache")
	}
	requireAllocs(t, 0, func() { sinkValue, errSink = allocNullValid.Value() })
}

// TestAllocsOne asserts that String and StringFixed cost EXACTLY one
// allocation — the result string itself — never more, never zero (zero would
// mean a value escaped into the small-value cache and the case belongs in
// TestAllocsStringCached instead, hence the explicit skip).
func TestAllocsOne(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, s := range allocShapes {
		values := []struct {
			name string
			d    Decimal
		}{
			{name: s.name + "_a", d: s.a},
			{name: s.name + "_b", d: s.b},
		}
		for _, v := range values {
			t.Run("string/"+v.name, func(t *testing.T) {
				if _, ok := cachedString(v.d); ok {
					t.Skip("value served by the string cache: covered by TestAllocsStringCached")
				}
				if len(v.d.String()) == 1 {
					// The runtime serves 1-byte strings from a static table
					// (slicebytetostring), so single-digit values measure 0
					// allocations even without the cache.
					t.Skip("1-byte strings are interned by the runtime, not allocated")
				}
				requireAllocs(t, 1, func() { sinkString = v.d.String() })
			})
			t.Run("string_fixed/"+v.name, func(t *testing.T) {
				requireAllocs(t, 1, func() { sinkString = v.d.StringFixed(4) })
			})
		}
	}
}

// allocCachedValues are values inside the small-value cache window
// (-1000.00 .. +1000.00, prec ≤ 2) whose String must be served entirely from
// the precomputed cache.
var allocCachedValues = []struct {
	name string
	d    Decimal
}{
	{name: "one_point_five", d: RequireFromString("1.5")},
	{name: "negative_999_99", d: RequireFromString("-999.99")},
	{name: "zero", d: RequireFromString("0")},
	{name: "one_thousand", d: RequireFromString("1000")},
}

// TestAllocsStringCached asserts that String on cache-window values performs
// zero allocations: returning a precomputed immutable string copies nothing.
func TestAllocsStringCached(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	if !strCacheEnabled {
		t.Skip("string cache compiled out by zerodecimal_nostrcache")
	}
	for _, tc := range allocCachedValues {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, 0, func() { sinkString = tc.d.String() })
		})
	}
}

// allocMarshalOps are the marshalers that cost EXACTLY one allocation — the
// returned byte slice — on every shape; byte slices, unlike 1-byte strings,
// are never interned by the runtime, so no shape needs a skip.
var allocMarshalOps = []struct {
	name string
	bind func(s allocShape) func()
}{
	{name: "marshal_text", bind: func(s allocShape) func() {
		return func() { sinkBytes, errSink = s.a.MarshalText() }
	}},
	{name: "marshal_json", bind: func(s allocShape) func() {
		return func() { sinkBytes, errSink = s.a.MarshalJSON() }
	}},
	{name: "marshal_binary", bind: func(s allocShape) func() {
		return func() { sinkBytes, errSink = s.a.MarshalBinary() }
	}},
}

// TestAllocsCodecMarshal asserts MarshalText, MarshalJSON, and MarshalBinary
// each perform exactly one heap allocation per call on every shape.
func TestAllocsCodecMarshal(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	for _, op := range allocMarshalOps {
		t.Run(op.name, func(t *testing.T) {
			for _, s := range allocShapes {
				t.Run(s.name, func(t *testing.T) {
					requireAllocs(t, 1, op.bind(s))
				})
			}
		})
	}
}

// TestAllocsSQLValueCached asserts Value on cache-window values returns the
// pre-boxed driver.Value with zero allocations: the boxing was paid once in
// the cache's init.
func TestAllocsSQLValueCached(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	if !strCacheEnabled {
		t.Skip("string cache compiled out by zerodecimal_nostrcache")
	}
	for _, tc := range allocCachedValues {
		t.Run(tc.name, func(t *testing.T) {
			requireAllocs(t, 0, func() { sinkValue, errSink = tc.d.Value() })
		})
	}
}

// allocUncachedValue lies outside the small-value cache window in every
// build mode (prec 4 > the window's prec ≤ 2), so its Value always takes the
// uncached path.
var allocUncachedValue = RequireFromString("1234.5678")

// TestAllocsSQLValueUncached pins uncached Value at exactly two allocations:
// String allocates the canonical string, and returning it as a driver.Value
// boxes the string header into the interface (runtime.convTstring) — the
// bytes themselves are shared, not copied. There is no cheaper portable
// shape: a driver.Value must carry a concrete boxed type.
func TestAllocsSQLValueUncached(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	requireAllocs(t, 2, func() { sinkValue, errSink = allocUncachedValue.Value() })
}

// allocRescaleOverflow is a precision-0 maximal coefficient no raise can fit.
var allocRescaleOverflow = RequireFromString("340282366920938463463374607431768211455")

// TestAllocsRescaleOverflow pins Rescale's ErrOverflow path at zero
// allocations — the one sentinel the shape matrix cannot reach.
func TestAllocsRescaleOverflow(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation counts are unreliable under -race")
	}
	requireAllocs(t, 0, func() { sinkDecimal, errSink = allocRescaleOverflow.Rescale(1) })
}
