package main

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestGeomeanRatioDataParsesSummary(t *testing.T) {
	data := `goos: darwin
goarch: arm64
cpu: Apple M1 Pro
                 │ baseline │ new         │
                 │ sec/op   │ sec/op      │
Shared-10          20.00n     6.316n        -68.42%
geomean            20.00n     6.316n        -68.42%
`
	got, err := geomeanRatioData("test", data)
	if err != nil {
		t.Fatal(err)
	}
	want := 0.3158
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("ratio = %v, want %v", got, want)
	}
}

func TestGeomeanRatioDataTied(t *testing.T) {
	data := "│ sec/op │ sec/op │\ngeomean 10.0n 10.0n ~\n"
	got, err := geomeanRatioData("test", data)
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("ratio = %v, want 1", got)
	}
}

func TestGeomeanRatioDataRejectsInvalidSummary(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "missing", data: "│ B/op │ B/op │\ngeomean 8.0 4.0 -50.00%\n"},
		{name: "short", data: "│ sec/op │ sec/op │\ngeomean 8.0n 4.0n\n"},
		{name: "unknown", data: "│ sec/op │ sec/op │\ngeomean 8.0n 4.0n ?\n"},
		{name: "non_positive", data: "│ sec/op │ sec/op │\ngeomean 8.0n 0.0n -100.00%\n"},
		{name: "nan", data: "│ sec/op │ sec/op │\ngeomean 8.0n NaN NaN%\n"},
		{name: "positive_inf", data: "│ sec/op │ sec/op │\ngeomean 8.0n +Inf +Inf%\n"},
		{name: "negative_inf", data: "│ sec/op │ sec/op │\ngeomean 8.0n -Inf -Inf%\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := geomeanRatioData(tc.name, tc.data); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRenderDescribesNormalizationAndRatios(t *testing.T) {
	metadata := collectionMetadata{
		goos: "darwin", goarch: "arm64", cpu: "Test CPU", goVersion: "go1.99",
		benchTime: "250ms", count: 12,
	}
	svg := render([]bar{
		{name: "zerodecimal +PGO", ratio: 0.9, pgo: true},
		{name: "zerodecimal", ratio: 1, self: true},
		{name: "competitor", ratio: 2.25},
	}, themes[0], metadata)
	for _, want := range []string{
		"pairwise native-API geomean latency",
		"zerodecimal = 1.0×",
		"Test CPU",
		"darwin/arm64",
		"go1.99",
		"0.9×",
		"1.0×",
		"2.2×",
		"native precision/rounding contracts may differ",
		"250ms × 12",
		"not an application PGO prediction",
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("rendered SVG does not contain %q", want)
		}
	}
}

