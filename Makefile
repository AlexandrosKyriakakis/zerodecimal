.PHONY: test test-race test-alloc test-matrix test-tags test-32bit-compile lint cover fuzz fuzz-all bench bench-compare bench-pgo bench-production bench-production-smoke bench-production-parallel bench-production-parallel-strcache inline generate

# Default: race-enabled unit tests, then alloc assertions (alloc counts need !race).
test: test-race test-alloc

test-race:
	go test -race -failfast -coverpkg=. -coverprofile=coverage.out -covermode=atomic .

test-alloc:
	go test -run '^Test.*Alloc' -count=1 .

# Run the same precision/cache cross-product as the CI build-tag job. The
# explicit-off and both-cache-tag rows guard zerodecimal_nostrcache's override
# semantics rather than assuming they are equivalent to an untagged build.
test-tags:
	@set -eu; \
	for precision in default zerodecimal_prec9 zerodecimal_prec12; do \
		for cache in default zerodecimal_nostrcache zerodecimal_strcache zerodecimal_strcache,zerodecimal_nostrcache; do \
			precision_tags="$$precision"; \
			cache_tags="$$cache"; \
			if [ "$$precision_tags" = default ]; then precision_tags=""; fi; \
			if [ "$$cache_tags" = default ]; then cache_tags=""; fi; \
			if [ -n "$$precision_tags" ] && [ -n "$$cache_tags" ]; then \
				tags="$$precision_tags,$$cache_tags"; \
			else \
				tags="$$precision_tags$$cache_tags"; \
			fi; \
			echo "==> go test tags: $${tags:-default}"; \
			go test -tags="$$tags" -count=1 ./...; \
		done; \
	done

# Cross-compile test binaries for the two primary 32-bit Go ports. CI also
# executes the full linux/386 suite so architecture-specific assertions run.
test-32bit-compile:
	CGO_ENABLED=0 GOOS=linux GOARCH=386 go test -c -o /dev/null .
	CGO_ENABLED=0 GOOS=linux GOARCH=386 go vet ./...
	CGO_ENABLED=0 GOOS=linux GOARCH=arm go test -c -o /dev/null .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm go vet ./...

test-matrix: test-tags test-32bit-compile

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

bench-production:
	$(MAKE) -C benchmarks production-default

bench-production-smoke:
	$(MAKE) -C benchmarks production-smoke

bench-production-parallel:
	$(MAKE) -C benchmarks production-parallel

bench-production-parallel-strcache:
	$(MAKE) -C benchmarks production-parallel-strcache

generate:
	go generate ./...

inline: # audit which hot-path functions the compiler inlines
	go build -gcflags='-m' . 2>&1 | grep -E 'inline' || true

# Swallow positional args used by fuzz / fuzz-all.
# ref. https://stackoverflow.com/questions/6273608
%:
	@
