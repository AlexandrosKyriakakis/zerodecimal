.PHONY: require-clean verify bench bench-all bench-split bench-zd bench-udec bench-alpaca bench-ss bench-eric bench-dec bench-gv compare pgo chart publish collect _collect production-smoke production-default production-strcache production-micro production-pipeline production-parallel production-parallel-strcache

COMPARATIVE_BENCHTIME ?= 100ms
COMPARATIVE_COUNT ?= 10
override GOENV := off
override GOFLAGS :=
override GOWORK := off
export GOENV GOFLAGS GOWORK
BENCH_COLLECTION_COMMAND ?= GOENV=off GOFLAGS= GOWORK=off make collect COMPARATIVE_BENCHTIME=$(COMPARATIVE_BENCHTIME) COMPARATIVE_COUNT=$(COMPARATIVE_COUNT)
ifeq ($(MAKELEVEL),0)
override BENCH_SOURCE_REVISION_CAPTURED := $(shell git rev-parse HEAD)
override BENCH_SOURCE_WORKTREE_CAPTURED := $(shell test -z "$$(git status --porcelain)" && echo clean || echo dirty)
override BENCH_COLLECTION_COMMAND_CAPTURED := $(BENCH_COLLECTION_COMMAND)
else
BENCH_SOURCE_REVISION_CAPTURED := $(BENCH_SOURCE_REVISION_CAPTURED)
BENCH_SOURCE_WORKTREE_CAPTURED := $(BENCH_SOURCE_WORKTREE_CAPTURED)
BENCH_COLLECTION_COMMAND_CAPTURED := $(BENCH_COLLECTION_COMMAND_CAPTURED)
endif
export BENCH_SOURCE_REVISION_CAPTURED BENCH_SOURCE_WORKTREE_CAPTURED BENCH_COLLECTION_COMMAND_CAPTURED
PRODUCTION_BENCHTIME ?= 2s
PRODUCTION_COUNT ?= 10
PRODUCTION_CPU ?= 1
PRODUCTION_PARALLEL_CPU ?= 1,2,4,8
COMPARATIVE_BENCH = ^Benchmark(Parse|String|Add|Sub|Mul|Div|QuoRem|Cmp|RoundBank|Truncate|MarshalJSON|UnmarshalJSON|MarshalBinary|UnmarshalBinary|AppendText|SQLValue|SQLScan|NewFromFloat)$$

require-clean:
	@test "$(BENCH_SOURCE_WORKTREE_CAPTURED)" = clean || { echo "refusing benchmark publication from a dirty worktree" >&2; exit 1; }

verify:
	go test -run='^Test(ComparativeBenchmarkFixtures|PublishCannotRunStandaloneOrRebindExistingArtifacts|PrivateCollect.*)$$' -count=1 .
	go test ./internal/benchfilter ./internal/chartgen ./internal/provenance

# Quick full sweep: every lib/op/shape once, mainly to smoke out API misuse.
bench:
	GOFLAGS= go test -bench='$(COMPARATIVE_BENCH)' -benchmem -benchtime=0.1s -count=1 -run NONE .

# Single-process collection: all libraries use one binary and host session,
# though Go still emits consecutive samples in fixed leaf/library order.
# bench-split derives the per-library scratch files.
bench-all:
	@source_hash='$(BENCH_SOURCE_HASH_CAPTURED)'; \
	if test -z "$$source_hash"; then source_hash=$$(GOENV=off GOFLAGS= go run ./internal/sourcehash -repo ..); fi; \
	printf 'benchmark-source-sha256: %s\n' "$$source_hash" > bench-all.txt; \
	GOFLAGS= go test -bench='$(COMPARATIVE_BENCH)' -benchmem -benchtime=$(COMPARATIVE_BENCHTIME) -count=$(COMPARATIVE_COUNT) -timeout=2h -run NONE . >> bench-all.txt

