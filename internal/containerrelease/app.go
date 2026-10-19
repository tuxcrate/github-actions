// Package containerrelease implements the shared container release workflow.
package containerrelease

import (
	"errors"
	"os"
)

// Run executes a release command using the process environment.
func Run(args []string) error {
	a := app{exec: execCommand, getenv: os.Getenv}
	return a.execute(args)
}

func (a app) execute(args []string) error {
	if len(args) == 1 {
		switch args[0] {
		case "metadata":
			return a.metadata()
		case "reserve":
			return a.reserve()
		case "plan":
			return a.plan()
		case "manifest":
			return a.manifest()
		case "check-manifest":
			return a.checkManifest()
		case "upstream-release":
			return a.upstreamRelease()
		}
	}
	return errors.New("usage: container-release {plan|metadata|reserve|manifest|check-manifest|upstream-release}")
}
