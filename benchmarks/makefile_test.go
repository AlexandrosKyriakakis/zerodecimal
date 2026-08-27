package benchmarks

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPublishCannotRunStandaloneOrRebindExistingArtifacts(t *testing.T) {
	rawBefore, err := os.ReadFile("bench-all.txt")
	if err != nil {
		t.Fatal(err)
	}
	provenanceBefore, err := os.ReadFile("benchmark-provenance.txt")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "--no-print-directory", "publish")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("make publish unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "use make collect") {
		t.Fatalf("make publish output = %q, want make collect guidance", output)
	}
	rawAfter, readErr := os.ReadFile("bench-all.txt")
	if readErr != nil {
		t.Fatal(readErr)
	}
	provenanceAfter, readErr := os.ReadFile("benchmark-provenance.txt")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(rawAfter, rawBefore) || !bytes.Equal(provenanceAfter, provenanceBefore) {
		t.Fatal("standalone publication modified or rebound existing benchmark artifacts")
	}
}

func TestPrivateCollectRequiresInternalGates(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "inactive", args: []string{"_collect", "MAKE=true"}, want: "_collect is internal"},
		{name: "missing_source_hash", args: []string{"_collect", "MAKE=true", "BENCH_COLLECTION_ACTIVE=1"}, want: "invalid captured benchmark source hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--no-print-directory"}, tc.args...)
			command := exec.Command("make", args...)
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("make %v unexpectedly succeeded:\n%s", tc.args, output)
			}
			if !strings.Contains(string(output), tc.want) {
				t.Fatalf("make %v output = %q, want %q", tc.args, output, tc.want)
			}
		})
	}
}

func TestPrivateCollectDoesNotTrustMAKEOverride(t *testing.T) {
	validHash := strings.Repeat("a", 64)
	command := exec.Command("make", "--no-print-directory", "-n", "_collect",
		"BENCH_COLLECTION_ACTIVE=1", "BENCH_SOURCE_HASH_CAPTURED="+validHash, "MAKE=true")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run private collection: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "make --no-print-directory bench-all") || strings.Contains(text, "true bench-all") {
		t.Fatalf("recursive collection can be bypassed through MAKE override:\n%s", text)
	}
}
