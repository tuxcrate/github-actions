package containerrelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/tuxcrate/github-actions/internal/releaseconfig"
)

// upstreamRelease fetches public metadata outside the build. Only this JSON,
// never the workflow's authenticated token, is exposed to the Containerfile.
func (a app) upstreamRelease() error {
	f, err := os.Open("meta.yaml")
	if err != nil {
		return err
	}
	defer f.Close()
	repository, version, err := releaseconfig.GitHubReleaseSource(f, a.getenv("GITHUB_REPOSITORY"), a.getenv("TRACK"))
	if err != nil {
		return err
	}
	if repository == "" {
		return a.output("secret-files", "")
	}
	if a.getenv("GH_TOKEN") == "" {
		return errors.New("authenticated GitHub release prefetch requires GH_TOKEN")
	}
	var data string
	for _, tag := range []string{"v" + version, version} {
		result, err := a.exec("", "gh", "api", "--hostname", "github.com", "repos/"+repository+"/releases/tags/"+tag)
		if err != nil {
			return fmt.Errorf("fetch upstream release metadata: %w", err)
		}
		if result.code != 0 {
			if strings.Contains(result.stderr, "(HTTP 404)") {
				continue
			}
			// Do not echo an authenticated command's output or headers.
			return fmt.Errorf("fetch upstream release metadata failed (exit %d)", result.code)
		}
		var release struct {
			Tag        string `json:"tag_name"`
			Draft      *bool  `json:"draft"`
			Prerelease *bool  `json:"prerelease"`
		}
		if err := json.Unmarshal([]byte(result.stdout), &release); err != nil || release.Tag != tag || release.Draft == nil || *release.Draft || release.Prerelease == nil {
			return errors.New("invalid upstream GitHub release metadata")
		}
		data = result.stdout
		break
	}
	// Unusual tag formats remain the Containerfile's responsibility.
	if data == "" {
		fmt.Fprintln(os.Stderr, "::warning::No bare or v-prefixed upstream release tag found; using the Containerfile's fallback.")
		return a.output("secret-files", "")
	}
	metadata, err := os.CreateTemp(a.getenv("RUNNER_TEMP"), "upstream-release-*.json")
	if err != nil {
		return err
	}
	if _, err := metadata.WriteString(data); err != nil {
		metadata.Close()
		return err
	}
	if err := metadata.Close(); err != nil {
		return err
	}
	return a.output("secret-files", "github_release_json="+metadata.Name())
}
