package releaseconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/tuxcrate/github-actions/internal/version"
)

var (
	argRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Plan decodes and validates configuration for repository without reading the
// process environment or writing workflow outputs.
func Plan(r io.Reader, repository string) ([]Build, error) {
	image, err := ImageName("", repository)
	if err != nil {
		return nil, err
	}
	defaults := Build{Image: image, Context: ".", Containerfile: "Containerfile", Platforms: "linux/amd64,linux/arm64"}
	config, err := decodeConfig(r)
	if err != nil {
		return nil, fmt.Errorf("decode tracks: %w", err)
	}
	if config.Image != "" {
		defaults.Image, err = ImageName(config.Image, repository)
		if err != nil {
			return nil, err
		}
	}
	if len(config.Tracks) == 0 || len(config.Tracks) > 256 {
		return nil, errors.New("configuration requires 1–256 tracks")
	}
	owners, names := map[string]string{}, map[string]bool{}
	var builds []Build
	for _, t := range config.Tracks {
		if !trackRE.MatchString(t.Name) || names[t.Name] {
			return nil, fmt.Errorf("invalid or duplicate track %q", t.Name)
		}
		names[t.Name] = true
		if t.Upstream != nil {
			if err := t.Upstream.validate(); err != nil {
				return nil, fmt.Errorf("track %s: %w", t.Name, err)
			}
			if t.Version != "" {
				return nil, fmt.Errorf("track %s: use version or upstream.version, not both", t.Name)
			}
			t.Version = t.Upstream.Version
			if t.Upstream.Datasource == "deb" {
				t.Version, _, _ = strings.Cut(t.Version, "-")
			}
		}
		if err := version.Validate(t.Version); err != nil {
			return nil, fmt.Errorf("track %s: %w", t.Name, err)
		}
		prerelease := t.Prerelease || strings.Contains(t.Version, "-")
		imageVersion, tags, err := version.ResolveTags(t.Version, t.Tags, prerelease)
		if err != nil {
			return nil, fmt.Errorf("track %s: %w", t.Name, err)
		}
		prerelease = prerelease || strings.Contains(imageVersion, "-")
		for _, tag := range tags {
			if owner, exists := owners[tag]; exists {
				return nil, fmt.Errorf("tag %q is owned by both %s and %s", tag, owner, t.Name)
			}
			owners[tag] = t.Name
		}
		b := defaults
		b.Track, b.Version = t.Name, t.Version
		b.ImageVersion = imageVersion
		b.Prerelease = prerelease
		appVersion := t.Version
		if t.Upstream != nil {
			appVersion = t.Upstream.Version
		}
		b.BuildArgs = "APP_VERSION=" + appVersion
		baseImage := t.BaseImage
		if err := validateBaseImage(baseImage); err != nil {
			return nil, fmt.Errorf("track %s: %w", t.Name, err)
		}
		expanded, _ := json.Marshal(tags)
		b.Tags = string(expanded)
		if t.Context != "" {
			b.Context = t.Context
		}
		if t.Containerfile != "" {
			b.Containerfile = t.Containerfile
		}
		if !sourcePath(b.Context) || !sourcePath(b.Containerfile) {
			return nil, fmt.Errorf("track %s: build paths must be relative to source", t.Name)
		}
		if t.Platforms != "" {
			b.Platforms = t.Platforms
		}
		if strings.TrimSpace(b.Platforms) == "" || strings.ContainsAny(b.Platforms, "\r\n") {
			return nil, errors.New("platforms must be a nonempty comma-separated list")
		}
		keys := make([]string, 0, len(t.BuildArgs))
		for key, value := range t.BuildArgs {
			if !argRE.MatchString(key) || key == "APP_VERSION" || key == "BASE_IMAGE" || strings.ContainsAny(value, "\r\n") {
				return nil, fmt.Errorf("invalid build argument %q", key)
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			b.BuildArgs += "\n" + key + "=" + t.BuildArgs[key]
		}
		if baseImage != "" {
			b.BuildArgs += "\nBASE_IMAGE=" + baseImage
		}
		builds = append(builds, b)
	}
	// Validate every track before filtering so dormant tracks cannot hide invalid
	// configuration or conflicting tag ownership.
	active := make([]Build, 0, len(builds))
	for i, b := range builds {
		superseded := false
		if config.Tracks[i].Prerelease {
			for _, other := range builds {
				if !other.Prerelease && newerStableVersion(other.Version, b.Version) {
					superseded = true
					break
				}
			}
		}
		if !superseded {
			active = append(active, b)
		}
	}
	return active, nil
}

// newerStableVersion compares validated application versions where stable has
// no prerelease suffix. A missing fourth component is treated as zero.
func newerStableVersion(stable, candidate string) bool {
	core, _, prerelease := strings.Cut(candidate, "-")
	a, b := strings.Split(stable, "."), strings.Split(core, ".")
	for i := 0; i < 4; i++ {
		x, y := "0", "0"
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		// Validated numeric components have no leading zeros. Compare lengths
		// first to avoid integer overflow for arbitrarily large components.
		if len(x) != len(y) {
			return len(x) > len(y)
		}
		if x != y {
			return x > y
		}
	}
	return prerelease // A stable version is newer than its suffixed prerelease.
}
