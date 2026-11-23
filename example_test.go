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

	total, err := price.MulRound(qty, 2, zerodecimal.ToNearestEven)
	if err != nil {
		panic(err)
	}
	fmt.Println(total)
	fmt.Println(total.StringFixed(4))
	// Output:
	// 299.97
	// 299.9700
}

func ExampleDecimal_MustMulRound() {
	// The Must twins of the direct-round operations suit call sites whose
	// operands have proven bounds and whose scale and mode are constants: a
	// rounding step in a chain, without error plumbing.
	price := zerodecimal.RequireFromString("19.99")
	qty := zerodecimal.RequireFromString("1.5")
	fee := zerodecimal.RequireFromString("0.004")
	third := zerodecimal.NewFromInt(3)
	even := zerodecimal.ToNearestEven

	fmt.Println(price.MustMulRound(qty, 2, even))                   // 29.985, a tie
	fmt.Println(price.MustMulAddRound(qty, fee, 2, even))           // 29.989
	fmt.Println(price.MustDivRound(third, 2, even))                 // 6.6633...
	fmt.Println(price.MustMulDivRound(qty, third, 2, even))         // 9.995, a tie
	fmt.Println(zerodecimal.MustAvgRound(price, 2, even, qty, fee)) // 7.1646...

	// A violated bound panics with the bare sentinel the erroring form returns.
	defer func() {
		fmt.Println(errors.Is(recover().(error), zerodecimal.ErrDivideByZero))
	}()
	_ = price.MustDivRound(zerodecimal.Decimal{}, 2, even)
	// Output:
	// 29.98
	// 29.99
	// 6.66
	// 10
	// 7.16
	// true
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

func ExampleNewDivisor() {
	// Split amounts pro rata, amount × part / whole, dividing every product
	// by one precomputed Divisor.
	whole := zerodecimal.NewFromInt(3)
	part := zerodecimal.NewFromInt(2)
	byWhole, err := zerodecimal.NewDivisor(whole)
	if err != nil {
		panic(err)
	}
	for _, amount := range []zerodecimal.Decimal{
		zerodecimal.RequireFromString("100"),
		zerodecimal.RequireFromString("4.95"),
		zerodecimal.RequireFromString("0.05"),
	} {
		product := amount.MustMul(part)
		share, err := product.DivRoundBy(&byWhole, 2, zerodecimal.ToNearestEven)
		if err != nil {
			panic(err)
		}
		// DivBy is Div bit for bit, so swapping one for the other is safe.
		fmt.Println(share, product.MustDivBy(&byWhole) == product.MustDiv(whole))
	}
	// Output:
	// 66.67 true
	// 3.3 true
	// 0.03 true
}

func ExampleDecimal_MulAddRound() {
	qty := zerodecimal.RequireFromString("1.5")
	price := zerodecimal.RequireFromString("19.99")
	fee := zerodecimal.RequireFromString("0.004")

	// 1.5 * 19.99 + 0.004 = 29.989, rounded once.
	total, err := qty.MulAddRound(price, fee, 2, zerodecimal.ToNearestEven)
	if err != nil {
		panic(err)
	}
	fmt.Println(total)

	// Rounding the product first rounds twice: 29.985 ties down to 29.98,
	// and 29.98 + 0.004 rounds back to 29.98.
	product, err := qty.MulRound(price, 2, zerodecimal.ToNearestEven)
	if err != nil {
		panic(err)
	}
	fmt.Println(product.MustAdd(fee).RoundBank(2))
	// Output:
	// 29.99
	// 29.98
}

func ExampleAllocate() {
	// A $1.00 fee over fills of 1, 1, 1 and 3 shares. The leftover cents go
	// to the largest remainders, and a tie to the lower index.
	fee := zerodecimal.RequireFromString("1.00")
	qty := []zerodecimal.Decimal{
		zerodecimal.NewFromInt(1), zerodecimal.NewFromInt(1),
		zerodecimal.NewFromInt(1), zerodecimal.NewFromInt(3),
	}
	shares, err := zerodecimal.Allocate(nil, fee, qty, 2)
	if err != nil {
		panic(err)
	}
	for _, s := range shares {
		fmt.Println(s.StringFixed(2))
	}

	// Rounding each exact share (1/6, 1/6, 1/6 and 1/2 of the fee) on its
	// own charges a cent too much.
	var rounded zerodecimal.Decimal
	for _, q := range qty {
		s, err := fee.MulDivRound(q, zerodecimal.NewFromInt(6), 2, zerodecimal.ToNearestEven)
		if err != nil {
			panic(err)
		}
		rounded = rounded.MustAdd(s)
	}
	fmt.Println(rounded.StringFixed(2))
	// Output:
	// 0.17
	// 0.17
	// 0.16
	// 0.50
	// 1.01
}

func ExampleSplit() {
	// $10.00 three ways: the leftover cent goes to the first share.
	shares, err := zerodecimal.Split(nil, zerodecimal.RequireFromString("10.00"), 3, 2)
	if err != nil {
		panic(err)
	}
	for _, s := range shares {
		fmt.Println(s.StringFixed(2))
	}

	// A negative total splits by magnitude; dst is reused when it has room.
	shares, err = zerodecimal.Split(shares, zerodecimal.RequireFromString("-0.05"), 3, 2)
	if err != nil {
		panic(err)
	}
	fmt.Println(shares)
	// Output:
	// 3.34
	// 3.33
	// 3.33
	// [-0.02 -0.02 -0.01]
}

func ExampleDecimal_Div() {
	// Div is the compatibility operation: it truncates at adaptive precision
	// up to DefaultPrec.
	third, err := zerodecimal.NewFromInt(1).Div(zerodecimal.NewFromInt(3))
	if err != nil {
		panic(err)
	}
	fmt.Println(third.Prec() == zerodecimal.DefaultPrec)

	// Choose the scale and rounding mode directly when loss is intentional.
	rounded, err := zerodecimal.NewFromInt(1).DivRound(
		zerodecimal.NewFromInt(3), 3, zerodecimal.TowardZero,
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(rounded)

	exact, err := zerodecimal.NewFromInt(1).DivExact(zerodecimal.NewFromInt(8))
	if err != nil {
		panic(err)
	}
	fmt.Println(exact)

	_, err = zerodecimal.NewFromInt(1).Div(zerodecimal.Decimal{})
	fmt.Println(errors.Is(err, zerodecimal.ErrDivideByZero))
	// Output:
	// true
	// 0.333
	// 0.125
	// true
}

func ExampleDecimal_DivInt() {
	// Split a $12.60 fee evenly over the fills of an order, without building
	// a Decimal for the count.
	fee := zerodecimal.RequireFromString("12.60")
	fills := []string{"fill-1", "fill-2", "fill-3"}
	share, err := fee.DivInt(int64(len(fills)))
	if err != nil {
		panic(err)
	}
	fmt.Println(share)
	fmt.Println(share.MustMulInt(int64(len(fills))).Equal(fee))

	// A zero divisor is DivInt's only error.
	_, err = fee.DivInt(0)
	fmt.Println(errors.Is(err, zerodecimal.ErrDivideByZero))
	// Output:
	// 4.2
	// true
	// true
}

func ExampleDecimal_MulDivRound() {
	// Split a $10.01 fee over a fill of 3 units: the share of one unit.
	fee := zerodecimal.RequireFromString("10.01")
	one, fill := zerodecimal.NewFromInt(1), zerodecimal.NewFromInt(3)
	share, err := fee.MulDivRound(one, fill, 2, zerodecimal.ToNearestEven)
	if err != nil {
		panic(err)
	}
	fmt.Println(share)

	// A 19-place rate times cents has no exact Decimal product, so MulExact
	// then DivRound fails where MulDivRound rounds the exact value once.
	rate := zerodecimal.RequireFromString("1.0837000000000000001")
	_, err = fee.MulExact(rate)
	fmt.Println(errors.Is(err, zerodecimal.ErrInexact))
	converted, err := fee.MulDivRound(rate, one, 2, zerodecimal.ToNearestEven)
	if err != nil {
		panic(err)
	}
	fmt.Println(converted)
	// Output:
	// 3.34
	// true
	// 10.85
}

func ExampleDecimal_MulDivRoundBy() {
	// Split a $10 fee over three fills in proportion to their quantities,
	// fee × qty / total, dividing every share by one precomputed Divisor.
	fee := zerodecimal.NewFromInt(10)
	total := zerodecimal.NewFromInt(7)
	byTotal, err := zerodecimal.NewDivisor(total)
	if err != nil {
		panic(err)
	}
	for _, qty := range []string{"2.5", "1.25", "3.25"} {
		q := zerodecimal.RequireFromString(qty)
		share, err := fee.MulDivRoundBy(q, &byTotal, 2, zerodecimal.ToNearestEven)
		if err != nil {
			panic(err)
		}
		// MulDivRoundBy is MulDivRound bit for bit, so swapping one for the
		// other is safe.
		same, err := fee.MulDivRound(q, total, 2, zerodecimal.ToNearestEven)
		fmt.Println(share, err == nil && share == same)
	}
	// Output:
	// 3.57 true
	// 1.79 true
	// 4.64 true
}

func ExampleDecimal_QuoRem() {
	q, r := zerodecimal.RequireFromString("7.5").MustQuoRem(zerodecimal.NewFromInt(2))
	fmt.Println(q, r)
	// Output: 3 1.5
}

func ExampleAccumulator() {
	// The volume-weighted average price of three fills: Σ qty·price / Σ qty.
	fills := []struct{ qty, price string }{
		{"100", "10.25"},
		{"50", "10.40"},
		{"25", "10.10"},
	}
	var notional, shares zerodecimal.Accumulator // zero values are empty sums
	for _, f := range fills {
		qty := zerodecimal.RequireFromString(f.qty)
		notional.AddMul(qty, zerodecimal.RequireFromString(f.price))
		shares.Add(qty)
	}
	total, err := notional.Sum()
	if err != nil {
		panic(err)
	}
	volume, err := shares.Sum()
	if err != nil {
		panic(err)
	}
	fmt.Println(total, volume)

	// Div truncates at DefaultPrec places like Decimal.Div; DivRound rounds
	// the exact quotient once, to the requested scale.
	vwap, err := notional.Div(volume)
	if err != nil {
		panic(err)
	}
	fmt.Println(vwap.Prec() == zerodecimal.DefaultPrec)
	rounded, err := notional.DivRound(volume, 4, zerodecimal.ToNearestEven)
	if err != nil {
		panic(err)
	}
	fmt.Println(rounded)
	// Output:
	// 1797.5 175
	// true
	// 10.2714
}

func ExampleDecimal_CopySign() {
	amount := zerodecimal.RequireFromString("12.50")
	direction := zerodecimal.NewFromInt(-1)

	fmt.Println(amount.CopySign(direction)) // magnitude of amount, sign of direction
	fmt.Println(amount.Neg().CopySign(amount))
	fmt.Println(zerodecimal.Decimal{}.CopySign(direction)) // zero never carries a sign
	// Output:
	// -12.5
	// 12.5
	// 0
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

func ExampleDecimal_IsRounded() {
	// Does an amount already fit cents? No rounded copy is built to find out.
	fmt.Println(zerodecimal.RequireFromString("12.34").IsRounded(2))
	fmt.Println(zerodecimal.RequireFromString("12.345").IsRounded(2))

	// Stored trailing zeros do not count: 1.2500 is a whole number of cents.
	d := zerodecimal.RequireFromString("1.25").MustRescale(4)
	fmt.Println(d.IsRounded(2), d.Prec())
	// Output:
	// true
	// false
	// true 4
}

func ExampleDecimal_FracDigits() {
	// Arithmetic keeps trailing zeros, so Prec is the stored precision and
	// FracDigits the precision Trim would give.
	sum := zerodecimal.RequireFromString("0.75").MustAdd(zerodecimal.RequireFromString("0.75")) // 1.50
	fmt.Println(sum.Prec(), sum.FracDigits())

	fmt.Println(zerodecimal.NewFromInt(42).FracDigits())
	fmt.Println(zerodecimal.RequireFromString("0.000001").FracDigits() > 5) // needs more than 5 places
	// Output:
	// 2 1
	// 0
	// true
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
	// Bare JSON numbers decode exactly, scientific notation included.
	if err := json.Unmarshal([]byte(`1.5e-7`), &d); err != nil {
		panic(err)
	}
	fmt.Println(d)

	// Quoted JSON is semantically unescaped before strict decimal parsing.
	if err := json.Unmarshal([]byte(`"1\u002e5"`), &d); err != nil {
		panic(err)
	}
	fmt.Println(d)

	// A required Decimal rejects null rather than silently keeping d stale.
	err := json.Unmarshal([]byte(`null`), &d)
	fmt.Println(errors.Is(err, zerodecimal.ErrJSONNull), d)
	// Output:
	// 0.00000015
	// 1.5
	// true 1.5
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

func ExampleStrictNullDecimal() {
	n := zerodecimal.NewStrictNullDecimal(zerodecimal.NewFromInt(7))

	// Nullable exact SQL boundaries reject floats and clear stale state.
	err := n.Scan(float64(0.5))
	fmt.Println(errors.Is(err, zerodecimal.ErrScanFloat), n.Valid)

	if err := n.Scan("123.45"); err != nil {
		panic(err)
	}
	fmt.Println(n.Valid, n.Decimal)

	if err := n.Scan(nil); err != nil {
		panic(err)
	}
	fmt.Println(n.Valid)
	// Output:
	// true false
	// true 123.45
	// false
}
