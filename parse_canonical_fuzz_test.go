//go:build fuzz

package zerodecimal

import (
	"errors"
	"math/big"
	"testing"

	"github.com/shopspring/decimal"
)

// FuzzParseCanonicalCompleteness is an iff acceptance oracle, not only a
// success-value check. shopspring parses each grammar-valid literal into an
// unbounded coefficient and exponent; math/big canonicalizes that exact value
// and proves whether its final coefficient fits 128 bits and its final scale
// fits MaxPrec. NewFromString and ParseBytes must succeed exactly in that
// representable region and return the oracle's bare range sentinel outside it.
func FuzzParseCanonicalCompleteness(f *testing.F) {
	for _, raw := range []string{
		"0",
		"1.5",
		maxCoefficientText,
		maxCoefficientText + ".0",
		maxCoefficientText + "0e-1",
		maxCoefficientText + "00000000000000000000e-20",
		"34028236692093846346337460743176821145.50",
		"1.00000000000000000000",
		"10e-20",
		"10e-21",
		"35000000000000000001e38",
		"340282366920938463463374607431768211456.0",
		"-" + maxCoefficientText + "00e-2",
		"000000000000000000000000000000000000000000001.230000e+2",
	} {
		f.Add(raw)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		if !canonicalFuzzLiteral(raw) {
			return
		}

		ss, err := decimal.NewFromString(raw)
		if err != nil {
			t.Fatalf("shopspring rejected grammar-valid bounded literal %q: %v", raw, err)
		}
		want, wantErr := canonicalFuzzOracle(ss)

		got, gotErr := NewFromString(raw)
		if !errors.Is(gotErr, wantErr) {
			t.Fatalf("NewFromString(%q) error = %v, oracle wants %v (want=%+v)", raw, gotErr, wantErr, want)
		}
		gotBytes, bytesErr := ParseBytes([]byte(raw))
		if !errors.Is(bytesErr, wantErr) {
			t.Fatalf("ParseBytes(%q) error = %v, oracle wants %v (want=%+v)", raw, bytesErr, wantErr, want)
		}
		if wantErr != nil {
			return
		}
		if got != want || gotBytes != want {
			t.Fatalf("parse(%q) = string:%+v bytes:%+v, want canonical %+v", raw, got, gotBytes, want)
		}

		// Keep shopspring in the value side of the oracle too: converting our
		// canonical result back to its unbounded domain must preserve the exact
		// source value, independently of the representation comparison above.
		gotSS, err := decimal.NewFromString(got.String())
		if err != nil || !gotSS.Equal(ss) {
			t.Fatalf("parse(%q) value mismatch vs shopspring: got=%s source=%s err=%v", raw, got, ss, err)
		}
	})
}

// canonicalFuzzLiteral recognizes exactly the production grammar while
// bounding the exponent magnitude to keep each big.Int oracle operation
// cheap. The production parser's separate maxParseLen gate remains in force.
func canonicalFuzzLiteral(raw string) bool {
	if len(raw) == 0 || len(raw) > maxParseLen {
		return false
	}
	i := 0
	if raw[i] == '+' || raw[i] == '-' {
		i++
		if i == len(raw) {
			return false
		}
	}

	intStart := i
	for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
		i++
	}
	if i == intStart {
		return false
	}
	if i < len(raw) && raw[i] == '.' {
		i++
		fracStart := i
		for i < len(raw) && raw[i] >= '0' && raw[i] <= '9' {
			i++
		}
		if i == fracStart {
			return false
		}
	}
	if i == len(raw) {
		return true
	}
	if raw[i] != 'e' && raw[i] != 'E' {
		return false
	}
	i++
	if i < len(raw) && (raw[i] == '+' || raw[i] == '-') {
		i++
	}
	if i == len(raw) {
		return false
	}

	expMagnitude := 0
	for ; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return false
		}
		if expMagnitude <= 200 {
			expMagnitude = expMagnitude*10 + int(raw[i]-'0')
		}
	}
	return expMagnitude <= 200
}

func canonicalFuzzOracle(ss decimal.Decimal) (Decimal, error) {
	coefficient := ss.Coefficient()
	neg := coefficient.Sign() < 0
	coefficient.Abs(coefficient)
	if coefficient.Sign() == 0 {
		return Decimal{}, nil
	}

	exp := int(ss.Exponent())
	ten := big.NewInt(10)
	quotient := new(big.Int)
	remainder := new(big.Int)
	for exp < 0 {
		quotient.QuoRem(coefficient, ten, remainder)
		if remainder.Sign() != 0 {
			break
		}
		coefficient.Set(quotient)
		exp++
	}

	if exp >= 0 {
		factor := new(big.Int).Exp(ten, big.NewInt(int64(exp)), nil)
		coefficient.Mul(coefficient, factor)
	} else if -exp > int(MaxPrec) {
		// Match the parser's stable error precedence: an already-overwide
		// canonical coefficient is overflow even when its scale is also high.
		if coefficient.BitLen() > 128 {
			return Decimal{}, ErrOverflow
		}
		return Decimal{}, ErrPrecOutOfRange
	}

	if coefficient.BitLen() > 128 {
		return Decimal{}, ErrOverflow
	}
	hi := new(big.Int).Rsh(new(big.Int).Set(coefficient), 64).Uint64()
	prec := uint8(0)
	if exp < 0 {
		prec = uint8(-exp)
	}
	return newDecimal(u128{hi: hi, lo: coefficient.Uint64()}, neg, prec), nil
}
