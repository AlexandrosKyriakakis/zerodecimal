package benchmarks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fuzzRegressionCommand      = "go test -tags=fuzz -run '^Test' -count=1 ./..."
	benchmarkModuleTestCommand = "go test -count=1 ./..."
	benchmarkArtifactCondition = "always() && steps.changes.outputs.run == 'true' && steps.collection.outcome == 'success' && github.event.pull_request.head.repo.full_name == github.repository"
	benchmarkArtifactAction    = "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
)

func TestFuzzWorkflowRunsTaggedRegressionTests(t *testing.T) {
	workflow := readWorkflow(t, "fuzz.yaml")
	step := workflowStep(t, workflow, "Fuzz-tag regression tests")
	if err := validateFuzzRegressionStep(step); err != nil {
		t.Fatalf("invalid fuzz-tag regression step: %v:\n%s", err, step)
	}
}

func TestWorkflowStepKeepsSameIndentCommentBeforeControlField(t *testing.T) {
	const name = "Pinned step"
	workflow := "jobs:\n" +
		"  test:\n" +
		"    steps:\n" +
		"      - name: " + name + "\n" +
		"      # A YAML comment does not end the preceding step mapping.\n" +
		"        continue-on-error: true\n" +
		"        run: " + fuzzRegressionCommand + "\n" +
		"      - name: Next step\n" +
		"        run: true\n"

	step := workflowStep(t, workflow, name)
	if !strings.Contains(step, "continue-on-error: true") {
		t.Fatalf("workflow step lost a control field after a same-indent comment:\n%s", step)
	}
	err := validateRunOnlyStep(step, fuzzRegressionCommand)
	if err == nil || !strings.Contains(err.Error(), `"continue-on-error"`) {
		t.Fatalf("control field after same-indent comment produced error %v:\n%s", err, step)
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
		{"continue on error", replaceOnce(t, step, runLine, "        continue-on-error: true\n"+runLine)},
		{"custom shell", replaceOnce(t, step, runLine, "        shell: /bin/true {0}\n"+runLine)},
		{"working directory", replaceOnce(t, step, runLine, "        working-directory: benchmarks\n"+runLine)},
		{"same-indent comment before control key", replaceOnce(t, step, runLine, "      # This comment does not end the YAML step mapping.\n        continue-on-error: true\n"+runLine)},
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

func TestBenchmarkModuleWorkflowRunsTestsUncached(t *testing.T) {
	workflow := readWorkflow(t, "test.yaml")
	step := workflowStep(t, workflow, "Test benchmark fixtures and helpers")
	if err := validateRunOnlyStep(step, benchmarkModuleTestCommand); err != nil {
		t.Fatalf("invalid benchmark-module test step: %v:\n%s", err, step)
	}

	cached := replaceOnce(t, step, benchmarkModuleTestCommand, "go test ./...")
	if err := validateRunOnlyStep(cached, benchmarkModuleTestCommand); err == nil {
		t.Fatalf("cached benchmark-module test command was accepted:\n%s", cached)
	}
}

func TestBenchmarkWorkflowUploadsSuccessfulCollectionEvenWhenVerificationFails(t *testing.T) {
	workflow := readWorkflow(t, "benchmarks.yaml")
	step := workflowStep(t, workflow, "Upload raw and published benchmark evidence")
	if err := validateBenchmarkArtifactStep(step); err != nil {
		t.Fatalf("invalid benchmark artifact upload step: %v:\n%s", err, step)
	}
	for _, trailing := range []string{
		"A source-changing PR is not green merely because collection succeeded.",
		"steps.verification.outcome",
	} {
		if strings.Contains(step, trailing) {
			t.Fatalf("benchmark artifact step includes trailing material %q:\n%s", trailing, step)
		}
	}
}

func TestBenchmarkArtifactStepPinsIgnoreTrailingComments(t *testing.T) {
	const (
		name       = "Upload raw and published benchmark evidence"
		commentPin = "steps.collection.outcome == 'success'; uses: actions/upload-artifact@; steps.verification.outcome"
	)
	workflow := func(condition, uses string) string {
		return "jobs:\n" +
			"  collect:\n" +
			"    steps:\n" +
			"      - name: " + name + "\n" +
			"        if: " + condition + "\n" +
			"        uses: " + uses + "\n" +
			"\n" +
			"      # " + commentPin + "\n" +
			"      - name: Next step\n" +
			"        run: true\n"
	}

	tests := []struct {
		name      string
		condition string
		uses      string
		wantErr   bool
	}{
		{"valid step ignores forbidden trailing pin", benchmarkArtifactCondition, benchmarkArtifactAction, false},
		{"required condition only in trailing comment", "always()", benchmarkArtifactAction, true},
		{"required action only in trailing comment", benchmarkArtifactCondition, "actions/checkout@deadbeef", true},
		{"upload action without ref", benchmarkArtifactCondition, "actions/upload-artifact@", true},
		{"upload action at wrong ref", benchmarkArtifactCondition, "actions/upload-artifact@deadbeef", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := workflowStep(t, workflow(tt.condition, tt.uses), name)
			if strings.Contains(step, commentPin) {
				t.Fatalf("workflow step includes trailing comment block:\n%s", step)
			}
			err := validateBenchmarkArtifactStep(step)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateBenchmarkArtifactStep() error = %v, want error %v:\n%s", err, tt.wantErr, step)
			}
		})
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
	marker := "      - name: " + name
	lines := strings.Split(workflow, "\n")
	start := -1
	for i, line := range lines {
		if line != marker {
			continue
		}
		if start >= 0 {
			t.Fatalf("workflow step %q is not unique", name)
		}
		start = i
	}
	if start < 0 {
		t.Fatalf("workflow step %q not found", name)
	}
	lines = lines[start:]
	stepIndent := leadingSpaces(lines[0])
	end := len(lines)
	// Blank and comment-only lines do not end a YAML mapping. Find the next
	// semantic line at the step's indentation (normally the next list item),
	// then trim comments that precede that boundary so they cannot satisfy pins.
	// A same-indent comment followed by another indented field remains internal
	// to this step and therefore cannot hide a control key from validation.
	for i := 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if leadingSpaces(lines[i]) <= stepIndent {
			end = i
			break
		}
	}
	for end > 1 {
		trimmed := strings.TrimSpace(lines[end-1])
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		end--
	}
	return strings.Join(lines[:end], "\n")
}