bench-split:
	go run ./internal/benchfilter -input bench-all.txt -library zd -count $(COMPARATIVE_COUNT) > bench-zd.txt
	go run ./internal/benchfilter -input bench-all.txt -library udec -count $(COMPARATIVE_COUNT) > bench-udec.txt
	go run ./internal/benchfilter -input bench-all.txt -library alpaca -count $(COMPARATIVE_COUNT) > bench-alpaca.txt
	go run ./internal/benchfilter -input bench-all.txt -library ss -count $(COMPARATIVE_COUNT) > bench-ss.txt
	go run ./internal/benchfilter -input bench-all.txt -library eric -count $(COMPARATIVE_COUNT) > bench-eric.txt
	go run ./internal/benchfilter -input bench-all.txt -library dec -count $(COMPARATIVE_COUNT) > bench-dec.txt
	go run ./internal/benchfilter -input bench-all.txt -library gv -count $(COMPARATIVE_COUNT) > bench-gv.txt

# Per-library convenience runs keep go test and filtering in separate recipes,
# so a test failure cannot be hidden by a successful pipeline consumer.
bench-zd:
	GOFLAGS= go test -bench=/zd/ -benchmem -benchtime=$(COMPARATIVE_BENCHTIME) -count=$(COMPARATIVE_COUNT) -timeout=2h -run NONE . > bench-zd-raw.txt
	go run ./internal/benchfilter -input bench-zd-raw.txt -library zd -count $(COMPARATIVE_COUNT) > bench-zd.txt

bench-udec:
	GOFLAGS= go test -bench=/udec/ -benchmem -benchtime=$(COMPARATIVE_BENCHTIME) -count=$(COMPARATIVE_COUNT) -timeout=2h -run NONE . > bench-udec-raw.txt
	go run ./internal/benchfilter -input bench-udec-raw.txt -library udec -count $(COMPARATIVE_COUNT) > bench-udec.txt

bench-alpaca:
	GOFLAGS= go test -bench=/alpaca/ -benchmem -benchtime=$(COMPARATIVE_BENCHTIME) -count=$(COMPARATIVE_COUNT) -timeout=2h -run NONE . > bench-alpaca-raw.txt
	go run ./internal/benchfilter -input bench-alpaca-raw.txt -library alpaca -count $(COMPARATIVE_COUNT) > bench-alpaca.txt

bench-ss:
	GOFLAGS= go test -bench=/ss/ -benchmem -benchtime=$(COMPARATIVE_BENCHTIME) -count=$(COMPARATIVE_COUNT) -timeout=2h -run NONE . > bench-ss-raw.txt
	go run ./internal/benchfilter -input bench-ss-raw.txt -library ss -count $(COMPARATIVE_COUNT) > bench-ss.txt

bench-eric:
	GOFLAGS= go test -bench=/eric/ -benchmem -benchtime=$(COMPARATIVE_BENCHTIME) -count=$(COMPARATIVE_COUNT) -timeout=2h -run NONE . > bench-eric-raw.txt
	go run ./internal/benchfilter -input bench-eric-raw.txt -library eric -count $(COMPARATIVE_COUNT) > bench-eric.txt

# dec is anchored because the unanchored regex would also match udec.
bench-dec:
	GOFLAGS= go test -bench=/^dec$$/ -benchmem -benchtime=$(COMPARATIVE_BENCHTIME) -count=$(COMPARATIVE_COUNT) -timeout=2h -run NONE . > bench-dec-raw.txt
	go run ./internal/benchfilter -input bench-dec-raw.txt -library dec -count $(COMPARATIVE_COUNT) > bench-dec.txt

# gv (govalues) only emits the three shapes it can represent (<=19 significant
# digits); its large/near_max rows are absent by design.
bench-gv:
	GOFLAGS= go test -bench=/gv/ -benchmem -benchtime=$(COMPARATIVE_BENCHTIME) -count=$(COMPARATIVE_COUNT) -timeout=2h -run NONE . > bench-gv-raw.txt
	go run ./internal/benchfilter -input bench-gv-raw.txt -library gv -count $(COMPARATIVE_COUNT) > bench-gv.txt

