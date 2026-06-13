package zerodecimal

// Shortest binary-to-decimal digit generation via the Dragonbox algorithm by
// Junekey Jeon, ported from Go 1.26's internal/strconv (ftoadbox.go and
// math.go), which carries the following notice:
//
//	Copyright 2025 The Go Authors. All rights reserved.
//	Use of this source code is governed by a BSD-style license that can be
//	found in the Go source tree's LICENSE file.
//
// The byte-emitting tail (dboxDigits/formatBase10) is removed: dboxShortest64
// and dboxShortest32 return the shortest decimal significand and its power of
// ten directly, so NewFromFloat builds the coefficient with no text round trip.
// The pow10 mantissa table is dboxPow10 (internal/gen, big.Int self-verified);
// the runtime stays free of math/big and allocations. For binary-to-decimal
// rounding this uses round to nearest, tie to even — matching strconv, the
// documented oracle for the float constructors.
//
// The original paper:  https://github.com/jk-jeon/dragonbox (Dragonbox.pdf)

import "math/bits"

const (
	float64MantBits = 52 // p = 52 for float64.
	float32MantBits = 23 // p = 23 for float32.
)

// mulLog10_2 returns ⌊x·log10(2)⌋ for x in [-1600, 1600].
func mulLog10_2(x int) int {
	// log(2)/log(10) ≈ 0.30102999566 ≈ 78913 / 2^18.
	return (x * 78913) >> 18
}

// mulLog10_2MinusLog10_4Over3 returns ⌊x·log10(2) − log10(4/3)⌋ for x in
// [-2985, 2936] (Dragonbox section 6.3).
func mulLog10_2MinusLog10_4Over3(e int) int {
	return (e*631305 - 261663) >> 21
}

// dboxUadd128 returns the full 128 bits of u + n.
func dboxUadd128(u u128, n uint64) u128 {
	sum := u.lo + n
	if sum < u.lo {
		u.hi++
	}
	u.lo = sum
	return u
}

// dboxUmul128Upper64 returns the upper 64 bits of x·y.
func dboxUmul128Upper64(x, y uint64) uint64 {
	hi, _ := bits.Mul64(x, y)
	return hi
}

// dboxUmul192Upper128 returns the upper 128 bits (out of 192) of x·y.
func dboxUmul192Upper128(x uint64, y u128) u128 {
	hi, lo := bits.Mul64(x, y.hi)
	t := dboxUmul128Upper64(x, y.lo)
	return dboxUadd128(u128{hi: hi, lo: lo}, t)
}

// dboxUmul192Lower128 returns the lower 128 bits (out of 192) of x·y.
func dboxUmul192Lower128(x uint64, y u128) u128 {
	high := x * y.hi
	hi, lo := bits.Mul64(x, y.lo)
	return u128{hi: high + hi, lo: lo}
}

// dboxUmul96Upper64 returns the upper 64 bits (out of 96) of x·y (float32).
func dboxUmul96Upper64(x uint32, y uint64) uint64 {
	yh := uint32(y >> 32)
	yl := uint32(y) //nolint:gosec // deliberate low-32-bit split of y
	xyh := uint64(x) * uint64(yh)
	xyl := uint64(x) * uint64(yl)
	return xyh + (xyl >> 32)
}

// dboxUmul96Lower64 returns the lower 64 bits (out of 96) of x·y (float32).
func dboxUmul96Lower64(x uint32, y uint64) uint64 {
	return uint64(x) * y
}

// dboxMulPow64 computes the integer part of u·φ and reports whether the
// fractional part is zero (Dragonbox section 5.2.1).
func dboxMulPow64(u uint64, phi u128) (intPart uint64, isInt bool) {
	r := dboxUmul192Upper128(u, phi)
	return r.hi, r.lo == 0
}

// dboxMulPow32 is dboxMulPow64 for float32.
func dboxMulPow32(u uint32, phi uint64) (intPart uint32, isInt bool) {
	r := dboxUmul96Upper64(u, phi)
	//nolint:gosec // r>>32 is the integer part (≤ 32 bits); uint32(r) tests fraction
	return uint32(r >> 32), uint32(r) == 0
}

// dboxParity64 computes only the parity of the integer part and whether the
// fractional part is zero.
func dboxParity64(mant2 uint64, phi u128, beta int) (parity, isInt bool) {
	r := dboxUmul192Lower128(mant2, phi)
	parity = ((r.hi >> (64 - beta)) & 1) != 0
	isInt = ((r.hi << beta) | (r.lo >> (64 - beta))) == 0
	return
}

// dboxParity32 is dboxParity64 for float32.
func dboxParity32(mant2 uint32, phi uint64, beta int) (parity, isInt bool) {
	r := dboxUmul96Lower64(mant2, phi)
	parity = ((r >> (64 - beta)) & 1) != 0
	//nolint:gosec // truncation is the intended low-bits fractional test
	isInt = uint32(r>>(32-beta)) == 0
	return
}

