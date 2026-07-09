// Command chartgen renders comparison-light.svg and comparison-dark.svg: a
// horizontal bar chart of each decimal library's pairwise geomean latency
// relative to zerodecimal, with zerodecimal (and zerodecimal+PGO) highlighted.
// The two transparent variants
// are swapped by prefers-color-scheme via a <picture> element in the README,
// so the chart matches GitHub's light/dark theme.
//
// It reads the committed bench-vs-<lib>.txt and bench-pgo.txt benchstat files,
// so the chart is regenerated from the published numbers rather than
// hand-drawn. Run it from the benchmarks module root (the Makefile `chart`
// target does this):
//
//	make chart
//
// Pairwise ratios, rather than raw absolute geomeans across files, are required
// because libraries with a smaller numeric domain or API surface have fewer
// rows. The Makefile filters zerodecimal to each competitor's exact supported
// row set before benchstat, so each summary compares like with like. The PGO
// bar is the pgo/default summary ratio from bench-pgo.txt.
package main

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// lib pairs a short display name with its bench-vs file. Order here is only
// the read order; bars are sorted by latency before drawing.
type lib struct {
	name string
	file string
}

var libs = []lib{
	{"dec128", "bench-vs-dec128.txt"},
	{"udecimal", "bench-vs-udecimal.txt"},
	{"govalues", "bench-vs-govalues.txt"},
	{"ericlagergren", "bench-vs-ericlagergren.txt"},
	{"alpacadecimal", "bench-vs-alpacadecimal.txt"},
	{"shopspring", "bench-vs-shopspring.txt"},
}

var publishedInputs = []string{
	"bench-vs-dec128.txt",
	"bench-vs-udecimal.txt",
	"bench-vs-govalues.txt",
	"bench-vs-ericlagergren.txt",
	"bench-vs-alpacadecimal.txt",
	"bench-vs-shopspring.txt",
	"bench-pgo.txt",
	"bench-all.txt",
	"bench-zd-pgo-default.txt",
	"bench-zd-pgo.txt",
}

var pgoSourceInputs = []string{
	"zd.pprof",
	"bench-zd-pgo-default-raw.txt",
	"bench-zd-pgo-raw.txt",
}

// geomeanRatio returns new/base from the first sec/op geomean line in a
// benchstat comparison. Comparison generation guarantees the two columns have
// identical row sets; the displayed summary percentage is therefore their
// pairwise relative geomean.
func geomeanRatio(file string) (float64, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	return geomeanRatioData(file, string(data))
}

func geomeanRatioData(name, data string) (float64, error) {
	inSec := false
	for line := range strings.SplitSeq(data, "\n") {
		if strings.Contains(line, "sec/op") {
			inSec = true
			continue
		}
		if inSec && strings.HasPrefix(line, "geomean") {
			f := strings.Fields(line)
			if len(f) < 4 {
				return 0, fmt.Errorf("%s: short geomean line %q", name, line)
			}
			change := f[3]
			if change == "~" {
				return 1, nil
			}
			if !strings.HasSuffix(change, "%") {
				return 0, fmt.Errorf("%s: invalid geomean change %q", name, change)
			}
			pct, err := strconv.ParseFloat(strings.TrimSuffix(change, "%"), 64)
			if err != nil {
				return 0, fmt.Errorf("%s: parse geomean change %q: %w", name, change, err)
			}
			ratio := 1 + pct/100
			if ratio <= 0 {
				return 0, fmt.Errorf("%s: non-positive geomean ratio from %q", name, change)
			}
			return ratio, nil
		}
	}
	return 0, fmt.Errorf("%s: no sec/op geomean line", name)
}

type bar struct {
	name  string
	ratio float64
	self  bool // zerodecimal (default build)
	pgo   bool // zerodecimal rebuilt with PGO
}

