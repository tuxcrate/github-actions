package releaseconfig

type trackConfig struct {
	Image  string  `yaml:"image"`
	Tracks []track `yaml:"tracks"`
}

type track struct {
	Name          string            `yaml:"name"`
	Upstream      *upstream         `yaml:"upstream,omitempty"`
	GitHubRelease *GitHubRelease    `yaml:"githubRelease,omitempty"`
	Version       string            `yaml:"version"`
	Prerelease    bool              `yaml:"prerelease,omitempty"`
	BaseImage     string            `yaml:"baseImage,omitempty"`
	Tags          []string          `yaml:"tags"`
	Context       string            `yaml:"context,omitempty"`
	Containerfile string            `yaml:"containerfile,omitempty"`
	Platforms     string            `yaml:"platforms,omitempty"`
	BuildArgs     map[string]string `yaml:"buildArgs,omitempty"`
}

// GitHubRelease opts a track into authenticated release asset resolution.
// Tag and asset names support the ${version} placeholder.
type GitHubRelease struct {
	Tag    string            `yaml:"tag"`
	Assets map[string]string `yaml:"assets"`
}

// upstream describes Renovate discovery. The optional githubRelease mapping
// explicitly opts into resolving build inputs from this upstream's release.
type upstream struct {
	Datasource  string `yaml:"datasource"`
	DepName     string `yaml:"depName"`
	RegistryURL string `yaml:"registryUrl"`
	Version     string `yaml:"version"`
}

// Build is a validated track's build-matrix entry.
type Build struct {
	Track         string `json:"track"`
	Version       string `json:"version"`
	ImageVersion  string `json:"image-version"`
	Prerelease    bool   `json:"prerelease"`
	Image         string `json:"image"`
	Tags          string `json:"tags"` // JSON array of all expanded moving tags.
	Context       string `json:"context"`
	Containerfile string `json:"containerfile"`
	Platforms     string `json:"platforms"`
	BuildArgs     string `json:"build-args"`
}
