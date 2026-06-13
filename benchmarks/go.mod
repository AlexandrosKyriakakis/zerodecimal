module github.com/AlexandrosKyriakakis/zerodecimal/benchmarks

go 1.26

replace github.com/AlexandrosKyriakakis/zerodecimal => ../

require (
	github.com/AlexandrosKyriakakis/zerodecimal v0.0.0-00010101000000-000000000000
	github.com/alpacahq/alpacadecimal v0.0.9
	github.com/ericlagergren/decimal v0.0.0-20240411145413-00de7ca16731
	github.com/quagmt/udecimal v1.9.0
	github.com/shopspring/decimal v1.4.0
)

require github.com/jokruger/dec128 v1.0.20

require github.com/govalues/decimal v0.1.36