type collectionMetadata struct {
	goos, goarch, cpu string
	goVersion         string
	benchstatVersion  string
	goenv             string
	goexperiment      string
	gomaxprocs        string
	gogc              string
	gomemlimit        string
	godebug           string
	benchTime         string
	count             int
	revision          string
	worktree          string
	command           string
	rawSHA256         string
	artifactSHA256    map[string]string
	pgoSourceSHA256   map[string]string
}

func main() {
	publish := flag.Bool("publish", false, "publish collection provenance before rendering charts")
	rawFile := flag.String("raw", "bench-all.txt", "unified raw benchmark output")
	benchTime := flag.String("benchtime", "", "per-sample benchmark duration")
	count := flag.Int("count", 0, "samples per benchmark row")
	revision := flag.String("revision", "", "source commit used for collection")
	worktree := flag.String("worktree", "", "source worktree state at collection start (clean or dirty)")
	command := flag.String("command", "", "exact top-level collection command")
	flag.Parse()

	var metadata collectionMetadata
	var err error
	if *publish {
		benchstatVersion, versionErr := installedBenchstatVersion()
		if versionErr != nil {
			fail(versionErr)
		}
		metadata, err = loadCollectionMetadata(*rawFile, *benchTime, *count, *revision, *worktree, *command, benchstatVersion)
		if err != nil {
			fail(err)
		}
		if err = bindPublishedInputs(&metadata, *rawFile); err != nil {
			fail(err)
		}
		//nolint:gosec // G306: 0644 is intentional for a public provenance artifact.
		if err = os.WriteFile("benchmark-provenance.txt", []byte(renderProvenance(metadata)), 0o644); err != nil {
			fail(err)
		}
		fmt.Println("wrote benchmark-provenance.txt")
	} else {
		metadata, err = readProvenance("benchmark-provenance.txt")
		if err != nil {
			fail(err)
		}
		if err = validatePublishedInputs(metadata); err != nil {
			fail(err)
		}
	}

	var bars []bar
	for _, l := range libs {
		zdToCompetitor, ratioErr := geomeanRatio(l.file)
		if ratioErr != nil {
			fmt.Fprintln(os.Stderr, "chartgen:", ratioErr)
			os.Exit(1)
		}
		bars = append(bars, bar{name: l.name, ratio: 1 / zdToCompetitor})
	}

	// PGO and default cover the same complete zerodecimal matrix.
	zdPGOToDefault, err := geomeanRatio("bench-pgo.txt")
	if err != nil {
		fmt.Fprintln(os.Stderr, "chartgen:", err)
		os.Exit(1)
	}
	bars = append(bars,
		bar{name: "zerodecimal", ratio: 1, self: true},
		bar{name: "zerodecimal +PGO", ratio: zdPGOToDefault, pgo: true},
	)

	sort.Slice(bars, func(i, j int) bool { return bars[i].ratio < bars[j].ratio })

	// Two transparent variants, swapped by prefers-color-scheme via a <picture>
	// element in the README so the chart matches GitHub's light/dark theme.
	for _, t := range themes {
		out := "comparison-" + t.name + ".svg"
		// The generated SVGs are committed documentation assets and must remain
		// readable by users other than the account that regenerated them.
		//nolint:gosec // G306: 0644 is intentional for public repository assets.
		if err := os.WriteFile(out, []byte(render(bars, t, metadata)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "chartgen:", err)
			os.Exit(1)
		}
		fmt.Println("wrote", out)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "chartgen:", err)
	os.Exit(1)
}

