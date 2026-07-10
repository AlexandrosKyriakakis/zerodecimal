package provenance

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSourceHashTracksContentNotHistoryOrUntrackedFiles(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "--quiet")
	writeFile(t, repo, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeFile(t, repo, "decimal.go", "package decimal\n")
	writeFile(t, repo, "README.md", "not benchmark source\n")
	runGit(t, repo, "add", "go.mod", "decimal.go", "README.md")

	original := mustSourceHash(t, repo)
	writeFile(t, repo, "README.md", "history-independent and excluded\n")
	writeFile(t, repo, "untracked.go", "package ignored_until_tracked\n")
	if got := mustSourceHash(t, repo); got != original {
		t.Fatalf("excluded/untracked changes changed source hash: got %s, want %s", got, original)
	}

	writeFile(t, repo, "decimal.go", "package decimal\n\nconst changed = true\n")
	changed := mustSourceHash(t, repo)
	if changed == original {
		t.Fatal("tracked source content change did not change source hash")
	}

	runGit(t, repo, "add", "untracked.go")
	if got := mustSourceHash(t, repo); got == changed {
		t.Fatal("newly tracked source file did not change source hash")
	}
}

func TestSourceHashSurvivesHistoryRewrite(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "--quiet")
	runGit(t, repo, "config", "user.name", "Benchmark Test")
	runGit(t, repo, "config", "user.email", "benchmark@example.invalid")
	writeFile(t, repo, "go.mod", "module example.com/test\n\ngo 1.26\n")
	writeFile(t, repo, "decimal.go", "package decimal\n")
	runGit(t, repo, "add", "go.mod", "decimal.go")
	runGit(t, repo, "commit", "--quiet", "-m", "original history")

	originalCommit := gitOutput(t, repo, "rev-parse", "HEAD")
	originalHash := mustSourceHash(t, repo)
	runGit(t, repo, "commit", "--quiet", "--amend", "-m", "rewritten history")
	rewrittenCommit := gitOutput(t, repo, "rev-parse", "HEAD")
	if rewrittenCommit == originalCommit {
		t.Fatal("commit rewrite did not change the commit identity")
	}
	if got := mustSourceHash(t, repo); got != originalHash {
		t.Fatalf("history rewrite changed source hash: got %s, want %s", got, originalHash)
	}
}

func TestSourceHashIncludesPathAndMode(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "--quiet")
	writeFile(t, repo, "one.go", "package one\n")
	runGit(t, repo, "add", "one.go")
	original := mustSourceHash(t, repo)

	if err := os.Chmod(filepath.Join(repo, "one.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "one.go")
	modeChanged := mustSourceHash(t, repo)
	if modeChanged == original {
		t.Fatal("tracked executable mode change did not change source hash")
	}

	runGit(t, repo, "mv", "one.go", "two.go")
	if got := mustSourceHash(t, repo); got == modeChanged {
		t.Fatal("tracked source rename did not change source hash")
	}
}

func TestSourceHashRejectsEmptyTrackedSet(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "--quiet")
	if _, err := SourceHash(repo); err == nil {
		t.Fatal("expected empty tracked source set to fail")
	}
}

func mustSourceHash(t *testing.T, repo string) string {
	t.Helper()
	hash, err := SourceHash(repo)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func writeFile(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func gitOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}
