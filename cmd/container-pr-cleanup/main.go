// Command container-pr-cleanup removes preview images belonging to closed PRs.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tuxcrate/github-actions/internal/prcleanup"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "report deletions without changing GHCR")
	flag.Parse()
	if err := prcleanup.Run(os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GH_TOKEN"), *dryRun, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
