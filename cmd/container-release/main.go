// Command container-release orchestrates releases for the shared container workflow.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tuxcrate/github-actions/internal/containerrelease"
)

func main() {
	flag.Parse()
	if err := containerrelease.Run(flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
