package main

import (
	"strings"
	"testing"
)

const raw = `benchmark-source-sha256: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
goos: darwin
goarch: arm64
cpu: Apple M1 Pro
BenchmarkAdd/zd/small-10          1000000  2.0 ns/op  0 B/op  0 allocs/op
BenchmarkAdd/udec/small-10       1000000  4.0 ns/op  0 B/op  0 allocs/op
BenchmarkMul/zd/unsupported-10   1000000  3.0 ns/op  0 B/op  0 allocs/op
PASS
`

func TestFilterLibrary(t *testing.T) {
	var out strings.Builder
	if err := filter(&out, strings.NewReader(raw), "zd", nil, 1); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "/zd/") || strings.Contains(got, "/udec/") {
		t.Fatalf("library segments remain in output:\n%s", got)
	}
	if !strings.Contains(got, "BenchmarkAdd/small-10") || !strings.Contains(got, "BenchmarkMul/unsupported-10") {
		t.Fatalf("zerodecimal rows missing:\n%s", got)
	}
	if !strings.Contains(got, "benchmark-source-sha256: ") || !strings.Contains(got, "goos: darwin") || !strings.Contains(got, "PASS") {
		t.Fatalf("benchmark metadata missing:\n%s", got)
	}
}

func TestFilterIntersection(t *testing.T) {
	names := map[string]int{"BenchmarkAdd/small-10": 1}
	input := strings.ReplaceAll(raw, "/zd/", "/")
	var out strings.Builder
	if err := filter(&out, strings.NewReader(input), "", names, 0); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "BenchmarkAdd/small-10") {
		t.Fatalf("common row missing:\n%s", got)
	}
	if strings.Contains(got, "BenchmarkMul/unsupported-10") {
		t.Fatalf("unmatched row retained:\n%s", got)
	}
}

func TestFilterIntersectionRejectsMissingReference(t *testing.T) {
	names := map[string]int{
		"BenchmarkAdd/small-10": 1,
		"BenchmarkMissing-10":   1,
	}
	input := strings.ReplaceAll(raw, "/zd/", "/")
	var out strings.Builder
	if err := filter(&out, strings.NewReader(input), "", names, 0); err == nil || !strings.Contains(err.Error(), "BenchmarkMissing-10") {
		t.Fatalf("error = %v, want missing-reference error", err)
	}
}

func TestFilterRejectsWrongSampleCount(t *testing.T) {
	var out strings.Builder
	if err := filter(&out, strings.NewReader(raw), "zd", nil, 10); err == nil || !strings.Contains(err.Error(), "sample count") {
		t.Fatalf("error = %v, want sample-count error", err)
	}
}

func TestBenchmarkName(t *testing.T) {
	name, ok := benchmarkName("BenchmarkAdd/small-10        \t100 2 ns/op")
	if !ok || name != "BenchmarkAdd/small-10" {
		t.Fatalf("benchmarkName = %q, %v", name, ok)
	}
	if _, ok := benchmarkName("goos: darwin"); ok {
		t.Fatal("metadata parsed as benchmark")
	}
}