func TestLoadCollectionMetadata(t *testing.T) {
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	raw := filepath.Join(t.TempDir(), "bench-all.txt")
	sourceHash := strings.Repeat("d", 64)
	if err := os.WriteFile(raw, []byte("benchmark-source-sha256: "+sourceHash+"\ngoos: darwin\ngoarch: arm64\ncpu: Test CPU\nBenchmarkX-1 10 1 ns/op\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	revision := strings.Repeat("a", 40)
	m, err := loadCollectionMetadata(raw, "100ms", 10, revision, sourceHash, "clean", "make collect", "golang.org/x/perf v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if m.goos != "darwin" || m.goarch != "arm64" || m.cpu != "Test CPU" || m.goVersion != runtime.Version() ||
		m.benchTime != "100ms" || m.count != 10 || m.revision != revision || m.sourceHash != sourceHash || m.worktree != "clean" || m.benchstatVersion == "" || m.gowork != "off (enforced)" ||
		m.goexperiment == "" || m.gomaxprocs == "" || m.gogc == "" || m.gomemlimit == "" || m.godebug == "" {
		t.Fatalf("unexpected metadata: %+v", m)
	}
	m.rawSHA256 = strings.Repeat("a", 64)
	m.artifactSHA256 = make(map[string]string, len(publishedInputs))
	for _, file := range publishedInputs {
		m.artifactSHA256[file] = strings.Repeat("b", 64)
	}
	m.artifactSHA256["bench-all.txt"] = m.rawSHA256
	m.pgoSourceSHA256 = make(map[string]string, len(pgoSourceInputs))
	for _, file := range pgoSourceInputs {
		m.pgoSourceSHA256[file] = strings.Repeat("c", 64)
	}
	provenance := renderProvenance(m)
	for _, want := range []string{revision, "benchmark-source-sha256: " + sourceHash, "source-worktree-at-collection-start: clean", "goenv: off (enforced)", "gowork: off (enforced)", "goflags: empty (enforced)", "build-tags: none", "string-cache: off", "fixed library order"} {
		if !strings.Contains(provenance, want) {
			t.Errorf("provenance missing %q", want)
		}
	}
	parsed, err := parseProvenanceData(provenance)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.revision != m.revision || parsed.sourceHash != m.sourceHash || parsed.rawSHA256 != m.rawSHA256 || parsed.artifactSHA256["bench-pgo.txt"] != m.artifactSHA256["bench-pgo.txt"] ||
		parsed.pgoSourceSHA256["bench-zd-pgo-raw.txt"] != m.pgoSourceSHA256["bench-zd-pgo-raw.txt"] {
		t.Fatalf("parsed provenance mismatch: %+v", parsed)
	}
	contradictory := strings.Replace(provenance,
		"artifact-sha256-bench-all.txt: "+m.rawSHA256,
		"artifact-sha256-bench-all.txt: "+strings.Repeat("f", 64), 1)
	if _, err := parseProvenanceData(contradictory); err == nil {
		t.Fatal("contradictory raw and bench-all hashes were accepted")
	}
}

func TestPublicationRequiresCollectionGuard(t *testing.T) {
	t.Setenv(collectionGuardEnv, "")
	if publicationAuthorized() {
		t.Fatal("publication unexpectedly authorized without collection guard")
	}
	t.Setenv(collectionGuardEnv, "1")
	if !publicationAuthorized() {
		t.Fatal("publication guard was not recognized")
	}
}

func TestLoadCollectionMetadataRejectsGOFLAGS(t *testing.T) {
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "-tags=zerodecimal_strcache")
	t.Setenv("GOWORK", "off")
	raw := filepath.Join(t.TempDir(), "bench-all.txt")
	sourceHash := strings.Repeat("b", 64)
	if err := os.WriteFile(raw, []byte("benchmark-source-sha256: "+sourceHash+"\ngoos: darwin\ngoarch: arm64\ncpu: Test CPU\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCollectionMetadata(raw, "100ms", 10, strings.Repeat("a", 40), sourceHash, "clean", "make collect", "golang.org/x/perf v1.2.3"); err == nil || !strings.Contains(err.Error(), "GOFLAGS") {
		t.Fatalf("error = %v, want GOFLAGS rejection", err)
	}
}

func TestLoadCollectionMetadataRejectsGOENV(t *testing.T) {
	t.Setenv("GOENV", filepath.Join(t.TempDir(), "goenv"))
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	raw := filepath.Join(t.TempDir(), "bench-all.txt")
	sourceHash := strings.Repeat("b", 64)
	if err := os.WriteFile(raw, []byte("benchmark-source-sha256: "+sourceHash+"\ngoos: darwin\ngoarch: arm64\ncpu: Test CPU\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCollectionMetadata(raw, "100ms", 10, strings.Repeat("a", 40), sourceHash, "clean", "make collect", "golang.org/x/perf v1.2.3"); err == nil || !strings.Contains(err.Error(), "GOENV") {
		t.Fatalf("error = %v, want GOENV rejection", err)
	}
}

func TestLoadCollectionMetadataRejectsGOWORK(t *testing.T) {
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", filepath.Join(t.TempDir(), "go.work"))
	raw := filepath.Join(t.TempDir(), "bench-all.txt")
	sourceHash := strings.Repeat("b", 64)
	if err := os.WriteFile(raw, []byte("benchmark-source-sha256: "+sourceHash+"\ngoos: darwin\ngoarch: arm64\ncpu: Test CPU\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCollectionMetadata(raw, "100ms", 10, strings.Repeat("a", 40), sourceHash, "clean", "make collect", "golang.org/x/perf v1.2.3"); err == nil || !strings.Contains(err.Error(), "GOWORK") {
		t.Fatalf("error = %v, want GOWORK rejection", err)
	}
}

func TestLoadCollectionMetadataRejectsInvalidInputs(t *testing.T) {
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
	raw := filepath.Join(t.TempDir(), "bench-all.txt")
	validSourceHash := strings.Repeat("b", 64)
	if err := os.WriteFile(raw, []byte("benchmark-source-sha256: "+validSourceHash+"\ngoos: darwin\ngoarch: arm64\ncpu: Test CPU\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, benchTime, revision, sourceHash, worktree, command string
		count                                                    int
	}{
		{name: "duration", benchTime: "bad", count: 10, revision: strings.Repeat("a", 40), sourceHash: validSourceHash, worktree: "clean", command: "make collect"},
		{name: "count", benchTime: "100ms", count: 1, revision: strings.Repeat("a", 40), sourceHash: validSourceHash, worktree: "clean", command: "make collect"},
		{name: "revision", benchTime: "100ms", count: 10, revision: "bad", sourceHash: validSourceHash, worktree: "clean", command: "make collect"},
		{name: "revision_hex", benchTime: "100ms", count: 10, revision: strings.Repeat("z", 40), sourceHash: validSourceHash, worktree: "clean", command: "make collect"},
		{name: "source_hash", benchTime: "100ms", count: 10, revision: strings.Repeat("a", 40), sourceHash: "bad", worktree: "clean", command: "make collect"},
		{name: "source_hash_hex", benchTime: "100ms", count: 10, revision: strings.Repeat("a", 40), sourceHash: strings.Repeat("z", 64), worktree: "clean", command: "make collect"},
		{name: "source_binding", benchTime: "100ms", count: 10, revision: strings.Repeat("a", 40), sourceHash: strings.Repeat("c", 64), worktree: "clean", command: "make collect"},
		{name: "worktree", benchTime: "100ms", count: 10, revision: strings.Repeat("a", 40), sourceHash: validSourceHash, worktree: "unknown", command: "make collect"},
		{name: "dirty", benchTime: "100ms", count: 10, revision: strings.Repeat("a", 40), sourceHash: validSourceHash, worktree: "dirty", command: "make collect"},
		{name: "command", benchTime: "100ms", count: 10, revision: strings.Repeat("a", 40), sourceHash: validSourceHash, worktree: "clean"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := loadCollectionMetadata(raw, tc.benchTime, tc.count, tc.revision, tc.sourceHash, tc.worktree, tc.command, "golang.org/x/perf v1.2.3"); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestGOENVOffIgnoresPersistedGOFLAGS(t *testing.T) {
	goenvFile := filepath.Join(t.TempDir(), "goenv")
	if err := os.WriteFile(goenvFile, []byte("GOFLAGS=-tags=zerodecimal_strcache\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	baseEnv := make([]string, 0, len(os.Environ()))
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GOENV=") && !strings.HasPrefix(value, "GOFLAGS=") {
			baseEnv = append(baseEnv, value)
		}
	}
	persisted := exec.Command("go", "env", "GOFLAGS")
	persisted.Env = slices.Clone(baseEnv)
	persisted.Env = append(persisted.Env, "GOENV="+goenvFile)
	got, err := persisted.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "-tags=zerodecimal_strcache" {
		t.Fatalf("persisted GOFLAGS = %q", got)
	}
	off := exec.Command("go", "env", "GOFLAGS")
	off.Env = slices.Clone(baseEnv)
	off.Env = append(off.Env, "GOENV=off", "GOFLAGS=")
	got, err = off.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != "" {
		t.Fatalf("GOENV=off GOFLAGS = %q, want empty", got)
	}
}

func TestPublishedInputHashes(t *testing.T) {
	dir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()
	sourceHash := strings.Repeat("d", 64)
	for _, file := range publishedInputs {
		data := []byte(file)
		if file == "bench-all.txt" {
			data = []byte("benchmark-source-sha256: " + sourceHash + "\n" + file)
		}
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range pgoSourceInputs {
		if err := os.WriteFile(file, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := collectionMetadata{sourceHash: sourceHash}
	if err := bindPublishedInputs(&m, "bench-all.txt"); err != nil {
		t.Fatal(err)
	}
	if err := validatePublishedInputs(m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publishedInputs[0], []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validatePublishedInputs(m); err == nil {
		t.Fatal("expected changed artifact hash to fail validation")
	}
	if err := os.WriteFile(publishedInputs[0], []byte(publishedInputs[0]), 0o600); err != nil {
		t.Fatal(err)
	}

	// Simulate internally consistent artifact hashes around a bench-all file
	// whose embedded source identity disagrees with the provenance identity.
	otherSourceHash := strings.Repeat("e", 64)
	if err := os.WriteFile("bench-all.txt", []byte("benchmark-source-sha256: "+otherSourceHash+"\nbench-all.txt"), 0o600); err != nil {
		t.Fatal(err)
	}
	benchAllHash, err := fileSHA256("bench-all.txt")
	if err != nil {
		t.Fatal(err)
	}
	m.rawSHA256 = benchAllHash
	m.artifactSHA256["bench-all.txt"] = benchAllHash
	err = validatePublishedInputs(m)
	if err == nil || !strings.Contains(err.Error(), "embedded benchmark source hash") {
		t.Fatalf("error = %v, want embedded source/provenance mismatch", err)
	}
}

func TestEmbeddedBenchmarkSourceHashRequiresFirstLine(t *testing.T) {
	want := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name    string
		data    string
		wantErr bool
	}{
		{name: "valid", data: "benchmark-source-sha256: " + want + "\ngoos: darwin\n"},
		{name: "valid_crlf", data: "benchmark-source-sha256: " + want + "\r\ngoos: windows\r\n"},
		{name: "missing", data: "goos: darwin\n", wantErr: true},
		{name: "not_first", data: "goos: darwin\nbenchmark-source-sha256: " + want + "\n", wantErr: true},
		{name: "invalid", data: "benchmark-source-sha256: bad\n", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := embeddedBenchmarkSourceHash("bench-all.txt", []byte(tc.data))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("hash = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("hash = %q, want %q", got, want)
			}
		})
	}
}