func loadCollectionMetadata(rawFile, benchTime string, count int, revision, worktree, command, benchstatVersion string) (collectionMetadata, error) {
	var m collectionMetadata
	data, err := os.ReadFile(rawFile)
	if err != nil {
		return m, err
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		switch key {
		case "goos":
			m.goos = value
		case "goarch":
			m.goarch = value
		case "cpu":
			m.cpu = value
		}
	}
	if m.goos == "" || m.goarch == "" || m.cpu == "" {
		return m, fmt.Errorf("%s: missing GOOS, GOARCH, or CPU metadata", rawFile)
	}
	d, err := time.ParseDuration(benchTime)
	if err != nil || d <= 0 {
		return m, fmt.Errorf("invalid benchtime %q", benchTime)
	}
	if count < 2 {
		return m, fmt.Errorf("count must be at least 2, got %d", count)
	}
	if len(revision) != 40 {
		return m, fmt.Errorf("revision must be a 40-character commit SHA")
	}
	if _, err := hex.DecodeString(revision); err != nil {
		return m, fmt.Errorf("revision must be hexadecimal: %w", err)
	}
	if worktree != "clean" {
		return m, fmt.Errorf("benchmark publication requires a clean worktree, got %q", worktree)
	}
	if strings.TrimSpace(command) == "" {
		return m, fmt.Errorf("collection command is empty")
	}
	if strings.TrimSpace(benchstatVersion) == "" {
		return m, fmt.Errorf("benchstat version is empty")
	}
	if os.Getenv("GOFLAGS") != "" {
		return m, fmt.Errorf("GOFLAGS must be empty for benchmark publication")
	}
	if os.Getenv("GOENV") != "off" {
		return m, fmt.Errorf("GOENV must be off for benchmark publication")
	}
	m.goVersion = runtime.Version()
	m.benchstatVersion = benchstatVersion
	m.goenv = "off (enforced)"
	m.goexperiment = envOrUnset("GOEXPERIMENT")
	m.gomaxprocs = envOrUnset("GOMAXPROCS")
	m.gogc = envOrUnset("GOGC")
	m.gomemlimit = envOrUnset("GOMEMLIMIT")
	m.godebug = envOrUnset("GODEBUG")
	m.benchTime = d.String()
	m.count = count
	m.revision = revision
	m.worktree = worktree
	m.command = command
	return m, nil
}

func envOrUnset(key string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return "<unset>"
}

func installedBenchstatVersion() (string, error) {
	path, err := exec.LookPath("benchstat")
	if err != nil {
		return "", err
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read benchstat build info: %w", err)
	}
	version := info.Main.Path + " " + info.Main.Version
	if info.Main.Sum != "" {
		version += " " + info.Main.Sum
	}
	return version, nil
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func bindPublishedInputs(m *collectionMetadata, rawFile string) error {
	var err error
	m.rawSHA256, err = fileSHA256(rawFile)
	if err != nil {
		return fmt.Errorf("hash raw collection: %w", err)
	}
	m.artifactSHA256 = make(map[string]string, len(publishedInputs))
	for _, file := range publishedInputs {
		m.artifactSHA256[file], err = fileSHA256(file)
		if err != nil {
			return fmt.Errorf("hash %s: %w", file, err)
		}
	}
	m.pgoSourceSHA256 = make(map[string]string, len(pgoSourceInputs))
	for _, file := range pgoSourceInputs {
		m.pgoSourceSHA256[file], err = fileSHA256(file)
		if err != nil {
			return fmt.Errorf("hash %s: %w", file, err)
		}
	}
	return nil
}

func validatePublishedInputs(m collectionMetadata) error {
	for _, file := range publishedInputs {
		want, ok := m.artifactSHA256[file]
		if !ok {
			return fmt.Errorf("provenance has no hash for %s", file)
		}
		got, err := fileSHA256(file)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%s hash %s does not match provenance %s", file, got, want)
		}
	}
	return nil
}

func readProvenance(path string) (collectionMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return collectionMetadata{}, err
	}
	return parseProvenanceData(string(data))
}

