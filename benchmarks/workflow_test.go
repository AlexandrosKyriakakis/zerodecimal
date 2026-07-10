package benchmarks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fuzzRegressionCommand = "go test -tags=fuzz -run '^Test' -count=1 ./..."

func TestFuzzWorkflowRunsTaggedRegressionTests(t *testing.T) {
	workflow := readWorkflow(t, "fuzz.yaml")
	step := workflowStep(t, workflow, "Fuzz-tag regression tests")
	if err := validateFuzzRegressionStep(step); err != nil {
		t.Fatalf("invalid fuzz-tag regression step: %v:\n%s", err, step)
	}
}

func TestFuzzWorkflowRegressionStepRejectsMutations(t *testing.T) {
	workflow := readWorkflow(t, "fuzz.yaml")
	step := workflowStep(t, workflow, "Fuzz-tag regression tests")
	runLine := "        run: " + fuzzRegressionCommand

	tests := []struct {
		name string
		step string
	}{
		{"literal false guard", replaceOnce(t, step, runLine, "        if: false\n"+runLine)},
		{"expression guard", replaceOnce(t, step, runLine, "        if: ${{ github.event_name == 'pull_request' }}\n"+runLine)},
		{"changed tags", replaceOnce(t, step, fuzzRegressionCommand, "go test -run '^Test' -count=1 ./...")},
		{"extra command", replaceOnce(t, step, fuzzRegressionCommand, fuzzRegressionCommand+" && echo done")},
		{"block scalar", replaceOnce(t, step, runLine, "        run: |\n          "+fuzzRegressionCommand)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateFuzzRegressionStep(tt.step); err == nil {
				t.Fatalf("mutated fuzz-tag regression step was accepted:\n%s", tt.step)
			}
		})
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

func validateFuzzRegressionStep(step string) error {
	fields, err := workflowStepFields(step)
	if err != nil {
		return err
	}
	if _, ok := fields["if"]; ok {
		return fmt.Errorf("step-level if key is not allowed")
	}
	runs := fields["run"]
	if len(runs) != 1 {
		return fmt.Errorf("expected exactly one step-level run key, got %d", len(runs))
	}
	if got := strings.TrimSpace(runs[0]); got != fuzzRegressionCommand {
		return fmt.Errorf("run command = %q, want exact scalar %q", got, fuzzRegressionCommand)
	}
	return nil
}

func workflowStepFields(step string) (map[string][]string, error) {
	lines := strings.Split(step, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, fmt.Errorf("empty workflow step")
	}
	stepIndent := len(lines[0]) - len(strings.TrimLeft(lines[0], " "))
	fieldIndent := stepIndent + 2
	fields := make(map[string][]string)
	for _, line := range lines[1:] {
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent != fieldIndent {
			continue
		}
		entry := line[fieldIndent:]
		colon := strings.IndexByte(entry, ':')
		if colon < 1 {
			continue
		}
		key := strings.TrimSpace(entry[:colon])
		if key == "'if'" || key == `"if"` {
			key = "if"
		}
		fields[key] = append(fields[key], entry[colon+1:])
	}
	return fields, nil
}

func replaceOnce(t *testing.T, input, old, replacement string) string {
	t.Helper()
	if count := strings.Count(input, old); count != 1 {
		t.Fatalf("mutation target %q occurs %d times in workflow step", old, count)
	}
	return strings.Replace(input, old, replacement, 1)
}
