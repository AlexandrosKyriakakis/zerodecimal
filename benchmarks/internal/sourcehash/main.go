// Command sourcehash prints the history-independent identity of the tracked
// source files that affect zerodecimal's comparative benchmark publication.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/AlexandrosKyriakakis/zerodecimal/benchmarks/internal/provenance"
)

func main() {
	repo := flag.String("repo", "..", "repository root")
	flag.Parse()
	hash, err := provenance.SourceHash(*repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sourcehash:", err)
		os.Exit(1)
	}
	fmt.Println(hash)
}