func parseProvenanceData(data string) (collectionMetadata, error) {
	var m collectionMetadata
	values := make(map[string]string)
	for line := range strings.SplitSeq(data, "\n") {
		key, value, ok := strings.Cut(line, ": ")
		if ok {
			values[key] = value
		}
	}
	m.revision = values["source-commit"]
	m.worktree = values["source-worktree-at-collection-start"]
	m.goVersion = values["go-version"]
	m.goos = values["goos"]
	m.goarch = values["goarch"]
	m.cpu = values["cpu"]
	m.benchstatVersion = values["benchstat"]
	m.goenv = values["goenv"]
	m.goexperiment = values["goexperiment"]
	m.gomaxprocs = values["gomaxprocs"]
	m.gogc = values["gogc"]
	m.gomemlimit = values["gomemlimit"]
	m.godebug = values["godebug"]
	m.benchTime = values["benchtime-per-sample"]
	m.command = values["collection-command"]
	m.rawSHA256 = values["raw-collection-sha256"]
	m.count, _ = strconv.Atoi(values["samples-per-row"])
	m.artifactSHA256 = make(map[string]string, len(publishedInputs))
	for _, file := range publishedInputs {
		m.artifactSHA256[file] = values["artifact-sha256-"+file]
	}
	m.pgoSourceSHA256 = make(map[string]string, len(pgoSourceInputs))
	for _, file := range pgoSourceInputs {
		m.pgoSourceSHA256[file] = values["pgo-source-sha256-"+file]
	}
	if len(m.revision) != 40 || m.goVersion == "" || m.goos == "" || m.goarch == "" || m.cpu == "" ||
		m.benchstatVersion == "" || m.goenv != "off (enforced)" || m.goexperiment == "" || m.gomaxprocs == "" || m.gogc == "" || m.gomemlimit == "" || m.godebug == "" ||
		m.command == "" || m.count < 2 || values["goflags"] != "empty (enforced)" || values["build-tags"] != "none" ||
		!strings.HasPrefix(values["string-cache"], "off") {
		return collectionMetadata{}, fmt.Errorf("incomplete or invalid benchmark provenance")
	}
	if _, err := hex.DecodeString(m.revision); err != nil {
		return collectionMetadata{}, fmt.Errorf("invalid provenance revision: %w", err)
	}
	if m.worktree != "clean" {
		return collectionMetadata{}, fmt.Errorf("invalid provenance worktree %q", m.worktree)
	}
	if d, err := time.ParseDuration(m.benchTime); err != nil || d <= 0 {
		return collectionMetadata{}, fmt.Errorf("invalid provenance benchtime %q", m.benchTime)
	}
	if err := validateSHA256("raw collection", m.rawSHA256); err != nil {
		return collectionMetadata{}, err
	}
	for _, file := range publishedInputs {
		if err := validateSHA256(file, m.artifactSHA256[file]); err != nil {
			return collectionMetadata{}, err
		}
	}
	for _, file := range pgoSourceInputs {
		if err := validateSHA256(file, m.pgoSourceSHA256[file]); err != nil {
			return collectionMetadata{}, err
		}
	}
	return m, nil
}

func validateSHA256(name, sum string) error {
	if len(sum) != sha256.Size*2 {
		return fmt.Errorf("invalid %s SHA-256 length", name)
	}
	if _, err := hex.DecodeString(sum); err != nil {
		return fmt.Errorf("invalid %s SHA-256: %w", name, err)
	}
	return nil
}

// theme is a color set; bars and the card are transparent, so only text, the
// muted footnote/subtitle, the competitor bars, and the zerodecimal accent
// change between light and dark.
type theme struct {
	name, text, muted, bar, accent, accentPGO string
}

var themes = []theme{
	{"light", "#1f2328", "#57606a", "#afb8c1", "#2da44e", "#0969da"},
	{"dark", "#e6edf3", "#8b949e", "#484f58", "#3fb950", "#4493f8"},
}