func validateFuzzRegressionStep(step string) error {
	return validateRunOnlyStep(step, fuzzRegressionCommand)
}

func validateRunOnlyStep(step, command string) error {
	fields, err := workflowStepFields(step)
	if err != nil {
		return err
	}
	for key := range fields {
		if key != "run" {
			return fmt.Errorf("step-level %q key is not allowed", key)
		}
	}
	runs := fields["run"]
	if len(runs) != 1 {
		return fmt.Errorf("expected exactly one step-level run key, got %d", len(runs))
	}
	if got := strings.TrimSpace(runs[0]); got != command {
		return fmt.Errorf("run command = %q, want exact scalar %q", got, command)
	}
	return nil
}

func validateBenchmarkArtifactStep(step string) error {
	fields, err := workflowStepFields(step)
	if err != nil {
		return err
	}
	conditions := fields["if"]
	if len(conditions) != 1 {
		return fmt.Errorf("expected exactly one step-level if key, got %d", len(conditions))
	}
	if got := strings.TrimSpace(conditions[0]); got != benchmarkArtifactCondition {
		return fmt.Errorf("if condition = %q, want %q", got, benchmarkArtifactCondition)
	}
	uses := fields["uses"]
	if len(uses) != 1 {
		return fmt.Errorf("expected exactly one step-level uses key, got %d", len(uses))
	}
	if got := yamlScalarValue(uses[0]); got != benchmarkArtifactAction {
		return fmt.Errorf("uses action = %q, want %q", got, benchmarkArtifactAction)
	}
	return nil
}

func yamlScalarValue(value string) string {
	value = strings.TrimSpace(value)
	if scalar, _, ok := strings.Cut(value, " #"); ok {
		return strings.TrimSpace(scalar)
	}
	return value
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
		if trimmed := strings.TrimSpace(line); trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
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

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

func replaceOnce(t *testing.T, input, old, replacement string) string {
	t.Helper()
	if count := strings.Count(input, old); count != 1 {
		t.Fatalf("mutation target %q occurs %d times in workflow step", old, count)
	}
	return strings.Replace(input, old, replacement, 1)
}
