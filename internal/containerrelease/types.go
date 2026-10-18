package containerrelease

import "github.com/tuxcrate/github-actions/internal/releaseconfig"

type commandResult struct {
	stdout, stderr string
	code           int
}

type commandRunner func(input string, args ...string) (commandResult, error)

type app struct {
	exec   commandRunner
	getenv func(string) string
}

type release struct {
	TagName     string `json:"tag_name"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
	Body        string `json:"body"`
}

type releaseRequest struct {
	TagName         string `json:"tag_name"`
	TargetCommitish string `json:"target_commitish"`
	Name            string `json:"name"`
	Draft           bool   `json:"draft"`
	Prerelease      bool   `json:"prerelease"`
	Body            string `json:"body"`
}

type buildMatrix struct {
	Include []releaseconfig.Build `json:"include"`
}

type releaseManifest struct {
	SchemaVersion int      `json:"schemaVersion"`
	Image         string   `json:"image"`
	Track         string   `json:"track"`
	Version       string   `json:"version"`
	ImageVersion  string   `json:"imageVersion"`
	Revision      uint64   `json:"revision"`
	ImmutableTag  string   `json:"immutableTag"`
	Digest        string   `json:"digest"`
	Aliases       []string `json:"aliasesAtPublication"`
	Platforms     []string `json:"platforms"`
	SourceCommit  string   `json:"sourceCommit"`
	OSFlavor      string   `json:"os_flavor"`
	OSVersion     string   `json:"os_version"`
}
