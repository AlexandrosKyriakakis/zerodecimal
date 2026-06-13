// Command chartgen renders benchmarks/comparison.svg: a horizontal bar chart
// of each decimal library's geomean latency (ns/op), zerodecimal highlighted.
//
// It reads the committed bench-vs-<lib>.txt benchstat files, so the chart is
// regenerated from the published numbers rather than hand-drawn. Run it from
// the benchmarks module root (the Makefile `chart` target does this):
//
//	go run ./internal/chartgen
//
// zerodecimal's own bar is taken from the dec128 comparison (the full
// five-shape run); each competitor's bar is its own geomean column.
package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
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

// geomeans returns the competitor and zerodecimal geomean ns/op from the
// first (sec/op) geomean line of a bench-vs file.
func geomeans(file string) (comp, zd float64, err error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return 0, 0, err
	}
	inSec := false
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.Contains(line, "sec/op") {
			inSec = true
			continue
		}
		if inSec && strings.HasPrefix(line, "geomean") {
			f := strings.Fields(line)
			if len(f) < 3 {
				return 0, 0, fmt.Errorf("%s: short geomean line %q", file, line)
			}
			if comp, err = parseNs(f[1]); err != nil {
				return 0, 0, fmt.Errorf("%s: %w", file, err)
			}
			if zd, err = parseNs(f[2]); err != nil {
				return 0, 0, fmt.Errorf("%s: %w", file, err)
			}
			return comp, zd, nil
		}
	}
	return 0, 0, fmt.Errorf("%s: no sec/op geomean line", file)
}

// parseNs converts a benchstat duration token (e.g. "13.15n", "1.20µ") to
// nanoseconds.
func parseNs(tok string) (float64, error) {
	scale := 1.0
	switch {
	case strings.HasSuffix(tok, "n"):
		tok = strings.TrimSuffix(tok, "n")
	case strings.HasSuffix(tok, "µ"), strings.HasSuffix(tok, "u"):
		tok, scale = strings.TrimRight(tok, "µu"), 1e3
	case strings.HasSuffix(tok, "m"):
		tok, scale = strings.TrimSuffix(tok, "m"), 1e6
	case strings.HasSuffix(tok, "s"):
		tok, scale = strings.TrimSuffix(tok, "s"), 1e9
	}
	v, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", tok, err)
	}
	return v * scale, nil
}

type bar struct {
	name string
	ns   float64
	self bool
}

func main() {
	var bars []bar
	var zdNs float64
	for _, l := range libs {
		comp, zd, err := geomeans(l.file)
		if err != nil {
			fmt.Fprintln(os.Stderr, "chartgen:", err)
			os.Exit(1)
		}
		bars = append(bars, bar{name: l.name, ns: comp})
		if l.name == "dec128" { // the full five-shape run; canonical zd geomean
			zdNs = zd
		}
	}
	bars = append(bars, bar{name: "zerodecimal", ns: zdNs, self: true})

	sort.Slice(bars, func(i, j int) bool { return bars[i].ns < bars[j].ns })

	const out = "comparison.svg"
	if err := os.WriteFile(out, []byte(render(bars)), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "chartgen:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", out)
}

// render builds the SVG. Layout: a label column, a proportional bar, and a
// value label, one row per library, on a self-contained white card.
func render(bars []bar) string {
	const (
		width    = 760
		labelW   = 150
		valueW   = 64
		padX     = 16
		rowH     = 36
		barH     = 20
		titleH   = 64
		footH    = 30
		barX     = labelW
		barMax   = width - labelW - valueW - padX
		accent = "#2da44e"
		gray   = "#afb8c1"
		text   = "#1f2328"
		muted  = "#57606a"
	)
	font := "-apple-system,Segoe UI,Helvetica,Arial,sans-serif"
	height := titleH + len(bars)*rowH + footH

	var maxNs float64
	for _, b := range bars {
		if b.ns > maxNs {
			maxNs = b.ns
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="%s">`+"\n", width, height, width, height, font)
	fmt.Fprintf(&sb, `<rect width="%d" height="%d" rx="8" fill="#ffffff" stroke="#d0d7de"/>`+"\n", width, height)
	fmt.Fprintf(&sb, `<text x="%d" y="28" font-size="16" font-weight="700" fill="%s">zerodecimal vs other Go decimal libraries</text>`+"\n", padX, text)
	fmt.Fprintf(&sb, `<text x="%d" y="48" font-size="12" fill="%s">geomean latency, ns/op (shorter is faster) — Apple M1 Pro, Go 1.26, count=10</text>`+"\n", padX, muted)

	for i, b := range bars {
		y := titleH + i*rowH
		barY := y + (rowH-barH)/2
		w := 2.0
		if maxNs > 0 {
			w = b.ns / maxNs * float64(barMax)
		}
		fill, weight, lblFill := gray, "400", text
		if b.self {
			fill, weight, lblFill = accent, "700", accent
		}
		fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="12.5" font-weight="%s" fill="%s" text-anchor="end">%s</text>`+"\n",
			labelW-10, barY+barH-6, weight, lblFill, b.name)
		fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="%.1f" height="%d" rx="3" fill="%s"/>`+"\n", barX, barY, w, barH, fill)
		fmt.Fprintf(&sb, `<text x="%.1f" y="%d" font-size="12" font-weight="%s" fill="%s">%s</text>`+"\n",
			float64(barX)+w+6, barY+barH-6, weight, lblFill, fmtNs(b.ns))
	}

	fmt.Fprintf(&sb, `<text x="%d" y="%d" font-size="10.5" fill="%s">govalues geomean is over the three shapes it can represent (≤19 significant digits); all others over five.</text>`+"\n",
		padX, height-10, muted)
	sb.WriteString("</svg>\n")
	return sb.String()
}

func fmtNs(ns float64) string {
	if ns < 100 {
		return strconv.FormatFloat(ns, 'f', 1, 64) + " ns"
	}
	return strconv.FormatFloat(ns, 'f', 0, 64) + " ns"
}
