// Package provenance defines the source identity used by the comparative
// benchmark publication pipeline.
package provenance

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// SourcePathspecs is the complete Git pathspec whose contents can affect the
// comparative benchmark results or their publication. Keep this list aligned
// with the collection workflow by having the workflow call SourceHash instead
// of spelling out a second copy.
var SourcePathspecs = []string{
	"*.go",
	".gitignore",
	"go.mod",
	"go.sum",
	"benchmarks/go.mod",
	"benchmarks/go.sum",
	"benchmarks/Makefile",
	".github/workflows/benchmarks.yaml",
}

type trackedFile struct {
	mode string
	path string
}

// SourceHash returns a history-independent SHA-256 identity for the tracked
// benchmark-source files in repoRoot. It hashes each path, Git mode, and the
// current worktree content with unambiguous length framing. Commit IDs are
// deliberately excluded so an unchanged source tree retains its identity
// across rebases and squash merges.
func SourceHash(repoRoot string) (string, error) {
	files, err := trackedSourceFiles(repoRoot)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", errors.New("benchmark source pathspec matched no tracked files")
	}

	h := sha256.New()
	_, _ = h.Write([]byte("zerodecimal-benchmark-source-v1\x00"))
	for _, file := range files {
		data, readErr := readTrackedFile(repoRoot, file)
		if readErr != nil {
			return "", readErr
		}
		writeFrame(h, []byte(file.mode))
		writeFrame(h, []byte(file.path))
		writeFrame(h, data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type frameWriter interface {
	Write([]byte) (int, error)
}

func writeFrame(w frameWriter, data []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(data)))
	_, _ = w.Write(size[:])
	_, _ = w.Write(data)
}

func trackedSourceFiles(repoRoot string) ([]trackedFile, error) {
	args := []string{"-C", repoRoot, "ls-files", "--stage", "-z", "--"}
	args = append(args, SourcePathspecs...)
	output, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("list benchmark source files: %w", err)
	}

	records := strings.Split(string(output), "\x00")
	files := make([]trackedFile, 0, len(records))
	for _, record := range records {
		if record == "" {
			continue
		}
		metadata, path, ok := strings.Cut(record, "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 || fields[2] != "0" || path == "" {
			return nil, fmt.Errorf("invalid git ls-files record %q", record)
		}
		files = append(files, trackedFile{mode: fields[0], path: path})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	for i := 1; i < len(files); i++ {
		if files[i-1].path == files[i].path {
			return nil, fmt.Errorf("duplicate benchmark source path %q", files[i].path)
		}
	}
	return files, nil
}

func readTrackedFile(repoRoot string, file trackedFile) ([]byte, error) {
	path := filepath.Join(repoRoot, filepath.FromSlash(file.path))
	if file.mode == "120000" {
		target, err := os.Readlink(path)
		if err != nil {
			return nil, fmt.Errorf("read benchmark source symlink %s: %w", file.path, err)
		}
		return []byte(target), nil
	}
	if file.mode == "160000" {
		return nil, fmt.Errorf("benchmark source path %s is a submodule", file.path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read benchmark source %s: %w", file.path, err)
	}
	return data, nil
}