compare:
	go run ./internal/benchfilter -input bench-zd.txt -reference bench-udec.txt > bench-zd-for-udec.txt
	go run ./internal/benchfilter -input bench-zd.txt -reference bench-alpaca.txt > bench-zd-for-alpaca.txt
	go run ./internal/benchfilter -input bench-zd.txt -reference bench-ss.txt > bench-zd-for-ss.txt
	go run ./internal/benchfilter -input bench-zd.txt -reference bench-eric.txt > bench-zd-for-eric.txt
	go run ./internal/benchfilter -input bench-zd.txt -reference bench-dec.txt > bench-zd-for-dec.txt
	go run ./internal/benchfilter -input bench-zd.txt -reference bench-gv.txt > bench-zd-for-gv.txt
	benchstat udecimal=bench-udec.txt zerodecimal=bench-zd-for-udec.txt > bench-vs-udecimal.txt
	benchstat alpacadecimal=bench-alpaca.txt zerodecimal=bench-zd-for-alpaca.txt > bench-vs-alpacadecimal.txt
	benchstat shopspring=bench-ss.txt zerodecimal=bench-zd-for-ss.txt > bench-vs-shopspring.txt
	benchstat ericlagergren=bench-eric.txt zerodecimal=bench-zd-for-eric.txt > bench-vs-ericlagergren.txt
	benchstat dec128=bench-dec.txt zerodecimal=bench-zd-for-dec.txt > bench-vs-dec128.txt
	benchstat govalues=bench-gv.txt zerodecimal=bench-zd-for-gv.txt > bench-vs-govalues.txt

# Deterministically regenerate charts only after verifying the committed
# provenance hashes match the committed benchstat inputs.
chart:
	GOFLAGS= go run ./internal/chartgen

# Publication is intentionally not a standalone operation: binding arbitrary
# existing artifacts to the current source would make stale measurements look
# current. `collect` captures the source identity before measuring and invokes
# the private continuation only after regenerating every input.
publish:
	@echo "make publish is disabled; use make collect to regenerate and bind all benchmark artifacts" >&2
	@exit 1

# PGO delta: collect a CPU profile over the zd benchmarks, then collect
# standalone default and PGO populations with identical selection/timing
# flags. The unified/interleaved bench-zd.txt remains exclusive to competitor
# comparisons and is not a valid baseline for this standalone PGO run.
pgo:
	GOFLAGS= go test -bench=/zd/ -benchtime=0.2s -count=3 -timeout=2h -cpuprofile=zd.pprof -run NONE .
	@source_hash='$(BENCH_SOURCE_HASH_CAPTURED)'; \
	if test -z "$$source_hash"; then source_hash=$$(GOENV=off GOFLAGS= GOWORK=off go run ./internal/sourcehash -repo ..); fi; \
	printf 'benchmark-source-sha256: %s\n' "$$source_hash" > bench-zd-pgo-default-raw.txt
	GOFLAGS= go test -bench=/zd/ -benchmem -benchtime=$(COMPARATIVE_BENCHTIME) -count=$(COMPARATIVE_COUNT) -timeout=2h -run NONE . >> bench-zd-pgo-default-raw.txt
	go run ./internal/benchfilter -input bench-zd-pgo-default-raw.txt -library zd -count $(COMPARATIVE_COUNT) > bench-zd-pgo-default.txt
	@source_hash='$(BENCH_SOURCE_HASH_CAPTURED)'; \
	if test -z "$$source_hash"; then source_hash=$$(GOENV=off GOFLAGS= GOWORK=off go run ./internal/sourcehash -repo ..); fi; \
	printf 'benchmark-source-sha256: %s\n' "$$source_hash" > bench-zd-pgo-raw.txt
	GOFLAGS= go test -bench=/zd/ -benchmem -benchtime=$(COMPARATIVE_BENCHTIME) -count=$(COMPARATIVE_COUNT) -timeout=2h -pgo=zd.pprof -run NONE . >> bench-zd-pgo-raw.txt
	go run ./internal/benchfilter -input bench-zd-pgo-raw.txt -library zd -count $(COMPARATIVE_COUNT) > bench-zd-pgo.txt
	benchstat default=bench-zd-pgo-default.txt pgo=bench-zd-pgo.txt > bench-pgo.txt

