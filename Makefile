.PHONY: test test-race test-alloc lint cover fuzz fuzz-all bench bench-compare bench-pgo inline generate

# Default: race-enabled unit tests, then alloc assertions (alloc counts need !race).
test: test-race test-alloc

test-race:
	go test -race -failfast -coverpkg=. -coverprofile=coverage.out -covermode=atomic .

test-alloc:
	go test -run 'TestAllocs' -count=1 .

# Single test: go test -run 'TestParse/empty_string' .
# Single subtest under race: go test -race -run 'TestRoundBank/tie_negative' .

lint:
	golangci-lint run --config=.golangci.yaml ./...

cover: test-race
	go tool cover -html=coverage.out

fuzz: # usage: make fuzz FuzzAdd
	$(eval fuzzName := $(filter-out $@,$(MAKECMDGOALS)))
	go test -tags=fuzz -run='^$$' -fuzz='^$(fuzzName)$$' -fuzztime=30s -timeout=10m .

fuzz-all: # usage: make fuzz-all 30   (seconds per target, default 10)
	$(eval fuzzTime := $(filter-out $@,$(MAKECMDGOALS)))
	sh scripts/fuzz-all.sh $(fuzzTime)

bench:
	$(MAKE) -C benchmarks bench

bench-compare:
	$(MAKE) -C benchmarks compare

bench-pgo:
	$(MAKE) -C benchmarks pgo

generate:
	go generate ./...

inline: # audit which hot-path functions the compiler inlines
	go build -gcflags='-m' . 2>&1 | grep -E 'inline' || true

# Swallow positional args used by fuzz / fuzz-all.
# ref. https://stackoverflow.com/questions/6273608
%:
	@
