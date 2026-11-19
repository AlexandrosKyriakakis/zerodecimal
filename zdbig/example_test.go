package zdbig_test

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/AlexandrosKyriakakis/zerodecimal"
	"github.com/AlexandrosKyriakakis/zerodecimal/zdbig"
)

func ExampleFromBigInt() {
	// A finite pgtype.Numeric holds exactly this pair: its Int and its Exp.
	// This one is -1234567 · 10^-4.
	coef, exp := big.NewInt(-1234567), int32(-4)
	d, err := zdbig.FromBigInt(coef, exp)
	if err != nil {
		panic(err)
	}
	fmt.Println(d)

	// A nil coefficient is zero, though, and a pgtype.Numeric that is SQL
	// NULL, NaN or an infinity has a nil Int: FromBigInt cannot tell it from a
	// zero and reports no error. Check Valid, NaN and InfinityModifier first.
	d, err = zdbig.FromBigInt(nil, 0)
	fmt.Println(d, err)

	// A positive exponent scales the coefficient...
	d, err = zdbig.FromBigInt(big.NewInt(12), 3)
	if err != nil {
		panic(err)
	}
	fmt.Println(d)

	// ...a negative one past MaxPrec strips trailing zeros while the value
	// stays exact (10^45 · 10^-40 = 100000, here at precision 19)...
	wide := new(big.Int).Exp(big.NewInt(10), big.NewInt(45), nil)
	d, err = zdbig.FromBigInt(wide, -40)
	if err != nil {
		panic(err)
	}
	fmt.Println(d, d.Prec())

	// ...and nothing is ever rounded: a value with no exact Decimal is an
	// error. 10^-20 needs 20 places, and 2^128 does not fit 128 bits.
	_, err = zdbig.FromBigInt(big.NewInt(1), -20)
	fmt.Println(errors.Is(err, zerodecimal.ErrPrecOutOfRange))
	_, err = zdbig.FromBigInt(new(big.Int).Lsh(big.NewInt(1), 128), 0)
	fmt.Println(errors.Is(err, zerodecimal.ErrOverflow))
	// Output:
	// -123.4567
	// 0 <nil>
	// 12000
	// 100000 19
	// true
	// true
}

func ExampleToBigInt() {
	// -123.45 written with two trailing zeros: the pair keeps them.
	d := zerodecimal.MustNew(-1_234_500, -4)
	coef, exp := zdbig.ToBigInt(d)
	fmt.Println(coef, exp)

	// FromBigInt gives the same Decimal back, representation included.
	back, err := zdbig.FromBigInt(coef, exp)
	if err != nil {
		panic(err)
	}
	fmt.Println(back == d)
	// Output:
	// -1234500 -4
	// true
}
