// Command benchfilter extracts one library from a unified Go benchmark run,
// or restricts an input to benchmark names present in a reference file.
// Keeping the latter intersection explicit prevents benchstat geomeans from
// combining different row sets when a competitor omits an unsupported API or
// numeric shape.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	input := flag.String("input", "", "Go benchmark file to filter")
	library := flag.String("library", "", "library name segment to extract and remove")
	reference := flag.String("reference", "", "keep only benchmark names present in this file")
	count := flag.Int("count", 0, "require exactly this many samples per emitted benchmark name")
	flag.Parse()
	if *input == "" || (*library == "" && *reference == "") || (*library != "" && *reference != "") {
		fmt.Fprintln(os.Stderr, "usage: benchfilter -input FILE (-library NAME | -reference FILE)")
		os.Exit(2)
	}

	in, err := os.Open(*input)
	if err != nil {
		fail(err)
	}
	defer in.Close()

	var names map[string]int
	if *reference != "" {
		names, err = benchmarkNames(*reference)
		if err != nil {
			fail(err)
		}
	}
	if err := filter(os.Stdout, in, *library, names, *count); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "benchfilter:", err)
	os.Exit(1)
}

func benchmarkNames(path string) (map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	names := make(map[string]int)
	s := bufio.NewScanner(f)
	for s.Scan() {
		if name, ok := benchmarkName(s.Text()); ok {
			names[name]++
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%s: no benchmark results", path)
	}
	return names, nil
}

func filter(w io.Writer, r io.Reader, library string, names map[string]int, expectedCount int) error {
	if expectedCount < 0 {
		return fmt.Errorf("sample count must not be negative")
	}
	emitted := make(map[string]int)
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := s.Text()
		name, benchmark := benchmarkName(line)
		if benchmark {
			switch {
			case library != "":
				segment := "/" + library + "/"
				if !strings.Contains(name, segment) {
					continue
				}
				line = strings.Replace(line, segment, "/", 1)
				name = strings.Replace(name, segment, "/", 1)
			case names != nil:
				if _, ok := names[name]; !ok {
					continue
				}
			}
			emitted[name]++
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	if len(emitted) == 0 {
		return fmt.Errorf("no benchmark results emitted")
	}
	for name, want := range names {
		if got := emitted[name]; got != want {
			return fmt.Errorf("benchmark %s sample count = %d, want reference count %d", name, got, want)
		}
	}
	if expectedCount > 0 {
		for name, got := range emitted {
			if got != expectedCount {
				return fmt.Errorf("benchmark %s sample count = %d, want %d", name, got, expectedCount)
			}
		}
	}
	return nil
}

func benchmarkName(line string) (string, bool) {
	if !strings.HasPrefix(line, "Benchmark") {
		return "", false
	}
	name, _, ok := strings.Cut(line, "\t")
	if !ok {
		name, _, ok = strings.Cut(line, " ")
	}
	return strings.TrimSpace(name), ok
}
