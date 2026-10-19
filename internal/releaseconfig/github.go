package releaseconfig

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
)

var githubRepositoryRE = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]*/[A-Za-z0-9_-][A-Za-z0-9_.-]*$`)

// GitHubReleaseSource returns the public GitHub repository and installation pin
// for a track. Other datasources and custom registries do not use this prefetch.
func GitHubReleaseSource(r io.Reader, repository, name string) (string, string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", "", err
	}
	if _, err := Plan(bytes.NewReader(data), repository); err != nil {
		return "", "", err
	}
	config, err := decodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", "", err
	}
	for _, t := range config.Tracks {
		if t.Name != name {
			continue
		}
		u := t.Upstream
		if u == nil || u.Datasource != "github-releases" || (u.RegistryURL != "" && u.RegistryURL != "https://github.com") {
			return "", "", nil
		}
		if !githubRepositoryRE.MatchString(u.DepName) {
			return "", "", fmt.Errorf("GitHub release prefetch requires owner/repo, got %q", u.DepName)
		}
		return u.DepName, u.Version, nil
	}
	return "", "", fmt.Errorf("unknown release track %q", name)
}
