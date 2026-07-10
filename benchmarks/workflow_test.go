package benchmarks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFuzzWorkflowRunsTaggedRegressionTests(t *testing.T) {
	workflow := readWorkflow(t, "fuzz.yaml")
	step := workflowStep(t, workflow, "Fuzz-tag regression tests")
	want := "run: go test -tags=fuzz -run '^Test' -count=1 ./..."
	if !strings.Contains(step, want) {
		t.Fatalf("fuzz-tag regression step does not contain %q:\n%s", want, step)
	}
}

func TestBenchmarkWorkflowUploadsSuccessfulCollectionEvenWhenVerificationFails(t *testing.T) {
	workflow := readWorkflow(t, "benchmarks.yaml")
	step := workflowStep(t, workflow, "Upload raw and published benchmark evidence")
	for _, want := range []string{
		"if: always()",
		"steps.collection.outcome == 'success'",
		"uses: actions/upload-artifact@",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("benchmark artifact step does not contain %q:\n%s", want, step)
		}
	}
	if strings.Contains(step, "steps.verification.outcome") {
		t.Fatalf("benchmark artifact upload is still gated on verification:\n%s", step)
	}
}

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", ".github", "workflows", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func workflowStep(t *testing.T, workflow, name string) string {
	t.Helper()
	marker := "      - name: " + name + "\n"
	start := strings.Index(workflow, marker)
	if start < 0 {
		t.Fatalf("workflow step %q not found", name)
	}
	rest := workflow[start:]
	if end := strings.Index(rest[len(marker):], "\n      - "); end >= 0 {
		rest = rest[:len(marker)+end]
	}
	return rest
}
