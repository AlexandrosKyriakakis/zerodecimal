package zerodecimal

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBoundsCheckFreeHotPaths recompiles the package with the compiler's
// bounds-check diagnostic (-d=ssa/check_bce) and fails if any pinned hot-path
// file regains an IsInBounds or IsSliceInBounds op. The pin exists because
// the formatting and division paths rely on masked indices and restated
// invariants for their bounds-check freedom, which silently erodes under
// refactoring; parse.go, arith.go, and cache.go retain a handful of checks
// the prove pass cannot discharge and are deliberately not pinned.
func TestBoundsCheckFreeHotPaths(t *testing.T) {
	// A unique build tag defeats the build cache: cached compilations replay
	// no diagnostics, so a re-run against unchanged sources would otherwise
	// pass vacuously even after a regression. The tag matches no build
	// constraint in the package, leaving the compiled file set unchanged.
	nonce := "zerodecimal_bcepin_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	out, err := exec.Command(
		"go", "build", "-tags="+nonce, "-gcflags=-d=ssa/check_bce/debug=1", ".",
	).CombinedOutput()
	require.NoError(t, err, "go build -d=ssa/check_bce: %s", out)

	// Diagnostic lines look like "./format.go:90:6: Found IsInBounds".
	hits := map[string][]string{}
	for line := range strings.Lines(string(out)) {
		if !strings.Contains(line, "Found Is") {
			continue
		}
		file, _, ok := strings.Cut(strings.TrimPrefix(line, "./"), ":")
		require.True(t, ok, "unparsable check_bce line %q", line)
		hits[file] = append(hits[file], strings.TrimSpace(line))
	}

	tests := []struct {
		name string
		file string
	}{
		{name: "format_go", file: "format.go"},
		{name: "decimal_go", file: "decimal.go"},
		{name: "div10_go", file: "div10.go"},
		{name: "round_go", file: "round.go"},
		{name: "u128_go", file: "u128.go"},
		{name: "u256_go", file: "u256.go"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Empty(t, hits[tc.file], "bounds checks must not return to %s", tc.file)
		})
	}
}
