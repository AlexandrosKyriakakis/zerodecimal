.PHONY: bench bench-zd bench-udec bench-alpaca bench-ss bench-eric compare pgo

# Quick full sweep: every lib/op/shape once, mainly to smoke out API misuse.
bench:
	go test -bench=. -benchmem -benchtime=0.1s -count=1 -run NONE .

# Per-library runs for benchstat: the lib segment is stripped from the
# benchmark names (sed to stdout, never in-place — macOS portable) so the
# same op/shape rows line up across files.
bench-zd:
	go test -bench=/zd/ -benchmem -benchtime=0.1s -count=10 -timeout=2h -run NONE . | sed "s|/zd/|/|g" > bench-zd.txt

bench-udec:
	go test -bench=/udec/ -benchmem -benchtime=0.1s -count=10 -timeout=2h -run NONE . | sed "s|/udec/|/|g" > bench-udec.txt

bench-alpaca:
	go test -bench=/alpaca/ -benchmem -benchtime=0.1s -count=10 -timeout=2h -run NONE . | sed "s|/alpaca/|/|g" > bench-alpaca.txt

bench-ss:
	go test -bench=/ss/ -benchmem -benchtime=0.1s -count=10 -timeout=2h -run NONE . | sed "s|/ss/|/|g" > bench-ss.txt

bench-eric:
	go test -bench=/eric/ -benchmem -benchtime=0.1s -count=10 -timeout=2h -run NONE . | sed "s|/eric/|/|g" > bench-eric.txt

compare:
	benchstat udecimal=bench-udec.txt zerodecimal=bench-zd.txt > bench-vs-udecimal.txt
	benchstat alpacadecimal=bench-alpaca.txt zerodecimal=bench-zd.txt > bench-vs-alpacadecimal.txt
	benchstat shopspring=bench-ss.txt zerodecimal=bench-zd.txt > bench-vs-shopspring.txt
	benchstat ericlagergren=bench-eric.txt zerodecimal=bench-zd.txt > bench-vs-ericlagergren.txt

# PGO delta: collect a CPU profile over the zd benchmarks, re-run them with
# the benchmark binary rebuilt against that profile, and publish the
# default-vs-PGO benchstat. Needs bench-zd.txt (run `make bench-zd` first).
# zd.pprof and bench-zd-pgo.txt are scratch; bench-pgo.txt is the published
# artifact.
pgo:
	go test -bench=/zd/ -benchtime=0.2s -count=3 -timeout=2h -cpuprofile=zd.pprof -run NONE .
	go test -bench=/zd/ -benchmem -benchtime=0.1s -count=10 -timeout=2h -pgo=zd.pprof -run NONE . | sed "s|/zd/|/|g" > bench-zd-pgo.txt
	benchstat default=bench-zd.txt pgo=bench-zd-pgo.txt > bench-pgo.txt
