package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type trackConfig struct {
	Image  string  `yaml:"image"`
	Tracks []track `yaml:"tracks"`
}

type track struct {
	Name          string            `yaml:"name"`
	Upstream      *upstream         `yaml:"upstream,omitempty"`
	Version       string            `yaml:"version"`
	BaseImage     string            `yaml:"baseImage,omitempty"`
	Aliases       []string          `yaml:"aliases"`
	Context       string            `yaml:"context,omitempty"`
	Containerfile string            `yaml:"containerfile,omitempty"`
	Platforms     string            `yaml:"platforms,omitempty"`
	BuildArgs     map[string]string `yaml:"buildArgs,omitempty"`
}

// Upstream is discovery metadata for Renovate, not a runtime download source.
type upstream struct {
	Datasource  string `yaml:"datasource"`
	DepName     string `yaml:"depName"`
	RegistryURL string `yaml:"registryUrl"`
	Version     string `yaml:"version"`
}

func (u upstream) validate() error {
	if u.Datasource != "deb" && u.Datasource != "pypi" {
		// Renovate owns datasource discovery; do not maintain an allowlist here.
		if !datasourceRE.MatchString(u.Datasource) {
			return errors.New("upstream requires a datasource identifier")
		}
		if fields := strings.Fields(u.DepName); len(fields) != 1 || fields[0] != u.DepName {
			return errors.New("upstream requires a nonempty depName without whitespace")
		}
		if u.RegistryURL != "" {
			registry, err := url.Parse(u.RegistryURL)
			if err != nil || registry.Host == "" || (registry.Scheme != "https" && registry.Scheme != "http") || registry.User != nil || registry.Fragment != "" {
				return errors.New("upstream registryUrl must be an HTTP(S) URL")
			}
		}
		return validateVersion(u.Version)
	}
	if u.Datasource == "pypi" {
		if !pypiPackageRE.MatchString(u.DepName) {
			return errors.New("upstream requires a PyPI package depName")
		}
		if !pypiVersionRE.MatchString(u.Version) {
			return errors.New("upstream version must be a stable X.Y.Z PyPI package version")
		}
		if u.RegistryURL != "" {
			registry, err := url.Parse(u.RegistryURL)
			if err != nil || registry.Host == "" || (registry.Scheme != "https" && registry.Scheme != "http") || registry.User != nil || registry.Fragment != "" {
				return errors.New("upstream registryUrl must be an HTTP(S) PyPI index URL")
			}
		}
		return nil
	}
	if !debPackageRE.MatchString(u.DepName) {
		return errors.New("upstream requires a Debian package depName")
	}
	if !debVersionRE.MatchString(u.Version) {
		return errors.New("upstream version must be a stable X.Y.Z-N Debian package version")
	}
	registry, err := url.Parse(u.RegistryURL)
	if err != nil || registry.Host == "" || (registry.Scheme != "https" && registry.Scheme != "http") || registry.User != nil || registry.Fragment != "" {
		return errors.New("upstream registryUrl must be an HTTP(S) APT repository URL")
	}
	for _, key := range []string{"suite", "components", "binaryArch"} {
		if registry.Query().Get(key) == "" {
			return fmt.Errorf("upstream registryUrl requires %s", key)
		}
	}
	return nil
}

func expandAliases(version string, aliases []string) []string {
	parts := strings.SplitN(version, ".", 3)
	replacer := strings.NewReplacer("{major}", parts[0], "{minor}", parts[1])
	expanded := make([]string, len(aliases))
	for i, alias := range aliases {
		expanded[i] = replacer.Replace(alias)
	}
	return expanded
}

type build struct {
	Track         string `json:"track"`
	Version       string `json:"version"`
	Image         string `json:"image"`
	Aliases       string `json:"aliases"` // JSON array, including an explicit empty array.
	Context       string `json:"context"`
	Containerfile string `json:"containerfile"`
	Platforms     string `json:"platforms"`
	BuildArgs     string `json:"build-args"`
}

