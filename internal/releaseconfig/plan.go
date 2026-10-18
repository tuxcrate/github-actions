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
		if t.Aliases == nil {
			return nil, fmt.Errorf("track %s must declare aliases (use [] for none)", t.Name)
		}
		t.Aliases = version.ExpandAliases(t.Version, t.Aliases)
		if err := version.ValidateAliases(t.Version, t.Aliases); err != nil {
			return nil, fmt.Errorf("track %s: %w", t.Name, err)
		}
		for _, tag := range append([]string{t.Version}, t.Aliases...) {
			if owner, exists := owners[tag]; exists {
				return nil, fmt.Errorf("tag %q is owned by both %s and %s", tag, owner, t.Name)
			}
			owners[tag] = t.Name
		}
		b := defaults
		b.Track, b.Version = t.Name, t.Version
		appVersion := t.Version
		if t.Upstream != nil {
			appVersion = t.Upstream.Version
		}
		b.BuildArgs = "APP_VERSION=" + appVersion
		baseImage := t.BaseImage
		if err := validateBaseImage(baseImage); err != nil {
			return nil, fmt.Errorf("track %s: %w", t.Name, err)
		}
		aliases, _ := json.Marshal(t.Aliases)
		b.Aliases = string(aliases)
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
	return builds, nil
}
