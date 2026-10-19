package releaseconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/tuxcrate/github-actions/internal/version"
)

var (
	githubRepositoryRE = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)
	githubNameRE       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)
	githubArchRE       = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
)

// GitHubSource contains a track's validated, expanded release asset selectors.
type GitHubSource struct {
	Repository string
	Tag        string
	Assets     map[string]string
	Prerelease bool
}

func (t track) githubSource() (*GitHubSource, error) {
	if t.GitHubRelease == nil {
		return nil, nil
	}
	u := t.Upstream
	if u == nil || u.Datasource != "github-releases" || (u.RegistryURL != "" && u.RegistryURL != "https://github.com") {
		return nil, errors.New("githubRelease requires a public github-releases upstream")
	}
	if !githubRepositoryRE.MatchString(u.DepName) {
		return nil, errors.New("githubRelease requires an owner/repo dependency name")
	}
	tag := strings.ReplaceAll(t.GitHubRelease.Tag, "${version}", u.Version)
	if !strings.Contains(t.GitHubRelease.Tag, "${version}") || !githubNameRE.MatchString(tag) {
		return nil, errors.New("githubRelease.tag requires a tag template using only ${version} and filename-safe characters")
	}
	if len(t.GitHubRelease.Assets) == 0 {
		return nil, errors.New("githubRelease.assets requires architecture-to-asset-name templates")
	}
	assets := make(map[string]string, len(t.GitHubRelease.Assets))
	names := make(map[string]bool)
	for arch, template := range t.GitHubRelease.Assets {
		name := strings.ReplaceAll(template, "${version}", u.Version)
		if !githubArchRE.MatchString(arch) || !githubNameRE.MatchString(name) || names[name] {
			return nil, fmt.Errorf("invalid or duplicate githubRelease asset for architecture %q", arch)
		}
		assets[arch], names[name] = name, true
	}
	platforms := t.Platforms
	if platforms == "" {
		platforms = "linux/amd64,linux/arm64"
	}
	for _, platform := range strings.Split(platforms, ",") {
		parts := strings.Split(strings.TrimSpace(platform), "/")
		if len(parts) < 2 || len(parts) > 3 || assets[parts[1]] == "" {
			return nil, fmt.Errorf("githubRelease.assets has no asset for platform %q", platform)
		}
	}
	prerelease := t.Prerelease || strings.Contains(u.Version, "-")
	imageVersion, _, err := version.ResolveTags(u.Version, t.Tags, prerelease)
	if err != nil {
		return nil, err
	}
	return &GitHubSource{Repository: u.DepName, Tag: tag, Assets: assets, Prerelease: prerelease || strings.Contains(imageVersion, "-")}, nil
}

// GitHubReleaseSource returns nil for tracks that have not opted into asset
// resolution, including PyPI/APT tracks. It validates even dormant tracks.
func GitHubReleaseSource(r io.Reader, repository, name string) (*GitHubSource, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if _, err := Plan(bytes.NewReader(data), repository); err != nil {
		return nil, err
	}
	config, err := decodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	for _, t := range config.Tracks {
		if t.Name == name {
			return t.githubSource()
		}
	}
	return nil, fmt.Errorf("unknown release track %q", name)
}