var (
	datasourceRE     = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	debPackageRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*$`)
	debVersionRE     = regexp.MustCompile(`^` + versionPattern + `-[0-9]+$`)
	pypiPackageRE    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)
	pypiVersionRE    = regexp.MustCompile(`^` + versionPattern + `$`)
	trackRE          = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	aliasRE          = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,127}$`)
	argRE            = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	baseRepositoryRE = regexp.MustCompile(`^[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?::[0-9]+)?(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)
	platformRE       = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9_]+(?:/[a-zA-Z0-9._-]+)?$`)
	commitRE         = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func validateBaseImage(image string) error {
	if image == "" {
		return nil // Optional for existing callers with a literal FROM reference.
	}
	reference, digest, ok := strings.Cut(image, "@")
	colon := strings.LastIndex(reference, ":")
	if !ok || !digestRE.MatchString(digest) || colon <= strings.LastIndex(reference, "/") || colon < 1 ||
		!baseRepositoryRE.MatchString(reference[:colon]) || !aliasRE.MatchString(reference[colon+1:]) {
		return errors.New("baseImage must be a complete image:tag@sha256:<64 lowercase hex characters> reference")
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func (a app) trackName() string {
	if name := a.getenv("TRACK"); name != "" {
		return name
	}
	if strings.Contains(a.getenv("APP_VERSION"), "-") {
		return "preview"
	}
	return "stable"
}

func validateAliases(version string, aliases []string) error {
	seen := map[string]bool{version: true}
	for _, alias := range aliases {
		if !aliasRE.MatchString(alias) || versionRE.MatchString(alias) || releaseRE.MatchString("v"+alias) || seen[alias] {
			return fmt.Errorf("invalid, reserved, or duplicate alias %q", alias)
		}
		seen[alias] = true
		if strings.Contains(version, "-") {
			// Prereleases may own named channels, never stable numeric aliases.
			if alias == "latest" || alias == "stable" || (alias[0] >= '0' && alias[0] <= '9') {
				return fmt.Errorf("prerelease cannot own stable alias %q", alias)
			}
		}
	}
	return nil
}

func (a app) tags(version string, revision uint64) ([]string, error) {
	if a.getenv("ALIASES") == "" {
		return imageTags(version, revision), nil
	}
	var aliases []string
	if err := json.Unmarshal([]byte(a.getenv("ALIASES")), &aliases); err != nil {
		return nil, fmt.Errorf("decode aliases: %w", err)
	}
	if err := validateAliases(version, aliases); err != nil {
		return nil, err
	}
	return append([]string{fmt.Sprintf("%s-r%d", version, revision), version}, aliases...), nil
}

func sourcePath(path string) bool {
	clean := filepath.Clean(path)
	return path != "" && !filepath.IsAbs(path) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) && !strings.ContainsAny(path, "\r\n")
}

func decodeConfig(r io.Reader) (trackConfig, error) {
	var config trackConfig
	d := yaml.NewDecoder(r)
	d.KnownFields(true)
	if err := d.Decode(&config); err != nil {
		return config, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return config, errors.New("expected exactly one YAML configuration document")
	}
	return config, nil
}

func (a app) builds() ([]build, error) {
	image, err := imageName("", a.getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return nil, err
	}
	defaults := build{Image: image, Context: ".", Containerfile: "Containerfile", Platforms: "linux/amd64,linux/arm64"}
	f, err := os.Open("meta.yaml")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	config, err := decodeConfig(f)
	if err != nil {
		return nil, fmt.Errorf("decode tracks: %w", err)
	}
	if config.Image != "" {
		defaults.Image, err = imageName(config.Image, a.getenv("GITHUB_REPOSITORY"))
		if err != nil {
			return nil, err
		}
	}
	if len(config.Tracks) == 0 || len(config.Tracks) > 256 {
		return nil, errors.New("configuration requires 1–256 tracks")
	}
	owners, names := map[string]string{}, map[string]bool{}
	var builds []build
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
		if err := validateVersion(t.Version); err != nil {
			return nil, fmt.Errorf("track %s: %w", t.Name, err)
		}
		if t.Aliases == nil {
			return nil, fmt.Errorf("track %s must declare aliases (use [] for none)", t.Name)
		}
		t.Aliases = expandAliases(t.Version, t.Aliases)
		if err := validateAliases(t.Version, t.Aliases); err != nil {
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

func (a app) plan() error {
	builds, err := a.builds()
	if err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Include []build `json:"include"`
	}{builds})
	if err != nil {
		return err
	}
	return a.output("matrix", string(data))
}

func (a app) manifest() error {
	return a.writeManifest("")
}

func (a app) checkManifest() error {
	localImage := a.getenv("LOCAL_IMAGE")
	if localImage == "" {
		return errors.New("a local image is required to validate PR release metadata")
	}
	getenv := a.getenv
	a.getenv = func(key string) string {
		if key == "RELEASE_TAG" {
			return "v" + getenv("APP_VERSION") + "-r0"
		}
		return getenv(key)
	}
	// PR validation uses a local image and a synthetic revision. It never
	// reserves a revision, creates a GitHub release, or publishes release tags.
	return a.writeManifest(localImage)
}

func (a app) writeManifest(localImage string) error {
	version := a.getenv("APP_VERSION")
	if err := validateVersion(version); err != nil {
		return err
	}
	tag := a.getenv("RELEASE_TAG")
	match := releaseRE.FindStringSubmatch(tag)
	if match == nil || match[1] != version {
		return errors.New("release tag does not match application version")
	}
	revision, err := strconv.ParseUint(match[len(match)-1], 10, 64)
	if err != nil {
		return err
	}
	tags, err := a.tags(version, revision)
	if err != nil {
		return err
	}
	image, err := imageName(a.getenv("IMAGE"), a.getenv("GITHUB_REPOSITORY"))
	if err != nil {
		return err
	}
	if !digestRE.MatchString(a.getenv("IMAGE_DIGEST")) {
		return errors.New("invalid image digest")
	}
	platforms := strings.Split(a.getenv("PLATFORMS"), ",")
	for i, platform := range platforms {
		platform = strings.TrimSpace(platform)
		if !platformRE.MatchString(platform) {
			return fmt.Errorf("invalid or empty release platform %q", platform)
		}
		platforms[i] = platform
	}
	track := a.trackName()
	if !trackRE.MatchString(track) {
		return errors.New("invalid release track")
	}
	if !commitRE.MatchString(a.getenv("GITHUB_SHA")) {
		return errors.New("invalid or empty source commit")
	}
	imageRef := image + "@" + a.getenv("IMAGE_DIGEST")
	if localImage != "" {
		imageRef = localImage
	}
	// A release has one OS identity; inspect the first declared platform.
	flavor, osVersion, err := a.imageOS(imageRef, platforms[0])
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(struct {
		SchemaVersion int      `json:"schemaVersion"`
		Image         string   `json:"image"`
		Track         string   `json:"track"`
		Version       string   `json:"version"`
		Revision      uint64   `json:"revision"`
		ImmutableTag  string   `json:"immutableTag"`
		Digest        string   `json:"digest"`
		Aliases       []string `json:"aliasesAtPublication"`
		Platforms     []string `json:"platforms"`
		SourceCommit  string   `json:"sourceCommit"`
		OSFlavor      string   `json:"os_flavor"`
		OSVersion     string   `json:"os_version"`
	}{1, image, track, version, revision, tags[0], a.getenv("IMAGE_DIGEST"), tags[1:], platforms, a.getenv("GITHUB_SHA"), flavor, osVersion}, "", "  ")
	if err != nil {
		return err
	}
	// Publication time comes from the published GitHub release, not the draft.
	return os.WriteFile("release.json", append(data, '\n'), 0600)
}
