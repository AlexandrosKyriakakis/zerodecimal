// Command gen computes the reciprocal-division tables for package
// zerodecimal and writes them to tables.go in the package root.
//
// Every constant is derived with math/big and self-verified against its
// defining property before a single byte is written: a wrong magic constant
// must fail generation loudly, never ship silently. Run via the
// //go:generate directive in div10.go.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"math/big"
	"math/bits"
	"math/rand/v2"
	"os"
)

// maxPrec mirrors zerodecimal.MaxPrec: the largest k with 10^k < 2^64.
const maxPrec = 19

// maxPow10u128 is the largest k carried by pow10u128: 10^38 is the largest
// product of two in-range coefficients' scales and still fits 127 bits.
const maxPow10u128 = 38

// entry mirrors zerodecimal.pow10Entry minus the padding.
type entry struct {
	d, m, dn, v uint64
	p, s        uint8
}

func main() {
	entries := make([]entry, maxPrec+1)
	for k := 0; k <= maxPrec; k++ {
		entries[k] = computeEntry(k)
		verifyEntry(k, entries[k])
	}

	src, err := format.Source(render(entries))
	if err != nil {
		panic(fmt.Sprintf("gen: formatting generated source: %v", err))
	}
	if err := os.WriteFile("tables.go", src, 0o600); err != nil {
		panic(fmt.Sprintf("gen: writing tables.go: %v", err))
	}
}

// pow2 returns 2^n.
func pow2(n int) *big.Int {
	if n < 0 {
		panic(fmt.Sprintf("gen: pow2 of negative exponent %d", n))
	}
	return new(big.Int).Lsh(big.NewInt(1), uint(n))
}

// bigPow returns base^k.
func bigPow(base, k int) *big.Int {
	return new(big.Int).Exp(big.NewInt(int64(base)), big.NewInt(int64(k)), nil)
}

// toUint64 converts b to uint64, panicking if it does not fit — every table
// constant must fit one limb by construction.
func toUint64(what string, k int, b *big.Int) uint64 {
	if b.Sign() < 0 || b.BitLen() > 64 {
		panic(fmt.Sprintf("gen: k=%d: %s = %s does not fit uint64", k, what, b))
	}
	return b.Uint64()
}

// computeEntry derives every constant for divisor d = 10^k from first
// principles in arbitrary precision.
func computeEntry(k int) entry {
	d := toUint64("10^k", k, bigPow(10, k))

	// Möller-Granlund constants: normalize d so its top bit is set, then
	// v = ⌊(2^128-1)/dn⌋ - 2^64.
	//nolint:gosec // bits.LeadingZeros64 of a nonzero value returns 0..63
	s := uint8(bits.LeadingZeros64(d))
	dn := d << s
	vBig := new(big.Int).Sub(pow2(128), big.NewInt(1))
	vBig.Div(vBig, new(big.Int).SetUint64(dn))
	vBig.Sub(vBig, pow2(64))
	v := toUint64("reciprocal v", k, vBig)

	if k == 0 {
		// Division by 10^0 is short-circuited before any table lookup, so
		// the magic pair (m, p) is unused and left zero.
		return entry{d: d, m: 0, dn: dn, v: v, p: 0, s: s}
	}

	// Granlund-Montgomery-Warren magic for the odd factor 5^k: p is the
	// smallest integer with 5^k ≤ 2^(p+k); m = ⌈2^(64+p)/5^k⌉ then fits
	// uint64 and divides every pre-shifted dividend < 2^(64-k) exactly,
	// with no add-back fixup.
	five := bigPow(5, k)
	p := 0
	for pow2(p+k).Cmp(five) < 0 {
		p++
	}
	mBig := new(big.Int).Add(pow2(64+p), new(big.Int).Sub(five, big.NewInt(1)))
	mBig.Div(mBig, five)
	m := toUint64("magic m", k, mBig)

	return entry{d: d, m: m, dn: dn, v: v, p: uint8(p), s: s}
}

