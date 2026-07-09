//go:build fuzz

package zerodecimal

import (
	"testing"
	"unsafe"
)

// FuzzStringFixedFullUint8 checks every mutated places byte against an
// independent string-surgery oracle and verifies AppendFixed's exact-capacity
// no-growth contract, including an existing destination prefix.
func FuzzStringFixedFullUint8(f *testing.F) {
	f.Add(false, uint64(0), uint64(0), uint8(0), uint8(0))
	f.Add(true, maxUint64, maxUint64, MaxPrec, uint8(19))
	f.Add(false, maxUint64, maxUint64, uint8(0), uint8(47))
	f.Add(true, uint64(0), uint64(15), uint8(1), uint8(48))
	f.Add(false, maxUint64, maxUint64, MaxPrec, uint8(100))
	f.Add(false, maxUint64, maxUint64, uint8(0), uint8(255))

	f.Fuzz(func(t *testing.T, neg bool, hi, lo uint64, rawPrec, places uint8) {
		d := newDecimal(u128{hi: hi, lo: lo}, neg, rawPrec%(MaxPrec+1))
		want := fixedOracle(d, places)
		if got := d.StringFixed(places); got != want {
			t.Fatalf("StringFixed(%d) = %q, want %q for %+v", places, got, want, d)
		}

		const prefix = "value="
		dst := make([]byte, len(prefix), len(prefix)+len(want))
		copy(dst, prefix)
		data := unsafe.SliceData(dst)
		got := d.AppendFixed(dst, places)
		if unsafe.SliceData(got) != data {
			t.Fatalf("AppendFixed grew exact-capacity destination for %+v at %d places", d, places)
		}
		if string(got) != prefix+want {
			t.Fatalf("AppendFixed(%d) = %q, want %q for %+v", places, got, prefix+want, d)
		}
	})
}

// FuzzStringCacheMode checks String, AppendText, cachedString, and
// cachedValue against an independent canonical oracle in both default-off and
// opt-in cache builds. The hit predicate is reconstructed without cacheIndex
// so the property also compiles when the cache implementation is absent.
func FuzzStringCacheMode(f *testing.F) {
	f.Add(false, uint64(0), uint64(0), uint8(0))
	f.Add(false, uint64(0), uint64(100000), uint8(2))
	f.Add(true, uint64(0), uint64(100000), uint8(2))
	f.Add(false, uint64(0), uint64(100001), uint8(2))
	f.Add(false, uint64(1), uint64(0), uint8(2))
	f.Add(false, uint64(0), uint64(15), uint8(3))

	f.Fuzz(func(t *testing.T, neg bool, hi, lo uint64, rawPrec uint8) {
		d := newDecimal(u128{hi: hi, lo: lo}, neg, rawPrec%(MaxPrec+1))
		want := canonicalOracle(d.neg, d.coef, d.prec)
		if got := d.String(); got != want {
			t.Fatalf("String() = %q, want %q for %+v", got, want, d)
		}

		const prefix = "decimal="
		dst := make([]byte, len(prefix), len(prefix)+len(want))
		copy(dst, prefix)
		data := unsafe.SliceData(dst)
		got, err := d.AppendText(dst)
		if err != nil {
			t.Fatalf("AppendText: %v", err)
		}
		if unsafe.SliceData(got) != data || string(got) != prefix+want {
			t.Fatalf("AppendText = %q, want %q without growth for %+v", got, prefix+want, d)
		}

		expectHit := false
		if strCacheEnabled && d.coef.hi == 0 && d.prec <= 2 && d.coef.lo <= cacheSpan {
			scaled := d.coef.lo * pow10u64[2-d.prec]
			expectHit = scaled <= cacheSpan
		}
		cached, hit := cachedString(d)
		if hit != expectHit {
			t.Fatalf("cachedString hit = %t, want %t for %+v", hit, expectHit, d)
		}
		if hit && cached != want {
			t.Fatalf("cachedString = %q, want %q for %+v", cached, want, d)
		}
		value, valueHit := cachedValue(d)
		if valueHit != expectHit {
			t.Fatalf("cachedValue hit = %t, want %t for %+v", valueHit, expectHit, d)
		}
		if valueHit && value != want {
			t.Fatalf("cachedValue = %v, want %q for %+v", value, want, d)
		}
	})
}