// render builds one transparent SVG: a label column, a proportional bar, and a
// value label, one row per library. No background rect, so it blends into
// whatever page background GitHub paints in the active theme.
func render(bars []bar, t theme, m collectionMetadata) string {
	const (
		width  = 760
		labelW = 150
		valueW = 64
		padX   = 16
		rowH   = 36
		barH   = 20
		titleH = 78
		footH  = 46
		barX   = labelW
		barMax = width - labelW - valueW - padX
	)
	text, muted, gray, accent, accentPGO := t.text, t.muted, t.bar, t.accent, t.accentPGO
	font := "-apple-system,Segoe UI,Helvetica,Arial,sans-serif"
	height := titleH + len(bars)*rowH + footH

	var maxRatio float64
	for _, b := range bars {
		if b.ratio > maxRatio {
			maxRatio = b.ratio
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="%s">`+"\n", width, height, width, height, font)
	fmt.Fprintf(&sb, `<text x="%d" y="28" font-size="16" font-weight="700" fill="%s">zerodecimal vs other Go decimal libraries</text>`+"\n", padX, text)
	fmt.Fprintf(&sb, `<text x="%d" y="48" font-size="12" fill="%s">pairwise native-API geomean latency (zerodecimal = 1.0×; shorter is faster)</text>`+"\n", padX, muted)
	fmt.Fprintf(&sb, `<text x="%d" y="64" font-size="11" fill="%s">%s · %s/%s · %s · %s × %d</text>`+"\n",
		padX, muted, m.cpu, m.goos, m.goarch, m.goVersion, m.benchTime, m.count)

	for i, b := range bars {
		y := titleH + i*rowH
		barY := y + (rowH-barH)/2
		w := 2.0
		if maxRatio > 0 {
			w = b.ratio / maxRatio * float64(barMax)
		}
		fill, weight, lblFill := gray, "400", text
		switch {
		case b.self:
			fill, weight, lblFill = accent, "700", accent
		case b.pgo:
			fill, weight, lblFill = accentPGO, "700", accentPGO
		}
		fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="12.5" font-weight="%s" fill="%s" text-anchor="end">%s</text>`+"\n",
			labelW-10, barY+barH-6, weight, lblFill, b.name)
		fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="%.1f" height="%d" rx="3" fill="%s"/>`+"\n", barX, barY, w, barH, fill)
		fmt.Fprintf(&sb, `<text x="%.1f" y="%d" font-size="12" font-weight="%s" fill="%s">%s</text>`+"\n",
			float64(barX)+w+6, barY+barH-6, weight, lblFill, fmtRatio(b.ratio))
	}

	fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="10.5" fill="%s">Common successful rows only; native precision/rounding contracts may differ (see methodology).</text>`+"\n",
		padX, height-26, muted)
	fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="10.5" fill="%s">PGO is an in-sample self-profiled benchmark-binary result, not an application PGO prediction.</text>`+"\n",
		padX, height-10, muted)
	sb.WriteString("</svg>\n")
	return sb.String()
}

func renderProvenance(m collectionMetadata) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `zerodecimal comparative benchmark provenance

source-commit: %s
source-worktree-at-collection-start: %s
go-version: %s
goos: %s
goarch: %s
cpu: %s
benchstat: %s
goenv: off (enforced)
goflags: empty (enforced)
goexperiment: %s
gomaxprocs: %s
gogc: %s
gomemlimit: %s
godebug: %s
build-tags: none
string-cache: off (zerodecimal_strcache not set)
benchtime-per-sample: %s
samples-per-row: %d
collection-command: %s
collection-order: fixed library order; samples for each leaf are consecutive
pgo-profile: synthetic in-sample benchmark-binary profile; not a production application profile
raw-collection-sha256: %s
`, m.revision, m.worktree, m.goVersion, m.goos, m.goarch, m.cpu,
		m.benchstatVersion, m.goexperiment, m.gomaxprocs, m.gogc, m.gomemlimit, m.godebug,
		m.benchTime, m.count, m.command, m.rawSHA256)
	for _, file := range publishedInputs {
		fmt.Fprintf(&sb, "artifact-sha256-%s: %s\n", file, m.artifactSHA256[file])
	}
	for _, file := range pgoSourceInputs {
		fmt.Fprintf(&sb, "pgo-source-sha256-%s: %s\n", file, m.pgoSourceSHA256[file])
	}
	return sb.String()
}

func fmtRatio(ratio float64) string {
	return strconv.FormatFloat(ratio, 'f', 1, 64) + "×"
}