// verifyEntry cross-checks e against the defining properties of each
// constant, panicking on any mismatch. The magic path is exercised on the
// boundary dividends most likely to expose an off-by-one plus a deterministic
// random sample, all compared against big.Int division.
func verifyEntry(k int, e entry) {
	fail := func(format string, args ...any) {
		panic(fmt.Sprintf("gen: k=%d: ", k) + fmt.Sprintf(format, args...))
	}

	dBig := bigPow(10, k)
	if new(big.Int).SetUint64(e.d).Cmp(dBig) != 0 {
		fail("d = %d, want 10^k = %s", e.d, dBig)
	}
	//nolint:gosec // bits.LeadingZeros64 of a nonzero value returns 0..63
	if want := uint8(bits.LeadingZeros64(e.d)); e.s != want {
		fail("s = %d, want %d", e.s, want)
	}
	if want := e.d << e.s; e.dn != want || e.dn>>63 != 1 {
		fail("dn = %#x, want normalized %#x", e.dn, want)
	}
	// v definition check, plus the identity div2by1 callers rely on: the
	// same value must fall out of a single hardware 2-by-1 division.
	wantV := new(big.Int).Sub(pow2(128), big.NewInt(1))
	wantV.Div(wantV, new(big.Int).SetUint64(e.dn))
	wantV.Sub(wantV, pow2(64))
	if new(big.Int).SetUint64(e.v).Cmp(wantV) != 0 {
		fail("v = %#x, want %s", e.v, wantV.Text(16))
	}
	if hw, _ := bits.Div64(^e.dn, ^uint64(0), e.dn); hw != e.v {
		fail("v = %#x, but bits.Div64(^dn, 2^64-1, dn) = %#x", e.v, hw)
	}

	if k == 0 {
		if e.m != 0 || e.p != 0 {
			fail("k=0 magic must be zero (unused), got m=%#x p=%d", e.m, e.p)
		}
		return
	}

	// Minimality of p: 5^k ≤ 2^(p+k) must hold at p and fail at p-1.
	five := bigPow(5, k)
	if pow2(int(e.p)+k).Cmp(five) < 0 {
		fail("p = %d too small: 5^k > 2^(p+k)", e.p)
	}
	if e.p > 0 && pow2(int(e.p)-1+k).Cmp(five) >= 0 {
		fail("p = %d not minimal: 5^k ≤ 2^(p-1+k)", e.p)
	}

	check := func(n uint64) {
		q, _ := bits.Mul64(n>>k, e.m)
		q >>= e.p
		r := n - q*e.d
		wantQ, wantR := new(big.Int).QuoRem(
			new(big.Int).SetUint64(n), dBig, new(big.Int))
		if new(big.Int).SetUint64(q).Cmp(wantQ) != 0 ||
			new(big.Int).SetUint64(r).Cmp(wantR) != 0 {
			fail("magic divides n=%d wrong: got q=%d r=%d, want q=%s r=%s",
				n, q, r, wantQ, wantR)
		}
	}

	for _, n := range []uint64{
		0, 1, e.d - 1, e.d, e.d + 1, ^uint64(0), ^uint64(0) - e.d + 1,
	} {
		check(n)
	}
	//nolint:gosec // deterministic sampling for self-verification, not crypto
	rng := rand.New(rand.NewPCG(0x6E4, uint64(k)))
	for range 1 << 16 {
		check(rng.Uint64())
	}
}

// render emits the generated source. Output must already be gofumpt-clean;
// format.Source only normalizes whitespace.
func render(entries []entry) []byte {
	var b bytes.Buffer

	b.WriteString("// Code generated by internal/gen. DO NOT EDIT.\n\n")
	b.WriteString("package zerodecimal\n\n")

	b.WriteString(`// pow10Tab holds the reciprocal-division constants for 10^k, k = 0..19
// (see pow10Entry). It is sized 32 so pow10Tab[k&31] compiles without a
// bounds check; entries 20..31 stay zero and are unreachable under the
// documented k ≤ MaxPrec preconditions.
var pow10Tab = [32]pow10Entry{
`)
	for k, e := range entries {
		if k == 0 {
			b.WriteString("\t// k = 0: m and p are zero because they are unused —\n")
			b.WriteString("\t// every divmod path short-circuits k == 0 before the lookup.\n")
		}
		fmt.Fprintf(&b, "\t{d: %d, m: 0x%016x, dn: 0x%016x, v: 0x%016x, p: %d, s: %d}, // k = %d\n",
			e.d, e.m, e.dn, e.v, e.p, e.s, k)
	}
	b.WriteString("}\n\n")

	b.WriteString(`// pow10u64 holds 10^k for k = 0..19; entries 20..31 stay zero, padding the
// array to a power of two for bounds-check-free indexing.
var pow10u64 = [32]uint64{
`)
	for k := 0; k <= maxPrec; k++ {
		fmt.Fprintf(&b, "\t%s, // 10^%d\n", bigPow(10, k), k)
	}
	b.WriteString("}\n\n")

	b.WriteString(`// pow10u128 holds 10^k for k = 0..38 — the full range of precision-product
// scale factors; entries 39..63 stay zero, padding the array to a power of
// two for bounds-check-free indexing.
var pow10u128 = [64]u128{
`)
	for k := 0; k <= maxPow10u128; k++ {
		p := bigPow(10, k)
		if p.BitLen() > 128 {
			panic(fmt.Sprintf("gen: 10^%d does not fit u128", k))
		}
		mask64 := new(big.Int).SetUint64(^uint64(0))
		hi := new(big.Int).Rsh(p, 64).Uint64()
		lo := new(big.Int).And(p, mask64).Uint64()
		if hi == 0 {
			fmt.Fprintf(&b, "\t{lo: %d}, // 10^%d\n", lo, k)
		} else {
			fmt.Fprintf(&b, "\t{hi: 0x%016x, lo: 0x%016x}, // 10^%d\n", hi, lo, k)
		}
	}
	b.WriteString("}\n")

	return b.Bytes()
}