collect: require-clean verify
	make --no-print-directory _collect BENCH_COLLECTION_ACTIVE=1 BENCH_SOURCE_HASH_CAPTURED="$$(GOENV=off GOFLAGS= GOWORK=off go run ./internal/sourcehash -repo ..)"

_collect:
	@test "$(BENCH_COLLECTION_ACTIVE)" = 1 || { echo "_collect is internal; use make collect" >&2; exit 1; }
	@case "$(BENCH_SOURCE_HASH_CAPTURED)" in ''|*[!0-9a-f]*) echo "invalid captured benchmark source hash" >&2; exit 1;; esac
	@test "$${#BENCH_SOURCE_HASH_CAPTURED}" -eq 64 || { echo "invalid captured benchmark source hash length" >&2; exit 1; }
	make --no-print-directory bench-all
	make --no-print-directory bench-split
	make --no-print-directory compare
	make --no-print-directory pgo
	ZERODECIMAL_BENCH_COLLECTION_ACTIVE=1 GOFLAGS= go run ./internal/chartgen -publish -raw bench-all.txt -benchtime $(COMPARATIVE_BENCHTIME) -count $(COMPARATIVE_COUNT) -revision $(BENCH_SOURCE_REVISION_CAPTURED) -source-hash $(BENCH_SOURCE_HASH_CAPTURED) -worktree $(BENCH_SOURCE_WORKTREE_CAPTURED) -command '$(BENCH_COLLECTION_COMMAND_CAPTURED)'

# Production-oriented zerodecimal benchmarks are separate from the competitor
# matrix above. Targets print raw Go benchmark output; redirect/tee it to a
# host-specific scratch path rather than overwriting committed comparisons.
production-smoke:
	go test -run='^TestProductionBenchmarkFixtures$$' -count=1 .
	go test -run='^$$' -bench='^BenchmarkProduction' -benchmem -benchtime=1x -count=1 .
	go test -tags=zerodecimal_strcache -run='^$$' -bench='^BenchmarkProduction' -benchmem -benchtime=1x -count=1 .

production-default:
	go test -run='^$$' -bench='^BenchmarkProduction' -benchmem -benchtime=$(PRODUCTION_BENCHTIME) -count=$(PRODUCTION_COUNT) -cpu=$(PRODUCTION_CPU) -timeout=2h .

production-strcache:
	go test -tags=zerodecimal_strcache -run='^$$' -bench='^BenchmarkProduction' -benchmem -benchtime=$(PRODUCTION_BENCHTIME) -count=$(PRODUCTION_COUNT) -cpu=$(PRODUCTION_CPU) -timeout=2h .

production-micro:
	go test -run='^$$' -bench='^BenchmarkProductionMicro' -benchmem -benchtime=$(PRODUCTION_BENCHTIME) -count=$(PRODUCTION_COUNT) -cpu=$(PRODUCTION_CPU) -timeout=2h .

production-pipeline:
	go test -run='^$$' -bench='^BenchmarkProductionPipeline' -benchmem -benchtime=$(PRODUCTION_BENCHTIME) -count=$(PRODUCTION_COUNT) -cpu=$(PRODUCTION_CPU) -timeout=2h .

# RunParallel rows are collected at several GOMAXPROCS settings. Cache modes
# have separate targets so their raw outputs cannot be accidentally combined.
production-parallel:
	go test -run='^$$' -bench='^BenchmarkProductionParallel$$' -benchmem -benchtime=$(PRODUCTION_BENCHTIME) -count=$(PRODUCTION_COUNT) -cpu=$(PRODUCTION_PARALLEL_CPU) -timeout=2h .

production-parallel-strcache:
	go test -tags=zerodecimal_strcache -run='^$$' -bench='^BenchmarkProductionParallel$$' -benchmem -benchtime=$(PRODUCTION_BENCHTIME) -count=$(PRODUCTION_COUNT) -cpu=$(PRODUCTION_PARALLEL_CPU) -timeout=2h .
