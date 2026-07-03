package zerodecimal_test

import (
	"encoding/json"
	"errors"
	"fmt"

	zerodecimal "github.com/AlexandrosKyriakakis/zerodecimal"
)

func Example() {
	price, err := zerodecimal.NewFromString("99.99")
	if err != nil {
		panic(err)
	}
	qty := zerodecimal.NewFromInt(3)

	total, err := price.Mul(qty)
	if err != nil {
		panic(err)
	}
	fmt.Println(total)
	fmt.Println(total.StringFixed(4))
	// Output:
	// 299.97
	// 299.9700
}

func ExampleNewFromString() {
	d, err := zerodecimal.NewFromString("1.5000")
	if err != nil {
		panic(err)
	}
	fmt.Println(d) // trailing fractional zeros trim at parse time

	sci, err := zerodecimal.NewFromString("1.23e4")
	if err != nil {
		panic(err)
	}
	fmt.Println(sci)

	_, err = zerodecimal.NewFromString(".5") // both sides of the dot need a digit
	fmt.Println(errors.Is(err, zerodecimal.ErrInvalidFormat))
	// Output:
	// 1.5
	// 12300
	// true
}

func ExampleNewFromStringTrunc() {
	// Strict parsing rejects values needing more than MaxPrec fractional digits...
	_, err := zerodecimal.NewFromString("0.123456789012345678901")
	fmt.Println(errors.Is(err, zerodecimal.ErrPrecOutOfRange))

	// ...while the Trunc variant truncates toward zero at MaxPrec instead.
	d, err := zerodecimal.NewFromStringTrunc("0.123456789012345678901")
	if err != nil {
		panic(err)
	}
	fmt.Println(d)
	// Output:
	// true
	// 0.1234567890123456789
}

func ExampleDecimal_Add() {
	a := zerodecimal.RequireFromString("0.1")
	b := zerodecimal.RequireFromString("0.2")
	fmt.Println(a.MustAdd(b))

	x, y := 0.1, 0.2 // the float64 arithmetic that decimals exist to avoid
	fmt.Println(x + y)
	// Output:
	// 0.3
	// 0.30000000000000004
}

func ExampleDecimal_Div() {
	third, err := zerodecimal.NewFromInt(1).Div(zerodecimal.NewFromInt(3))
	if err != nil {
		panic(err)
	}
	fmt.Println(third) // truncated at DefaultPrec fractional digits

	fmt.Println(zerodecimal.NewFromInt(1).MustDiv(zerodecimal.NewFromInt(8)))

	_, err = zerodecimal.NewFromInt(1).Div(zerodecimal.Zero)
	fmt.Println(errors.Is(err, zerodecimal.ErrDivideByZero))
	// Output:
	// 0.3333333333333333333
	// 0.125
	// true
}

func ExampleDecimal_QuoRem() {
	q, r := zerodecimal.RequireFromString("7.5").MustQuoRem(zerodecimal.NewFromInt(2))
	fmt.Println(q, r)
	// Output: 3 1.5
}

func ExampleDecimal_Round() {
	fmt.Println(zerodecimal.RequireFromString("2.5").Round(0)) // ties away from zero
	fmt.Println(zerodecimal.RequireFromString("-2.5").Round(0))
	fmt.Println(zerodecimal.RequireFromString("1.2345").Round(2))
	// Output:
	// 3
	// -3
	// 1.23
}

func ExampleDecimal_RoundBank() {
	fmt.Println(zerodecimal.RequireFromString("2.5").RoundBank(0)) // ties to even
	fmt.Println(zerodecimal.RequireFromString("3.5").RoundBank(0))
	fmt.Println(zerodecimal.RequireFromString("-2.5").RoundBank(0))
	// Output:
	// 2
	// 4
	// -2
}

func ExampleDecimal_Equal() {
	a := zerodecimal.RequireFromString("1.5")
	// An arithmetic result keeps its precision: 0.75 + 0.75 is 1.50, not 1.5.
	b := zerodecimal.RequireFromString("0.75").MustAdd(zerodecimal.RequireFromString("0.75"))

	fmt.Println(a == b)     // == compares representations
	fmt.Println(a.Equal(b)) // Equal compares values
	fmt.Println(a.Cmp(b))
	// Output:
	// false
	// true
	// 0
}

func ExampleDecimal_Trim() {
	a := zerodecimal.RequireFromString("1.5")
	b := zerodecimal.RequireFromString("0.75").MustAdd(zerodecimal.RequireFromString("0.75")) // 1.50

	fmt.Println(a == b)               // representations differ...
	fmt.Println(a.Trim() == b.Trim()) // ...until Trim canonicalizes them
	fmt.Println(b.Trim())
	// Output:
	// false
	// true
	// 1.5
}

func ExampleDecimal_Rescale() {
	d := zerodecimal.RequireFromString("1.5")

	cents := d.MustRescale(2) // exactly two fractional digits for wire formats
	_, _, lo, prec := cents.ToHiLo()
	fmt.Println(cents, lo, prec)

	fmt.Println(zerodecimal.RequireFromString("2.345").MustRescale(2)) // lowering rounds ties to even

	_, err := zerodecimal.RequireFromString("340282366920938463463374607431768211455").Rescale(1)
	fmt.Println(errors.Is(err, zerodecimal.ErrOverflow))
	// Output:
	// 1.5 150 2
	// 2.34
	// true
}

func ExampleDecimal_MarshalJSON() {
	type Order struct {
		Price zerodecimal.Decimal `json:"price"`
		Qty   zerodecimal.Decimal `json:"qty"`
	}
	data, err := json.Marshal(Order{
		Price: zerodecimal.RequireFromString("19.99"),
		Qty:   zerodecimal.NewFromInt(2),
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(string(data)) // always quoted: bare JSON numbers lose digits past 2^53
	// Output: {"price":"19.99","qty":"2"}
}

func ExampleDecimal_UnmarshalJSON() {
	var d zerodecimal.Decimal
	// Bare JSON numbers decode exactly too, scientific notation included.
	if err := json.Unmarshal([]byte(`1.5e-7`), &d); err != nil {
		panic(err)
	}
	fmt.Println(d)
	// Output: 0.00000015
}

func ExampleNullDecimal() {
	var n zerodecimal.NullDecimal

	// SQL NULL scans to Valid == false without an error.
	if err := n.Scan(nil); err != nil {
		panic(err)
	}
	fmt.Println(n.Valid)

	// Driver strings parse with the strict literal grammar.
	if err := n.Scan("123.45"); err != nil {
		panic(err)
	}
	fmt.Println(n.Valid, n.Decimal)

	// Value renders the canonical string every database agrees on.
	v, err := n.Value()
	if err != nil {
		panic(err)
	}
	fmt.Println(v)
	// Output:
	// false
	// true 123.45
	// 123.45
}