// dboxDelta64 returns δ^(i) from φ.
func dboxDelta64(phi u128, beta int) uint32 {
	//nolint:gosec // δ^(i) fits 32 bits by construction (β small, top bits shifted off)
	return uint32(phi.hi >> (64 - 1 - beta))
}

// dboxDelta32 returns δ^(i) from φ.
func dboxDelta32(phi uint64, beta int) uint32 {
	//nolint:gosec // δ^(i) fits 32 bits by construction
	return uint32(phi >> (64 - 1 - beta))
}

// dboxRange64 returns the left and right float64 endpoints.
func dboxRange64(phi u128, beta int) (left, right uint64) {
	left = (phi.hi - (phi.hi >> (float64MantBits + 2))) >> (64 - float64MantBits - 1 - beta)
	right = (phi.hi + (phi.hi >> (float64MantBits + 1))) >> (64 - float64MantBits - 1 - beta)
	return
}

// dboxRange32 returns the left and right float32 endpoints.
func dboxRange32(phi uint64, beta int) (left, right uint32) {
	//nolint:gosec // endpoints fit 32 bits by construction (top bits shifted off)
	left = uint32((phi - (phi >> (float32MantBits + 2))) >> (64 - float32MantBits - 1 - beta))
	//nolint:gosec // endpoints fit 32 bits by construction
	right = uint32((phi + (phi >> (float32MantBits + 1))) >> (64 - float32MantBits - 1 - beta))
	return
}

// dboxRoundUp64 computes y^(ru).
func dboxRoundUp64(phi u128, beta int) uint64 {
	return (phi.hi>>(64-float64MantBits-2-beta) + 1) / 2
}

// dboxRoundUp32 computes y^(ru).
func dboxRoundUp32(phi uint64, beta int) uint32 {
	//nolint:gosec // y^(ru) fits 32 bits by construction
	return uint32(phi>>(64-float32MantBits-2-beta)+1) / 2
}

// dboxPow64 returns the precomputed φ̃k mantissa and binary exponent β for
// float64. The φ.lo++ correction for k outside [0, 55] is part of the
// algorithm (it selects the over-approximation of φ̃k there).
func dboxPow64(k, e int) (phi u128, beta int) {
	phi = dboxPow10[k-dboxPow10Min]
	if k < 0 || k > 55 {
		phi.lo++
	}
	beta = e + (1 + mulLog2_10(k)) - 1
	return
}

// dboxPow32 returns the precomputed φ̃k mantissa (high 64 bits only) and β for
// float32, with the matching over-approximation correction for k outside
// [0, 27].
func dboxPow32(k, e int) (mant uint64, beta int) {
	m := dboxPow10[k-dboxPow10Min]
	if k < 0 || k > 27 {
		m.hi++
	}
	beta = e + (1 + mulLog2_10(k)) - 1
	return m.hi, beta
}

// mulLog2_10 returns ⌊x·log2(10)⌋ for x in [-500, 500].
func mulLog2_10(x int) int {
	// log(10)/log(2) ≈ 3.32192809489 ≈ 108853 / 2^15.
	return (x * 108853) >> 15
}

// dboxTrimZeros trims trailing decimal zeros from x, returning x/10^p and p.
// Ported from Go 1.26 strconv.trimZeros (exact-division rotate trick).
func dboxTrimZeros(x uint64) (uint64, int) {
	const (
		div1e8m  = 0xc767074b22e90e21
		div1e8le = dboxMaxUint64 / 100000000

		div1e4m  = 0xd288ce703afb7e91
		div1e4le = dboxMaxUint64 / 10000

		div1e2m  = 0x8f5c28f5c28f5c29
		div1e2le = dboxMaxUint64 / 100

		div1e1m  = 0xcccccccccccccccd
		div1e1le = dboxMaxUint64 / 10
	)
	p := 0
	for d := bits.RotateLeft64(x*div1e8m, -8); d <= div1e8le; d = bits.RotateLeft64(x*div1e8m, -8) {
		x = d
		p += 8
	}
	if d := bits.RotateLeft64(x*div1e4m, -4); d <= div1e4le {
		x = d
		p += 4
	}
	if d := bits.RotateLeft64(x*div1e2m, -2); d <= div1e2le {
		x = d
		p += 2
	}
	if d := bits.RotateLeft64(x*div1e1m, -1); d <= div1e1le {
		x = d
		p++
	}
	return x, p
}

const dboxMaxUint64 = 1<<64 - 1

