package containerrelease

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/tuxcrate/github-actions/internal/releaseconfig"
)

var (
	githubDigestRE = regexp.MustCompile(`^sha256:([0-9a-f]{64})$`)
	githubHTTPRE   = regexp.MustCompile(`\(HTTP ([0-9]{3})\)`)
)

// upstreamRelease resolves public release assets outside the build. Only URLs,
// checksums and the channel flag enter build arguments, never the job token.
func (a app) upstreamRelease() error {
	f, err := os.Open("meta.yaml")
	if err != nil {
		return err
	}
	defer f.Close()
	source, err := releaseconfig.GitHubReleaseSource(f, a.getenv("GITHUB_REPOSITORY"), a.getenv("TRACK"))
	if err != nil {
		return err
	}
	args := ""
	if source != nil {
		if a.getenv("GH_TOKEN") == "" {
			return errors.New("authenticated GitHub release resolution requires GH_TOKEN")
		}
		result, err := a.exec("", "gh", "api", "--hostname", "github.com", "repos/"+source.Repository+"/releases/tags/"+source.Tag)
		if err != nil {
			// Do not echo authenticated command output or headers.
			return errors.New("fetch upstream release metadata failed")
		}
		if result.code != 0 {
			if status := githubHTTPRE.FindStringSubmatch(result.stderr); len(status) == 2 {
				return fmt.Errorf("fetch upstream release metadata failed (HTTP %s)", status[1])
			}
			return fmt.Errorf("fetch upstream release metadata failed (exit %d)", result.code)
		}
		args, err = githubBuildArgs(source, result.stdout)
		if err != nil {
			return err
		}
	}
	// Local builds can capture the same public arguments without Actions output.
	if a.getenv("GITHUB_OUTPUT") == "" {
		_, err := fmt.Fprint(os.Stdout, args)
		return err
	}
	return a.output("build-args", args)
}

func githubBuildArgs(source *releaseconfig.GitHubSource, data string) (string, error) {
	var release struct {
		Tag        string `json:"tag_name"`
		Draft      *bool  `json:"draft"`
		Prerelease *bool  `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal([]byte(data), &release); err != nil || release.Tag != source.Tag || release.Draft == nil || *release.Draft || release.Prerelease == nil {
		return "", errors.New("invalid upstream GitHub release metadata")
	}
	if *release.Prerelease != source.Prerelease {
		return "", errors.New("upstream GitHub release channel does not match the track")
	}
	arches := make([]string, 0, len(source.Assets))
	for arch := range source.Assets {
		arches = append(arches, arch)
	}
	sort.Strings(arches)
	args := []string{fmt.Sprintf("UPSTREAM_PRERELEASE=%t", *release.Prerelease)}
	for _, arch := range arches {
		name := source.Assets[arch]
		matches := 0
		for _, asset := range release.Assets {
			if asset.Name != name {
				continue
			}
			matches++
			checksum := githubDigestRE.FindStringSubmatch(asset.Digest)
			u, err := url.Parse(asset.URL)
			// Accept only the selected public release asset, with no credentials,
			// query parameters or control characters that could inject build args.
			if len(checksum) != 2 || err != nil || !githubAssetURL(u, asset.URL, source.Repository, source.Tag, name) {
				return "", fmt.Errorf("invalid GitHub release asset URL or SHA-256 digest for %s", arch)
			}
			args = append(args, "UPSTREAM_URL_"+strings.ToUpper(arch)+"="+asset.URL, "UPSTREAM_SHA256_"+strings.ToUpper(arch)+"="+checksum[1])
		}
		if matches != 1 {
			return "", fmt.Errorf("expected exactly one GitHub release asset for %s, found %d", arch, matches)
		}
	}
	return strings.Join(args, "\n"), nil
}

func githubAssetURL(u *url.URL, raw, repository, tag, name string) bool {
	if u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\t ") {
		return false
	}
	// Owner/repository names are case-insensitive; tags and filenames are not.
	suffix := "/" + tag + "/" + name
	return strings.HasSuffix(u.Path, suffix) && strings.EqualFold(strings.TrimSuffix(u.Path, suffix), "/"+repository+"/releases/download")
}