// dboxShortest64 is Go 1.26's dboxFtoa64 with byte emission removed. Given the
// float64 with significand mant and binary exponent exp (value = mant·2^exp),
// it returns (digits, e10) such that the shortest round-trip decimal of the
// float is digits·10^e10, with digits free of trailing zeros.
//
// PRECONDITION: the decimal exponent k it derives lies in dboxPow10's range —
// guaranteed for the NewFromFloat guarded domain. The lookup masks would
// otherwise be unchecked, so dboxPow10 covers a safe superset (see internal/gen).
func dboxShortest64(mant uint64, exp int, denorm bool) (uint64, int) {
	if mant == 1<<float64MantBits && !denorm {
		// Algorithm 5.6 (page 24): the left endpoint is closer.
		k0 := -mulLog10_2MinusLog10_4Over3(exp)
		phi, beta := dboxPow64(k0, exp)
		xi, zi := dboxRange64(phi, beta)
		if exp != 2 && exp != 3 {
			xi++
		}
		q := zi / 10
		if xi <= q*10 {
			qt, zeros := dboxTrimZeros(q)
			return qt, -k0 + 1 + zeros
		}
		yru := dboxRoundUp64(phi, beta)
		if exp == -77 && yru%2 != 0 {
			yru--
		} else if yru < xi {
			yru++
		}
		return yru, -k0
	}

	// κ = 2 for float64 (section 5.1.3).
	const (
		κ     = 2
		p10κ  = 100       // 10^κ
		p10κ1 = p10κ * 10 // 10^(κ+1)
	)

	// Algorithm 5.2 (page 15).
	k0 := -mulLog10_2(exp)
	phi, beta := dboxPow64(κ+k0, exp)
	zi, exact := dboxMulPow64((mant*2+1)<<beta, phi)
	s, r := zi/p10κ1, uint32(zi%p10κ1)
	di := dboxDelta64(phi, beta)

	if r < di {
		if r != 0 || !exact || mant%2 == 0 {
			st, zeros := dboxTrimZeros(s)
			return st, -k0 + 1 + zeros
		}
		s--
		r = p10κ * 10
	} else if r == di {
		parity, exact := dboxParity64(mant*2-1, phi, beta)
		if parity || (exact && mant%2 == 0) {
			st, zeros := dboxTrimZeros(s)
			return st, -k0 + 1 + zeros
		}
	}

	// Algorithm 5.4 (page 18).
	d := r + p10κ/2 - di/2
	t, rho := d/p10κ, d%p10κ
	yru := 10*s + uint64(t)
	if rho == 0 {
		parity, exact := dboxParity64(mant*2, phi, beta)
		if parity != ((d-p10κ/2)%2 != 0) || exact && yru%2 != 0 {
			yru--
		}
	}
	return yru, -k0
}

// dboxShortest32 is dboxShortest64 for float32 (Go 1.26's dboxFtoa32). digits
// is returned widened to uint64; it never exceeds 9 significant decimal digits.
func dboxShortest32(mant uint32, exp int, denorm bool) (uint64, int) {
	if mant == 1<<float32MantBits && !denorm {
		// Algorithm 5.6 (page 24).
		k0 := -mulLog10_2MinusLog10_4Over3(exp)
		phi, beta := dboxPow32(k0, exp)
		xi, zi := dboxRange32(phi, beta)
		if exp != 2 && exp != 3 {
			xi++
		}
		q := zi / 10
		if xi <= q*10 {
			qt, zeros := dboxTrimZeros(uint64(q))
			return qt, -k0 + 1 + zeros
		}
		yru := dboxRoundUp32(phi, beta)
		if exp == -77 && yru%2 != 0 {
			yru--
		} else if yru < xi {
			yru++
		}
		return uint64(yru), -k0
	}

	// κ = 1 for float32 (section 5.1.3).
	const (
		κ     = 1
		p10κ  = 10
		p10κ1 = p10κ * 10
	)

	// Algorithm 5.2 (page 15).
	k0 := -mulLog10_2(exp)
	phi, beta := dboxPow32(κ+k0, exp)
	zi, exact := dboxMulPow32((mant*2+1)<<beta, phi)
	s, r := zi/p10κ1, zi%p10κ1
	di := dboxDelta32(phi, beta)

	if r < di {
		if r != 0 || !exact || mant%2 == 0 {
			st, zeros := dboxTrimZeros(uint64(s))
			return st, -k0 + 1 + zeros
		}
		s--
		r = p10κ * 10
	} else if r == di {
		parity, exact := dboxParity32(mant*2-1, phi, beta)
		if parity || (exact && mant%2 == 0) {
			st, zeros := dboxTrimZeros(uint64(s))
			return st, -k0 + 1 + zeros
		}
	}

	// Algorithm 5.4 (page 18).
	d := r + p10κ/2 - di/2
	t, rho := d/p10κ, d%p10κ
	yru := 10*s + t
	if rho == 0 {
		parity, exact := dboxParity32(mant*2, phi, beta)
		if parity != ((d-p10κ/2)%2 != 0) || exact && yru%2 != 0 {
			yru--
		}
	}
	return uint64(yru), -k0
}
